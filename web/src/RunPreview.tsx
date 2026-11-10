import { useEffect, useRef, useState } from 'react'
import { stringify } from 'yaml'
import { ApiError, api } from './api'
import { Field, Notice } from './components'
import { randomSuffix, type Plan, type SavedPlan } from './model'
import { focusInvalidField, inventoryExamples } from './inputGuidance'
import {
  CheckSummary,
  FailureSummary,
  executionLabel,
  verdictLabel,
  useDisplayMetadata,
  type DisplayResult,
} from './reportPresentation'
import type { Diagnostic } from './api'

interface PreviewResult {
  host_count: number
  candidate_check_count: number
  privileged_check_count: number
  custom_shell_check_count: number
  active_probe_check_count: number
  configured_root_hosts: string[]
  identity_unverified_hosts: string[]
  validated_plan: Plan
  hosts: {
    host: { id: string; address: string; port: number; username?: string }
    roles: string[]
  }[]
}

interface RunReport {
  run_id: string
  run_status: string
  verdict: string
  termination_reason?: string
  hosts: {
    host_id: string
    status: string
    item_results: DisplayResult[]
    failures?: { code: string; diagnostics?: Diagnostic[] }[]
  }[]
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
  sshHostKeyPolicy,
  planRecordKey,
  rerunConfig,
  selectedPlanKey,
  plans,
  onSelectPlan,
  onSubmissionUncertain,
}: {
  document: Plan
  sshHostKeyPolicy?: 'record-only' | 'strict'
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
  const [submissionDiagnostic, setSubmissionDiagnostic] = useState<ApiError | null>(null)
  const [report, setReport] = useState<RunReport | null>(null)
  const [step, setStep] = useState(1)
  const revision = useRef(0)
  const documentRef = useRef(document)
  if (documentRef.current !== document) {
    documentRef.current = document
    revision.current++
  }
  const [bindingAttempted, setBindingAttempted] = useState(false)
  const [credentialAttempted, setCredentialAttempted] = useState(false)
  const { metadata, error: metadataError } = useDisplayMetadata(runId)
  const roleMap = Object.fromEntries(
    document.roles.map((role) => [
      role.role_id,
      (bindings[role.role_id] ?? '')
        .split(',')
        .map((id) => id.trim())
        .filter(Boolean),
    ]),
  )
  const knownIDs =
    parsedInventory?.hosts.map((host) => host.id) ??
    (format === 'quick'
      ? source
          .split(',')
          .map((entry) => entry.trim().split('=')[0])
          .filter(Boolean)
      : null)
  const unknownIDs = knownIDs
    ? [
        ...new Set(
          Object.values(roleMap)
            .flat()
            .filter((id) => !knownIDs.includes(id)),
        ),
      ]
    : []
  const bindingError =
    Object.values(roleMap).flat().length === 0
      ? '请至少绑定一台目标主机；清单存在主机不等于已绑定角色。'
      : unknownIDs.length
        ? `角色引用了清单中不存在的主机：${unknownIDs.join('、')}。请同时检查主机清单和角色绑定。`
        : ''
  const missingUser =
    backend === 'native_ssh' && result?.hosts.some(({ host }) => !host.username?.trim())
  const credentialError =
    backend !== 'native_ssh'
      ? ''
      : missingUser
        ? '目标主机缺少 SSH 登录用户名，请返回主机清单，在 Ansible INI/YAML 中填写 ansible_user。'
        : auth === 'password' && !password
          ? '请填写 SSH 密码。'
          : auth === 'private_key' && !privateKey.trim()
            ? '请填写 SSH 私钥内容。'
            : auth === 'agent' && (!agentSocket.trim() || !keyFingerprint.trim())
              ? '请填写 Agent socket 和选定公钥指纹。'
              : ''
  const eventsController = useRef<AbortController | null>(null)
  const eventCursor = useRef('')
  const reconnectEvents = useRef<() => void>(() => undefined)
  const pendingIdempotencyKey = useRef('')
  useEffect(() => {
    try {
      pendingIdempotencyKey.current = sessionStorage.getItem('readyspec.pending-run-key') ?? ''
      if (pendingIdempotencyKey.current) {
        setSubmissionUncertain(true)
        onSubmissionUncertain?.(true)
      }
    } catch {
      // Private browsing/storage policy may disable session storage.
    }
    return () => eventsController.current?.abort()
  }, [onSubmissionUncertain])
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
          const loaded = await api.request<RunReport>(`/runs/${id}/report`, undefined, {
            signal: controller.signal,
          })
          if (!controller.signal.aborted) setReport(loaded)
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
    setResult(null)
  }, [document])
  useEffect(() => {
    if (!rerunConfig) return
    revision.current++
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
    revision.current++
    setResult(null)
    setError('')
    pendingIdempotencyKey.current = ''
    try {
      sessionStorage.removeItem('readyspec.pending-run-key')
    } catch {
      // Ignore unavailable session storage.
    }
  }
  async function preview() {
    setBindingAttempted(true)
    if (bindingError) {
      setError(bindingError)
      window.setTimeout(focusInvalidField, 0)
      return
    }
    const requestRevision = revision.current
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
      if (revision.current !== requestRevision) return
      if (validated.host_count === 0) throw new Error('请至少绑定一台目标主机。')
      setResult(validated)
      setStep(4)
    } catch (err) {
      if (revision.current === requestRevision)
        setError(
          err instanceof ApiError && err.diagnostics.length
            ? err.diagnostics.map((item) => `${item.item_id ?? ''} ${item.message}`).join('；')
            : err instanceof Error
              ? err.message
              : '静态预览失败',
        )
    } finally {
      setBusy(false)
    }
  }
  async function parseInventory() {
    const requestRevision = revision.current
    setBusy(true)
    setError('')
    try {
      const parsed = await api.request<ParsedInventory>('/inventory/parse', { source, format })
      if (revision.current !== requestRevision) return
      setParsedInventory(parsed)
    } catch (cause) {
      if (revision.current === requestRevision) {
        setParsedInventory(null)
        setError(cause instanceof Error ? cause.message : '主机清单解析失败')
      }
    } finally {
      setBusy(false)
    }
  }
  async function submit() {
    if (!result) return
    setCredentialAttempted(true)
    if (credentialError || bindingError) {
      setError(credentialError || bindingError)
      setStep(missingUser ? 2 : 3)
      window.setTimeout(focusInvalidField, 0)
      return
    }
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
      const accepted = await api.request<{ run_id?: string; status?: string }>('/runs', {
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
        idempotency_key: currentSubmissionKey(),
        auth_identity: {
          method: backend === 'mock_simulator' ? 'offline' : auth,
          ...(auth === 'agent' ? { public_key_fingerprint: keyFingerprint } : {}),
        },
        credentials,
        ...(backend === 'mock_simulator' ? { simulator_profiles: JSON.parse(profiles) } : {}),
      })
      if (typeof accepted.run_id !== 'string' || accepted.run_id.length === 0) {
        throw new ApiError(
          '服务未返回有效任务 ID，提交接受状态尚未确认。',
          502,
          [],
          '',
          '',
          'unknown',
        )
      }
      setRunId(accepted.run_id)
      setRunStatus(accepted.status ?? '已接受')
      setSubmissionUncertain(false)
      setSubmissionDiagnostic(null)
      onSubmissionUncertain?.(false)
      setHostPhases([])
      setPassword('')
      setSudoPassword('')
      setPrivateKey('')
      setPassphrase('')
      setAgentSocket('')
      setKeyFingerprint('')
      pendingIdempotencyKey.current = ''
      try {
        sessionStorage.removeItem('readyspec.pending-run-key')
      } catch {
        // Ignore unavailable session storage.
      }
      connectEvents(accepted.run_id, true)
    } catch (cause) {
      setPassword('')
      setSudoPassword('')
      setPrivateKey('')
      setPassphrase('')
      setAgentSocket('')
      setKeyFingerprint('')
      const uncertain = !(cause instanceof ApiError) || cause.acceptance !== 'rejected'
      setSubmissionUncertain(uncertain)
      setSubmissionDiagnostic(cause instanceof ApiError ? cause : null)
      onSubmissionUncertain?.(uncertain)
      setError(cause instanceof Error ? cause.message : '任务提交失败')
    } finally {
      setBusy(false)
    }
  }
  async function checkSubmissionStatus() {
    if (!pendingIdempotencyKey.current) return
    setBusy(true)
    try {
      const result = await api.request<{
        acceptance: 'accepted' | 'rejected' | 'unknown'
        run_id?: string
        status?: string
        recovery_error?: string
      }>('/runs/submission-status', { idempotency_key: pendingIdempotencyKey.current })
      if (result.acceptance === 'accepted' && result.run_id) {
        setRunId(result.run_id)
        setRunStatus(result.status ?? '已接受')
        setSubmissionUncertain(false)
        onSubmissionUncertain?.(false)
        pendingIdempotencyKey.current = ''
        try {
          sessionStorage.removeItem('readyspec.pending-run-key')
        } catch {
          // Ignore unavailable session storage.
        }
        connectEvents(result.run_id, true)
      } else if (result.acceptance === 'rejected') {
        setSubmissionUncertain(false)
        onSubmissionUncertain?.(false)
        setError('服务端确认该提交未被接受。可检查凭据后使用原提交键再次提交。')
      } else {
        setError('服务端暂时无法确认提交状态，请稍后再次查询。')
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '提交状态查询失败')
    } finally {
      setBusy(false)
    }
  }
  function currentSubmissionKey() {
    if (pendingIdempotencyKey.current) return pendingIdempotencyKey.current
    pendingIdempotencyKey.current = `web-${randomSuffix(32)}`
    try {
      sessionStorage.setItem('readyspec.pending-run-key', pendingIdempotencyKey.current)
    } catch {
      // The request still remains idempotent for this mounted component.
    }
    return pendingIdempotencyKey.current
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
            <Field
              label="主机清单"
              required
              hint={
                format === 'quick'
                  ? '每台填写 主机标识=地址[:端口]，以英文逗号分隔。快速主机表不支持用户名，请使用 Ansible INI/YAML。主机名须能从管理端解析。'
                  : '主机名用作角色绑定标识；ansible_host 为地址，ansible_user 为 SSH 登录用户，ansible_port 默认 22。'
              }
              error={
                credentialAttempted && missingUser
                  ? credentialError
                  : bindingAttempted && unknownIDs.length
                    ? bindingError
                    : ''
              }
            >
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
            <details className="inventory-example">
              <summary>填写示例（{format}）</summary>
              <pre>{inventoryExamples[format]}</pre>
              <button
                type="button"
                className="button"
                onClick={() => void navigator.clipboard?.writeText(inventoryExamples[format])}
              >
                复制示例
              </button>
            </details>
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
                <ul>
                  {parsedInventory.hosts.map((host) => (
                    <li key={host.id}>
                      {host.id} · {host.address}:{host.port} · SSH 用户：
                      {host.username || '未设置，请用 INI/YAML 配置'}
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
            <Notice>
              {sshHostKeyPolicy === 'record-only'
                ? '当前服务仅记录首次连接的 SSH 主机指纹，不依赖 known_hosts。该指纹不是可信身份认证；执行重连和清理仍须匹配已记录的指纹。'
                : sshHostKeyPolicy === 'strict'
                  ? '当前服务启用严格模式：使用管理端账户的 known_hosts 校验主机密钥，与浏览器所在电脑无关。未知密钥需核对后登记，变化或吊销时停止并调查。'
                  : '服务未声明主机密钥策略，连接采用服务端配置；页面与服务连接正常不代表目标身份已验证。'}
            </Notice>
            {document.roles.map((role) => (
              <Field
                key={role.role_id}
                label={`绑定主机 · ${role.role_id}`}
                required={scope === 'FULL' && role.min_nodes > 0}
                hint={`填写清单中的主机标识，以英文逗号分隔。方案最少节点 ${role.min_nodes}；局部复测不受原最少数量限制。`}
                error={bindingAttempted ? bindingError : ''}
              >
                <input
                  value={bindings[role.role_id] ?? ''}
                  list="parsed-hosts"
                  onChange={(e) =>
                    changed(() =>
                      setBindings((previous) => ({ ...previous, [role.role_id]: e.target.value })),
                    )
                  }
                />
              </Field>
            ))}
            <datalist id="parsed-hosts">
              {knownIDs?.map((id) => (
                <option key={id} value={id} />
              ))}
            </datalist>
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
                  <Field
                    label="SSH 密码"
                    required
                    error={credentialAttempted && !password ? '请填写 SSH 密码。' : ''}
                  >
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
                    <Field
                      label="SSH 私钥内容"
                      required
                      error={
                        credentialAttempted && !privateKey.trim() ? '请填写 SSH 私钥内容。' : ''
                      }
                    >
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
                    <Field
                      label="本机 Agent socket"
                      required
                      error={
                        credentialAttempted && !agentSocket.trim() ? '请填写 Agent socket。' : ''
                      }
                    >
                      <input value={agentSocket} onChange={(e) => setAgentSocket(e.target.value)} />
                    </Field>
                    <Field
                      label="选定公钥指纹"
                      required
                      error={
                        credentialAttempted && !keyFingerprint.trim() ? '请填写公钥指纹。' : ''
                      }
                    >
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
        {submissionDiagnostic && (
          <section className="panel" aria-label="提交诊断">
            <strong>
              {submissionDiagnostic.acceptance === 'rejected'
                ? '任务未接受'
                : submissionDiagnostic.acceptance === 'accepted'
                  ? '任务已接受'
                  : '任务接受状态未知'}
            </strong>
            {submissionDiagnostic.diagnostics.map((item, index) => (
              <p key={`${item.code}-${index}`}>
                {item.host_id ? `${item.host_id}：` : ''}
                {item.message}
                {item.action ? `（建议：${item.action}）` : ''}
              </p>
            ))}
            {(submissionDiagnostic.requestId || submissionDiagnostic.submissionId) && (
              <p>
                诊断 ID：{submissionDiagnostic.requestId || '无'} · 提交 ID：
                {submissionDiagnostic.submissionId || '无'}
              </p>
            )}
            <button
              type="button"
              className="button"
              onClick={() => {
                const lines = [
                  `request_id=${submissionDiagnostic.requestId || 'unavailable'}`,
                  `submission_id=${submissionDiagnostic.submissionId || 'unavailable'}`,
                  `acceptance=${submissionDiagnostic.acceptance || 'unknown'}`,
                  ...submissionDiagnostic.diagnostics.map(
                    (item) =>
                      `${item.code}${item.host_id ? ` host_id=${item.host_id}` : ''}: ${item.message}`,
                  ),
                ]
                void navigator.clipboard?.writeText(lines.join('\n'))
              }}
            >
              复制诊断摘要
            </button>
          </section>
        )}
        {submissionUncertain && (
          <Notice tone="warning">
            任务接受状态尚未确认。先查询原提交状态；不会自动重新提交。离开向导将要求显式放弃该提交意图。
            <button
              type="button"
              className="button"
              disabled={busy}
              onClick={() => void checkSubmissionStatus()}
            >
              查询提交状态
            </button>
          </Notice>
        )}
        {runId && (
          <>
            <Notice>
              任务 {runId} · {executionLabel(runStatus) || '等待事件'}
              {report
                ? ` · ${report.run_status} / ${report.verdict} · ${verdictLabel(report.verdict)}`
                : ''}
              {' · '}
              {eventConnection === 'live'
                ? '页面与服务连接正常'
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
            {metadataError && <Notice tone="warning">{metadataError}</Notice>}
            <p>
              任务执行：{executionLabel(report.run_status)} · 检查结论：
              {verdictLabel(report.verdict)}。执行完成不等于检查通过。
            </p>
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
                    <td>
                      {executionLabel(host.status)}（{host.status}）
                      <FailureSummary
                        failures={host.failures}
                        empty={host.item_results.length === 0}
                      />
                    </td>
                    <td>
                      {host.item_results.map((item) => (
                        <CheckSummary
                          key={`${item.role_id}/${item.item_id}`}
                          item={item}
                          metadata={metadata}
                        />
                      ))}
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
