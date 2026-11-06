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
  async subscribe(
    path: string,
    lastEventId: string | undefined,
    signal: AbortSignal,
    onEvent: (event: { id: string; name: string; data: unknown }) => void,
  ) {
    const response = await fetch(`/api/v1${path}`, {
      credentials: 'same-origin',
      signal,
      headers: {
        Accept: 'text/event-stream',
        ...(this.csrf ? { 'X-CSRF-Token': this.csrf } : {}),
        ...(lastEventId ? { 'Last-Event-ID': lastEventId } : {}),
      },
    })
    if (response.status === 401) {
      this.csrf = ''
      for (const listener of this.sessionExpiryListeners) listener()
      throw new ApiError('会话已过期，请重新认证后继续。', 401)
    }
    if (!response.ok || !response.body) {
      throw new ApiError('无法订阅检查任务事件。', response.status)
    }
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    while (!signal.aborted) {
      const chunk = await reader.read()
      if (chunk.done) break
      buffer += decoder.decode(chunk.value, { stream: true })
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary)
        buffer = buffer.slice(boundary + 2)
        let id = ''
        let name = 'message'
        const data: string[] = []
        for (const line of block.split('\n')) {
          if (line.startsWith('id:')) id = line.slice(3).trim()
          else if (line.startsWith('event:')) name = line.slice(6).trim()
          else if (line.startsWith('data:')) data.push(line.slice(5).trimStart())
        }
        if (data.length) {
          let value: unknown
          try {
            value = JSON.parse(data.join('\n'))
          } catch {
            value = data.join('\n')
          }
          onEvent({ id, name, data: value })
        }
        boundary = buffer.indexOf('\n\n')
      }
    }
    await reader.cancel().catch(() => undefined)
  }
  async login(token = '') {
    this.csrf = ''
    const response = await this.request<{ csrf_token: string }>('/auth/session', { token })
    this.csrf = response.csrf_token
  }
}

export const api = new Client()
