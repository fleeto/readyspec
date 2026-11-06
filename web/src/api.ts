export interface Diagnostic {
  code: string
  message: string
  role_id?: string
  item_id?: string
  resource?: string
  property?: string
  related_items?: string[]
}

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public diagnostics: Diagnostic[] = [],
  ) {
    super(message)
  }
}

class Client {
  private csrf = ''
  private sessionExpiryListeners = new Set<() => void>()
  onSessionExpired(listener: () => void) {
    this.sessionExpiryListeners.add(listener)
    return () => {
      this.sessionExpiryListeners.delete(listener)
    }
  }
  async request<T>(path: string, body?: unknown): Promise<T> {
    const response = await fetch(`/api/v1${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      credentials: 'same-origin',
      headers: {
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...(this.csrf ? { 'X-CSRF-Token': this.csrf } : {}),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
    const data = await response
      .json()
      .catch(() => ({ error: '服务返回了无法读取的响应，请重试。' }))
    if (response.status === 401 && path !== '/auth/session') {
      this.csrf = ''
      for (const listener of this.sessionExpiryListeners) listener()
      throw new ApiError('会话已过期，请重新认证后继续。', 401)
    }
    if (!response.ok)
      throw new ApiError(
        data.error ?? '请求失败，请重试。',
        response.status,
        Array.isArray(data.diagnostics) ? data.diagnostics : [],
      )
    return data as T
  }
  async login(token = '') {
    this.csrf = ''
    const response = await this.request<{ csrf_token: string }>('/auth/session', { token })
    this.csrf = response.csrf_token
  }
}

export const api = new Client()
