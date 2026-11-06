import { useEffect, useRef, useState } from 'react'
import { stringify } from 'yaml'
import { api } from './api'
import { Field, Notice } from './components'
import type { Plan } from './model'

interface PreviewResult {
  host_count: number
  candidate_check_count: number
  privileged_check_count: number
  custom_shell_check_count: number
  active_probe_check_count: number
  configured_root_hosts: string[]
  identity_unverified_hosts: string[]
  hosts: { host: { id: string; address: string; port: number }; roles: string[] }[]
}

interface RunReport {
  run_id: string
  run_status: string
  verdict: string
  termination_reason?: string
  hosts: { host_id: string; status: string; item_results: { item_id: string; status: string }[] }[]
}

export function RunPreview({ document }: { document: Plan }) {
  const [source, setSource] = useState('')
  const [format, setFormat] = useState('quick')
  const [bindings, setBindings] = useState<Record<string, string>>({})
  const [overrides, setOverrides] = useState('[]')
  const [result, setResult] = useState<PreviewResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [mode, setMode] = useState('GATE')
  const [scope, setScope] = useState('FULL')
  const [backend, setBackend] = useState('native_ssh')
  const [auth, setAuth] = useState('password')
  const [password, setPassword] = useState('')
  const [sudoPassword, setSudoPassword] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [agentSocket, setAgentSocket] = useState('')
  const [keyFingerprint, setKeyFingerprint] = useState('')
  const [profiles, setProfiles] = useState('{"hosts":{}}')
  const [runId, setRunId] = useState('')
  const [runStatus, setRunStatus] = useState('')
  const [report, setReport] = useState<RunReport | null>(null)
  const eventsController = useRef<AbortController | null>(null)
  useEffect(() => () => eventsController.current?.abort(), [])
  function changed(update: () => void) {
    update()
    setResult(null)
    setError('')
  }
  async function preview() {
    setResult(null)
    setError('')
    setBusy(true)
    try {
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
      setResult(
        await api.request<PreviewResult>('/runs/preview', {
          source: stringify(document),
          normalize_draft: true,
          inventory: { source, format },
          role_map: roleMap,
          field_overrides: fields,
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : '静态预览失败')
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
      const credentials = Object.fromEntries(result.hosts.map(({ host }) => [
        host.id,
        backend === 'mock_simulator'
          ? {}
          : auth === 'password'
            ? { password, sudo_password: sudoPassword }
            : auth === 'private_key'
              ? { private_key: privateKey, sudo_password: sudoPassword }
              : { agent_socket: agentSocket, sudo_password: sudoPassword },
      ]))
      const accepted = await api.request<{ run_id: string; status: string }>('/runs', {
        plan: stringify(document),
        inventory: { source, format },
        role_map: roleMap,
        field_overrides: JSON.parse(overrides),
        run_mode: mode,
        run_scope: scope,
        executor_backend: backend,
        idempotency_key: `web-${crypto.randomUUID()}`,
        auth_identity: {
          method: backend === 'mock_simulator' ? 'offline' : auth,
          ...(auth === 'agent' ? { public_key_fingerprint: keyFingerprint } : {}),
        },
        credentials,
        ...(backend === 'mock_simulator' ? { simulator_profiles: JSON.parse(profiles) } : {}),
      })
      setRunId(accepted.run_id)
      setRunStatus(accepted.status)
      setPassword('')
      setSudoPassword('')
      setPrivateKey('')
      setAgentSocket('')
      setKeyFingerprint('')
      const controller = new AbortController()
      eventsController.current = controller
      const fetchReport = () => {
        void api.request<RunReport>(`/runs/${accepted.run_id}/report`).then(setReport).catch((cause) => {
          setError(cause instanceof Error ? cause.message : '报告读取失败')
        })
      }
      void api.subscribe(`/runs/${accepted.run_id}/events`, undefined, controller.signal, (event) => {
        const data = event.data as { run?: { status?: string }; status?: string }
        const status = data.run?.status ?? data.status
        if (status) {
          setRunStatus(status)
          if (['COMPLETED', 'PARTIAL', 'FAILED', 'CANCELLED'].includes(status)) fetchReport()
        }
        if (event.name === 'TERMINAL_ARCHIVED') fetchReport()
      }).catch((cause) => {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : '事件订阅失败')
      })
    } catch (cause) {
      setPassword('')
      setPrivateKey('')
      setError(cause instanceof Error ? cause.message : '任务提交失败')
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>主机绑定、静态预览与执行</h2>
      </div>
      <div className="modal-body">
        <p>
          当前方案：{document.name || '未命名方案'}
          。在方案工作台打开或编辑方案后，可在这里预览主机与检查项。
        </p>
        <Notice>静态预览不连接目标主机。只有提交任务后才进行 SSH 身份、凭据、权限与环境预检。</Notice>
        <fieldset disabled={busy}>
          <Field label="清单格式">
            <select value={format} onChange={(e) => changed(() => setFormat(e.target.value))}>
              <option value="quick">快速主机表</option>
              <option value="ini">Ansible INI</option>
              <option value="yaml">Ansible YAML</option>
            </select>
          </Field>
          <Field label="主机清单" hint="快速格式：node=192.0.2.1；多个主机以逗号分隔。">
            <textarea
              rows={5}
              value={source}
              onChange={(e) => changed(() => setSource(e.target.value))}
            />
          </Field>
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
              <select value={mode} onChange={(e) => setMode(e.target.value)}>
                <option value="GATE">正式门禁</option><option value="TRIAL">草稿试跑</option>
              </select>
            </Field>
            <Field label="执行范围">
              <select value={scope} onChange={(e) => setScope(e.target.value)}>
                <option value="FULL">全量检查</option><option value="PARTIAL">局部复测</option>
              </select>
            </Field>
          </div>
          <Field label="执行器">
            <select value={backend} onChange={(e) => setBackend(e.target.value)}>
              <option value="native_ssh">原生 SSH</option><option value="mock_simulator">离线模拟器</option>
            </select>
          </Field>
          {backend === 'native_ssh' ? (
            <>
              <Field label="SSH 认证方式">
                <select value={auth} onChange={(e) => setAuth(e.target.value)}>
                  <option value="password">密码</option><option value="private_key">私钥材料</option><option value="agent">本机 SSH Agent</option>
                </select>
              </Field>
              {auth === 'password' && <Field label="SSH 密码"><input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} /></Field>}
              {auth === 'private_key' && <Field label="SSH 私钥内容"><textarea rows={5} autoComplete="off" value={privateKey} onChange={(e) => setPrivateKey(e.target.value)} /></Field>}
              {result && result.privileged_check_count > 0 && <Field label="sudo 密码（仅在目标要求时）"><input type="password" autoComplete="new-password" value={sudoPassword} onChange={(e) => setSudoPassword(e.target.value)} /></Field>}
              {auth === 'agent' && <>
                <Field label="本机 Agent socket"><input value={agentSocket} onChange={(e) => setAgentSocket(e.target.value)} /></Field>
                <Field label="选定公钥指纹"><input value={keyFingerprint} onChange={(e) => setKeyFingerprint(e.target.value)} placeholder="SHA256:…" /></Field>
              </>}
              <Notice>接受任务前，服务会取得所需凭据或 Agent 连接。私钥材料只进入任务内存，不写入工作区。</Notice>
            </>
          ) : (
            <Field label="模拟器画像（JSON）"><textarea rows={6} value={profiles} onChange={(e) => setProfiles(e.target.value)} /></Field>
          )}
          <button className="button" disabled={!source.trim()} onClick={() => void preview()}>
            {busy ? '正在生成预览…' : '生成静态预览'}
          </button>
          <button className="button primary" disabled={!result || busy || (backend === 'native_ssh' && auth === 'password' && !password.trim())} onClick={() => void submit()}>
            {busy ? '正在提交…' : '接受并执行任务'}
          </button>
        </fieldset>
        {error && <Notice tone="danger">{error}</Notice>}
        {runId && <Notice>任务 {runId} · {runStatus || '等待事件'}{report ? ` · ${report.run_status} / ${report.verdict}` : ''}</Notice>}
        {report && <div className="table-scroll"><table><thead><tr><th>主机</th><th>任务状态</th><th>检查项结果</th></tr></thead><tbody>{report.hosts.map((host) => <tr key={host.host_id}><td>{host.host_id}</td><td>{host.status}</td><td>{host.item_results.map((item) => `${item.item_id}: ${item.status}`).join(' · ')}</td></tr>)}</tbody></table></div>}
        {result && (
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
