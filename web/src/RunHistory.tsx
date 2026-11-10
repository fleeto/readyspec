import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Download, RefreshCw, XCircle } from 'lucide-react'
import { ApiError, api } from './api'
import { Badge, Empty, Notice, Spinner } from './components'
import { formatTime } from './model'
import {
  CheckSummary,
  FailureSummary,
  checkName,
  executionLabel,
  verdictLabel,
  useDisplayMetadata,
} from './reportPresentation'
import type { Diagnostic } from './api'
import {
  cleanupLabel,
  itemCounts,
  stageLabels,
  stageStatus,
  verdictReasonTexts,
} from './reportDetails'

interface RunSummary {
  run_id: string
  plan_id: string
  plan_name: string
  plan_version: string
  status: string
  accepted_at: string
  run_mode: string
  run_scope: string
  executor_backend: string
  verdict?: string
  plan_digest?: string
}

interface RunState {
  run: RunSummary
  sequence: number
  host_phases: {
    sequence: number
    host_id: string
    phase: string
    status?: string
    attempt?: number
    at?: string
  }[]
  stage_transitions: {
    sequence: number
    host_id: string
    phase: string
    status?: string
    attempt?: number
    execution_mode?: string
    started_at?: string
    ended_at?: string
    omitted?: boolean
    reason?: string
  }[]
  recovery_error?: string
  recovery_warning?: string
}

interface Evidence {
  resource: string
  property: string
  expected?: unknown
  actual?: unknown
  error?: boolean
  source?: string
  start_time?: string
  end_time?: string
  observation_only?: boolean
}

interface ItemResult {
  role_id: string
  item_id: string
  status: string
  template_id: string
  template_version: string
  execution_mode: string
  evidence_mode: string
  parameter_sources?: Record<string, string>
  evidence?: Evidence[]
}

interface RunReport {
  run_id: string
  plan_digest: string
  run_mode: string
  run_scope: string
  executor_backend: string
  auth_method?: string
  run_status: string
  verdict: string
  termination_reason?: string
  verdict_reasons?: string[]
  started_at: string
  finished_at: string
  topology_results: {
    code: string
    status: string
    host_id?: string
    role_id?: string
    message?: string
  }[]
  hosts: {
    host_id: string
    status: string
    login_uid?: number
    high_risk_root_audit?: boolean
    arch?: string
    roles: string[]
    item_results: ItemResult[]
    failures: { code: string; phase: string; message: string; diagnostics?: Diagnostic[] }[]
  }[]
  cleanup_items: {
    cleanup_id: string
    host_id: string
    status: string
    remote_path?: string
    reason?: string
    audit_reason?: string
  }[]
  failures: { code: string; phase: string; host_id?: string; message: string }[]
}

function ReportRegion({
  title,
  count,
  initiallyOpen = false,
  children,
}: {
  title: string
  count?: number
  initiallyOpen?: boolean
  children: ReactNode
}) {
  return (
    <section className="report-region" aria-label={title}>
      <details open={initiallyOpen}>
        <summary>
          <h4>{title}</h4>
          {count !== undefined && <span className="count">{count}</span>}
        </summary>
        <div className="report-region-body">{children}</div>
      </details>
    </section>
  )
}

export function RunHistory({ onRerun }: { onRerun?: (config: Record<string, unknown>) => void }) {
  const [runs, setRuns] = useState<RunSummary[]>([])
  const [selected, setSelected] = useState<RunSummary | null>(null)
  const [report, setReport] = useState<RunReport | null>(null)
  const [runState, setRunState] = useState<RunState | null>(null)
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [roleFilter, setRoleFilter] = useState('')
  const [domainFilter, setDomainFilter] = useState('')
  const [onlyProblems, setOnlyProblems] = useState(false)
  const [view, setView] = useState<'host' | 'check'>('host')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const selection = useRef(0)
  const { metadata, error: metadataError } = useDisplayMetadata(
    selected?.run_id ?? '',
    selected?.plan_digest,
  )

  const refresh = useCallback(async () => {
    setError('')
    try {
      const result = await api.request<{ items: RunSummary[] }>('/runs')
      setRuns(result.items)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '任务历史读取失败')
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  async function loadReport(runID: string) {
    for (let attempt = 0; attempt < 4; attempt++) {
      try {
        return await api.request<RunReport>(`/runs/${encodeURIComponent(runID)}/report`)
      } catch (cause) {
        if (!(cause instanceof ApiError) || cause.status !== 409 || attempt === 3) throw cause
        await new Promise((resolve) => window.setTimeout(resolve, [500, 1000, 2000][attempt]))
      }
    }
    throw new ApiError('归档报告暂不可读，请稍后刷新。', 409)
  }

  async function openRun(run: RunSummary) {
    const revision = ++selection.current
    setSelected(run)
    setReport(null)
    setRunState(null)
    setBusy(true)
    setError('')
    try {
      const state = await api.request<RunState>(`/runs/${encodeURIComponent(run.run_id)}`)
      if (selection.current !== revision) return
      setRunState(state)
      if (state.recovery_error) {
        setError(`任务存储恢复异常：${state.recovery_error}`)
        if (!state.run.verdict) return
      }
      if (state.recovery_warning) setError(`任务记录已恢复：${state.recovery_warning}`)
      try {
        const loaded = await loadReport(run.run_id)
        if (selection.current === revision) setReport(loaded)
      } catch (cause) {
        if (!(cause instanceof ApiError) || cause.status !== 409) throw cause
        // Accepted Runs may still be executing; their durable state and phases
        // remain useful while the authoritative report is not ready.
      }
    } catch (cause) {
      if (selection.current !== revision) return
      if (cause instanceof ApiError && cause.status === 409)
        setError('归档报告暂不可读，请稍后刷新。')
      else setError(cause instanceof Error ? cause.message : '报告读取失败')
    } finally {
      if (selection.current === revision) setBusy(false)
    }
  }

  async function cancelRun(run: RunSummary) {
    setBusy(true)
    setError('')
    try {
      await api.request(`/runs/${encodeURIComponent(run.run_id)}/cancel`, {})
      await refresh()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '取消请求失败')
    } finally {
      setBusy(false)
    }
  }

  async function rerun(run: RunSummary, partial = false) {
    setBusy(true)
    setError('')
    try {
      const config = await api.request<Record<string, unknown>>(
        `/runs/${encodeURIComponent(run.run_id)}/rerun-config`,
      )
      if (partial) {
        const hosts = Array.isArray(config.partial_hosts) ? (config.partial_hosts as string[]) : []
        if (hosts.length === 0) {
          setError('该任务没有可局部复测的异常主机。')
          return
        }
        onRerun?.({
          ...config,
          rerun_scope: 'PARTIAL',
          partial_source_run_id: run.run_id,
          partial_host_ids: hosts,
        })
      } else {
        onRerun?.(config)
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '重跑配置读取失败')
    } finally {
      setBusy(false)
    }
  }

  const rows = useMemo(() => {
    if (!report) return [] as { host_id: string; role_id: string; item: ItemResult }[]
    return report.hosts.flatMap((host) =>
      host.item_results.map((item) => ({ host_id: host.host_id, role_id: item.role_id, item })),
    )
  }, [report])
  const filteredRows = rows.filter(({ host_id, role_id, item }) => {
    const text =
      `${host_id} ${role_id} ${item.item_id} ${item.template_id} ${checkName(item, metadata)}`.toLowerCase()
    return (
      (!query || text.includes(query.toLowerCase())) &&
      (!statusFilter || item.status === statusFilter) &&
      (!roleFilter || role_id === roleFilter) &&
      (!domainFilter || item.template_id.startsWith(domainFilter)) &&
      (!onlyProblems || ['FAIL', 'ERROR', 'UNSUPPORTED'].includes(item.status))
    )
  })
  const roles = Array.from(new Set(rows.map((row) => row.role_id))).sort()
  const grouped =
    view === 'host'
      ? (report?.hosts.map((host) => ({
          key: host.host_id,
          title: host.host_id,
          rows: filteredRows.filter((row) => row.host_id === host.host_id),
        })) ?? [])
      : Array.from(new Set(filteredRows.map((row) => `${row.role_id}/${row.item.item_id}`)))
          .sort()
          .map((key) => {
            const matching = filteredRows.filter(
              (row) => `${row.role_id}/${row.item.item_id}` === key,
            )
            return {
              key,
              title: `${checkName(matching[0].item, metadata)} · ${key}`,
              rows: matching,
            }
          })

  return (
    <section className="panel">
      <div className="panel-heading">
        <div className="heading-inline">
          <h2>任务历史与报告</h2>
          <span className="count">{runs.length}</span>
        </div>
        <button className="button" onClick={() => void refresh()} disabled={busy}>
          <RefreshCw size={15} />
          刷新
        </button>
      </div>
      {error && <Notice tone="warning">{error}</Notice>}
      {runs.length === 0 ? (
        <Empty icon={<RefreshCw size={20} />} title="暂无检查任务">
          提交任务后，状态和归档报告会显示在这里。
        </Empty>
      ) : (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>方案</th>
                <th>运行类别</th>
                <th>执行器</th>
                <th>任务状态</th>
                <th>结论</th>
                <th>接受时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((run) => (
                <tr key={run.run_id}>
                  <td>
                    {run.plan_name || run.plan_id} · {run.plan_version}
                    <small className="cell-secondary mono">{run.run_id}</small>
                  </td>
                  <td>
                    {run.run_mode} / {run.run_scope}
                  </td>
                  <td>{run.executor_backend}</td>
                  <td>
                    {executionLabel(run.status)} <small>{run.status}</small>
                  </td>
                  <td>{run.verdict ? verdictLabel(run.verdict) : '尚无归档报告'}</td>
                  <td>{formatTime(run.accepted_at)}</td>
                  <td className="run-actions-cell">
                    <div className="run-actions">
                      <button
                        type="button"
                        className="button"
                        disabled={busy}
                        onClick={() => void openRun(run)}
                      >
                        查看报告
                      </button>
                      <button
                        type="button"
                        className="button"
                        disabled={busy}
                        onClick={() => void rerun(run)}
                      >
                        <RefreshCw size={14} />
                        使用最近配置重跑
                      </button>
                      <button
                        type="button"
                        className="button"
                        disabled={busy}
                        onClick={() => void rerun(run, true)}
                      >
                        复测异常主机
                      </button>
                      {['PENDING', 'RUNNING', 'CANCELLING'].includes(run.status) && (
                        <button
                          type="button"
                          className="button danger"
                          disabled={busy}
                          onClick={() => void cancelRun(run)}
                        >
                          <XCircle size={14} />
                          请求取消
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {selected && (
        <div className="run-report">
          <div className="panel-heading">
            <h3>
              {selected.plan_name || selected.plan_id} · {selected.plan_version}
            </h3>
            {report && (
              <button
                className="button"
                onClick={() => {
                  const url = URL.createObjectURL(
                    new Blob([JSON.stringify(report, null, 2)], { type: 'application/json' }),
                  )
                  const anchor = window.document.createElement('a')
                  anchor.href = url
                  anchor.download = `${report.run_id}.report.json`
                  anchor.click()
                  URL.revokeObjectURL(url)
                }}
              >
                <Download size={15} />
                下载 JSON
              </button>
            )}
          </div>
          {busy && <Spinner />}
          {runState && (
            <>
              <p>
                任务状态：{executionLabel(runState.run.status)}（{runState.run.status}） ·
                已记录阶段 {runState.sequence}
              </p>
              {!report && !runState.recovery_error && (
                <Notice tone="info">报告尚未归档；以下显示已持久化的运行状态与阶段。</Notice>
              )}
              {runState.recovery_error && (
                <Notice tone="danger">恢复诊断：{runState.recovery_error}</Notice>
              )}
              {runState.recovery_warning && (
                <Notice tone="warning">恢复提示：{runState.recovery_warning}</Notice>
              )}
            </>
          )}
          {report && (
            <>
              {metadataError && <Notice tone="warning">{metadataError}</Notice>}
              <p>
                任务执行：{executionLabel(report.run_status)}（{report.run_status}） · 检查结论：
                {verdictLabel(report.verdict)}（{report.verdict}）
              </p>
              <Notice
                tone={
                  report.run_mode !== 'GATE' ||
                  report.run_scope !== 'FULL' ||
                  report.executor_backend !== 'native_ssh'
                    ? 'warning'
                    : 'info'
                }
              >
                {report.run_mode !== 'GATE' ||
                report.run_scope !== 'FULL' ||
                report.executor_backend !== 'native_ssh'
                  ? '此任务属于试跑、局部范围或模拟执行，不能作为正式全量门禁放行依据。'
                  : `归档结论：${verdictLabel(report.verdict)}（${report.verdict}） · ${executionLabel(report.run_status)}`}
              </Notice>
              {report.hosts.some((host) => host.login_uid === 0 || host.high_risk_root_audit) && (
                <Notice tone="warning">
                  以下主机使用 root（UID 0）身份执行检查，请重点审计执行范围与结果：
                  {report.hosts
                    .filter((host) => host.login_uid === 0 || host.high_risk_root_audit)
                    .map((host) => host.host_id)
                    .join('、')}
                </Notice>
              )}
              <ul className="verdict-reasons">
                {verdictReasonTexts(report.verdict_reasons, report.verdict, metadata).map(
                  (reason, index) => (
                    <li key={`${index}/${reason}`}>{reason}</li>
                  ),
                )}
              </ul>
              <p>
                方案摘要：<code>{report.plan_digest}</code> · 开始 {formatTime(report.started_at)} ·
                完成 {formatTime(report.finished_at)}
              </p>
              {report.termination_reason && (
                <Notice tone="warning">终止原因：{report.termination_reason}</Notice>
              )}
              <ReportRegion
                title="主机检查汇总"
                count={report.hosts.filter((host) => host.status === 'COMPLETED').length}
                initiallyOpen
              >
                <p>
                  {report.hosts.filter((host) => host.status === 'COMPLETED').length} /{' '}
                  {report.hosts.length} 台主机完成检查执行。完成执行不代表全部检查通过。
                </p>
                <div className="table-scroll" tabIndex={0} aria-label="主机检查汇总，可横向滚动">
                  <table>
                    <thead>
                      <tr>
                        <th>主机</th>
                        <th>执行状态</th>
                        <th>必要项 通过/已检查</th>
                        <th>非必要项 通过/已检查</th>
                      </tr>
                    </thead>
                    <tbody>
                      {report.hosts.map((host) => {
                        const required = itemCounts(host.item_results, metadata, true)
                        const optional = itemCounts(host.item_results, metadata, false)
                        return (
                          <tr key={host.host_id}>
                            <td>{host.host_id}</td>
                            <td>{executionLabel(host.status)}</td>
                            <td>{required ? `${required.passed}/${required.checked}` : ''}</td>
                            <td>{optional ? `${optional.passed}/${optional.checked}` : ''}</td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              </ReportRegion>
              <ReportRegion title="拓扑核验" count={report.topology_results.length}>
                <div className="table-scroll" tabIndex={0} aria-label="拓扑核验，可横向滚动">
                  <table>
                    <thead>
                      <tr>
                        <th>核验项</th>
                        <th>状态</th>
                        <th>主机或角色</th>
                        <th>详情</th>
                      </tr>
                    </thead>
                    <tbody>
                      {report.topology_results.map((item, index) => (
                        <tr key={`${item.code}-${index}`}>
                          <td>{item.code}</td>
                          <td>{item.status}</td>
                          <td>{item.host_id || item.role_id || ''}</td>
                          <td>{item.message ?? ''}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </ReportRegion>
              <ReportRegion title="检查项明细" count={rows.length}>
                <div className="toolbar">
                  <button
                    className={`button ${view === 'host' ? 'primary' : ''}`}
                    onClick={() => setView('host')}
                  >
                    按主机查看
                  </button>
                  <button
                    className={`button ${view === 'check' ? 'primary' : ''}`}
                    onClick={() => setView('check')}
                  >
                    按检查项查看
                  </button>
                  <input
                    aria-label="搜索主机或检查项"
                    placeholder="筛选…"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                  />
                  <select
                    aria-label="按状态筛选"
                    value={statusFilter}
                    onChange={(event) => setStatusFilter(event.target.value)}
                  >
                    <option value="">全部状态</option>
                    {['PASS', 'FAIL', 'ERROR', 'UNSUPPORTED', 'SKIP'].map((status) => (
                      <option key={status}>{status}</option>
                    ))}
                  </select>
                  <select
                    aria-label="按角色筛选"
                    value={roleFilter}
                    onChange={(event) => setRoleFilter(event.target.value)}
                  >
                    <option value="">全部角色</option>
                    {roles.map((role) => (
                      <option key={role}>{role}</option>
                    ))}
                  </select>
                  <select
                    aria-label="按检查领域筛选"
                    value={domainFilter}
                    onChange={(event) => setDomainFilter(event.target.value)}
                  >
                    <option value="">全部领域</option>
                    {['H', 'O', 'S', 'N', 'R', 'A', 'D', 'L'].map((domain) => (
                      <option key={domain}>{domain}</option>
                    ))}
                  </select>
                  {(query || statusFilter || roleFilter || domainFilter || onlyProblems) && (
                    <button
                      className="text-button"
                      onClick={() => {
                        setQuery('')
                        setStatusFilter('')
                        setRoleFilter('')
                        setDomainFilter('')
                        setOnlyProblems(false)
                      }}
                    >
                      清除筛选
                    </button>
                  )}
                  <label>
                    <input
                      type="checkbox"
                      checked={onlyProblems}
                      onChange={(event) => setOnlyProblems(event.target.checked)}
                    />
                    仅看异常
                  </label>
                </div>
                {grouped.map((group) => (
                  <section key={group.key} className="report-group">
                    <h4>{group.title}</h4>
                    {view === 'host' && (
                      <FailureSummary
                        failures={report.hosts.find((host) => host.host_id === group.key)?.failures}
                        empty={
                          report.hosts.find((host) => host.host_id === group.key)?.item_results
                            .length === 0
                        }
                      />
                    )}
                    {view === 'host' && report.hosts.find((host) => host.host_id === group.key) && (
                      <p>
                        执行身份：登录 UID{' '}
                        {report.hosts.find((host) => host.host_id === group.key)?.login_uid ??
                          '未记录'}
                        {' · 架构 '}
                        {report.hosts.find((host) => host.host_id === group.key)?.arch ?? '未记录'}
                        {' · 认证方式 '}
                        {report.auth_method ?? '未记录'}
                      </p>
                    )}
                    <div className="table-scroll" tabIndex={0} aria-label="检查项结果，可横向滚动">
                      <table>
                        <thead>
                          <tr>
                            <th>主机</th>
                            <th>角色</th>
                            <th>检查项</th>
                            <th>状态</th>
                            <th>证据</th>
                          </tr>
                        </thead>
                        <tbody>
                          {group.rows.map(({ host_id, role_id, item }) => (
                            <tr key={`${host_id}/${role_id}/${item.item_id}`}>
                              <td>{host_id}</td>
                              <td>{role_id}</td>
                              <td>
                                <CheckSummary item={item} metadata={metadata} />
                                <small>
                                  {item.template_id}@{item.template_version}
                                </small>
                                <small className="cell-secondary">
                                  {item.evidence_mode} · {item.execution_mode}
                                </small>
                                {item.parameter_sources &&
                                  Object.keys(item.parameter_sources).length > 0 && (
                                    <small className="cell-secondary">
                                      参数来源：
                                      {Object.entries(item.parameter_sources)
                                        .sort(([left], [right]) => left.localeCompare(right))
                                        .map(([name, source]) => `${name}=${source}`)
                                        .join(' · ')}
                                    </small>
                                  )}
                              </td>
                              <td>
                                <Badge status={item.status as never} />
                              </td>
                              <td>
                                {item.evidence?.length
                                  ? item.evidence.map((evidence, index) => (
                                      <details key={`${evidence.resource}/${index}`}>
                                        <summary>
                                          {evidence.property} ·{' '}
                                          {evidence.error
                                            ? '取证错误'
                                            : evidence.observation_only
                                              ? '观测值'
                                              : '判定证据'}
                                        </summary>
                                        <dl>
                                          <dt>资源 / 来源</dt>
                                          <dd>
                                            {evidence.resource} · {evidence.source ?? '未记录'}
                                          </dd>
                                          <dt>期望值</dt>
                                          <dd>
                                            <pre>
                                              {evidence.expected === undefined
                                                ? '未记录'
                                                : JSON.stringify(evidence.expected, null, 2)}
                                            </pre>
                                          </dd>
                                          <dt>观测值</dt>
                                          <dd>
                                            <pre>
                                              {evidence.actual === undefined
                                                ? '未记录'
                                                : JSON.stringify(evidence.actual, null, 2)}
                                            </pre>
                                          </dd>
                                          <dt>采集时间</dt>
                                          <dd>
                                            {evidence.start_time ?? '未记录'} –{' '}
                                            {evidence.end_time ?? '未记录'}
                                          </dd>
                                        </dl>
                                      </details>
                                    ))
                                  : '未记录'}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </section>
                ))}
              </ReportRegion>
              {report.failures.length > 0 && (
                <ReportRegion title="执行失败" count={report.failures.length} initiallyOpen>
                  {report.failures.map((failure, index) => (
                    <Notice key={`${failure.code}-${index}`} tone="danger">
                      {failure.host_id ? `${failure.host_id} · ` : ''}
                      {failure.phase} · {failure.code} · {failure.message}
                    </Notice>
                  ))}
                </ReportRegion>
              )}
            </>
          )}
          {runState && (
            <ReportRegion title="执行阶段" count={runState.stage_transitions.length}>
              <p>阶段表示程序在每台主机上执行的步骤，不代表检查项是否通过。</p>
              <div className="table-scroll" tabIndex={0} aria-label="执行阶段，可横向滚动">
                <table>
                  <thead>
                    <tr>
                      <th>主机</th>
                      <th>阶段</th>
                      <th>执行方式</th>
                      <th>状态</th>
                      <th>开始</th>
                      <th>结束</th>
                    </tr>
                  </thead>
                  <tbody>
                    {runState.stage_transitions.map((stage) => (
                      <tr
                        key={`${stage.sequence}/${stage.host_id}/${stage.execution_mode ?? ''}/${stage.phase}`}
                      >
                        <td>{stage.host_id}</td>
                        <td>
                          {stageLabels[stage.phase] ?? stage.phase}
                          {stageLabels[stage.phase] && (
                            <small className="cell-secondary">{stage.phase}</small>
                          )}
                        </td>
                        <td>
                          {{ STANDARD: '普通执行', PRIVILEGED: '特权执行' }[
                            stage.execution_mode ?? ''
                          ] ?? stage.execution_mode}
                        </td>
                        <td>{stageStatus(stage)}</td>
                        <td>{stage.started_at ? formatTime(stage.started_at) : ''}</td>
                        <td>{stage.ended_at ? formatTime(stage.ended_at) : ''}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </ReportRegion>
          )}
          {report && (
            <ReportRegion title="远端清理审计" count={report.cleanup_items.length}>
              <p>这里记录检查期间使用的临时目录是否已删除，不是检查项结论。</p>
              <div className="table-scroll" tabIndex={0} aria-label="远端清理审计，可横向滚动">
                <table>
                  <thead>
                    <tr>
                      <th>主机</th>
                      <th>清理结果</th>
                      <th>临时目录</th>
                      <th>异常说明</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.cleanup_items.map((item) => (
                      <tr key={item.cleanup_id}>
                        <td>{item.host_id}</td>
                        <td>{cleanupLabel(item.status)}</td>
                        <td>
                          <code>{item.remote_path ?? ''}</code>
                        </td>
                        <td>{item.audit_reason ?? item.reason ?? ''}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </ReportRegion>
          )}
        </div>
      )}
    </section>
  )
}
