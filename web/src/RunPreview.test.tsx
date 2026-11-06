import { fireEvent, render, screen } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { RunPreview } from './RunPreview'
import { newPlan } from './model'

test('static preview submits role bindings and clears stale results when input changes', async () => {
  const request = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        host_count: 1,
        candidate_check_count: 2,
        privileged_check_count: 1,
        custom_shell_check_count: 0,
        active_probe_check_count: 0,
        configured_root_hosts: ['node'],
        identity_unverified_hosts: ['node'],
        hosts: [{ host: { id: 'node', address: '192.0.2.1', port: 22 }, roles: ['app'] }],
      }),
      { status: 200 },
    ),
  )
  vi.stubGlobal('fetch', request)
  render(<RunPreview document={newPlan()} />)
  fireEvent.change(screen.getByLabelText('主机清单'), { target: { value: 'node=192.0.2.1' } })
  fireEvent.change(screen.getByLabelText(/绑定主机.*app/), { target: { value: 'node' } })
  fireEvent.click(screen.getByRole('button', { name: '生成静态预览' }))
  expect(await screen.findByText('候选检查项：2')).toBeInTheDocument()
  const body = JSON.parse(request.mock.calls[0][1].body)
  expect(body.role_map).toEqual({ app: ['node'] })
  expect(body.inventory.source).toBe('node=192.0.2.1')
  fireEvent.change(screen.getByLabelText('主机清单'), { target: { value: 'other=192.0.2.2' } })
  expect(screen.queryByText('候选检查项：2')).not.toBeInTheDocument()
  vi.unstubAllGlobals()
})
