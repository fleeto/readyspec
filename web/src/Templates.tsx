import { useEffect, useState } from 'react'
import { BookOpen, ChevronRight, Search, ShieldCheck, Trash2 } from 'lucide-react'
import { Badge, Empty, Modal, Notice } from './components'
import { domains, type CatalogEntry } from './catalog'
import { api } from './api'
import type { CheckTemplate } from './model'

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
  useEffect(() => {
    let active = true
    void api
      .request<{ items: CheckTemplate[]; definitions?: CatalogEntry[] }>('/templates')
      .then((result) => {
        if (active) {
          setAvailable(result.items ?? [])
          setCatalog(
            result.definitions ??
              (result.items ?? []).map((t) => ({
                id: t.template_id,
                name: t.name,
                summary: '',
                evidence: t.evidence_mode,
                privileged: t.execution_mode === 'PRIVILEGED',
              })),
          )
        }
      })
      .catch((err) => {
        if (active) setError(err instanceof Error ? err.message : '模板加载失败')
      })
    return () => {
      active = false
    }
  }, [])
  const [tab, setTab] = useState('templates'),
    [domain, setDomain] = useState(''),
    [query, setQuery] = useState(''),
    [selected, setSelected] = useState<CatalogEntry | null>(null)
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
              远端残留清理<span className="count">0</span>
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
          <Notice>远程检查尚未启用，当前没有由本服务产生的待清理残留。</Notice>
          <section className="panel">
            <div className="panel-heading">
              <h2>待清理残留</h2>
              <span className="count">0</span>
            </div>
            <Empty icon={<ShieldCheck size={27} />} title="暂无待清理残留">
              检查中断后，待核验的临时目录与关联进程将在这里展示。清理时会先核对归属，发现存活进程时再要求确认。
            </Empty>
          </section>
        </>
      )}
      {selected && (
        <Modal title={`${selected.name}详情`} drawer onClose={() => setSelected(null)}>
          <div className="drawer-body">
            <div className="eyebrow">CHECK TEMPLATE / {selected.id}</div>
            <span className="definition-badge">
              {available.some((t) => t.template_id === selected.id)
                ? '可编排 · 支持静态编译，远程执行尚未接入'
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
