export interface Diagnostic {
  code: string
  category?: string
  phase?: string
  host_id?: string
  field?: string
  message: string
  retryable?: boolean
  action?: string
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
    public requestId = '',
    public submissionId = '',
    public acceptance: 'accepted' | 'rejected' | 'unknown' | '' = '',
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
  async request<T>(
    path: string,
    body?: unknown,
    options: { method?: string; signal?: AbortSignal } = {},
  ): Promise<T> {
    const response = await fetch(`/api/v1${path}`, {
      method: options.method ?? (body === undefined ? 'GET' : 'POST'),
      signal: options.signal,
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
      throw new ApiError(
        '会话已过期，请重新认证后继续。',
        401,
        [],
        response.headers.get('X-Request-ID') ?? '',
      )
    }
    if (!response.ok)
      throw new ApiError(
        data.error ?? '请求失败，请重试。',
        response.status,
        Array.isArray(data.diagnostics) ? data.diagnostics : [],
        data.request_id ?? response.headers.get('X-Request-ID') ?? '',
        data.submission_id ?? '',
        data.acceptance ?? '',
      )
    return data as T
  }
  async download(path: string, signal?: AbortSignal) {
    const response = await fetch(`/api/v1${path}`, {
      credentials: 'same-origin',
      signal,
      headers: this.csrf ? { 'X-CSRF-Token': this.csrf } : {},
    })
    if (response.status === 401) {
      this.csrf = ''
      for (const listener of this.sessionExpiryListeners) listener()
      throw new ApiError('会话已过期，请重新认证后继续。', 401)
    }
    if (!response.ok) {
      const data = await response.json().catch(() => ({ error: '下载失败。' }))
      throw new ApiError(data.error ?? '下载失败。', response.status)
    }
    return {
      blob: await response.blob(),
      disposition: response.headers.get('Content-Disposition') ?? '',
    }
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
      let separator = /\r\n\r\n|\n\n|\r\r/
      let match = separator.exec(buffer)
      while (match) {
        const block = buffer.slice(0, match.index)
        buffer = buffer.slice(match.index + match[0].length)
        let id = ''
        let name = 'message'
        const data: string[] = []
        for (const line of block.split(/\r\n|\n|\r/)) {
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
        match = separator.exec(buffer)
      }
    }
    buffer += decoder.decode()
    await reader.cancel().catch(() => undefined)
  }
  async login(token = '') {
    this.csrf = ''
    const response = await this.request<{ csrf_token: string }>('/auth/session', { token })
    this.csrf = response.csrf_token
  }
}

export const api = new Client()
