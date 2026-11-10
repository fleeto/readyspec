import type { DisplayMetadata } from './reportPresentation'

export interface ReportResult {
  role_id?: string
  item_id: string
  status: string
}

export const stageLabels: Record<string, string> = {
  CONNECTING: '连接主机',
  DISCOVERING: '识别主机',
  PREPARING: '准备临时目录',
  UPLOADING: '上传检查内容',
  RUNNING: '执行检查',
  COLLECTING: '收集结果',
  CLEANING: '清理临时目录',
  OMITTED: '未安排的检查',
  SIMULATING_PREFLIGHT: '模拟预检',
  SIMULATING: '模拟执行',
}

export const verdictReasonLabels: Record<string, string> = {
  REQUIRED_CHECK_FAILED: '有必检项不满足要求。',
  TOPOLOGY_FAILED: '主机身份、架构或角色数量核验未通过。',
  REQUIRED_EVIDENCE_ERROR: '有必检项缺少有效证据。',
  TOPOLOGY_EVIDENCE_ERROR: '拓扑核验缺少有效证据。',
  TRIAL_NOT_GATE: '本次是试跑，不能作为正式门禁结论。',
  PARTIAL_SCOPE: '本次只检查部分范围。',
  SIMULATOR_BACKEND: '本次使用模拟执行器，没有检查真实主机。',
  RUN_NOT_COMPLETED: '任务未全部执行完成。',
  PLAN_NOT_RELEASED: '使用的方案不是已发布版本。',
  NO_REQUIRED_CHECKS: '方案没有必检项，因此不能判断为满足放行要求。',
  NO_TOPOLOGY_RESULTS: '没有拓扑核验结果。',
}

// Older archives may contain [""]. Explain what the frozen plan proves without
// claiming that an unrecorded failure or topology result did not exist.
export function verdictReasonTexts(
  stored: string[] | undefined,
  verdict: string,
  metadata: DisplayMetadata | null,
): string[] {
  const reasons = (stored ?? []).map((reason) => reason.trim()).filter(Boolean)
  if (reasons.length > 0) {
    return reasons.map((reason) => verdictReasonLabels[reason] ?? reason)
  }
  if (
    verdict === 'UNKNOWN' &&
    metadata &&
    !metadata.items.some((item) => item.enabled && item.importance === 'REQUIRED')
  ) {
    return [verdictReasonLabels.NO_REQUIRED_CHECKS]
  }
  return []
}

export function itemCounts(
  items: ReportResult[],
  metadata: DisplayMetadata | null,
  required: boolean,
) {
  if (!metadata) return null
  const matching = items.map((item) => ({
    item,
    definition: metadata.items.find(
      (entry) => entry.role_id === item.role_id && entry.item_id === item.item_id,
    ),
  }))
  if (
    matching.some(
      ({ definition }) =>
        !definition?.importance || !['REQUIRED', 'WARNING', 'INFO'].includes(definition.importance),
    )
  )
    return null
  const selected = matching.filter(
    ({ definition }) => (definition?.importance === 'REQUIRED') === required,
  )
  return {
    passed: selected.filter(({ item }) => item.status === 'PASS').length,
    checked: selected.length,
  }
}

export function stageStatus(stage: { started_at?: string; ended_at?: string; omitted?: boolean }) {
  if (stage.omitted) return '未执行'
  if (stage.ended_at) return '已结束'
  if (stage.started_at) return '进行中'
  return ''
}

export function cleanupLabel(status: string) {
  return (
    {
      CLEAN: '已核对并删除',
      UNKNOWN: '未能确认',
      PENDING: '尚未清理',
      IGNORED: '已忽略',
    }[status] ?? status
  )
}
