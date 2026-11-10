import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Activity,
  ArrowRight,
  BookOpen,
  Check,
  ChevronRight,
  CircleHelp,
  FileCheck2,
  Files,
  LayoutDashboard,
  Menu,
  Moon,
  Plus,
  Search,
  ShieldCheck,
  Sun,
  Upload,
  Wifi,
  X,
} from 'lucide-react'
import { api, ApiError, type Diagnostic } from './api'
import { RunPreview } from './RunPreview'
import { RunHistory } from './RunHistory'
import { Badge, Empty, Field, Modal, Notice, Spinner } from './components'
import {
  formatTime,
  isLoopback,
  itemCount,
  newPlan,
  type Plan,
  type SavedPlan,
  type SystemInfo,
  type Validation,
  type DraftSave,
} from './model'
import { Workbench } from './Workbench'
import { Templates } from './Templates'
import { stringify } from 'yaml'

type Page = 'overview' | 'workbench' | 'runs' | 'templates'
const navigation = [
  { id: 'overview', title: '总览', subtitle: 'Overview', icon: LayoutDashboard },
  { id: 'workbench', title: '方案工作台', subtitle: 'Plans & workbench', icon: Files },
  { id: 'runs', title: '检查与报告', subtitle: 'Runs & reports', icon: Activity },
  { id: 'templates', title: '模板与清理', subtitle: 'Templates & cleanup', icon: BookOpen },
] as const

function pageFromHash(): Page {
  const key = location.hash.slice(1)
  return navigation.some((n) => n.id === key) ? (key as Page) : 'overview'
}

export default function App() {
  const [page, setPage] = useState<Page>(pageFromHash)
  const [auth, setAuth] = useState<'loading' | 'login' | 'ready'>('loading')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [diagnostics, setDiagnostics] = useState<Diagnostic[]>([])
  const [selectedIssue, setSelectedIssue] = useState<Diagnostic | null>(null)
  const [workspaceError, setWorkspaceError] = useState('')
  const [sessionExpired, setSessionExpired] = useState(false)
  const [reauthOpen, setReauthOpen] = useState(false)
  const [reauthToken, setReauthToken] = useState('')
  const [reauthError, setReauthError] = useState('')
  const [reauthBusy, setReauthBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [info, setInfo] = useState<SystemInfo | null>(null)
  const [plans, setPlans] = useState<SavedPlan[]>([])
  const [document, setDocument] = useState<Plan>(newPlan)
  const [draftKey, setDraftKey] = useState<string | null>(null)
  const [draftRevision, setDraftRevision] = useState<string | null>(null)
  const [recordKey, setRecordKey] = useState<string | null>(null)
  const [recordRevision, setRecordRevision] = useState<string | null>(null)
  const [runPlan, setRunPlan] = useState<{ document: Plan; recordKey: string | null } | null>(null)
  const [editorEpoch, setEditorEpoch] = useState(0)
  const [saveEpoch, setSaveEpoch] = useState(0)
  const editorIdentity = useRef(0)
  const [dirty, setDirty] = useState(false)
  const [digest, setDigest] = useState('')
  const [busy, setBusy] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [mobileNav, setMobileNav] = useState(false)
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < 768)
  const sidebar = useRef<HTMLElement>(null)
  const navToggle = useRef<HTMLButtonElement>(null)
  const previousMobileNav = useRef(false)
  const [theme, setTheme] = useState(() => {
    try {
      return window.localStorage.getItem('readyspec-theme') === 'light' ? 'light' : 'dark'
    } catch {
      return 'dark'
    }
  })
  const [importing, setImporting] = useState(false)
  const [importIntent, setImportIntent] = useState<'workbench' | 'run'>('workbench')
  const [rerunConfig, setRerunConfig] = useState<Record<string, unknown> | null>(null)
  const [runSubmissionUncertain, setRunSubmissionUncertain] = useState(false)
  const runSubmissionUncertainRef = useRef(false)
  const [help, setHelp] = useState(false)
  const [confirm, setConfirm] = useState<{ title: string; action: () => void } | null>(null)
  const revision = useRef(0)

  useEffect(() => {
    const resize = () => {
      const mobile = window.innerWidth < 768
      setIsMobile(mobile)
      if (!mobile) setMobileNav(false)
    }
    window.addEventListener('resize', resize)
    return () => window.removeEventListener('resize', resize)
  }, [])
  useEffect(() => {
    const open = isMobile && mobileNav
    if (open) sidebar.current?.querySelector<HTMLElement>('[aria-current="page"]')?.focus()
    else if (previousMobileNav.current && !window.document.querySelector('[aria-modal="true"]'))
      navToggle.current?.focus()
    previousMobileNav.current = open
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (window.document.querySelector('[aria-modal="true"]')) return
      if (event.key === 'Escape') {
        event.preventDefault()
        setMobileNav(false)
      }
      if (event.key === 'Tab') {
        const controls = Array.from(
          sidebar.current?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href]') ?? [],
        )
        const first = controls[0],
          last = controls.at(-1)
        if (event.shiftKey && window.document.activeElement === first) {
          event.preventDefault()
          last?.focus()
        } else if (!event.shiftKey && window.document.activeElement === last) {
          event.preventDefault()
          first?.focus()
        }
      }
    }
    window.document.addEventListener('keydown', onKey)
    return () => window.document.removeEventListener('keydown', onKey)
  }, [isMobile, mobileNav])

  useEffect(
    () =>
      api.onSessionExpired(() => {
        setSessionExpired(true)
        setReauthOpen(true)
        setReauthError('')
        setReauthToken('')
      }),
    [],
  )

  async function reauthenticate(value: string) {
    setReauthBusy(true)
    setReauthError('')
    try {
      await api.login(value)
      setSessionExpired(false)
      setReauthOpen(false)
      setReauthToken('')
      setError('')
      setNotice('会话已恢复，当前未保存的编辑已保留。请重新发起刚才的操作。')
    } catch (err) {
      setReauthError(message(err))
    } finally {
      setReauthBusy(false)
    }
  }

  useEffect(() => {
    window.document.documentElement.dataset.theme = theme
    try {
      window.localStorage.setItem('readyspec-theme', theme)
    } catch {
      /* Theme still works when browser storage is disabled. */
    }
  }, [theme])
  useEffect(() => {
    runSubmissionUncertainRef.current = runSubmissionUncertain
  }, [runSubmissionUncertain])
  useEffect(() => {
    const listener = () => {
      const next = pageFromHash()
      if (runSubmissionUncertainRef.current && next !== 'runs') {
        location.hash = 'runs'
        setPage('runs')
        setConfirm({
          title: '任务接受状态尚未确认。放弃后将不能用原幂等键重试。',
          action: () => {
            runSubmissionUncertainRef.current = false
            setRunSubmissionUncertain(false)
            location.hash = next
            setPage(next)
          },
        })
        return
      }
      setPage(next)
    }
    window.addEventListener('hashchange', listener)
    return () => window.removeEventListener('hashchange', listener)
  }, [])
  const navigate = (next: Page) => {
    if (runSubmissionUncertainRef.current && next !== 'runs') {
      setConfirm({
        title: '任务接受状态尚未确认。放弃后将不能用原幂等键重试。',
        action: () => {
          runSubmissionUncertainRef.current = false
          setRunSubmissionUncertain(false)
          location.hash = next
          setPage(next)
          setMobileNav(false)
        },
      })
      return
    }
    location.hash = next
    setPage(next)
    if (next === 'runs' && page !== 'runs') {
      setRunPlan(null)
      setRerunConfig(null)
    }
    setMobileNav(false)
    setError('')
    setNotice('')
  }
  const load = useCallback(async () => {
    const [system, saved] = await Promise.all([
      api.request<SystemInfo>('/system/info'),
      api.request<{ items: SavedPlan[] }>('/plans'),
    ])
    setInfo(system)
    setPlans(saved.items)
    setWorkspaceError('')
    setAuth('ready')
  }, [])
  const login = useCallback(
    async (value: string, silent = false) => {
      setAuth('loading')
      setError('')
      try {
        await api.login(value)
        setToken('')
        setSessionExpired(false)
        setReauthOpen(false)
      } catch (err) {
        setAuth('login')
        setError(silent && err instanceof ApiError && err.status === 401 ? '' : message(err))
        return
      }
      try {
        await load()
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) {
          setAuth('login')
          setSessionExpired(false)
          setReauthOpen(false)
          setWorkspaceError('')
          setError(message(err))
          return
        }
        setWorkspaceError(message(err))
        setAuth('ready')
      }
    },
    [load],
  )
  useEffect(() => {
    const bootstrap = new URLSearchParams(location.hash.slice(1)).get('token') ?? ''
    if (bootstrap) history.replaceState(null, '', `${location.pathname}${location.search}#overview`)
    void login(bootstrap, bootstrap === '')
  }, [login])
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (dirty) {
        event.preventDefault()
        event.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [dirty])

  function mutate(edit: (p: Plan) => void) {
    setSelectedIssue(null)
    setDiagnostics([])
    const next = structuredClone(document)
    edit(next)
    delete next.digest
    revision.current++
    setDocument(next)
    setDirty(true)
    setDigest('')
    setNotice('')
    setError('')
  }
  function replace(
    next: Plan,
    saved = false,
    key: string | null = null,
    savedRevision: string | null = null,
  ) {
    revision.current++
    editorIdentity.current++
    setEditorEpoch(editorIdentity.current)
    setDraftKey(key)
    setDraftRevision(savedRevision)
    setRecordKey(saved && next.state !== 'DRAFT' ? key : null)
    setRecordRevision(saved && next.state !== 'DRAFT' ? savedRevision : null)
    setDocument(structuredClone(next))
    setDirty(!saved)
    setDigest(next.digest ?? '')
    setNotice('')
    setError('')
    navigate('workbench')
  }
  function guard(action: () => void) {
    if (dirty) setConfirm({ title: '放弃尚未保存的修改？', action })
    else action()
  }
  async function perform(action: 'validate' | 'save' | 'export' | 'compile') {
    const current = revision.current
    const instance = editorIdentity.current
    setBusy(true)
    setError('')
    setDiagnostics([])
    setNotice('')
    try {
      if (action === 'compile') {
        await api.request('/plans/compile', { source: stringify(document), normalize_draft: true })
        if (revision.current !== current || editorIdentity.current !== instance) return
        setNotice('静态编译通过。未连接目标主机，不代表目标环境已经就绪。')
        return
      }
      if (action === 'save') {
        const result = await api.request<DraftSave>('/plans', {
          source: stringify(document),
          ...(draftKey ? { draft_key: draftKey } : {}),
          ...(draftKey && draftRevision ? { expected_revision: draftRevision } : {}),
        })
        if (editorIdentity.current === instance) {
          setDraftKey(result.draft_key)
          setDraftRevision(result.revision)
        }
        if (revision.current !== current) {
          setNotice('操作已完成，但你又修改了方案。当前修改仍需重新校验或保存。')
          await refreshPlans()
          return
        }
        setDocument(result.document)
        setDigest('')
        setDirty(false)
        setSaveEpoch((epoch) => epoch + 1)
        await refreshPlans()
        setNotice('草稿已保存到当前工作区。')
        return
      }
      const result = await api.request<Validation>('/plans/validate', {
        source: stringify(document),
        normalize_draft: true,
      })
      if (revision.current !== current) {
        setNotice('操作已完成，但你又修改了方案。当前修改仍需重新校验或保存。')
        return
      }
      setDocument(result.document)
      setDigest(result.digest)
      if (action === 'export') {
        download(
          `${result.document.plan_id}-${result.document.version}.yaml`,
          stringify(result.document),
        )
        setNotice('方案已校验并导出。')
      } else setNotice('静态校验通过。此结果仅验证方案文件，不代表目标环境已经就绪。')
    } catch (err) {
      if (revision.current !== current || editorIdentity.current !== instance) return
      setError(message(err))
      if (err instanceof ApiError) setDiagnostics(err.diagnostics)
    } finally {
      setBusy(false)
    }
  }
  async function refreshPlans() {
    const response = await api.request<{ items: SavedPlan[] }>('/plans')
    setPlans(response.items)
  }

  async function releaseDraft() {
    if (!draftKey || !draftRevision || dirty) {
      setError('请先保存当前草稿，再发布已保存的修订。')
      return
    }
    setBusy(true)
    setError('')
    try {
      const record = await api.request<{
        record_key: string
        revision: string
        release_snapshot: Plan
      }>(`/plans/${encodeURIComponent(draftKey)}/release`, {
        expected_revision: draftRevision,
      })
      setDocument(record.release_snapshot)
      setDigest(record.release_snapshot.digest ?? '')
      setDraftKey(null)
      setDraftRevision(null)
      setRecordKey(record.record_key)
      setRecordRevision(record.revision)
      setDirty(false)
      await refreshPlans()
      const manifest = record.release_snapshot.release_manifest as
        Record<string, unknown> | undefined
      setNotice(
        `方案版本已发布并冻结。摘要：${String(manifest?.released_plan_digest ?? record.release_snapshot.digest ?? '未记录')}；模板锁 ${Object.keys((manifest?.template_lock as object) ?? {}).length} 项。`,
      )
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  async function archivePlan() {
    if (!recordKey || !recordRevision || document.state !== 'RELEASED') return
    setBusy(true)
    setError('')
    try {
      const record = await api.request<{
        record_key: string
        revision: string
        archive_snapshot: Plan
      }>(`/plans/${encodeURIComponent(recordKey)}/archive`, {
        expected_revision: recordRevision,
      })
      setDocument(record.archive_snapshot)
      setDigest(record.archive_snapshot.digest ?? '')
      setRecordRevision(record.revision)
      await refreshPlans()
      setNotice('方案版本已停用，历史检查任务保持不变。')
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  function requestDeleteDraft() {
    if (!draftKey || !draftRevision) return
    setConfirm({
      title: '删除当前草稿？此操作不会影响已归档的检查任务。',
      action: () => {
        void (async () => {
          setBusy(true)
          setError('')
          try {
            await api.request(
              `/plans/${encodeURIComponent(draftKey)}`,
              { expected_revision: draftRevision },
              { method: 'DELETE' },
            )
            await refreshPlans()
            replace(newPlan())
            setNotice('草稿已删除。')
          } catch (cause) {
            setError(message(cause))
          } finally {
            setBusy(false)
          }
        })()
      },
    })
  }

  function newVersionDraft() {
    const parts = document.version.split('.')
    const tail = Number(parts.at(-1))
    if (!Number.isSafeInteger(tail) || tail < 0) {
      setError('当前版本不支持自动递增，请手动创建数字版本草稿。')
      return
    }
    parts[parts.length - 1] = String(tail + 1)
    const next = structuredClone(document)
    next.state = 'DRAFT'
    next.version = parts.join('.')
    delete next.digest
    delete next.release_manifest
    delete next.updated_at
    replace(next)
  }

  if (auth === 'loading')
    return (
      <main className="connection-screen">
        <ShieldCheck size={36} />
        <h1>ReadySpec</h1>
        <p>
          <Spinner /> 正在连接工作区…
        </p>
      </main>
    )
  if (auth === 'login')
    return (
      <main className="connection-screen">
        <form
          className="login-panel"
          onSubmit={(e) => {
            e.preventDefault()
            void login(token)
          }}
        >
          <div className="login-brand">
            <ShieldCheck size={28} />
            <strong>ReadySpec</strong>
          </div>
          <h1>连接你的工作区</h1>
          <p>使用启动终端提供的访问令牌，或从终端中的完整访问链接打开控制台。</p>
          {error && <Notice tone="danger">{error}</Notice>}
          <Field label="访问令牌">
            <input
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
              required
            />
          </Field>
          <button className="button primary" type="submit">
            连接工作区 <ArrowRight size={16} />
          </button>
          {!isLoopback(location.hostname) && location.protocol === 'http:' && (
            <Notice tone="warning">当前使用内网 HTTP，访问令牌与业务数据将通过明文传输。</Notice>
          )}
        </form>
      </main>
    )

  if (workspaceError)
    return (
      <main className="connection-screen">
        <div className="login-panel">
          <ShieldCheck size={28} />
          <h1>工作区暂不可用</h1>
          <Notice tone="danger">{workspaceError}</Notice>
          <p>会话已建立。请确认工作区可读后重试，无须再次使用启动令牌。</p>
          <button
            className="button primary"
            onClick={() => {
              setAuth('loading')
              void load().catch((err) => {
                setWorkspaceError(message(err))
                setAuth('ready')
              })
            }}
          >
            重试读取工作区
          </button>
        </div>
      </main>
    )

  const currentNav = navigation.find((n) => n.id === page)!
  return (
    <div
      className={`app-shell ${collapsed ? 'sidebar-collapsed' : ''} ${mobileNav ? 'mobile-nav-open' : ''}`}
    >
      {mobileNav && (
        <button
          className="sidebar-scrim"
          aria-label="关闭导航遮罩"
          tabIndex={-1}
          onClick={() => setMobileNav(false)}
        />
      )}
      <aside
        id="primary-navigation"
        ref={sidebar}
        className="sidebar"
        aria-label="主导航"
        inert={isMobile && !mobileNav}
        aria-hidden={isMobile && !mobileNav ? true : undefined}
      >
        {isMobile && mobileNav && (
          <button
            className="icon-button sidebar-close"
            aria-label="关闭导航"
            onClick={() => setMobileNav(false)}
          >
            <X size={18} />
          </button>
        )}
        <a className="brand" href="#overview" onClick={() => navigate('overview')}>
          <span className="brand-mark">
            <ShieldCheck size={23} />
          </span>
          <span className="brand-text">
            ReadySpec<small>DEPLOYMENT READINESS</small>
          </span>
        </a>
        <div className="workspace-label">
          <span className="workspace-icon">
            <Files size={17} />
          </span>
          <span>
            本地工作区<small>{info?.workspace_label ?? 'Local workspace'}</small>
          </span>
          <span className="connection-dot" />
        </div>
        <div className="nav-section-label">工作空间</div>
        <nav>
          {navigation.map((n) => (
            <button
              key={n.id}
              title={n.title}
              className={`nav-item ${page === n.id ? 'active' : ''}`}
              aria-current={page === n.id ? 'page' : undefined}
              onClick={() => navigate(n.id)}
            >
              <n.icon size={19} />
              <span>
                {n.title}
                <small>{n.subtitle}</small>
              </span>
              {page === n.id && <ChevronRight size={14} className="nav-chevron" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <button className="nav-item" onClick={() => setHelp(true)} title="使用帮助">
            <CircleHelp size={18} />
            <span>使用帮助</span>
          </button>
          <div className="sidebar-status">
            <Wifi size={14} />
            <span>{sessionExpired ? '会话已过期' : '本地服务已连接'}</span>
            <code>{info?.version}</code>
          </div>
        </div>
      </aside>
      <div className="main-shell" inert={isMobile && mobileNav}>
        <header className="topbar">
          <div className="breadcrumb">
            <button
              className="icon-button"
              aria-label="切换导航"
              ref={navToggle}
              aria-controls="primary-navigation"
              aria-expanded={isMobile ? mobileNav : !collapsed}
              onClick={() => {
                if (isMobile) setMobileNav(!mobileNav)
                else setCollapsed(!collapsed)
              }}
            >
              <Menu size={19} />
            </button>
            <span>工作空间</span>
            <ChevronRight size={14} />
            <strong>{currentNav.title}</strong>
          </div>
          <div className="topbar-actions">
            <span className="local-label">
              <span className="connection-dot" /> 本地控制台
            </span>
            <button
              className="icon-button"
              aria-label={theme === 'dark' ? '切换浅色主题' : '切换深色主题'}
              onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
            >
              {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
            </button>
            <button className="icon-button" aria-label="使用帮助" onClick={() => setHelp(true)}>
              <CircleHelp size={18} />
            </button>
          </div>
        </header>
        <main className="main-content" id="main-content">
          {sessionExpired && (
            <Notice tone="warning">
              会话已过期。未保存草稿仍保留在当前页面，可继续编辑。
              <button
                className="text-button"
                onClick={() => {
                  setReauthToken('')
                  setReauthError('')
                  setReauthOpen(true)
                }}
              >
                重新认证
              </button>
            </Notice>
          )}
          {error && (
            <div className="feedback">
              <Notice tone="danger">
                {error}
                {diagnostics.map((diagnostic, index) => (
                  <div key={index}>
                    {diagnostic.role_id && <span>角色 {diagnostic.role_id} · </span>}
                    {diagnostic.item_id && (
                      <button
                        className="text-button"
                        onClick={() => {
                          navigate('workbench')
                          setSelectedIssue(diagnostic)
                        }}
                      >
                        定位检查项 {diagnostic.item_id}
                      </button>
                    )}
                    {diagnostic.property && <span> · 属性 {diagnostic.property}</span>}
                    <span> {diagnostic.message}</span>
                  </div>
                ))}
              </Notice>
              <button
                className="icon-button"
                aria-label="关闭错误提示"
                onClick={() => setError('')}
              >
                <X size={16} />
              </button>
            </div>
          )}
          {notice && (
            <div className="feedback">
              <Notice tone="success">{notice}</Notice>
              <button className="icon-button" aria-label="关闭提示" onClick={() => setNotice('')}>
                <X size={16} />
              </button>
            </div>
          )}
          {page === 'overview' && (
            <Overview
              plans={plans}
              onNew={() => guard(() => replace(newPlan()))}
              onImport={() => {
                setImportIntent('run')
                setImporting(true)
              }}
              onOpen={(p) => guard(() => replace(p.document, true, p.key, p.revision))}
              onTemplates={() => navigate('templates')}
              onRuns={() => navigate('runs')}
            />
          )}
          {page === 'workbench' && (
            <Workbench
              key={editorEpoch}
              document={document}
              draftKey={draftKey}
              selectedIssue={selectedIssue}
              plans={plans}
              dirty={dirty}
              digest={digest}
              busy={busy || sessionExpired}
              saveEpoch={saveEpoch}
              mutate={mutate}
              onNew={() => guard(() => replace(newPlan()))}
              onOpen={(p) => guard(() => replace(p.document, true, p.key, p.revision))}
              onImport={() => {
                setImportIntent('workbench')
                setImporting(true)
              }}
              onAction={(a) => void perform(a)}
              onRelease={() => void releaseDraft()}
              onArchive={() => void archivePlan()}
              onNewVersion={newVersionDraft}
              onDeleteDraft={requestDeleteDraft}
            />
          )}
          {page === 'runs' && (
            <>
              <PageHeading
                eyebrow="RUNS & REPORTS"
                title="检查与报告"
                description="追踪检查任务，查看就绪结论与可追溯的观测证据。"
              />
              <Notice>
                任务由本机服务持续管理。静态预览不会连接目标主机；接受任务后，服务会先完成全部目标的身份与权限预检。
              </Notice>
              <RunPreview
                sshHostKeyPolicy={info?.ssh_host_key_policy}
                key={`${JSON.stringify(runPlan?.document ?? document)}:${rerunConfig ? JSON.stringify(rerunConfig) : ''}`}
                document={runPlan?.document ?? document}
                planRecordKey={runPlan ? runPlan.recordKey : recordKey}
                selectedPlanKey={runPlan?.recordKey ?? recordKey ?? draftKey ?? ''}
                plans={plans}
                onSelectPlan={(selected) => {
                  setRunPlan({
                    document: selected.document,
                    recordKey: selected.document.state === 'DRAFT' ? null : selected.key,
                  })
                  setRerunConfig(null)
                }}
                onSubmissionUncertain={setRunSubmissionUncertain}
                rerunConfig={rerunConfig}
              />
              <RunHistory
                onRerun={(config) => {
                  const rawPlan = config.plan
                  if (typeof rawPlan !== 'string') {
                    setError('历史任务缺少冻结方案，无法重跑。')
                    return
                  }
                  try {
                    const parsed = JSON.parse(rawPlan) as Plan
                    const planRecord =
                      typeof config.plan_record_key === 'string' && config.plan_record_key !== ''
                        ? config.plan_record_key
                        : null
                    const selectedRecord = plans.find((item) => item.key === planRecord)
                    if (
                      parsed.state === 'ARCHIVED' ||
                      (planRecord !== null &&
                        (!selectedRecord || selectedRecord.document.state === 'ARCHIVED'))
                    ) {
                      setError(
                        '该方案版本已停用。请选择当前有效发布版本，或创建新草稿后以试跑方式重跑。',
                      )
                      return
                    }
                    setRunPlan({ document: parsed, recordKey: planRecord })
                    setRerunConfig(config)
                    setError('')
                    setNotice('已载入最近一次非秘密配置；请重新输入认证信息并生成预览。')
                  } catch {
                    setError('历史任务的冻结方案无法读取。')
                  }
                }}
              />
              <section className="workflow-explanation">
                {['选择检查方案', '录入主机清单', '分配角色与现场参数', '预览并执行'].map(
                  (step, i) => (
                    <div key={step}>
                      <span className="step-number">{i + 1}</span>
                      <strong>{step}</strong>
                      <small>
                        {
                          [
                            '导入自包含方案文件',
                            '自动判断所需 SSH / sudo 凭据',
                            '仅修改作者开放的参数',
                            '凭据不满足时拒绝执行',
                          ][i]
                        }
                      </small>
                    </div>
                  ),
                )}
              </section>
            </>
          )}
          {page === 'templates' && <Templates />}
        </main>
        <footer className="app-footer">
          <span>
            ReadySpec <span className="muted">/</span> 部署前环境检查
          </span>
          <span>本地处理 · 离线可用</span>
        </footer>
      </div>
      {importing && (
        <ImportDialog
          onClose={() => setImporting(false)}
          onImport={(p, key, savedRevision) => {
            setImporting(false)
            guard(() => {
              replace(p, Boolean(key), key ?? null, savedRevision ?? null)
              if (importIntent === 'run') navigate('runs')
            })
          }}
        />
      )}
      {help && (
        <Modal title="使用 ReadySpec" onClose={() => setHelp(false)}>
          <div className="modal-body prose">
            <p>
              在方案工作台定义目标环境与节点角色，添加自定义 Shell
              检查项，然后校验、保存或导出方案。
            </p>
            <p>草稿保存在当前服务的工作区。浏览器刷新后，尚未保存的编辑不会保留。</p>
            <p>
              静态校验不会执行
              Shell，也不会连接目标主机。接受任务后由本机服务执行预检与采集；正式放行只以完整原生
              SSH 门禁报告为依据。
            </p>
            <p>
              root / sudo 需求由检查项决定；正式执行时只需提供满足要求的凭据，无须额外勾选批准。
            </p>
          </div>
        </Modal>
      )}
      {confirm && (
        <Modal title={confirm.title} onClose={() => setConfirm(null)}>
          <div className="modal-body">
            <p>当前草稿的未保存修改将丢失。你也可以取消，先保存草稿。</p>
            <div className="dialog-actions">
              <button className="button" onClick={() => setConfirm(null)}>
                继续编辑
              </button>
              <button
                className="button danger"
                onClick={() => {
                  confirm.action()
                  setConfirm(null)
                }}
              >
                放弃修改
              </button>
            </div>
          </div>
        </Modal>
      )}
      {reauthOpen && (
        <Modal
          title="会话已过期"
          onClose={() => {
            setReauthOpen(false)
            setReauthToken('')
          }}
        >
          <form
            className="modal-body"
            onSubmit={(event) => {
              event.preventDefault()
              void reauthenticate(reauthToken)
            }}
          >
            <p>未保存草稿仍保留在当前页面。输入访问令牌即可继续保存或校验，无须刷新。</p>
            <Notice>
              如果使用一次性启动链接，请保持此页打开，在原地址重启服务后输入新令牌；也可以在新标签页完成登录，再恢复已有会话。
            </Notice>
            <Field label="新的访问令牌">
              <input
                type="password"
                autoComplete="off"
                value={reauthToken}
                onChange={(event) => setReauthToken(event.target.value)}
                disabled={reauthBusy}
              />
            </Field>
            {reauthError && <Notice tone="danger">{reauthError}</Notice>}
            <div className="dialog-actions">
              <button
                className="button"
                type="button"
                disabled={reauthBusy}
                onClick={() => void reauthenticate('')}
              >
                恢复已有会话
              </button>
              <button
                className="button primary"
                type="submit"
                disabled={reauthBusy || !reauthToken.trim()}
              >
                {reauthBusy && <Spinner />}重新认证
              </button>
            </div>
            <button
              className="text-button"
              type="button"
              onClick={() => {
                setReauthOpen(false)
                setReauthToken('')
              }}
            >
              继续编辑，稍后认证
            </button>
          </form>
        </Modal>
      )}
    </div>
  )
}

export function PageHeading({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow: string
  title: string
  description: string
  actions?: React.ReactNode
}) {
  return (
    <div className="page-heading">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </div>
  )
}

function Overview({
  plans,
  onNew,
  onImport,
  onOpen,
  onTemplates,
  onRuns,
}: {
  plans: SavedPlan[]
  onNew: () => void
  onImport: () => void
  onOpen: (p: SavedPlan) => void
  onTemplates: () => void
  onRuns: () => void
}) {
  const [query, setQuery] = useState('')
  const [recentRuns, setRecentRuns] = useState<
    {
      run_id: string
      status: string
      accepted_at: string
      plan_id: string
      plan_name: string
      plan_version: string
      run_mode: string
      run_scope: string
      executor_backend: string
      verdict?: string
    }[]
  >([])
  const [runsError, setRunsError] = useState('')
  useEffect(() => {
    let active = true
    void api
      .request<{ items: typeof recentRuns }>('/runs')
      .then((value) => {
        if (active) setRecentRuns(value.items.slice(0, 8))
      })
      .catch((cause) => {
        if (active) setRunsError(message(cause))
      })
    return () => {
      active = false
    }
  }, [])
  const filtered = plans.filter((p) =>
    `${p.document.name} ${p.document.plan_id}`.toLowerCase().includes(query.toLowerCase()),
  )
  return (
    <>
      <PageHeading
        eyebrow="WORKSPACE OVERVIEW"
        title="总览"
        description="定义环境要求，让每一次部署都有据可查。"
        actions={
          <>
            <button className="button" onClick={onImport}>
              <Upload size={16} />
              导入方案
            </button>
            <button className="button primary" onClick={onNew}>
              <Plus size={16} />
              新建检查方案
            </button>
          </>
        }
      />
      <Notice>
        支持方案编排、静态校验与持久化检查任务。只有归档报告中的 READY 结论可作为门禁依据。
      </Notice>
      <section className="stats-grid" aria-label="工作区统计">
        <Stat
          label="检查方案"
          value={plans.length}
          detail="已保存到当前工作区"
          icon={<Files size={19} />}
        />
        <Stat
          label="检查项"
          value={plans.reduce((n, p) => n + itemCount(p.document), 0)}
          detail="所有已保存方案合计"
          icon={<FileCheck2 size={19} />}
        />
        <Stat
          label="节点角色"
          value={plans.reduce((n, p) => n + p.document.roles.length, 0)}
          detail="按方案独立定义"
          icon={<ShieldCheck size={19} />}
        />
      </section>
      <section className="quick-grid">
        <div className="quick-panel">
          <span className="quick-icon">
            <Files size={21} />
          </span>
          <div>
            <div className="eyebrow">方案作者</div>
            <h2>编写环境要求</h2>
            <p>配置目标平台、节点角色和检查项，交付自包含的检查方案。</p>
            <button className="text-button" onClick={onNew}>
              打开方案工作台 <ArrowRight size={15} />
            </button>
          </div>
        </div>
        <div className="quick-panel">
          <span className="quick-icon">
            <FileCheck2 size={21} />
          </span>
          <div>
            <div className="eyebrow">部署工程师</div>
            <h2>核对交付方案</h2>
            <p>导入已有方案，核验结构、模板引用与语义摘要。</p>
            <button className="text-button" onClick={onImport}>
              导入并校验方案 <ArrowRight size={15} />
            </button>
          </div>
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div className="heading-inline">
            <h2>近期检查任务</h2>
            <span className="count">{recentRuns.length}</span>
          </div>
          <button className="text-button" onClick={onRuns}>
            查看检查与报告 <ArrowRight size={15} />
          </button>
        </div>
        {runsError ? (
          <Notice tone="warning">暂时无法读取近期任务：{runsError}</Notice>
        ) : recentRuns.length === 0 ? (
          <Empty icon={<Activity size={20} />} title="暂无检查任务">
            导入方案并进入检查向导后，可以在这里查看任务进度与归档报告。
          </Empty>
        ) : (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>方案</th>
                  <th>类别 / 范围</th>
                  <th>执行器</th>
                  <th>任务状态</th>
                  <th>结论</th>
                  <th>接受时间</th>
                </tr>
              </thead>
              <tbody>
                {recentRuns.map((run) => (
                  <tr key={run.run_id}>
                    <td>
                      {run.plan_name || run.plan_id} · {run.plan_version}
                    </td>
                    <td>
                      {run.run_mode} / {run.run_scope}
                    </td>
                    <td>{run.executor_backend}</td>
                    <td>{run.status}</td>
                    <td>{run.verdict ?? '尚无归档报告'}</td>
                    <td>{formatTime(run.accepted_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      <section className="panel">
        <div className="panel-heading">
          <div className="heading-inline">
            <h2>最近方案</h2>
            <span className="count">{plans.length}</span>
          </div>
          <label className="search-field">
            <Search size={15} />
            <input
              aria-label="搜索方案"
              placeholder="搜索名称或标识…"
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
                  <th>方案名称</th>
                  <th>状态</th>
                  <th>版本</th>
                  <th className="numeric">角色 / 检查项</th>
                  <th>最后保存</th>
                  <th>
                    <span className="sr-only">操作</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((p) => (
                  <tr key={p.key}>
                    <td>
                      <button className="table-link" onClick={() => onOpen(p)}>
                        {p.document.name || '未命名方案'}
                      </button>
                      <small className="cell-secondary mono">{p.document.plan_id}</small>
                    </td>
                    <td>
                      <Badge status={p.document.state} />
                    </td>
                    <td className="mono">{p.document.version}</td>
                    <td className="numeric mono">
                      {p.document.roles.length} / {itemCount(p.document)}
                    </td>
                    <td className="muted nowrap">{formatTime(p.updated_at)}</td>
                    <td>
                      <button
                        className="icon-button"
                        aria-label={`打开 ${p.document.name}`}
                        onClick={() => onOpen(p)}
                      >
                        <ArrowRight size={16} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty
            icon={<Files size={25} />}
            title={query ? '没有匹配的方案' : '从第一份检查方案开始'}
          >
            {query ? '尝试其他名称或标识。' : '新建方案，或导入研发团队交付的 YAML 文件。'}
            {query && (
              <button className="text-button" onClick={() => setQuery('')}>
                清除搜索
              </button>
            )}
          </Empty>
        )}
      </section>
      <div className="bottom-links">
        <button className="text-button" onClick={onTemplates}>
          <BookOpen size={16} />
          查阅 36 项预置模板定义 <ChevronRight size={14} />
        </button>
        <button className="text-button" onClick={onRuns}>
          查看检查与报告 <ChevronRight size={14} />
        </button>
      </div>
    </>
  )
}
function Stat({
  label,
  value,
  detail,
  icon,
}: {
  label: string
  value: number
  detail: string
  icon: React.ReactNode
}) {
  return (
    <div className="stat">
      <div className="stat-label">
        {label}
        {icon}
      </div>
      <strong>{value.toLocaleString()}</strong>
      <small>{detail}</small>
    </div>
  )
}

function ImportDialog({
  onClose,
  onImport,
}: {
  onClose: () => void
  onImport: (p: Plan, key?: string, revision?: string) => void
}) {
  const [source, setSource] = useState(''),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  return (
    <Modal title="导入检查方案" onClose={onClose} wide>
      <form
        className="modal-body"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          setError('')
          try {
            const result = await api.request<Validation>('/plans/validate', {
              source,
              normalize_draft: false,
            })
            if (result.document.state === 'DRAFT') {
              onImport(result.document)
            } else {
              const imported = await api.request<{
                record: {
                  record_key: string
                  revision: string
                  release_snapshot: Plan | null
                  archive_snapshot: Plan | null
                }
                created: boolean
              }>('/plans/import', { source })
              const record = imported.record
              onImport(
                record.archive_snapshot ?? record.release_snapshot ?? result.document,
                record.record_key,
                record.revision,
              )
            }
          } catch (err) {
            setError(message(err))
          } finally {
            setBusy(false)
          }
        }}
      >
        <p className="muted">
          选择 YAML / JSON 文件或粘贴方案内容。导入时严格核对已有摘要，不会重签被修改的文件。
        </p>
        <label className="upload-zone">
          <Upload size={20} />
          <span>
            选择方案文件 <small>YAML / JSON · 最大 10 MiB</small>
          </span>
          <input
            aria-label="选择方案文件"
            type="file"
            accept=".yaml,.yml,.json"
            onChange={async (e) => {
              const file = e.target.files?.[0]
              if (!file) return
              if (file.size > 10 * 1024 * 1024) {
                setError('文件超过 10 MiB 上限。')
                return
              }
              try {
                setSource(await file.text())
                setError('')
              } catch {
                setError('无法读取所选文件，请重新选择或粘贴方案内容。')
              }
            }}
          />
        </label>
        <Field label="方案 YAML">
          <textarea
            className="code-editor"
            rows={13}
            spellCheck={false}
            value={source}
            onChange={(e) => setSource(e.target.value)}
            placeholder="schema_version: readyspec.plan/v1"
            required
          />
        </Field>
        {error && <Notice tone="danger">{error}</Notice>}
        <div className="dialog-actions">
          <button className="button" type="button" onClick={onClose}>
            取消
          </button>
          <button className="button primary" type="submit" disabled={busy || !source.trim()}>
            {busy ? <Spinner /> : <Check size={16} />}校验并导入
          </button>
        </div>
      </form>
    </Modal>
  )
}

function message(error: unknown) {
  return error instanceof Error ? error.message : '操作失败，请重试。'
}
function download(name: string, source: string) {
  const url = URL.createObjectURL(new Blob([source], { type: 'application/yaml;charset=utf-8' }))
  const anchor = window.document.createElement('a')
  anchor.href = url
  anchor.download = name
  anchor.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
