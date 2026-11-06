import { useCallback, useEffect, useState } from 'react'
import { BookOpen, ChevronRight, Search, ShieldCheck, Trash2 } from 'lucide-react'
import { Badge, Empty, Modal, Notice } from './components'
import { domains, type CatalogEntry } from './catalog'
import { api } from './api'
import type { CheckTemplate } from './model'

type CleanupRow = {
  run_id: string
  auth_method: string
  item: {
    cleanup_id: string
    host_id: string
    machine_id?: string
    boot_id?: string
    status: string
    remote_path?: string
    reason?: string
    audit_reason?: string
  }
}

export function Templates({
  compact = false,
  onAdd,
}: {
  compact?: boolean
  onAdd?: (template: CheckTemplate) => void
}) {
  const [available, setAvailable] = useState<CheckTemplate[]>([])
  const [catalog, setCatalog] = useState<CatalogEntry[]>([])
  const [error, setError] = useState('')
  const [cleanupItems, setCleanupItems] = useState<CleanupRow[]>([])
  const [cleanupCredentials, setCleanupCredentials] = useState<
    Record<
      string,
      {
        password?: string
        sudoPassword?: string
        privateKey?: string
        passphrase?: string
        agentSocket?: string
        auditReason?: string
        confirmTerminate?: boolean
      }
    >
  >({})
  const [cleanupBusy, setCleanupBusy] = useState('')
  const [cleanupError, setCleanupError] = useState('')
  const [processPrompt, setProcessPrompt] = useState<{
    id: string
    host: string
    path: string
  } | null>(null)
  const [cleanupState, setCleanupState] = useState<'open' | 'all'>('open')
  const [tab, setTab] = useState('templates'),
    [domain, setDomain] = useState(''),
    [query, setQuery] = useState(''),
    [selected, setSelected] = useState<CatalogEntry | null>(null)
  const refreshCleanup = useCallback(async () => {
    const result = await api.request<{ items: CleanupRow[] }>(
      cleanupState === 'all' ? '/cleanups?state=all' : '/cleanups',
    )
    setCleanupItems(result.items ?? [])
  }, [cleanupState])
  useEffect(() => {
    let active = true
    void api
      .request<{ items: CheckTemplate[]; definitions?: CatalogEntry[] }>('/templates')
      .then((result) => {
        if (active) {
          setAvailable(result.items ?? [])
          setCatalog([
            ...(result.definitions ??
              (result.items ?? [])
                .filter((t) => t.source === 'BUILTIN')
                .map((t) => ({
                  id: t.template_id,
                  name: t.name,
                  summary: '',
                  evidence: t.evidence_mode,
                  privileged: t.execution_mode === 'PRIVILEGED',
                }))),
            ...(result.items ?? [])
              .filter((t) => t.source === 'USER_PUBLIC')
              .map((t) => ({
                id: t.template_id,
                name: t.name,
                summary: `${t.source} · ${t.version}`,
                evidence: t.evidence_mode,
                privileged: false,
              })),
          ])
        }
      })
      .catch((err) => {
        if (active) setError(err instanceof Error ? err.message : '模板加载失败')
      })
    return () => {
      active = false
    }
  }, [])
  useEffect(() => {
    if (tab !== 'cleanup') return
    let active = true
    void refreshCleanup().catch((err) => {
      if (active) setCleanupError(err instanceof Error ? err.message : '待清理项加载失败')
    })
    return () => {
      active = false
    }
  }, [tab, refreshCleanup])
  useEffect(() => {
    if (tab !== 'cleanup') {
      setCleanupCredentials({})
      setProcessPrompt(null)
    }
  }, [tab])
  useEffect(
    () =>
      api.onSessionExpired(() => {
        setCleanupCredentials({})
        setProcessPrompt(null)
      }),
    [],
  )
  const filtered = catalog.filter(
    (t) =>
      (!domain || t.id.startsWith(domain)) &&
      `${t.id} ${t.name} ${t.summary} ${t.evidence}`.toLowerCase().includes(query.toLowerCase()),
  )
  return (
    <>
      {!compact && (
        <>
          <div className="page-heading">
            <div>
              <div className="eyebrow">TEMPLATES & CLEANUP</div>
              <h1>模板与清理</h1>
              <p>查阅检查模板的权限与取证要求，管理远端待清理残留。</p>
            </div>
          </div>
          <div className="tabs" role="tablist" aria-label="模板与清理视图">
            <button
              role="tab"
              aria-selected={tab === 'templates'}
              onClick={() => setTab('templates')}
            >
              <BookOpen size={16} />
              检查模板<span className="count">36</span>
            </button>
            <button role="tab" aria-selected={tab === 'cleanup'} onClick={() => setTab('cleanup')}>
              <Trash2 size={15} />
              远端残留清理<span className="count">{cleanupItems.length}</span>
            </button>
          </div>
        </>
      )}
      {tab === 'templates' ? (
        <>
          <Notice>已接入的模板可加入方案，其余条目为规格定义。静态编译不连接目标主机。</Notice>
          {error && <Notice tone="danger">{error}</Notice>}
          <div className="catalog-layout">
            <aside className="panel domain-list">
              <button className={!domain ? 'selected' : ''} onClick={() => setDomain('')}>
                <BookOpen size={15} />
                全部领域<span className="count">36</span>
              </button>
              {domains.map(([code, name]) => (
                <button
                  key={code}
                  className={domain === code ? 'selected' : ''}
                  onClick={() => setDomain(code)}
                >
                  <span className="domain-code">{code}</span>
                  <span>{name}</span>
                  <span className="count">
                    {catalog.filter((t) => t.id.startsWith(code)).length}
                  </span>
                </button>
              ))}
            </aside>
            <section className="panel">
              <div className="panel-heading">
                <h2>
                  {domains.find(([code]) => code === domain)?.[1] ?? '全部检查模板'}{' '}
                  <span className="count">{filtered.length}</span>
                </h2>
                <label className="search-field">
                  <Search size={15} />
                  <input
                    aria-label="搜索模板"
                    placeholder="搜索名称、ID 或取证方式…"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                </label>
              </div>
              {filtered.length ? (
                <div className="table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>模板</th>
                        <th>取证方式</th>
                        <th>执行方式</th>
                        <th>状态</th>
                        <th>
                          <span className="sr-only">详情</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((t) => (
                        <tr key={t.id}>
                          <td>
                            <button className="table-link" onClick={() => setSelected(t)}>
                              <code className="template-id">{t.id}</code>
                              {t.name}
                            </button>
                            <small className="cell-secondary">{t.summary}</small>
                          </td>
                          <td>
                            <code className="small-text muted">{t.evidence}</code>
                          </td>
                          <td>
                            <Badge status={t.privileged ? 'PRIVILEGED' : 'STANDARD'} />
                          </td>
                          <td>
                            <span className="definition-badge">
                              {available.some((v) => v.template_id === t.id)
                                ? '可编排'
                                : '规格定义'}
                            </span>
                          </td>
                          <td>
                            {onAdd &&
                              available
                                .filter((v) => v.template_id === t.id)
                                .map((v) => (
                                  <button
                                    key={v.version}
                                    className="button small"
                                    aria-label={`添加 ${v.name}`}
                                    onClick={() => onAdd(v)}
                                  >
                                    添加
                                    {available.filter((entry) => entry.template_id === t.id)
                                      .length > 1
                                      ? ` ${v.evidence_mode}`
                                      : ''}
                                  </button>
                                ))}
                            <button
                              className="icon-button"
                              aria-label={`查看 ${t.name}`}
                              onClick={() => setSelected(t)}
                            >
                              <ChevronRight size={16} />
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <Empty icon={<Search size={24} />} title="没有匹配的模板">
                  尝试其他关键词，或清除领域筛选。
                  <button
                    className="text-button"
                    onClick={() => {
                      setDomain('')
                      setQuery('')
                    }}
                  >
                    重置筛选
                  </button>
                </Empty>
              )}
            </section>
          </div>
        </>
      ) : (
        <>
          <Notice>
            清理前会重新验证 SSH 主机身份、机器码、开机标识和目录 owner
            token。检测到关联进程时，必须显式确认才会尝试终止并删除。
          </Notice>
          {cleanupError && <Notice tone="danger">{cleanupError}</Notice>}
          <section className="panel">
            <div className="panel-heading">
              <h2>待清理残留</h2>
              <span className="count">{cleanupItems.length}</span>
              <label className="search-field">
                <span>显示</span>
                <select
                  aria-label="清理审计筛选"
                  value={cleanupState}
                  onChange={(event) => setCleanupState(event.target.value as 'open' | 'all')}
                >
                  <option value="open">未结案</option>
                  <option value="all">全部审计记录</option>
                </select>
              </label>
            </div>
            {cleanupItems.length === 0 ? (
              <Empty
                icon={<ShieldCheck size={27} />}
                title={cleanupState === 'all' ? '暂无清理审计记录' : '暂无未结案清理记录'}
              >
                {cleanupState === 'all'
                  ? '当前工作区没有已归档的远端清理记录。'
                  : '当前没有需要处置的远端清理记录。'}
              </Empty>
            ) : (
              <div className="table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Run / 主机</th>
                      <th>状态与目录</th>
                      <th>新凭据</th>
                      <th>处置</th>
                    </tr>
                  </thead>
                  <tbody>
                    {cleanupItems.map(({ run_id: runID, auth_method: authMethod, item }) => {
                      const id = `${runID}/${item.cleanup_id}`
                      const form = cleanupCredentials[id] ?? {}
                      const update = (patch: Partial<typeof form>) =>
                        setCleanupCredentials((old) => ({ ...old, [id]: { ...old[id], ...patch } }))
                      const resolve = async (action: 'CLEAN' | 'IGNORE') => {
                        setCleanupBusy(id)
                        setCleanupError('')
                        try {
                          const outcome = await api.request<CleanupRow['item']>(
                            `/runs/${encodeURIComponent(runID)}/cleanup/${encodeURIComponent(item.cleanup_id)}`,
                            {
                              action,
                              audit_reason: form.auditReason,
                              confirm_terminate: form.confirmTerminate,
                              credential: {
                                password: form.password,
                                sudo_password: form.sudoPassword,
                                private_key: form.privateKey,
                                passphrase: form.passphrase,
                                agent_socket: form.agentSocket,
                              },
                            },
                          )
                          if (
                            action === 'CLEAN' &&
                            outcome.status === 'UNKNOWN' &&
                            outcome.reason?.includes('associated processes are active')
                          ) {
                            setCleanupCredentials((old) => ({
                              ...old,
                              [id]: { auditReason: old[id]?.auditReason, confirmTerminate: false },
                            }))
                            setProcessPrompt({
                              id,
                              host: item.host_id,
                              path: item.remote_path ?? '路径未记录',
                            })
                            await refreshCleanup()
                            return
                          }
                          setCleanupCredentials((old) => {
                            const next = { ...old }
                            delete next[id]
                            return next
                          })
                          await refreshCleanup()
                        } catch (err) {
                          setCleanupError(err instanceof Error ? err.message : '清理处置失败')
                          setCleanupCredentials((old) => ({
                            ...old,
                            [id]: {
                              auditReason: old[id]?.auditReason,
                              confirmTerminate: old[id]?.confirmTerminate,
                            },
                          }))
                        } finally {
                          setCleanupBusy('')
                        }
                      }
                      return (
                        <tr key={id}>
                          <td>
                            <code>{runID}</code>
                            <small className="cell-secondary">
                              {item.host_id} · {item.cleanup_id}
                              {item.machine_id ? ` · machine ${item.machine_id}` : ''}
                              {item.boot_id ? ` · boot ${item.boot_id}` : ''}
                            </small>
                          </td>
                          <td>
                            <span
                              className={`definition-badge cleanup-${item.status.toLowerCase()}`}
                            >
                              {item.status}
                            </span>
                            <small className="cell-secondary">
                              {item.reason || item.remote_path}
                            </small>
                          </td>
                          {item.status === 'CLEAN' || item.status === 'IGNORED' ? (
                            <td colSpan={2}>
                              审计原因：
                              {item.audit_reason ||
                                (item.status === 'CLEAN' ? '已核验清理' : '未记录')}
                            </td>
                          ) : (
                            <>
                              <td>
                                {authMethod === 'password' ? (
                                  <input
                                    aria-label={`${item.host_id} 新密码`}
                                    type="password"
                                    value={form.password ?? ''}
                                    onChange={(e) => update({ password: e.target.value })}
                                  />
                                ) : null}
                                <input
                                  aria-label={`${item.host_id} 新 sudo 密码`}
                                  type="password"
                                  placeholder="sudo 密码（如需）"
                                  value={form.sudoPassword ?? ''}
                                  onChange={(e) => update({ sudoPassword: e.target.value })}
                                />
                                {authMethod === 'private_key' ? (
                                  <>
                                    <textarea
                                      aria-label={`${item.host_id} 新私钥`}
                                      value={form.privateKey ?? ''}
                                      onChange={(e) => update({ privateKey: e.target.value })}
                                    />
                                    <input
                                      aria-label={`${item.host_id} 私钥口令`}
                                      type="password"
                                      value={form.passphrase ?? ''}
                                      onChange={(e) => update({ passphrase: e.target.value })}
                                    />
                                  </>
                                ) : null}
                                {authMethod === 'agent' ? (
                                  <input
                                    aria-label={`${item.host_id} Agent socket`}
                                    value={form.agentSocket ?? ''}
                                    onChange={(e) => update({ agentSocket: e.target.value })}
                                  />
                                ) : null}
                                <label>
                                  <input
                                    type="checkbox"
                                    checked={!!form.confirmTerminate}
                                    onChange={(e) => update({ confirmTerminate: e.target.checked })}
                                  />
                                  确认可终止关联进程
                                </label>
                              </td>
                              <td>
                                <button
                                  className="button small"
                                  disabled={cleanupBusy === id || authMethod === 'offline'}
                                  onClick={() => void resolve('CLEAN')}
                                >
                                  重新核验并清理
                                </button>
                                <input
                                  aria-label={`${item.host_id} 忽略原因`}
                                  placeholder="忽略原因（必填）"
                                  value={form.auditReason ?? ''}
                                  onChange={(e) => update({ auditReason: e.target.value })}
                                />
                                <button
                                  className="button small"
                                  disabled={cleanupBusy === id || !form.auditReason?.trim()}
                                  onClick={() => void resolve('IGNORE')}
                                >
                                  记录忽略
                                </button>
                              </td>
                            </>
                          )}
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
          {processPrompt && (
            <Modal title="检测到关联进程" onClose={() => setProcessPrompt(null)}>
              <div className="modal-body">
                <Notice tone="warning">
                  主机 {processPrompt.host} 的目录 {processPrompt.path}{' '}
                  仍被进程使用。尚未尝试终止进程或删除目录。
                </Notice>
                <p>
                  如需继续，请确认可能终止这些进程。关闭此对话框会取消处置；重新操作时需再次提供认证信息。
                </p>
                <div className="modal-actions">
                  <button className="button" onClick={() => setProcessPrompt(null)}>
                    取消
                  </button>
                  <button
                    className="button danger"
                    onClick={() => {
                      setCleanupCredentials((old) => ({
                        ...old,
                        [processPrompt.id]: { ...old[processPrompt.id], confirmTerminate: true },
                      }))
                      setProcessPrompt(null)
                      setCleanupError('已确认可能终止关联进程。请重新输入凭据后再次执行清理。')
                    }}
                  >
                    确认并继续
                  </button>
                </div>
              </div>
            </Modal>
          )}
        </>
      )}
      {selected && (
        <Modal title={`${selected.name}详情`} drawer onClose={() => setSelected(null)}>
          <div className="drawer-body">
            <div className="eyebrow">CHECK TEMPLATE / {selected.id}</div>
            <span className="definition-badge">
              {available.some((t) => t.template_id === selected.id)
                ? '可编排 · 支持静态编译和 Native SSH 执行'
                : '规格定义 · 尚未接入执行'}
            </span>
            <p>{selected.summary}</p>
            <dl className="detail-list">
              <dt>领域</dt>
              <dd>{domains.find(([code]) => selected.id.startsWith(code))?.[1]}</dd>
              <dt>取证方式</dt>
              <dd>
                <code>{selected.evidence}</code>
              </dd>
              <dt>执行方式</dt>
              <dd>
                <Badge status={selected.privileged ? 'PRIVILEGED' : 'STANDARD'} />
              </dd>
              <dt>模板标识</dt>
              <dd>
                <code>{selected.id}</code>
              </dd>
            </dl>
            <Notice>
              {selected.privileged
                ? '该检查需要 root 权限。正式执行时，工具根据账户身份自动选择直接执行或 sudo；必要凭据不足时拒绝执行。'
                : '该检查使用当前登录身份直接执行，不经提权通道。'}
            </Notice>
            <div className="section-divider">判定与证据</div>
            <p className="muted">
              只有取得满足取证要求的完整证据，才能判定通过。权限不足、日志窗口缺口或工具缺失不能被当作通过。
            </p>
          </div>
        </Modal>
      )}
    </>
  )
}
