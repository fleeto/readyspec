import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { Templates } from './Templates'

test('cleanup confirmation never sends a destructive retry and ignore remains auditable', async () => {
  let ignored = false
  const requests: { path: string; method: string; body?: Record<string, unknown> }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://readyspec.test')
      const method = init?.method ?? 'GET'
      const body =
        typeof init?.body === 'string'
          ? (JSON.parse(init.body) as Record<string, unknown>)
          : undefined
      requests.push({ path: `${url.pathname}${url.search}`, method, body })
      if (url.pathname.endsWith('/templates')) {
        return new Response(JSON.stringify({ items: [], definitions: [] }), { status: 200 })
      }
      if (url.pathname.endsWith('/cleanups') && method === 'GET') {
        const item = {
          cleanup_id: 'cleanup-1',
          host_id: 'node-a',
          machine_id: 'machine-a',
          boot_id: 'boot-a',
          status: ignored ? 'IGNORED' : 'PENDING',
          remote_path: '/tmp/readyspec-run',
          ...(ignored ? { audit_reason: 'operator retained the directory for investigation' } : {}),
        }
        const items =
          ignored && !url.search ? [] : [{ run_id: 'run-1', auth_method: 'password', item }]
        return new Response(JSON.stringify({ items }), { status: 200 })
      }
      if (url.pathname.endsWith('/cleanup/cleanup-1') && method === 'POST') {
        if (body?.action === 'CLEAN') {
          return new Response(
            JSON.stringify({
              cleanup_id: 'cleanup-1',
              host_id: 'node-a',
              status: 'UNKNOWN',
              reason: 'associated processes are active',
              remote_path: '/tmp/readyspec-run',
            }),
            { status: 200 },
          )
        }
        ignored = true
        return new Response(
          JSON.stringify({
            cleanup_id: 'cleanup-1',
            host_id: 'node-a',
            status: 'IGNORED',
            remote_path: '/tmp/readyspec-run',
            audit_reason: body?.audit_reason,
          }),
          { status: 200 },
        )
      }
      throw new Error(`unexpected request: ${method} ${url.pathname}${url.search}`)
    }),
  )

  render(<Templates />)
  fireEvent.click(await screen.findByRole('tab', { name: /远端残留清理/ }))
  const cleanButton = await screen.findByRole('button', { name: '重新核验并清理' })
  fireEvent.click(cleanButton)
  expect(await screen.findByRole('dialog')).toBeInTheDocument()
  expect(screen.getByText(/尚未尝试终止进程或删除目录/)).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  expect(
    requests.filter((request) => request.method === 'POST' && request.body?.action === 'CLEAN'),
  ).toHaveLength(1)

  const reason = 'operator retained the directory for investigation'
  fireEvent.change(screen.getByLabelText('node-a 忽略原因'), { target: { value: reason } })
  const ignoreButton = screen.getByRole('button', { name: '记录忽略' })
  expect(ignoreButton).toBeEnabled()
  fireEvent.click(ignoreButton)
  await waitFor(() =>
    expect(requests.some((request) => request.body?.action === 'IGNORE')).toBe(true),
  )
  expect(requests.find((request) => request.body?.action === 'IGNORE')?.body?.audit_reason).toBe(
    reason,
  )

  fireEvent.change(screen.getByLabelText('清理审计筛选'), { target: { value: 'all' } })
  await waitFor(() => {
    expect(screen.getAllByRole('row').some((row) => row.textContent?.includes(reason))).toBe(true)
  })
})
