import { useState } from 'react'
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

export function RunPreview({ document }: { document: Plan }) {
  const [source, setSource] = useState('')
  const [format, setFormat] = useState('quick')
  const [bindings, setBindings] = useState<Record<string, string>>({})
  const [overrides, setOverrides] = useState('[]')
  const [result, setResult] = useState<PreviewResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
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
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>主机绑定与静态预览</h2>
      </div>
      <div className="modal-body">
        <p>
          当前方案：{document.name || '未命名方案'}
          。在方案工作台打开或编辑方案后，可在这里预览主机与检查项。
        </p>
        <Notice>静态预览不连接目标主机。实际身份、凭据、sudo 能力与环境就绪状态均未验证。</Notice>
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
          <button className="button" disabled={!source.trim()} onClick={() => void preview()}>
            {busy ? '正在生成预览…' : '生成静态预览'}
          </button>
        </fieldset>
        {error && <Notice tone="danger">{error}</Notice>}
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
