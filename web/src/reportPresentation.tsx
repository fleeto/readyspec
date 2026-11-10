import { useEffect, useState } from 'react'
import { api, ApiError, type Diagnostic } from './api'

export interface DisplayMetadata {
  run_id: string
  plan_digest?: string
  items: {
    role_id: string
    item_id: string
    item_name: string
    template_name: string
    importance?: string
    enabled?: boolean
  }[]
}
export interface DisplayResult {
  role_id?: string
  item_id: string
  status: string
  template_id?: string
  evidence?: { property: string; expected?: unknown; actual?: unknown; error?: boolean }[]
}
export function useDisplayMetadata(runID: string, digest?: string) {
  const [state, setState] = useState<{
    id: string
    metadata: DisplayMetadata | null
    error: string
  }>({ id: '', metadata: null, error: '' })
  useEffect(() => {
    if (!runID) return
    const controller = new AbortController()
    void api
      .request<DisplayMetadata>(`/runs/${encodeURIComponent(runID)}/display-metadata`, undefined, {
        signal: controller.signal,
      })
      .then((value) => {
        if (
          value.run_id !== runID ||
          (digest && value.plan_digest !== digest) ||
          !Array.isArray(value.items) ||
          value.items.some(
            (item) =>
              !item ||
              ['role_id', 'item_id', 'item_name', 'template_name'].some(
                (key) => typeof item[key as keyof typeof item] !== 'string',
              ) ||
              (item.importance !== undefined &&
                !['REQUIRED', 'WARNING', 'INFO'].includes(item.importance)) ||
              (item.enabled !== undefined && typeof item.enabled !== 'boolean'),
          )
        )
          throw new Error('invalid metadata')
        if (!controller.signal.aborted) setState({ id: runID, metadata: value, error: '' })
      })
      .catch((cause) => {
        if (!controller.signal.aborted)
          setState({
            id: runID,
            metadata: null,
            error:
              cause instanceof ApiError && [401, 403].includes(cause.status)
                ? '名称信息无访问权限，请恢复会话或检查权限。'
                : '检查项名称暂不可用，以下保留原检查项 ID。',
          })
      })
    return () => controller.abort()
  }, [runID, digest])
  return state.id === runID ? state : { metadata: null, error: '' }
}
export function checkName(item: DisplayResult, metadata: DisplayMetadata | null) {
  const match = metadata?.items.find(
    (entry) => entry.role_id === item.role_id && entry.item_id === item.item_id,
  )
  return match
    ? match.item_name.trim() || match.template_name.trim() || '未命名检查项'
    : item.item_id
}
export function executionLabel(status: string) {
  return (
    (
      {
        COMPLETED: '执行完成',
        PARTIAL: '部分主机执行失败',
        FAILED: '执行失败',
        CANCELLED: '已取消',
        RUNNING: '正在执行',
        PENDING: '等待执行',
        CANCELLING: '正在取消',
      } as Record<string, string>
    )[status] ?? status
  )
}
export function verdictLabel(verdict: string) {
  return (
    (
      { READY: '满足放行要求', NOT_READY: '不满足要求', UNKNOWN: '尚不能给出完整结论' } as Record<
        string,
        string
      >
    )[verdict] ?? verdict
  )
}
export const hostKeyGuidance: Record<string, string> = {
  HOST_KEY_UNKNOWN:
    '此连接启用了严格校验，主机密钥尚未登记。请从可信控制台核对指纹，再由操作者登记到运行 ReadySpec 的管理端账户的 known_hosts。',
  HOST_KEY_CHANGED:
    '主机密钥与已记录的指纹或严格模式的信任记录不一致。请停止连接并调查身份变更，勿直接覆盖或删除旧记录。',
  HOST_KEY_REVOKED: '主机密钥已吊销。请停止连接并联系管理员核实，勿重新信任该密钥。',
  KNOWN_HOSTS_UNREADABLE: '管理端无法读取 known_hosts。请检查运行 ReadySpec 的账户及文件权限。',
}
export function FailureSummary({
  failures = [],
  empty = false,
}: {
  failures?: { code: string; diagnostics?: Diagnostic[] }[]
  empty?: boolean
}) {
  return (
    <>
      {empty && <p>未执行检查</p>}
      {failures.map((failure, index) => {
        const code = failure.diagnostics?.[0]?.code ?? failure.code
        return (
          <p key={`${code}/${index}`}>
            {hostKeyGuidance[code] ??
              failure.diagnostics?.[0]?.message ??
              '检查步骤未能完成，请查看诊断详情。'}{' '}
            <code>{code}</code>
          </p>
        )
      })}
    </>
  )
}
function capacity(value: number) {
  return `${(value / 1e9).toFixed(2)} GB（${(value / 2 ** 30).toFixed(2)} GiB；${value} 字节）`
}
function byteCount(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}
export function CheckSummary({
  item,
  metadata,
}: {
  item: DisplayResult
  metadata: DisplayMetadata | null
}) {
  const evidence = item.evidence?.find((entry) => entry.property === 'stdout' && !entry.error)
  const expected = evidence?.expected as Record<string, unknown> | undefined
  const actual = evidence?.actual as Record<string, unknown> | undefined
  let comparison = ''
  if (
    item.template_id === 'H03' &&
    byteCount(expected?.min_memory) &&
    byteCount(actual?.mem_total_bytes)
  )
    comparison = `最低内存：${capacity(expected.min_memory)}；实际可见内存：${capacity(actual.mem_total_bytes)}。以操作系统 MemTotal 为准。`
  if (
    item.template_id === 'S04' &&
    byteCount(expected?.min_available) &&
    byteCount(actual?.available_bytes)
  )
    comparison = `目录 ${typeof expected.path === 'string' ? expected.path : ''} 最低可用空间：${capacity(expected.min_available)}；实际非特权用户可用：${capacity(actual.available_bytes)}。`
  if (
    item.template_id === 'H02' &&
    typeof expected?.ge === 'number' &&
    typeof evidence?.actual === 'string' &&
    /^\d+\s*$/.test(evidence.actual)
  )
    comparison = `最低逻辑 CPU：${expected.ge}；实际在线逻辑 CPU：${evidence.actual.trim()}。`
  const next =
    item.status === 'FAIL'
      ? '未达到方案要求；请核对门槛或调整目标环境。'
      : item.status === 'PASS'
        ? '达到该项要求。'
        : '尚无完整判定，请查看证据与诊断。'
  return (
    <div className="check-summary">
      <strong>{checkName(item, metadata)}</strong>
      <small className="cell-secondary mono">{item.item_id}</small>
      {comparison && <p>{comparison}</p>}
      <p>
        {next}（{item.status}）
      </p>
    </div>
  )
}
