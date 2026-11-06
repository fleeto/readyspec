import { useEffect, useRef, useState } from 'react'
import { stringify } from 'yaml'
import { ApiError, api } from './api'
import { Field, Notice } from './components'
import { randomSuffix, type Plan, type SavedPlan } from './model'

interface PreviewResult {
  host_count: number
  candidate_check_count: number
  privileged_check_count: number
  custom_shell_check_count: number
  active_probe_check_count: number
  configured_root_hosts: string[]
  identity_unverified_hosts: string[]
  validated_plan: Plan
  hosts: { host: { id: string; address: string; port: number }; roles: string[] }[]
}

interface RunReport {
  run_id: string
  run_status: string
  verdict: string
  termination_reason?: string
  hosts: { host_id: string; status: string; item_results: { item_id: string; status: string }[] }[]
}
interface StageTransition {
  sequence: number
  host_id: string
  execution_mode?: string
  phase: string
  attempt: number
  started_at?: string
  ended_at?: string
  omitted?: boolean
  reason?: string
}
interface ParsedInventory {
  hosts: { id: string; address: string; port: number; groups: string[]; username?: string }[]
  groups: Record<string, string[]>
}

export function RunPreview({
  document,
  planRecordKey,
  rerunConfig,
  selectedPlanKey,
  plans,
  onSelectPlan,
  onSubmissionUncertain,
}: {
  document: Plan
  planRecordKey?: string | null
  rerunConfig?: Record<string, unknown> | null
  selectedPlanKey?: string
  plans?: SavedPlan[]
  onSelectPlan?: (plan: SavedPlan) => void
  onSubmissionUncertain?: (uncertain: boolean) => void
}) {
  const [source, setSource] = useState('')
  const [format, setFormat] = useState('quick')
  const [bindings, setBindings] = useState<Record<string, string>>({})
  const [overrides, setOverrides] = useState('[]')
  const [result, setResult] = useState<PreviewResult | null>(null)
  const [parsedInventory, setParsedInventory] = useState<ParsedInventory | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [mode, setMode] = useState('GATE')
  const [scope, setScope] = useState('FULL')
  const [backend, setBackend] = useState('native_ssh')
  const [auth, setAuth] = useState('password')
  const [password, setPassword] = useState('')
  const [sudoPassword, setSudoPassword] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [agentSocket, setAgentSocket] = useState('')
  const [keyFingerprint, setKeyFingerprint] = useState('')
  const [profiles, setProfiles] = useState('{"hosts":{}}')
  const [budgets, setBudgets] = useState('{}')
  const [partialSourceRunID, setPartialSourceRunID] = useState('')
  const [partialCandidates, setPartialCandidates] = useState<string[]>([])
  const [partialHostIDs, setPartialHostIDs] = useState<string[]>([])
  const [runId, setRunId] = useState('')
  const [runStatus, setRunStatus] = useState('')
  const [eventConnection, setEventConnection] = useState<
    'idle' | 'connecting' | 'live' | 'reconnecting' | 'stale'
  >('idle')
  const [hostPhases, setHostPhases] = useState<
    { host_id: string; execution_mode?: string; phase: string; at: string }[]
  >([])
  const [stageTransitions, setStageTransitions] = useState<StageTransition[]>([])
  const [submissionUncertain, setSubmissionUncertain] = useState(false)
  const [report, setReport] = useState<RunReport | null>(null)
  const [step, setStep] = useState(1)
  const eventsController = useRef<AbortController | null>(null)
  const eventCursor = useRef('')
  const reconnectEvents = useRef<() => void>(() => undefined)
  const pendingIdempotencyKey = useRef('')
  useEffect(() => () => eventsController.current?.abort(), [])
  function connectEvents(id: string, resetCursor = false) {
    eventsController.current?.abort()
    if (resetCursor) eventCursor.current = ''
    const controller = new AbortController()
    eventsController.current = controller
    setEventConnection('connecting')
    let terminal = false
    const terminalStatuses = ['COMPLETED', 'PARTIAL', 'FAILED', 'CANCELLED']
    const fetchReport = async () => {
      for (let attempt = 0; attempt < 4 && !controller.signal.aborted; attempt++) {
        try {
          setReport(
            await api.request<RunReport>(`/runs/${id}/report`, undefined, {
              signal: controller.signal,
            }),
          )
          return
        } catch (cause) {
          if (controller.signal.aborted) return
          if (!(cause instanceof ApiError) || cause.status !== 409 || attempt === 3) {
            setError(cause instanceof Error ? cause.message : '报告读取失败')
            return
          }
          await new Promise((resolve) => window.setTimeout(resolve, [500, 1000, 2000][attempt]))
        }
      }
      if (!controller.signal.aborted) setError('归档报告暂不可读，请稍后刷新。')
    }
    reconnectEvents.current = () => connectEvents(id)
    void (async () => {
      const delays = [1000, 2000, 4000, 8000, 15000]
      for (let attempt = 0; !controller.signal.aborted; attempt++) {
        setEventConnection(attempt === 0 ? 'connecting' : 'reconnecting')
        try {
          await api.subscribe(
            `/runs/${id}/events`,
            eventCursor.current || undefined,
            controller.signal,
            (event) => {
              if (event.id) eventCursor.current = event.id
              const data = event.data as {
                run?: { status?: string }
                status?: string
                host_phases?: typeof hostPhases
                stage_transitions?: StageTransition[]
                projection?: {
                  run?: { status?: string }
                  host_phases?: typeof hostPhases
                  stage_transitions?: StageTransition[]
                }
              }
              const projection = data.projection
              const status = projection?.run?.status ?? data.run?.status ?? data.status
              const phases = projection?.host_phases ?? data.host_phases
              const transitions = projection?.stage_transitions ?? data.stage_transitions
              if (event.name === 'reset' || event.name === 'snapshot') setHostPhases(phases ?? [])
              else if (phases) setHostPhases(phases)
              if (event.name === 'reset' || event.name === 'snapshot')
                setStageTransitions(transitions ?? [])
              else if (transitions) setStageTransitions(transitions)
              if (status) {
                setRunStatus(status)
                if (terminalStatuses.includes(status)) {
                  terminal = true
                  void fetchReport()
                }
              }
              if (event.name === 'TERMINAL_ARCHIVED') {
                terminal = true
                void fetchReport()
              }
              setEventConnection('live')
            },
          )
          if (terminal || controller.signal.aborted) return
        } catch (cause) {
          if (controller.signal.aborted) return
          if (cause instanceof ApiError && cause.status === 401) {
            setEventConnection('stale')
            setError(cause.message)
            return
          }
        }
        if (attempt >= delays.length) {
          setEventConnection('stale')
          return
        }
        await new Promise<void>((resolve) => window.setTimeout(resolve, delays[attempt]))
      }
    })()
  }
  useEffect(
    () =>
      api.onSessionExpired(() => {
        setPassword('')
        setSudoPassword('')
        setPrivateKey('')
        setPassphrase('')
        setAgentSocket('')
        setKeyFingerprint('')
      }),
    [],
  )
  useEffect(() => {
    if (document.state === 'DRAFT') setMode('TRIAL')
  }, [document.state])
  useEffect(() => {
    if (!rerunConfig) return
    const inventoryInput = rerunConfig.inventory_input as
      { source?: string; format?: string } | undefined
    if (inventoryInput?.source !== undefined) {
      setSource(inventoryInput.source)
      setFormat(inventoryInput.format || 'quick')
    } else {
      const hosts = Array.isArray(rerunConfig.inventory)
        ? (rerunConfig.inventory as {
            id: string
            address: string
            port: number
            groups?: string[]
            username?: string
          }[])
        : []
      const groups = new Map<string, string[]>()
      for (const host of hosts) {
        const names = (host.groups ?? []).filter((group) => group !== 'all')
        for (const group of names.length ? names : ['all']) {
          const line = `${host.id} ansible_host=${host.address} ansible_port=${host.port || 22}${host.username ? ` ansible_user=${host.username}` : ''}`
          groups.set(group, [...(groups.get(group) ?? []), line])
        }
      }
      setSource([...groups].map(([group, lines]) => `[${group}]\n${lines.join('\n')}`).join('\n\n'))
      setFormat('ini')
    }
    setBindings(
      Object.fromEntries(
        Object.entries((rerunConfig.role_map as Record<string, string[]>) ?? {}).map(
          ([role, hostIDs]) => [role, hostIDs.join(', ')],
        ),
      ),
    )
    setOverrides(JSON.stringify(rerunConfig.field_overrides ?? [], null, 2))
    setMode(String(rerunConfig.run_mode ?? 'TRIAL'))
    setScope(String(rerunConfig.run_scope ?? 'FULL'))
    setBackend(String(rerunConfig.executor_backend ?? 'native_ssh'))
    setProfiles(JSON.stringify(rerunConfig.simulator_profiles ?? { hosts: {} }, null, 2))
    setBudgets(JSON.stringify(rerunConfig.budgets ?? {}, null, 2))
    const sourceRunID = String(rerunConfig.partial_source_run_id ?? '')
    const candidates = Array.isArray(rerunConfig.partial_hosts)
      ? (rerunConfig.partial_hosts as string[])
      : []
    const selected = Array.isArray(rerunConfig.partial_host_ids)
      ? (rerunConfig.partial_host_ids as string[])
      : candidates
    setPartialSourceRunID(sourceRunID)
    setPartialCandidates(candidates)
    setPartialHostIDs(selected)
    if (rerunConfig.rerun_scope === 'PARTIAL') setScope('PARTIAL')
    const identity = rerunConfig.auth_identity as
      { method?: string; public_key_fingerprint?: string } | undefined
    if (identity?.method) setAuth(identity.method)
    setKeyFingerprint(identity?.public_key_fingerprint ?? '')
    setResult(null)
    setParsedInventory(null)
    setStep(1)
  }, [rerunConfig])
  function changed(update: () => void) {
    if (submissionUncertain) return
    update()
    setResult(null)
    setError('')
    pendingIdempotencyKey.current = ''
  }
  async function preview() {
    setResult(null)
    setError('')
    setBusy(true)
    try {
      if (scope === 'PARTIAL' && (!partialSourceRunID || partialHostIDs.length === 0)) {
        throw new Error('局部复测必须从历史报告选择至少一台异常主机。')
      }
      const fields: unknown = JSON.parse(overrides)
      if (!Array.isArray(fields)) throw new Error('现场参数值必须是 JSON 数组')
      const roleMap = Object.fromEntries(
        document.roles.map((role) => [
          role.role_id,
          (bindings[role.role_id] ?? '')
            .split(',')
            .map((v) => v.trim())
            .filter(Boolean),
        ]),
      )
      const validated = await api.request<PreviewResult>('/runs/preview', {
        source: stringify(document),
        normalize_draft: document.state === 'DRAFT',
        inventory: { source, format },
        role_map: roleMap,
        field_overrides: fields,
        ...(scope === 'PARTIAL' ? { partial_host_ids: partialHostIDs } : {}),
      })
      setResult(validated)
      setStep(4)
    } catch (err) {
      setError(err instanceof Error ? err.message : '静态预览失败')
    } finally {
      setBusy(false)
    }
  }
  async function parseInventory() {
    setBusy(true)
    setError('')
    try {
      const parsed = await api.request<ParsedInventory>('/inventory/parse', { source, format })
      setParsedInventory(parsed)
    } catch (cause) {
      setParsedInventory(null)
      setError(cause instanceof Error ? cause.message : '主机清单解析失败')
    } finally {
      setBusy(false)
    }
  }
  async function submit() {
    if (!result) return
    setBusy(true)
    setError('')
    setReport(null)
    eventsController.current?.abort()
    try {
      const roleMap = Object.fromEntries(
        document.roles.map((role) => [
          role.role_id,
          (bindings[role.role_id] ?? '')
            .split(',')
            .map((v) => v.trim())
            .filter(Boolean),
        ]),
      )
      const credentials = Object.fromEntries(
        result.hosts.map(({ host }) => [
          host.id,
          backend === 'mock_simulator'
            ? {}
            : auth === 'password'
              ? { password, sudo_password: sudoPassword }
              : auth === 'private_key'
                ? { private_key: privateKey, passphrase, sudo_password: sudoPassword }
                : { agent_socket: agentSocket, sudo_password: sudoPassword },
        ]),
      )
      const accepted = await api.request<{ run_id: string; status: string }>('/runs', {
        plan: stringify(result.validated_plan),
        ...(planRecordKey ? { plan_record_key: planRecordKey } : {}),
        inventory: { source, format },
        role_map: roleMap,
        field_overrides: JSON.parse(overrides),
        run_mode: mode,
        run_scope: scope,
        executor_backend: backend,
        budgets: JSON.parse(budgets),
        ...(scope === 'PARTIAL'
          ? { partial_source_run_id: partialSourceRunID, partial_host_ids: partialHostIDs }
          : {}),
        idempotency_key:
          pendingIdempotencyKey.current ||
          (pendingIdempotencyKey.current = `web-${randomSuffix(32)}`),
        auth_identity: {
          method: backend === 'mock_simulator' ? 'offline' : auth,
          ...(auth === 'agent' ? { public_key_fingerprint: keyFingerprint } : {}),
        },
        credentials,
        ...(backend === 'mock_simulator' ? { simulator_profiles: JSON.parse(profiles) } : {}),
      })
      setRunId(accepted.run_id)
      setRunStatus(accepted.status)
      setSubmissionUncertain(false)
      onSubmissionUncertain?.(false)
      setHostPhases([])
      setPassword('')
      setSudoPassword('')
      setPrivateKey('')
      setPassphrase('')
      setAgentSocket('')
      setKeyFingerprint('')
      pendingIdempotencyKey.current = ''
      connectEvents(accepted.run_id, true)
    } catch (cause) {
      setPassword('')
      setSudoPassword('')
      setPrivateKey('')
      setPassphrase('')
      setAgentSocket('')
      setKeyFingerprint('')
      const uncertain = !(cause instanceof ApiError)
      setSubmissionUncertain(uncertain)
      onSubmissionUncertain?.(uncertain)
      setError(cause instanceof Error ? cause.message : '任务提交失败')
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>四步检查向导</h2>
      </div>
      <div className="modal-body">
        <nav className="wizard-steps" aria-label="检查向导步骤">
          {['选择方案', '主机清单', '角色与认证', '预览并提交'].map((title, index) => (
            <button
              key={title}
              className={`button ${step === index + 1 ? 'primary' : ''}`}
              disabled={index + 1 > step}
              onClick={() => setStep(index + 1)}
            >
              <span className="step-number">{index + 1}</span>
              {title}
            </button>
          ))}
        </nav>
        <p>
          当前方案：{document.name || '未命名方案'} · {document.plan_id}@{document.version} ·{' '}
          {document.state}
        </p>
        {document.state === 'ARCHIVED' && (
          <Notice tone="warning">此方案版本已停用，只能查看归档资料，不能创建新任务。</Notice>
        )}
        <Notice>
          静态预览不连接目标主机。只有提交任务后才进行 SSH 身份、凭据、权限与环境预检。
        </Notice>
        <fieldset disabled={busy}>
          <div hidden={step !== 1}>
            <h3>确认方案</h3>
            <Field label="从工作区选择方案">
              <select
                aria-label="向导检查方案"
                value={selectedPlanKey ?? ''}
                onChange={(event) => {
                  const selected = plans?.find((item) => item.key === event.target.value)
                  if (selected) onSelectPlan?.(selected)
                }}
              >
                <option value="">当前打开的方案</option>
                {(plans ?? []).map((item) => (
                  <option key={item.key} value={item.key}>
                    {item.document.name} · {item.document.version} · {item.document.state}
                  </option>
                ))}
              </select>
            </Field>
            <p>
              {document.name} · {document.plan_id}@{document.version} · {document.state}
            </p>
            {document.state !== 'ARCHIVED' && (
              <button className="button primary" onClick={() => setStep(2)}>
                下一步：主机清单
              </button>
            )}
          </div>
          <div hidden={step !== 2}>
            <h3>主机清单</h3>
            <Field label="清单格式">
              <select
                value={format}
                onChange={(e) =>
                  changed(() => {
                    setFormat(e.target.value)
                    setParsedInventory(null)
                  })
                }
              >
                <option value="quick">快速主机表</option>
                <option value="ini">Ansible INI</option>
                <option value="yaml">Ansible YAML</option>
              </select>
            </Field>
            <Field label="主机清单" hint="快速格式：node=192.0.2.1；多个主机以逗号分隔。">
              <textarea
                rows={5}
                value={source}
                onChange={(e) =>
                  changed(() => {
                    setSource(e.target.value)
                    setParsedInventory(null)
                  })
                }
              />
            </Field>
            <button
              className="button"
              disabled={!source.trim() || busy}
              onClick={() => void parseInventory()}
            >
              {busy ? '正在解析…' : '解析并预览分组'}
            </button>
            {parsedInventory && (
              <section className="inventory-preview">
                <strong>{parsedInventory.hosts.length} 台主机</strong>
                <ul>
                  {Object.entries(parsedInventory.groups).map(([group, hosts]) => (
                    <li key={group}>
                      {group}: {hosts.join(', ')}
                    </li>
                  ))}
                </ul>
              </section>
            )}
            <button className="button" onClick={() => setStep(1)}>
              上一步
            </button>
            <button
              className="button primary"
              disabled={!source.trim()}
              onClick={() => {
                if (parsedInventory) {
                  setBindings((previous) => {
                    const suggested = { ...previous }
                    for (const role of document.roles) {
                      if (!suggested[role.role_id] && parsedInventory.groups[role.role_id])
                        suggested[role.role_id] = parsedInventory.groups[role.role_id].join(', ')
                    }
                    return suggested
                  })
                }
                setStep(3)
              }}
            >
              下一步：角色与认证
            </button>
          </div>
          <div hidden={step !== 3}>
            <h3>角色绑定、现场参数与认证</h3>
            {document.roles.map((role) => (
              <Field
                key={role.role_id}
                label={`绑定主机 · ${role.role_id}`}
                hint="填写清单中的主机标识，以逗号分隔。"
              >
                <input
                  value={bindings[role.role_id] ?? ''}
                  onChange={(e) =>
                    changed(() =>
                      setBindings((previous) => ({ ...previous, [role.role_id]: e.target.value })),
                    )
                  }
                />
              </Field>
            ))}
            <Field
              label="现场参数值（JSON）"
              hint='仅支持检查项已开放的参数。例如：[{"scope":"host","target":"node","item_id":"data","parameter":"path","value":"/data"}]'
            >
              <textarea
                rows={4}
                value={overrides}
                onChange={(e) => changed(() => setOverrides(e.target.value))}
              />
            </Field>
            <div className="two-columns">
              <Field label="任务类别">
                <select value={mode} onChange={(e) => changed(() => setMode(e.target.value))}>
                  <option value="GATE" disabled={document.state !== 'RELEASED'}>
                    正式门禁
                  </option>
                  <option value="TRIAL">草稿试跑</option>
                </select>
              </Field>
              <Field label="执行范围">
                <select value={scope} onChange={(e) => changed(() => setScope(e.target.value))}>
                  <option value="FULL">全量检查</option>
                  <option
                    value="PARTIAL"
                    disabled={!partialSourceRunID || partialCandidates.length === 0}
                  >
                    局部复测
                  </option>
                </select>
              </Field>
            </div>
            {scope === 'PARTIAL' && (
              <fieldset className="partial-host-picker">
                <legend>复测异常主机</legend>
                <p>只允许选择原任务报告中有失败、错误、不支持项或主机失败的目标。</p>
                {partialCandidates.map((hostID) => (
                  <label key={hostID}>
                    <input
                      type="checkbox"
                      checked={partialHostIDs.includes(hostID)}
                      onChange={(event) =>
                        changed(() =>
                          setPartialHostIDs((ids) =>
                            event.target.checked
                              ? [...ids, hostID]
                              : ids.filter((id) => id !== hostID),
                          ),
                        )
                      }
                    />
                    {hostID}
                  </label>
                ))}
              </fieldset>
            )}
            <Field label="执行器">
              <select value={backend} onChange={(e) => changed(() => setBackend(e.target.value))}>
                <option value="native_ssh">原生 SSH</option>
                <option value="mock_simulator">离线模拟器</option>
              </select>
            </Field>
            {backend === 'native_ssh' ? (
              <>
                <Field label="SSH 认证方式">
                  <select
                    value={auth}
                    onChange={(e) =>
                      changed(() => {
                        setAuth(e.target.value)
                        setPassword('')
                        setSudoPassword('')
                        setPrivateKey('')
                        setPassphrase('')
                        setAgentSocket('')
                        setKeyFingerprint('')
                      })
                    }
                  >
                    <option value="password">密码</option>
                    <option value="private_key">私钥材料</option>
                    <option value="agent">本机 SSH Agent</option>
                  </select>
                </Field>
                {auth === 'password' && (
                  <Field label="SSH 密码">
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                    />
                  </Field>
                )}
                {auth === 'private_key' && (
                  <>
                    <Field label="SSH 私钥内容">
                      <textarea
                        rows={5}
                        autoComplete="off"
                        value={privateKey}
                        onChange={(e) => setPrivateKey(e.target.value)}
                      />
                    </Field>
                    <Field label="私钥口令（如需要）">
                      <input
                        type="password"
                        autoComplete="new-password"
                        value={passphrase}
                        onChange={(e) => setPassphrase(e.target.value)}
                      />
                    </Field>
                  </>
                )}
                {result && result.privileged_check_count > 0 && (
                  <Field label="sudo 密码（仅在目标要求时）">
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={sudoPassword}
                      onChange={(e) => setSudoPassword(e.target.value)}
                    />
                  </Field>
                )}
                {auth === 'agent' && (
                  <>
                    <Field label="本机 Agent socket">
                      <input value={agentSocket} onChange={(e) => setAgentSocket(e.target.value)} />
                    </Field>
                    <Field label="选定公钥指纹">
                      <input
                        value={keyFingerprint}
                        onChange={(e) => changed(() => setKeyFingerprint(e.target.value))}
                        placeholder="SHA256:…"
                      />
                    </Field>
                  </>
                )}
                <Notice>
                  接受任务前，服务会取得所需凭据或 Agent
                  连接。私钥材料只进入任务内存，不写入工作区。
                </Notice>
                {window.location.protocol === 'http:' &&
                  !['localhost', '127.0.0.1', '::1'].includes(window.location.hostname) && (
                    <Notice tone="warning">
                      当前连接使用未加密 HTTP。SSH 密码和 sudo
                      密码会以明文经过本机与管理端之间的网络。
                    </Notice>
                  )}
              </>
            ) : (
              <Field label="模拟器画像（JSON）">
                <textarea
                  rows={6}
                  value={profiles}
                  onChange={(e) => changed(() => setProfiles(e.target.value))}
                />
              </Field>
            )}
            <button className="button" onClick={() => setStep(2)}>
              上一步
            </button>
            <button
              className="button primary"
              disabled={!source.trim()}
              onClick={() => void preview()}
            >
              {busy ? '正在生成预览…' : '生成静态预览'}
            </button>
          </div>
          <div hidden={step !== 4}>
            <h3>静态预览与提交</h3>
            <button className="button" onClick={() => setStep(3)}>
              上一步
            </button>
            <Field
              label="任务预算（JSON）"
              hint="保留历史任务预算；留空对象时由服务端使用默认预算。"
            >
              <textarea
                rows={3}
                value={budgets}
                onChange={(e) => changed(() => setBudgets(e.target.value))}
              />
            </Field>
            <button
              className="button primary"
              disabled={
                !result ||
                busy ||
                document.state === 'ARCHIVED' ||
                (backend === 'native_ssh' && auth === 'password' && !password.trim())
              }
              onClick={() => void submit()}
            >
              {busy ? '正在提交…' : '接受并执行任务'}
            </button>
          </div>
        </fieldset>
        {error && <Notice tone="danger">{error}</Notice>}
        {submissionUncertain && (
          <Notice tone="warning">
            任务接受状态尚未确认。配置已冻结；重新输入凭据后会以原幂等键重试。离开向导将要求显式放弃该提交意图。
          </Notice>
        )}
        {runId && (
          <>
            <Notice>
              任务 {runId} · {runStatus || '等待事件'}
              {report ? ` · ${report.run_status} / ${report.verdict}` : ''}
              {' · '}
              {eventConnection === 'live'
                ? '实时连接正常'
                : eventConnection === 'stale'
                  ? '连接已中断'
                  : eventConnection === 'reconnecting'
                    ? '正在重连'
                    : eventConnection === 'connecting'
                      ? '正在连接'
                      : '尚未连接'}
            </Notice>
            {eventConnection === 'stale' && (
              <button className="button" onClick={() => connectEvents(runId)}>
                重新连接任务进度
              </button>
            )}
            {stageTransitions.length > 0 ? (
              <ul className="host-phase-list">
                {stageTransitions.map((transition) => (
                  <li
                    key={`${transition.sequence}/${transition.host_id}/${transition.execution_mode ?? ''}/${transition.phase}`}
                  >
                    <strong>{transition.host_id}</strong>
                    {transition.execution_mode ? ` · ${transition.execution_mode}` : ''} ·{' '}
                    {transition.phase}
                    {transition.omitted
                      ? ` · ${transition.reason ?? '未执行'}`
                      : ` · 尝试 ${transition.attempt}`}
                    {transition.started_at && ` · 开始 ${transition.started_at}`}
                    {transition.ended_at && ` · 结束 ${transition.ended_at}`}
                    {transition.started_at && transition.ended_at && (
                      <>
                        {' · 用时 '}
                        {Math.max(
                          0,
                          Date.parse(transition.ended_at) - Date.parse(transition.started_at),
                        )}
                        ms
                      </>
                    )}
                  </li>
                ))}
              </ul>
            ) : hostPhases.length > 0 ? (
              <ul className="host-phase-list">
                {hostPhases.map((phase) => (
                  <li key={`${phase.host_id}/${phase.execution_mode ?? ''}`}>
                    <strong>{phase.host_id}</strong>
                    {phase.execution_mode ? ` · ${phase.execution_mode}` : ''} · {phase.phase}
                  </li>
                ))}
              </ul>
            ) : null}
          </>
        )}
        {report && (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>主机</th>
                  <th>任务状态</th>
                  <th>检查项结果</th>
                </tr>
              </thead>
              <tbody>
                {report.hosts.map((host) => (
                  <tr key={host.host_id}>
                    <td>{host.host_id}</td>
                    <td>{host.status}</td>
                    <td>
                      {host.item_results
                        .map((item) => `${item.item_id}: ${item.status}`)
                        .join(' · ')}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {result && step === 4 && (
          <>
            <p>
              目标主机：{result.host_count} ·{' '}
              <span>候选检查项：{result.candidate_check_count}</span> · 特权检查项：
              {result.privileged_check_count} · 自定义 Shell：{result.custom_shell_check_count} ·
              主动探测：{result.active_probe_check_count}
            </p>
            {result.configured_root_hosts.length > 0 && (
              <Notice tone="warning">
                配置为 root 登录：{result.configured_root_hosts.join('、')}。实际登录身份尚未验证。
              </Notice>
            )}
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>主机</th>
                    <th>地址</th>
                    <th>角色</th>
                  </tr>
                </thead>
                <tbody>
                  {result.hosts.map((entry) => (
                    <tr key={entry.host.id}>
                      <td>{entry.host.id}</td>
                      <td>
                        {entry.host.address}:{entry.host.port}
                      </td>
                      <td>{entry.roles.join('、')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </section>
  )
}
