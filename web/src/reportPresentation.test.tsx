import { act, render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { api } from './api'
import { CheckSummary, FailureSummary, checkName, useDisplayMetadata } from './reportPresentation'

test('report explains measured memory, available space and CPU without changing verdicts', () => {
  const metadata = {
    run_id: 'run',
    items: [
      { role_id: 'app', item_id: 'memory', item_name: '最低内存', template_name: '内存' },
      { role_id: 'app', item_id: 'space', item_name: '目录可用空间', template_name: '磁盘' },
    ],
  }
  render(
    <>
      <FailureSummary
        empty
        failures={[
          { code: 'IDENTITY_ERROR', diagnostics: [{ code: 'HOST_KEY_UNKNOWN', message: 'safe' }] },
        ]}
      />
      <CheckSummary
        metadata={metadata}
        item={{
          role_id: 'app',
          item_id: 'memory',
          template_id: 'H03',
          status: 'FAIL',
          evidence: [
            {
              property: 'stdout',
              expected: { min_memory: 16000000000 },
              actual: { mem_total_bytes: 15959113728 },
            },
          ],
        }}
      />
      <CheckSummary
        metadata={metadata}
        item={{
          role_id: 'app',
          item_id: 'space',
          template_id: 'S04',
          status: 'FAIL',
          evidence: [
            {
              property: 'stdout',
              expected: { min_available: 100000000000, path: '/srv' },
              actual: { available_bytes: 40304996352 },
            },
          ],
        }}
      />
      <CheckSummary
        metadata={metadata}
        item={{
          role_id: 'app',
          item_id: 'cpu',
          template_id: 'H02',
          status: 'PASS',
          evidence: [{ property: 'stdout', expected: { ge: 4 }, actual: '4\n' }],
        }}
      />
    </>,
  )
  expect(screen.getByText('最低内存')).toBeInTheDocument()
  expect(screen.getByText(/实际可见内存：15.96 GB/)).toHaveTextContent('15959113728 字节')
  expect(screen.getByText(/实际非特权用户可用：40.30 GB/)).toBeInTheDocument()
  expect(screen.getByText(/最低逻辑 CPU：4；实际在线逻辑 CPU：4/)).toBeInTheDocument()
  expect(screen.getByText('未执行检查')).toBeInTheDocument()
  expect(screen.getByText(/可信控制台核对指纹/)).toBeInTheDocument()
  expect(screen.getAllByText(/未达到方案要求/)).toHaveLength(2)
})

function NamedCheck({ runID }: { runID: string }) {
  const { metadata, error } = useDisplayMetadata(runID)
  return (
    <>
      <div>{checkName({ role_id: 'app', item_id: 'item', status: 'FAIL' }, metadata)}</div>
      <p>{error}</p>
    </>
  )
}

test('metadata response from an old run never changes the newly selected run', async () => {
  let resolveOld!: (response: Response) => void
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes('/old/')
        ? new Promise<Response>((resolve) => {
            resolveOld = resolve
          })
        : new Response(
            JSON.stringify({
              run_id: 'new',
              items: [
                {
                  role_id: 'app',
                  item_id: 'item',
                  item_name: 'New frozen name',
                  template_name: '',
                },
              ],
            }),
          ),
    ),
  )
  const { rerender } = render(<NamedCheck runID="old" />)
  rerender(<NamedCheck runID="new" />)
  expect(await screen.findByText('New frozen name')).toBeInTheDocument()
  await act(async () =>
    resolveOld(
      new Response(
        JSON.stringify({
          run_id: 'old',
          items: [
            { role_id: 'app', item_id: 'item', item_name: 'Wrong old name', template_name: '' },
          ],
        }),
      ),
    ),
  )
  expect(screen.queryByText('Wrong old name')).not.toBeInTheDocument()
})

test('missing metadata degrades to ID while unauthorized metadata expires the session', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 404 })))
  const first = render(<NamedCheck runID="missing" />)
  expect(await screen.findByText(/名称暂不可用/)).toBeInTheDocument()
  expect(screen.getByText('item')).toBeInTheDocument()
  first.unmount()
  const expired = vi.fn()
  const unsubscribe = api.onSessionExpired(expired)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })))
  render(<NamedCheck runID="restricted" />)
  await waitFor(() => expect(expired).toHaveBeenCalledOnce())
  expect(await screen.findByText(/无访问权限/)).toBeInTheDocument()
  unsubscribe()
})

test('invalid metadata stays readable and untrusted names render as text', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue(new Response(JSON.stringify({ run_id: 'wrong-run', items: [] }))),
  )
  const invalid = render(<NamedCheck runID="expected-run" />)
  expect(await screen.findByText(/名称暂不可用/)).toBeInTheDocument()
  expect(screen.getByText('item')).toBeInTheDocument()
  invalid.unmount()
  const name = '<img src=x onerror=alert(1)>'
  const { container } = render(
    <CheckSummary
      item={{ role_id: 'app', item_id: 'item', status: 'PASS' }}
      metadata={{
        run_id: 'expected-run',
        items: [
          { role_id: 'other', item_id: 'item', item_name: 'Wrong role', template_name: '' },
          { role_id: 'app', item_id: 'item', item_name: name, template_name: '' },
        ],
      }}
    />,
  )
  expect(screen.getByText(name)).toBeInTheDocument()
  expect(screen.queryByText('Wrong role')).not.toBeInTheDocument()
  expect(container.querySelector('img')).toBeNull()
})
