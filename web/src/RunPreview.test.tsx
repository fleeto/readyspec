import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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
  fireEvent.click(screen.getByRole('button', { name: '下一步：主机清单' }))
  fireEvent.change(screen.getByLabelText('主机清单'), { target: { value: 'node=192.0.2.1' } })
  fireEvent.click(screen.getByRole('button', { name: '下一步：角色与认证' }))
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

test('partial rerun restores the frozen inventory and only offers anomalous hosts', () => {
  render(
    <RunPreview
      document={newPlan()}
      rerunConfig={{
        inventory_input: {
          source: 'node-a=192.0.2.1\nnode-b=192.0.2.2',
          format: 'quick',
        },
        inventory: [],
        role_map: { app: ['node-a', 'node-b'] },
        run_mode: 'TRIAL',
        run_scope: 'FULL',
        executor_backend: 'mock_simulator',
        partial_source_run_id: 'origin-run',
        partial_hosts: ['node-a'],
        partial_host_ids: ['node-a'],
        rerun_scope: 'PARTIAL',
        budgets: { run_seconds: 240 },
        simulator_profiles: { hosts: { 'node-a': { preflight: 'READY' } } },
      }}
    />,
  )
  fireEvent.click(screen.getByRole('button', { name: '下一步：主机清单' }))
  expect(screen.getByLabelText('主机清单')).toHaveValue('node-a=192.0.2.1\nnode-b=192.0.2.2')
  fireEvent.click(screen.getByRole('button', { name: '下一步：角色与认证' }))
  expect(screen.getByLabelText('执行范围')).toHaveValue('PARTIAL')
  expect(screen.getByRole('checkbox', { name: 'node-a' })).toBeChecked()
  expect(screen.queryByRole('checkbox', { name: 'node-b' })).not.toBeInTheDocument()
})

test('run snapshot renders stage attempt timing and omitted execution modes', async () => {
  const document = newPlan()
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    if (path.endsWith('/runs/preview')) {
      return new Response(
        JSON.stringify({
          host_count: 1,
          candidate_check_count: 1,
          privileged_check_count: 0,
          custom_shell_check_count: 0,
          active_probe_check_count: 0,
          configured_root_hosts: [],
          identity_unverified_hosts: [],
          validated_plan: document,
          hosts: [{ host: { id: 'node', address: '192.0.2.1', port: 22 }, roles: ['app'] }],
        }),
        { status: 200 },
      )
    }
    if (path.endsWith('/runs/run-1/events')) {
      const payload = {
        run: { status: 'COMPLETED' },
        stage_transitions: [
          {
            sequence: 2,
            host_id: 'node',
            execution_mode: 'STANDARD',
            phase: 'RUNNING',
            attempt: 1,
            started_at: '2026-11-06T12:00:00.000Z',
            ended_at: '2026-11-06T12:00:00.075Z',
          },
          {
            sequence: 0,
            host_id: 'node',
            execution_mode: 'PRIVILEGED',
            phase: 'OMITTED',
            attempt: 1,
            omitted: true,
            reason: 'NO_ENABLED_ITEMS',
          },
        ],
      }
      const frame = `id: 3\nevent: snapshot\ndata: ${JSON.stringify(payload)}\n\n`
      return new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(new TextEncoder().encode(frame))
            controller.close()
          },
        }),
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
      )
    }
    if (path.endsWith('/runs/run-1/report')) {
      return new Response(
        JSON.stringify({
          run_id: 'run-1',
          run_status: 'COMPLETED',
          verdict: 'UNKNOWN',
          hosts: [{ host_id: 'node', status: 'COMPLETED', item_results: [] }],
        }),
        { status: 200 },
      )
    }
    if (path.endsWith('/runs') && init?.method === 'POST') {
      return new Response(JSON.stringify({ run_id: 'run-1', status: 'PENDING' }), { status: 202 })
    }
    throw new Error(`unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  render(<RunPreview document={document} />)
  fireEvent.click(screen.getByRole('button', { name: '下一步：主机清单' }))
  fireEvent.change(screen.getByLabelText('主机清单'), { target: { value: 'node=192.0.2.1' } })
  fireEvent.click(screen.getByRole('button', { name: '下一步：角色与认证' }))
  fireEvent.change(screen.getByLabelText(/绑定主机.*app/), { target: { value: 'node' } })
  fireEvent.change(screen.getByLabelText('执行器'), { target: { value: 'mock_simulator' } })
  fireEvent.click(screen.getByRole('button', { name: '生成静态预览' }))
  fireEvent.click(await screen.findByRole('button', { name: '接受并执行任务' }))
  expect(await screen.findByText(/尝试 1 · 开始 .*结束 .*用时 75ms/)).toBeInTheDocument()
  expect(screen.getByText(/PRIVILEGED · OMITTED · NO_ENABLED_ITEMS/)).toBeInTheDocument()
  expect(await screen.findByText(/COMPLETED \/ UNKNOWN/)).toBeInTheDocument()
})

test('SSE reconnect resumes from its cursor and reset replaces the old projection', async () => {
  const document = newPlan()
  const eventRequests: RequestInit[] = []
  let eventAttempt = 0
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    if (path.endsWith('/runs/preview')) {
      return new Response(
        JSON.stringify({
          host_count: 1,
          candidate_check_count: 1,
          privileged_check_count: 0,
          custom_shell_check_count: 0,
          active_probe_check_count: 0,
          configured_root_hosts: [],
          identity_unverified_hosts: [],
          validated_plan: document,
          hosts: [{ host: { id: 'node', address: '192.0.2.1', port: 22 }, roles: ['app'] }],
        }),
        { status: 200 },
      )
    }
    if (path.endsWith('/runs/run-resume/events')) {
      eventRequests.push(init ?? {})
      eventAttempt++
      const frame =
        eventAttempt === 1
          ? `id: 5\nevent: snapshot\ndata: ${JSON.stringify({
              run: { status: 'RUNNING' },
              stage_transitions: [{ sequence: 5, host_id: 'node', phase: 'PREPARING', attempt: 1 }],
            })}\n\n`
          : `id: 8\nevent: reset\ndata: ${JSON.stringify({
              run: { status: 'COMPLETED' },
              stage_transitions: [],
            })}\n\n`
      return new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(new TextEncoder().encode(frame))
            controller.close()
          },
        }),
        { status: 200, headers: { 'Content-Type': 'text/event-stream' } },
      )
    }
    if (path.endsWith('/runs/run-resume/report')) {
      return new Response(
        JSON.stringify({
          run_id: 'run-resume',
          run_status: 'COMPLETED',
          verdict: 'UNKNOWN',
          hosts: [{ host_id: 'node', status: 'COMPLETED', item_results: [] }],
        }),
        { status: 200 },
      )
    }
    if (path.endsWith('/runs') && init?.method === 'POST') {
      return new Response(JSON.stringify({ run_id: 'run-resume', status: 'PENDING' }), {
        status: 202,
      })
    }
    throw new Error(`unexpected request: ${path}`)
  })
  vi.stubGlobal('fetch', fetchMock)

  render(<RunPreview document={document} />)
  fireEvent.click(screen.getByRole('button', { name: '下一步：主机清单' }))
  fireEvent.change(screen.getByLabelText('主机清单'), { target: { value: 'node=192.0.2.1' } })
  fireEvent.click(screen.getByRole('button', { name: '下一步：角色与认证' }))
  fireEvent.change(screen.getByLabelText(/绑定主机.*app/), { target: { value: 'node' } })
  fireEvent.change(screen.getByLabelText('执行器'), { target: { value: 'mock_simulator' } })
  fireEvent.click(screen.getByRole('button', { name: '生成静态预览' }))
  fireEvent.click(await screen.findByRole('button', { name: '接受并执行任务' }))

  expect(await screen.findByText(/PREPARING · 尝试 1/)).toBeInTheDocument()
  await waitFor(() => expect(eventRequests).toHaveLength(2))
  expect(await screen.findByText(/COMPLETED \/ UNKNOWN/)).toBeInTheDocument()
  expect(screen.queryByText(/PREPARING · 尝试 1/)).not.toBeInTheDocument()
  expect(eventRequests).toHaveLength(2)
  expect(eventRequests[1].headers).toMatchObject({ 'Last-Event-ID': '5' })
  vi.unstubAllGlobals()
})
