import {
  cloneElement,
  isValidElement,
  useEffect,
  useId,
  useRef,
  type ReactElement,
  type ReactNode,
} from 'react'
import {
  AlertCircle,
  CheckCircle2,
  Circle,
  Info,
  LoaderCircle,
  MinusCircle,
  ShieldAlert,
  X,
  XCircle,
} from 'lucide-react'

const states = {
  DRAFT: ['草稿', 'neutral', Circle],
  RELEASED: ['已发布', 'success', CheckCircle2],
  ARCHIVED: ['已停用', 'neutral', MinusCircle],
  PASS: ['通过', 'success', CheckCircle2],
  FAIL: ['不满足', 'danger', XCircle],
  ERROR: ['取证错误', 'warning', AlertCircle],
  UNSUPPORTED: ['不支持', 'neutral', MinusCircle],
  SKIP: ['已跳过', 'neutral', Circle],
  READY: ['就绪', 'success', CheckCircle2],
  NOT_READY: ['未就绪', 'danger', XCircle],
  UNKNOWN: ['无法确定', 'neutral', Info],
  STANDARD: ['普通执行', 'neutral', Circle],
  PRIVILEGED: ['特权执行', 'warning', ShieldAlert],
} as const

export function Badge({ status }: { status: keyof typeof states }) {
  const [label, color, Icon] = states[status]
  return (
    <span className={`badge badge-${color}`}>
      <Icon size={12} aria-hidden="true" />
      {label}
    </span>
  )
}
export function Notice({
  children,
  tone = 'info',
}: {
  children: ReactNode
  tone?: 'info' | 'warning' | 'danger' | 'success'
}) {
  const Icon = tone === 'danger' ? AlertCircle : tone === 'success' ? CheckCircle2 : Info
  return (
    <div className={`notice notice-${tone}`} role={tone === 'danger' ? 'alert' : 'status'}>
      <Icon size={17} aria-hidden="true" />
      <div>{children}</div>
    </div>
  )
}
export function Empty({
  icon,
  title,
  children,
  action,
}: {
  icon: ReactNode
  title: string
  children: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="empty-state">
      <div className="empty-icon">{icon}</div>
      <h3>{title}</h3>
      <p>{children}</p>
      {action}
    </div>
  )
}
export function Spinner() {
  return <LoaderCircle size={16} className="spin" aria-label="正在处理" />
}
export function Field({
  label,
  children,
  hint,
  required = false,
  error,
  onBlur,
}: {
  label: string
  children: ReactNode
  hint?: string
  required?: boolean
  error?: string
  onBlur?: () => void
}) {
  const id = useId()
  return (
    <div className="field" onBlur={onBlur}>
      <label htmlFor={id}>{label}</label>
      {required && <span className="required-label">必填</span>}
      {isValidElement(children)
        ? cloneElement(
            children as ReactElement<{
              id: string
              required?: boolean
              'aria-required'?: boolean
              'aria-invalid'?: boolean
              'aria-describedby'?: string
            }>,
            {
              id,
              ...(required ? { required: true, 'aria-required': true } : {}),
              'aria-invalid': !!error,
              'aria-describedby':
                [hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(' ') ||
                undefined,
            },
          )
        : children}
      {hint && <small id={`${id}-hint`}>{hint}</small>}
      {error && (
        <small id={`${id}-error`} role="alert" className="field-error">
          {error}
        </small>
      )}
    </div>
  )
}

const modalStack: string[] = []
let overflowBeforeModals = ''

export function Modal({
  title,
  children,
  onClose,
  wide = false,
  drawer = false,
}: {
  title: string
  children: ReactNode
  onClose: () => void
  wide?: boolean
  drawer?: boolean
}) {
  const titleID = useId()
  const ref = useRef<HTMLDivElement>(null)
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null
    if (modalStack.length === 0) overflowBeforeModals = document.body.style.overflow
    modalStack.push(titleID)
    document.body.style.overflow = 'hidden'
    const focusable = () =>
      Array.from(
        ref.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), a[href], [tabindex="0"]',
        ) ?? [],
      )
    focusable()[0]?.focus()
    const onKey = (event: KeyboardEvent) => {
      if (modalStack.at(-1) !== titleID) return
      if (event.key === 'Escape') {
        event.preventDefault()
        close.current()
      }
      if (event.key === 'Tab') {
        const controls = focusable()
        const first = controls[0],
          last = controls.at(-1)
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault()
          last?.focus()
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault()
          first?.focus()
        }
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      modalStack.splice(modalStack.indexOf(titleID), 1)
      if (modalStack.length === 0) document.body.style.overflow = overflowBeforeModals
      document.removeEventListener('keydown', onKey)
      if (before?.isConnected) before.focus()
    }
  }, [titleID])
  return (
    <div
      className={drawer ? 'drawer-backdrop' : 'modal-backdrop'}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        className={drawer ? 'context-drawer' : `modal ${wide ? 'modal-wide' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        ref={ref}
      >
        <header>
          <h2 id={titleID}>{title}</h2>
          <button className="icon-button" aria-label="关闭对话框" onClick={onClose}>
            <X size={18} />
          </button>
        </header>
        {children}
      </div>
    </div>
  )
}
