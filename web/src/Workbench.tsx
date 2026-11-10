import { useEffect, useState } from 'react'
import {
  Check,
  CheckCheck,
  ChevronDown,
  Code2,
  Copy,
  Download,
  FileCheck2,
  FilePlus2,
  Plus,
  Save,
  Search,
  Server,
  Settings2,
  Trash2,
  Undo2,
  Upload,
} from 'lucide-react'
import { Badge, Empty, Field, Modal, Notice, Spinner } from './components'
import {
  itemCount,
  randomSuffix,
  type CheckItem,
  type CheckTemplate,
  type Importance,
  type Plan,
  type Role,
  type SavedPlan,
} from './model'
import { Templates } from './Templates'
import { api, ApiError, type Diagnostic } from './api'
import {
  capacityHint,
  focusInvalidField,
  inputIssue,
  requiredParameters,
  type Property,
} from './inputGuidance'

interface Props {
  document: Plan
  draftKey: string | null
  selectedIssue?: { role_id?: string; item_id?: string; field?: string; message?: string } | null
  plans: SavedPlan[]
  dirty: boolean
  digest: string
  busy: boolean
  saveEpoch: number
  mutate: (edit: (p: Plan) => void) => void
  onNew: () => void
  onImport: () => void
  onOpen: (p: SavedPlan) => void
  onAction: (action: 'validate' | 'save' | 'export' | 'compile') => void
  onRelease: () => void
  onArchive: () => void
  onNewVersion: () => void
  onDeleteDraft: () => void
}

type DeletionUndo = { templates: CheckTemplate[]; index: number } & (
  { kind: 'role'; role: Role } | { kind: 'item'; roleID: string; item: CheckItem }
)

export function Workbench({
  document: plan,
  draftKey,
  selectedIssue,
  plans,
  dirty,
  digest,
  busy,
  saveEpoch,
  mutate,
  onNew,
  onOpen,
  onImport,
  onAction,
  onRelease,
  onArchive,
  onNewVersion,
  onDeleteDraft,
}: Props) {
  const [roleID, setRoleID] = useState('')
  const [itemID, setItemID] = useState('')
  const [query, setQuery] = useState('')
  const [catalog, setCatalog] = useState(false)
  const [deleting, setDeleting] = useState<'role' | 'item' | null>(null)
  const [undo, setUndo] = useState<DeletionUndo | null>(null)
  const [validateAll, setValidateAll] = useState(false)
  const [validationRequested, setValidationRequested] = useState(false)
  const validationKey = JSON.stringify(plan)
  const [parameterValidation, setParameterValidation] = useState<{
    key: string
    pending: boolean
    issue?: Diagnostic
    error?: string
  }>({ key: '', pending: false })
  const validationPending =
    validationRequested &&
    plan.state === 'DRAFT' &&
    (parameterValidation.key !== validationKey || parameterValidation.pending)
  useEffect(() => {
    if (!validationRequested || plan.state !== 'DRAFT') return
    const controller = new AbortController()
    setParameterValidation({ key: validationKey, pending: true })
    const timer = window.setTimeout(() => {
      void api
        .request(
          '/plans/validate',
          { source: validationKey, normalize_draft: true },
          { signal: controller.signal },
        )
        .then(() => {
          if (!controller.signal.aborted)
            setParameterValidation({ key: validationKey, pending: false })
        })
        .catch((cause) => {
          if (!controller.signal.aborted)
            setParameterValidation({
              key: validationKey,
              pending: false,
              issue: cause instanceof ApiError ? cause.diagnostics[0] : undefined,
              error: '参数校验未通过，请检查字段提示或手动校验草稿。',
            })
        })
    }, 400)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [validationKey, validationRequested, plan.state])
  const serverIssue =
    parameterValidation.key === validationKey ? parameterValidation.issue : undefined
  function guardAction(action: () => void) {
    setValidateAll(true)
    if (serverIssue?.item_id) {
      setRoleID(serverIssue.role_id ?? '')
      setItemID(serverIssue.item_id)
      window.setTimeout(focusInvalidField, 0)
      return
    }
    if (
      [plan.name, plan.plan_id, plan.version, plan.software_name, plan.software_version].some(
        (value) => !value.trim(),
      )
    ) {
      window.setTimeout(focusInvalidField, 0)
      return
    }
    for (const candidateRole of plan.roles)
      for (const candidate of candidateRole.items) {
        const snapshot = plan.embedded_templates.find(
          (entry) =>
            entry.template_id === candidate.template_id &&
            entry.version === candidate.template_version,
        )
        if (!snapshot) continue
        const properties = (snapshot.params_schema.properties ?? {}) as Record<string, Property>
        const values = Object.fromEntries(
          Object.entries(properties).map(([key, property]) => [
            key,
            candidate.explicit_null_params.includes(key)
              ? null
              : Object.hasOwn(candidate.params, key)
                ? candidate.params[key]
                : property.default,
          ]),
        )
        const required = requiredParameters(snapshot.params_schema, values)
        if (
          Object.entries(properties).some(([key, property]) =>
            inputIssue(property, values[key], required.has(key)),
          )
        ) {
          setRoleID(candidateRole.role_id)
          setItemID(candidate.item_id)
          window.setTimeout(focusInvalidField, 0)
          return
        }
      }
    action()
  }
  useEffect(() => {
    if (!selectedIssue) return
    if (selectedIssue.role_id) setRoleID(selectedIssue.role_id)
    if (selectedIssue.item_id) setItemID(selectedIssue.item_id)
  }, [selectedIssue])
  useEffect(() => setUndo(null), [saveEpoch])
  const role = plan.roles.find((r) => r.role_id === roleID) ?? plan.roles[0]
  const item = role?.items.find((i) => i.item_id === itemID)
  const template = plan.embedded_templates.find(
    (t) => t.template_id === item?.template_id && t.version === item?.template_version,
  )
  const editable = plan.state === 'DRAFT'
  const shown =
    role?.items.filter((i) =>
      `${i.name} ${i.item_id} ${i.template_id}`.toLowerCase().includes(query.toLowerCase()),
    ) ?? []
  const updateRole = (fn: (r: Role) => void) =>
    mutate((p) => {
      const r = p.roles.find((r) => r.role_id === role?.role_id)
      if (r) fn(r)
    })
  const updateItem = (fn: (i: CheckItem, t: CheckTemplate | undefined) => void) =>
    updateRole((r) => {
      const i = r.items.find((i) => i.item_id === itemID)
      if (i) fn(i, undefined)
    })

  function addShell() {
    if (!role) return
    const suffix = randomSuffix(8)
    const id = `check-${suffix}`
    mutate((p) => {
      const r = p.roles.find((r) => r.role_id === role.role_id)!
      p.embedded_templates.push({
        schema_version: 'readyspec.template/v1',
        template_id: `shell-${suffix}`,
        version: '1.0.0',
        name: '自定义 Shell',
        source: 'PLAN_LOCAL',
        domain: 'R',
        execution_mode: 'STANDARD',
        evidence_mode: 'ACTIVE_PROBE',
        params_schema: { type: 'object', properties: {} },
        implementation: { kind: 'CUSTOM_SHELL', command: 'test -d /data', expect_exit_code: 0 },
        digest: '',
      })
      r.items.push({
        item_id: id,
        name: '自定义 Shell 检查',
        template_id: `shell-${suffix}`,
        template_version: '1.0.0',
        template_digest: '',
        importance: 'REQUIRED',
        enabled: true,
        params: {},
        explicit_null_params: [],
        overridable_params: [],
      })
    })
    setItemID(id)
  }
  function addBuiltin(snapshot: CheckTemplate) {
    if (!role || !editable) return
    const id = `check-${randomSuffix(8)}`
    mutate((p) => {
      if (
        !p.embedded_templates.some(
          (t) => t.template_id === snapshot.template_id && t.version === snapshot.version,
        )
      )
        p.embedded_templates.push(structuredClone(snapshot))
      p.template_lock[`${snapshot.template_id}@${snapshot.version}`] = snapshot.digest
      p.roles
        .find((r) => r.role_id === role.role_id)!
        .items.push({
          item_id: id,
          name: snapshot.name,
          template_id: snapshot.template_id,
          template_version: snapshot.version,
          template_digest: snapshot.digest,
          importance: 'REQUIRED',
          enabled: true,
          params: {},
          explicit_null_params: [],
          overridable_params: [],
        })
    })
    setItemID(id)
    setCatalog(false)
  }
  function copyItem(source: CheckItem) {
    const suffix = randomSuffix(8),
      id = `check-${suffix}`
    mutate((p) => {
      const r = p.roles.find((r) => r.role_id === role.role_id)!
      const copy = structuredClone(source)
      copy.item_id = id
      copy.name += '（副本）'
      const original = p.embedded_templates.find(
        (t) => t.template_id === source.template_id && t.version === source.template_version,
      )
      if (original && original.source === 'PLAN_LOCAL') {
        const own = structuredClone(original)
        own.template_id = `shell-${suffix}`
        own.digest = ''
        p.embedded_templates.push(own)
        copy.template_id = own.template_id
        copy.template_digest = ''
      }
      r.items.push(copy)
    })
    setItemID(id)
  }
  function remove() {
    if (!role || (deleting === 'item' && !item)) return
    const deletedItems = deleting === 'role' ? role.items : [item!]
    const affected = new Set(deletedItems.map((i) => `${i.template_id}@${i.template_version}`))
    const remaining = new Set(
      plan.roles.flatMap((r) =>
        r.items
          .filter((i) => !deletedItems.some((d) => d.item_id === i.item_id))
          .map((i) => `${i.template_id}@${i.template_version}`),
      ),
    )
    const templates = structuredClone(
      plan.embedded_templates.filter(
        (t) =>
          t.source === 'PLAN_LOCAL' &&
          affected.has(`${t.template_id}@${t.version}`) &&
          !remaining.has(`${t.template_id}@${t.version}`),
      ),
    )
    setUndo(
      deleting === 'role'
        ? {
            kind: 'role',
            role: structuredClone(role),
            index: plan.roles.findIndex((r) => r.role_id === role.role_id),
            templates,
          }
        : {
            kind: 'item',
            roleID: role.role_id,
            item: structuredClone(item!),
            index: role.items.findIndex((i) => i.item_id === item!.item_id),
            templates,
          },
    )
    mutate((p) => {
      if (deleting === 'role') p.roles = p.roles.filter((r) => r.role_id !== role?.role_id)
      else {
        const r = p.roles.find((r) => r.role_id === role?.role_id)
        if (r) r.items = r.items.filter((i) => i.item_id !== itemID)
      }
      p.embedded_templates = p.embedded_templates.filter(
        (t) =>
          !templates.some(
            (removed) => removed.template_id === t.template_id && removed.version === t.version,
          ),
      )
    })
    setDeleting(null)
    setItemID('')
  }

  return (
    <>
      <div className="page-heading">
        <div>
          <div className="eyebrow">PLANS & WORKBENCH</div>
          <h1>方案工作台</h1>
          <p>按节点角色编排检查项，定义可交付的环境要求。</p>
        </div>
        <div className="page-actions">
          <button className="button" onClick={onImport}>
            <Upload size={16} />
            导入
          </button>
          <button className="button" onClick={onNew}>
            <FilePlus2 size={16} />
            新建方案
          </button>
        </div>
      </div>
      <section className="plan-toolbar">
        <div className="plan-identity">
          <FilesIcon />
          <div>
            <strong>{plan.name || '未命名方案'}</strong>
            <span className="mono muted">
              {plan.plan_id}@{plan.version}
            </span>
          </div>
          <Badge status={plan.state} />
          {dirty && (
            <span className="unsaved">
              <span />
              未保存
            </span>
          )}
        </div>
        <div className="plan-toolbar-actions">
          <button
            className="button"
            onClick={() => guardAction(() => onAction('export'))}
            disabled={busy}
          >
            <Download size={15} />
            导出 YAML
          </button>
          <button
            className="button"
            onClick={() => guardAction(() => onAction('validate'))}
            disabled={busy}
          >
            {busy ? <Spinner /> : <CheckCheck size={16} />}
            {editable ? '校验草稿' : '校验方案'}
          </button>
          <button
            className="button"
            onClick={() => guardAction(() => onAction('compile'))}
            disabled={busy}
          >
            静态编译
          </button>
          {editable ? (
            <>
              <button
                className="button primary"
                onClick={() => guardAction(onRelease)}
                disabled={busy || validationPending || dirty || !plan.plan_id || !plan.version}
                title={dirty ? '先保存草稿，再发布该修订' : undefined}
              >
                发布版本
              </button>
              {draftKey && (
                <button className="button danger" onClick={onDeleteDraft} disabled={busy}>
                  删除草稿
                </button>
              )}
            </>
          ) : (
            <>
              <button className="button" onClick={onNewVersion} disabled={busy}>
                创建新版本草稿
              </button>
              {plan.state === 'RELEASED' && (
                <button className="button danger" onClick={onArchive} disabled={busy}>
                  停用版本
                </button>
              )}
            </>
          )}
          <button
            className="button primary"
            onClick={() => onAction('save')}
            disabled={busy || !editable}
          >
            <Save size={15} />
            保存草稿
          </button>
        </div>
      </section>
      {digest && (
        <div className="digest-line">
          <Check size={14} />
          <span>已校验摘要</span>
          <code title={digest}>{digest}</code>
        </div>
      )}
      {validationPending && <p role="status">正在校验参数；草稿仍可保存。</p>}
      {serverIssue && (
        <Notice tone="warning">
          {serverIssue.message}{' '}
          {serverIssue.item_id && (
            <button
              type="button"
              className="text-button"
              onClick={() => {
                setRoleID(serverIssue.role_id ?? '')
                setItemID(serverIssue.item_id ?? '')
              }}
            >
              定位检查项 {serverIssue.item_id}
            </button>
          )}
        </Notice>
      )}
      {parameterValidation.key === validationKey &&
        parameterValidation.error &&
        !serverIssue?.item_id && <Notice tone="warning">{parameterValidation.error}</Notice>}
      {!editable && (
        <Notice>此方案为只读状态。你可以校验或导出；如需调整，请创建新的草稿版本。</Notice>
      )}
      <section className="panel metadata-panel">
        <div className="panel-heading">
          <h2>方案信息</h2>
          {plans.length > 0 && (
            <select
              aria-label="打开已保存方案"
              value=""
              onChange={(e) => {
                const selected = plans.find((p) => p.key === e.target.value)
                if (selected) onOpen(selected)
              }}
            >
              <option value="">打开已保存方案…</option>
              {plans.map((p) => (
                <option value={p.key} key={p.key}>
                  {p.document.name || '未命名方案'} · {p.document.version} ·{' '}
                  {p.document.state === 'DRAFT'
                    ? '草稿'
                    : p.document.state === 'RELEASED'
                      ? '已发布'
                      : '已停用'}
                </option>
              ))}
            </select>
          )}
        </div>
        <div className="metadata-grid">
          <Field
            label="方案名称"
            required
            error={validateAll && !plan.name.trim() ? '请填写方案名称。' : ''}
          >
            <input
              disabled={!editable}
              value={plan.name}
              onChange={(e) =>
                mutate((p) => {
                  p.name = e.target.value
                })
              }
            />
          </Field>
          <Field
            label="方案标识"
            required
            error={validateAll && !plan.plan_id.trim() ? '请填写方案标识。' : ''}
            hint="字母、数字、点、下划线或连字符"
          >
            <input
              className="mono"
              disabled={!editable}
              value={plan.plan_id}
              onChange={(e) =>
                mutate((p) => {
                  p.plan_id = e.target.value
                })
              }
            />
          </Field>
          <Field
            label="方案版本"
            required
            error={validateAll && !plan.version.trim() ? '请填写方案版本。' : ''}
          >
            <input
              className="mono"
              disabled={!editable}
              value={plan.version}
              onChange={(e) =>
                mutate((p) => {
                  p.version = e.target.value
                })
              }
            />
          </Field>
          <Field
            label="软件名称"
            required
            error={validateAll && !plan.software_name.trim() ? '请填写软件名称。' : ''}
          >
            <input
              disabled={!editable}
              value={plan.software_name}
              onChange={(e) =>
                mutate((p) => {
                  p.software_name = e.target.value
                })
              }
            />
          </Field>
          <Field
            label="软件版本"
            required
            error={validateAll && !plan.software_version.trim() ? '请填写软件版本。' : ''}
          >
            <input
              className="mono"
              disabled={!editable}
              value={plan.software_version}
              onChange={(e) =>
                mutate((p) => {
                  p.software_version = e.target.value
                })
              }
            />
          </Field>
        </div>
      </section>
      {undo && (
        <div className="undo-strip">
          <span>已删除。保存前可撤销此操作。</span>
          <button
            className="text-button"
            onClick={() => {
              mutate((p) => {
                if (undo.kind === 'role') {
                  if (!p.roles.some((r) => r.role_id === undo.role.role_id))
                    p.roles.splice(
                      Math.min(undo.index, p.roles.length),
                      0,
                      structuredClone(undo.role),
                    )
                } else {
                  const parent = p.roles.find((r) => r.role_id === undo.roleID)
                  if (!parent) return // The role was also removed; do not leave orphan templates.
                  if (!parent.items.some((i) => i.item_id === undo.item.item_id))
                    parent.items.splice(
                      Math.min(undo.index, parent.items.length),
                      0,
                      structuredClone(undo.item),
                    )
                }
                for (const template of undo.templates) {
                  if (
                    !p.embedded_templates.some(
                      (t) =>
                        t.template_id === template.template_id && t.version === template.version,
                    )
                  )
                    p.embedded_templates.push(structuredClone(template))
                }
              })
              setUndo(null)
            }}
          >
            <Undo2 size={14} />
            撤销删除
          </button>
          <button className="icon-button" aria-label="关闭撤销提示" onClick={() => setUndo(null)}>
            ×
          </button>
        </div>
      )}
      <div className="workbench-grid">
        <aside className="panel roles-panel">
          <div className="panel-heading">
            <h2>
              <Settings2 size={15} />
              环境与拓扑
            </h2>
          </div>
          <div className="topology-fields">
            <fieldset disabled={!editable}>
              <legend>CPU 架构</legend>
              {['x86_64', 'aarch64'].map((key) => (
                <label className="check-label mono" key={key}>
                  <input
                    type="checkbox"
                    checked={plan.topology_policy.allowed_arch.includes(key)}
                    onChange={(e) =>
                      mutate((p) => {
                        p.topology_policy.allowed_arch = e.target.checked
                          ? [...p.topology_policy.allowed_arch, key]
                          : p.topology_policy.allowed_arch.filter((v) => v !== key)
                      })
                    }
                  />
                  {key}
                </label>
              ))}
            </fieldset>
          </div>
          <div className="panel-heading">
            <h2>
              节点角色 <span className="count">{plan.roles.length}</span>
            </h2>
            <button
              className="icon-button"
              disabled={!editable}
              aria-label="添加节点角色"
              onClick={() => {
                const id = `role-${randomSuffix(6)}`
                mutate((p) => {
                  p.roles.push({
                    role_id: id,
                    name: '新节点角色',
                    min_nodes: 1,
                    max_nodes: null,
                    allow_colocation: false,
                    items: [],
                  })
                })
                setRoleID(id)
                setItemID('')
              }}
            >
              <Plus size={16} />
            </button>
          </div>
          <div className="role-list">
            {plan.roles.map((r) => (
              <button
                className={`role-button ${r.role_id === role?.role_id ? 'selected' : ''}`}
                key={r.role_id}
                onClick={() => {
                  setRoleID(r.role_id)
                  setItemID('')
                }}
              >
                <Server size={16} />
                <span>
                  <strong>{r.name}</strong>
                  <small className="mono">{r.role_id}</small>
                </span>
                <span className="count">{r.items.length}</span>
              </button>
            ))}
          </div>
          {role && (
            <div className="role-settings">
              <Field label="角色名称" required>
                <input
                  disabled={!editable}
                  value={role.name}
                  onChange={(e) =>
                    updateRole((r) => {
                      r.name = e.target.value
                    })
                  }
                />
              </Field>
              <div className="two-columns">
                <Field label="最少节点">
                  <input
                    type="number"
                    min="0"
                    disabled={!editable}
                    value={role.min_nodes}
                    onChange={(e) =>
                      updateRole((r) => {
                        r.min_nodes = Number(e.target.value)
                      })
                    }
                  />
                </Field>
                <Field label="最多节点" hint="留空表示不限">
                  <input
                    type="number"
                    min="1"
                    disabled={!editable}
                    value={role.max_nodes ?? ''}
                    onChange={(e) =>
                      updateRole((r) => {
                        r.max_nodes = e.target.value === '' ? null : Number(e.target.value)
                      })
                    }
                  />
                </Field>
              </div>
              <label className="check-label">
                <input
                  type="checkbox"
                  disabled={!editable}
                  checked={role.allow_colocation}
                  onChange={(e) =>
                    updateRole((r) => {
                      r.allow_colocation = e.target.checked
                    })
                  }
                />
                允许与其他角色混部
              </label>
              <button
                className="text-button destructive"
                disabled={!editable}
                onClick={() => setDeleting('role')}
              >
                <Trash2 size={13} />
                删除此角色
              </button>
            </div>
          )}
        </aside>
        <section className="panel items-panel">
          <div className="panel-heading">
            <div>
              <h2>
                {role?.name ?? '检查项'} <span className="count">{role?.items.length ?? 0}</span>
              </h2>
              <small className="muted">每个检查项独立属于当前节点角色</small>
            </div>
            <button className="button small" disabled={!editable || !role} onClick={addShell}>
              <Plus size={14} />
              自定义 Shell
            </button>
          </div>
          <div className="items-toolbar">
            <label className="search-field">
              <Search size={15} />
              <input
                aria-label="搜索检查项"
                placeholder="筛选检查项…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <button className="text-button" onClick={() => setCatalog(true)}>
              浏览模板 <ChevronDown size={13} />
            </button>
          </div>
          {shown.length > 0 ? (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>检查项</th>
                    <th>重要性</th>
                    <th>启用</th>
                    <th>
                      <span className="sr-only">复制</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((i) => (
                    <tr className={itemID === i.item_id ? 'selected-row' : ''} key={i.item_id}>
                      <td>
                        <button className="table-link" onClick={() => setItemID(i.item_id)}>
                          {i.name}
                        </button>
                        <small className="cell-secondary mono">{i.template_id}</small>
                      </td>
                      <td>
                        <span className={`importance importance-${i.importance.toLowerCase()}`}>
                          {{ REQUIRED: '必检', WARNING: '告警', INFO: '信息' }[i.importance]}
                        </span>
                      </td>
                      <td>
                        <input
                          aria-label={`启用 ${i.name}`}
                          type="checkbox"
                          disabled={!editable}
                          checked={i.enabled}
                          onChange={(e) =>
                            updateRole((r) => {
                              const target = r.items.find((v) => v.item_id === i.item_id)!
                              target.enabled = e.target.checked
                            })
                          }
                        />
                      </td>
                      <td>
                        <button
                          className="icon-button"
                          aria-label={`复制 ${i.name}`}
                          disabled={!editable}
                          onClick={() => copyItem(i)}
                        >
                          <Copy size={14} />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <Empty
              icon={<FileCheck2 size={25} />}
              title={query ? '没有匹配的检查项' : role ? '为这个角色添加检查项' : '先添加节点角色'}
            >
              {query ? '调整关键词后重试。' : '可添加自定义 Shell，或浏览预置模板定义。'}
            </Empty>
          )}
          <div className="items-footer">
            <span>
              {plan.roles.length} 个角色 · {itemCount(plan)} 个检查项
            </span>
            <span>先普通执行，后按需提权</span>
          </div>
        </section>
        <aside className="panel details-panel">
          <div className="panel-heading">
            <h2>
              <Settings2 size={16} />
              检查项配置
            </h2>
          </div>
          {item && template ? (
            <div className="detail-form">
              <div className="detail-badges">
                <Badge status={template.execution_mode} />
                <code>{template.evidence_mode}</code>
              </div>
              <Field label="检查项名称" required>
                <input
                  disabled={!editable}
                  value={item.name}
                  onChange={(e) =>
                    updateItem((i) => {
                      i.name = e.target.value
                    })
                  }
                />
              </Field>
              <Field label="重要性">
                <select
                  disabled={!editable}
                  value={item.importance}
                  onChange={(e) =>
                    updateItem((i) => {
                      i.importance = e.target.value as Importance
                    })
                  }
                >
                  <option value="REQUIRED">必检 · 影响门禁结论</option>
                  <option value="WARNING">告警 · 不阻断放行</option>
                  <option value="INFO">信息 · 仅供参考</option>
                </select>
              </Field>
              {template.source !== 'BUILTIN' && (
                <>
                  <div className="section-divider">自定义 Shell</div>
                  <Field label="Shell 脚本" hint="仅保存脚本定义，静态校验不会执行此脚本。">
                    <textarea
                      className="code-editor"
                      rows={7}
                      spellCheck={false}
                      disabled={!editable}
                      value={String(template.implementation.command ?? '')}
                      onChange={(e) =>
                        mutate((p) => {
                          const t = p.embedded_templates.find(
                            (t) =>
                              t.template_id === template.template_id &&
                              t.version === template.version,
                          )!
                          t.implementation.command = e.target.value
                        })
                      }
                    />
                  </Field>
                  <Field label="期望退出码">
                    <input
                      type="number"
                      min="0"
                      max="255"
                      disabled={!editable}
                      value={Number(template.implementation.expect_exit_code ?? 0)}
                      onChange={(e) =>
                        mutate((p) => {
                          const t = p.embedded_templates.find(
                            (t) =>
                              t.template_id === template.template_id &&
                              t.version === template.version,
                          )!
                          t.implementation.expect_exit_code = Number(e.target.value)
                        })
                      }
                    />
                  </Field>
                  <ShellAssertionSelfTest template={template} />
                  <Notice>
                    脚本随 SSH 登录身份执行，不自动 sudo 提权。root 登录时会以 root 身份运行。
                  </Notice>
                </>
              )}
              <Parameters
                validateAll={validateAll}
                onValidate={() => setValidationRequested(true)}
                issue={
                  serverIssue?.item_id === item.item_id
                    ? serverIssue
                    : selectedIssue?.item_id === item.item_id
                      ? selectedIssue
                      : undefined
                }
                key={item.item_id}
                item={item}
                template={template}
                editable={editable}
                onUpdate={(fn) => updateItem((i) => fn(i))}
                onAdd={(key, type) =>
                  mutate((p) => {
                    const i = p.roles.flatMap((r) => r.items).find((i) => i.item_id === itemID)!
                    const t = p.embedded_templates.find(
                      (t) => t.template_id === i.template_id && t.version === i.template_version,
                    )!
                    const properties = (t.params_schema.properties ?? {}) as Record<string, unknown>
                    t.params_schema.properties = { ...properties, [key]: { type } }
                    i.params[key] = type === 'number' ? 0 : type === 'boolean' ? false : ''
                  })
                }
              />
              <button
                className="text-button destructive"
                disabled={!editable}
                onClick={() => setDeleting('item')}
              >
                <Trash2 size={14} />
                删除检查项
              </button>
            </div>
          ) : (
            <Empty icon={<Settings2 size={24} />} title="选择一个检查项">
              点击检查项名称，编辑断言、参数与现场可调范围。
            </Empty>
          )}
        </aside>
      </div>
      {catalog && (
        <Modal title="预置检查模板" onClose={() => setCatalog(false)} wide>
          <div className="modal-body">
            <Templates compact onAdd={editable && role ? addBuiltin : undefined} />
          </div>
        </Modal>
      )}
      {deleting && (
        <Modal
          title={deleting === 'role' ? '删除节点角色？' : '删除检查项？'}
          onClose={() => setDeleting(null)}
        >
          <div className="modal-body">
            <p>
              将从当前草稿移除
              {deleting === 'role' ? `「${role?.name}」及其全部检查项` : `「${item?.name}」`}
              。保存前可撤销此操作。
            </p>
            <div className="dialog-actions">
              <button className="button" onClick={() => setDeleting(null)}>
                取消
              </button>
              <button className="button danger" onClick={remove}>
                <Trash2 size={14} />
                {deleting === 'role' ? '删除角色' : '删除检查项'}
              </button>
            </div>
          </div>
        </Modal>
      )}
    </>
  )
}

function ShellAssertionSelfTest({ template }: { template: CheckTemplate }) {
  const [stdout, setStdout] = useState('')
  const [stderr, setStderr] = useState('')
  const [exitCode, setExitCode] = useState(Number(template.implementation.expect_exit_code ?? 0))
  const [simulateStartFailure, setSimulateStartFailure] = useState(false)
  const [result, setResult] = useState('')
  const [promotion, setPromotion] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <section className="panel selftest-panel">
      <h4>断言自测（不执行脚本）</h4>
      <p className="muted">输入模拟采集结果，服务器只运行基础断言求值函数。</p>
      <div className="two-columns">
        <Field label="模拟退出码">
          <input
            type="number"
            min="0"
            max="255"
            value={exitCode}
            onChange={(event) => setExitCode(Number(event.target.value))}
          />
        </Field>
        <Field label="模拟 stdout">
          <textarea rows={3} value={stdout} onChange={(event) => setStdout(event.target.value)} />
        </Field>
      </div>
      <Field label="模拟 stderr">
        <textarea rows={3} value={stderr} onChange={(event) => setStderr(event.target.value)} />
      </Field>
      <label>
        <input
          type="checkbox"
          checked={simulateStartFailure}
          onChange={(event) => setSimulateStartFailure(event.target.checked)}
        />
        模拟命令启动失败（不运行脚本）
      </label>
      <button
        className="button"
        disabled={busy}
        onClick={async () => {
          setBusy(true)
          try {
            const value = await api.request<{ status: string; diagnostics: string[] }>(
              '/shell-selftest',
              {
                assertions: template.implementation.assertions ?? [],
                expect_exit_code: template.implementation.expect_exit_code ?? 0,
                observation: { exit_code: exitCode, stdout, stderr },
                simulate_start_failure: simulateStartFailure,
              },
            )
            setResult(
              `${value.status}${value.diagnostics.length ? ` · ${value.diagnostics.join(' ')}` : ''}`,
            )
          } catch (cause) {
            setResult(cause instanceof Error ? cause.message : '自测失败')
          } finally {
            setBusy(false)
          }
        }}
      >
        {busy ? '正在求值…' : '运行断言自测'}
      </button>
      {template.source === 'PLAN_LOCAL' && (
        <button
          className="button"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            setPromotion('')
            try {
              const value = await api.request<{ digest: string }>('/templates/promote', {
                template,
              })
              setPromotion(`已加入公共模板库：${value.digest}`)
            } catch (cause) {
              setPromotion(cause instanceof Error ? cause.message : '公共模板提升失败')
            } finally {
              setBusy(false)
            }
          }}
        >
          {busy ? '处理中…' : '提升为公共模板'}
        </button>
      )}
      {result && <output className="selftest-result">{result}</output>}
      {promotion && <output className="selftest-result">{promotion}</output>}
    </section>
  )
}

function FilesIcon() {
  return (
    <span className="plan-icon">
      <Code2 size={21} />
    </span>
  )
}

function Parameters({
  validateAll,
  onValidate,
  issue,
  item,
  template,
  editable,
  onUpdate,
  onAdd,
}: {
  validateAll: boolean
  onValidate: () => void
  issue?: { field?: string; message?: string }
  item: CheckItem
  template: CheckTemplate
  editable: boolean
  onUpdate: (fn: (i: CheckItem) => void) => void
  onAdd: (key: string, type: string) => void
}) {
  const [adding, setAdding] = useState(false),
    [name, setName] = useState(''),
    [type, setType] = useState('string')
  const [jsonDrafts, setJsonDrafts] = useState<Record<string, string>>({})
  const [jsonErrors, setJsonErrors] = useState<Record<string, string>>({})
  const [touched, setTouched] = useState<Record<string, boolean>>({})
  const properties = (template.params_schema.properties ?? {}) as Record<string, Property>
  const values = Object.fromEntries(
    Object.entries(properties).map(([key, definition]) => [
      key,
      item.explicit_null_params.includes(key)
        ? null
        : Object.hasOwn(item.params, key)
          ? item.params[key]
          : definition.default,
    ]),
  )
  const required = requiredParameters(template.params_schema, values)
  const keys = [
    ...new Set([
      ...Object.keys(properties),
      ...Object.keys(item.params),
      ...item.explicit_null_params,
    ]),
  ]
  return (
    <>
      <div className="section-divider">
        检查参数
        <button
          className="icon-button"
          disabled={!editable || template.source === 'BUILTIN'}
          aria-label="添加检查参数"
          onClick={() => setAdding(!adding)}
        >
          <Plus size={14} />
        </button>
      </div>
      {keys.length === 0 && (
        <p className="muted small-text">未定义参数。脚本中可通过 $RS_PARAM_参数名 读取参数。</p>
      )}
      {keys.map((key) => {
        const definition = properties[key]
        const value = Object.hasOwn(item.params, key) ? item.params[key] : definition?.default
        const isNull = item.explicit_null_params.includes(key)
        return (
          <div className="parameter" key={key}>
            <Field
              label={
                typeof definition?.title === 'string' && definition.title.trim()
                  ? definition.title
                  : key
              }
              required={required.has(key)}
              hint={
                definition?.['x-readyspec-type'] === 'capacity'
                  ? capacityHint
                  : typeof definition?.description === 'string'
                    ? definition.description
                    : undefined
              }
              error={
                jsonErrors[key] ||
                (issue?.field?.endsWith(
                  '/params/' + key.replaceAll('~', '~0').replaceAll('/', '~1'),
                )
                  ? issue.message
                  : '') ||
                (touched[key] || validateAll
                  ? inputIssue(definition, isNull ? null : value, required.has(key))
                  : '')
              }
              onBlur={() => {
                setTouched((previous) => ({ ...previous, [key]: true }))
                onValidate()
              }}
            >
              {definition?.enum ? (
                <select
                  disabled={!editable || isNull}
                  value={String(value ?? '')}
                  onChange={(e) =>
                    onUpdate((i) => {
                      if (e.target.value === '') delete i.params[key]
                      else
                        i.params[key] =
                          definition.type === 'integer' || definition.type === 'number'
                            ? Number(e.target.value)
                            : e.target.value
                    })
                  }
                >
                  <option value="">未设置</option>
                  {definition.enum.map((option) => (
                    <option key={option} value={option}>
                      {option}
                    </option>
                  ))}
                </select>
              ) : typeof value === 'boolean' || definition?.type === 'boolean' ? (
                <select
                  disabled={!editable || isNull}
                  value={value === undefined ? '' : String(value)}
                  onChange={(e) =>
                    onUpdate((i) => {
                      if (e.target.value === '') delete i.params[key]
                      else i.params[key] = e.target.value === 'true'
                    })
                  }
                >
                  <option value="">未设置</option>
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              ) : definition?.type === 'array' && definition.items?.enum ? (
                <select
                  multiple
                  disabled={!editable || isNull}
                  value={Array.isArray(value) ? value.map(String) : []}
                  onChange={(e) => {
                    const selected = Array.from(e.target.selectedOptions, (option) => option.value)
                    onUpdate((i) => {
                      i.params[key] = selected
                    })
                  }}
                >
                  {definition.items.enum.map((option) => (
                    <option key={option} value={option}>
                      {option}
                    </option>
                  ))}
                </select>
              ) : (typeof value === 'object' && value !== null) ||
                definition?.type === 'array' ||
                definition?.type === 'object' ? (
                <textarea
                  disabled={!editable || isNull}
                  rows={3}
                  value={
                    jsonDrafts[key] ??
                    (typeof value === 'string'
                      ? value
                      : JSON.stringify(value ?? (definition?.type === 'array' ? [] : {})))
                  }
                  onChange={(e) => {
                    const text = e.target.value
                    setJsonDrafts((previous) => ({ ...previous, [key]: text }))
                    try {
                      const parsed: unknown = JSON.parse(text)
                      onUpdate((i) => {
                        i.params[key] = parsed
                      })
                      setJsonErrors((previous) => ({ ...previous, [key]: '' }))
                    } catch {
                      onUpdate((i) => {
                        i.params[key] = text
                      })
                      setJsonErrors((previous) => ({ ...previous, [key]: '请输入有效 JSON' }))
                    }
                  }}
                />
              ) : (
                <input
                  disabled={!editable || isNull}
                  type={
                    typeof value === 'number' ||
                    definition?.type === 'integer' ||
                    definition?.type === 'number'
                      ? 'number'
                      : 'text'
                  }
                  value={isNull ? '' : String(value ?? '')}
                  onChange={(e) =>
                    onUpdate((i) => {
                      if (e.target.value === '') delete i.params[key]
                      else
                        i.params[key] =
                          typeof value === 'number' ||
                          definition?.type === 'integer' ||
                          definition?.type === 'number'
                            ? Number(e.target.value)
                            : e.target.value
                    })
                  }
                />
              )}
            </Field>
            {template.source === 'BUILTIN' && (
              <button
                className="text-button"
                disabled={!editable}
                onClick={() => {
                  setJsonDrafts((previous) => {
                    const next = { ...previous }
                    delete next[key]
                    return next
                  })
                  setJsonErrors((previous) => {
                    const next = { ...previous }
                    delete next[key]
                    return next
                  })
                  onUpdate((i) => {
                    delete i.params[key]
                    i.explicit_null_params = i.explicit_null_params.filter((v) => v !== key)
                  })
                }}
              >
                恢复默认值
              </button>
            )}
            <div className="parameter-options">
              <label className="check-label">
                <input
                  type="checkbox"
                  disabled={!editable}
                  checked={item.overridable_params.includes(key)}
                  onChange={(e) =>
                    onUpdate((i) => {
                      i.overridable_params = e.target.checked
                        ? [...i.overridable_params, key]
                        : i.overridable_params.filter((v) => v !== key)
                    })
                  }
                />
                现场可调
              </label>
              <label className="check-label">
                <input
                  type="checkbox"
                  disabled={!editable}
                  checked={isNull}
                  onChange={(e) =>
                    onUpdate((i) => {
                      if (e.target.checked) {
                        delete i.params[key]
                        i.explicit_null_params.push(key)
                      } else {
                        i.explicit_null_params = i.explicit_null_params.filter((v) => v !== key)
                        i.params[key] = ''
                      }
                    })
                  }
                />
                显式空值
              </label>
            </div>
          </div>
        )
      })}
      {adding && (
        <div className="add-parameter">
          <Field label="参数名">
            <input
              disabled={!editable}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="data_path"
              pattern="[A-Za-z_][A-Za-z0-9_]*"
            />
          </Field>
          <Field label="参数类型">
            <select disabled={!editable} value={type} onChange={(e) => setType(e.target.value)}>
              <option value="string">文本</option>
              <option value="number">数值</option>
              <option value="boolean">布尔值</option>
            </select>
          </Field>
          <button
            className="button small"
            disabled={!editable || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || keys.includes(name)}
            onClick={() => {
              onAdd(name, type)
              setName('')
              setAdding(false)
            }}
          >
            <Plus size={13} />
            添加参数
          </button>
        </div>
      )}
    </>
  )
}
