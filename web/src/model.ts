export type Importance = 'REQUIRED' | 'WARNING' | 'INFO'
export type EvidenceMode =
  | 'EXISTENCE'
  | 'METADATA'
  | 'CONFIG'
  | 'EFFECTIVE_STATE'
  | 'STATIC_BINARY'
  | 'ACTIVE_PROBE'
  | 'LOG_HISTORY'
export interface CheckTemplate {
  schema_version: string
  template_id: string
  version: string
  name: string
  source: 'BUILTIN' | 'USER_PUBLIC' | 'PLAN_LOCAL'
  domain: string
  execution_mode: 'STANDARD' | 'PRIVILEGED'
  evidence_mode: EvidenceMode
  params_schema: Record<string, unknown>
  implementation: Record<string, unknown>
  digest: string
}
export interface CheckItem {
  item_id: string
  name: string
  template_id: string
  template_version: string
  template_digest: string
  importance: Importance
  enabled: boolean
  params: Record<string, unknown>
  explicit_null_params: string[]
  overridable_params: string[]
}
export interface Role {
  role_id: string
  name: string
  min_nodes: number
  max_nodes: number | null
  allow_colocation: boolean
  items: CheckItem[]
}
export interface Plan {
  schema_version: string
  plan_id: string
  name: string
  software_name: string
  software_version: string
  version: string
  state: 'DRAFT' | 'RELEASED' | 'ARCHIVED'
  description?: string
  topology_policy: { allowed_arch: string[] }
  roles: Role[]
  embedded_templates: CheckTemplate[]
  template_lock: Record<string, string>
  digest?: string | null
  updated_at?: string | null
  release_manifest?: Record<string, unknown> | null
}
export interface SavedPlan {
  key: string
  document: Plan
  updated_at: string
}
export interface Validation {
  document: Plan
  digest: string
}
export interface DraftSave {
  document: Plan
  draft_key: string
  validated: false
}
export interface SystemInfo {
  version: string
  executor_available: boolean
  workspace_label?: string
}

export function randomSuffix(length: number) {
  // getRandomValues also works on the plain-HTTP LAN origins supported by serve.
  const bytes = crypto.getRandomValues(new Uint8Array(Math.ceil(length / 2)))
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0'))
    .join('')
    .slice(0, length)
}

export function newPlan(): Plan {
  return {
    schema_version: 'readyspec.plan/v1',
    plan_id: `plan-${randomSuffix(8)}`,
    name: '未命名方案',
    software_name: 'my-application',
    software_version: '1.0.0',
    version: '1.0.0',
    state: 'DRAFT',
    description: '',
    topology_policy: {
      allowed_arch: ['x86_64', 'aarch64'],
    },
    roles: [
      {
        role_id: 'app',
        name: '应用节点',
        min_nodes: 1,
        max_nodes: null,
        allow_colocation: false,
        items: [],
      },
    ],
    embedded_templates: [],
    template_lock: {},
  }
}

export function itemCount(plan: Plan) {
  return plan.roles.reduce((n, r) => n + r.items.length, 0)
}
export function isLoopback(host: string) {
  return ['localhost', '127.0.0.1', '[::1]', '::1'].includes(host)
}
export function formatTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' }).format(
    new Date(value),
  )
}
