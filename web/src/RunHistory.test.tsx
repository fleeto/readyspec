import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { RunHistory } from './RunHistory'

test('report details show parameter source categories without parameter values', async () => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input)
    if (path.endsWith('/runs')) {
      return new Response(
        JSON.stringify({
          items: [
            {
              run_id: 'run-1',
              plan_id: 'plan-1',
              plan_name: 'Sample',
              plan_version: '1.0.0',
              status: 'COMPLETED',
              accepted_at: '2026-11-06T12:00:00Z',
              run_mode: 'TRIAL',
              run_scope: 'FULL',
              executor_backend: 'mock_simulator',
            },
          ],
        }),
        { status: 200 },
      )
    }
    return new Response(
      JSON.stringify({
        run_id: 'run-1',
        plan_digest: `sha256:${'a'.repeat(64)}`,
        run_mode: 'TRIAL',
        run_scope: 'FULL',
        executor_backend: 'mock_simulator',
        run_status: 'COMPLETED',
        verdict: 'UNKNOWN',
        verdict_reasons: [],
        started_at: '2026-11-06T12:00:00Z',
        finished_at: '2026-11-06T12:00:01Z',
        topology_results: [],
        hosts: [
          {
            host_id: 'node-a',
            status: 'COMPLETED',
            roles: ['app'],
            item_results: [
              {
                role_id: 'app',
                item_id: 'disk',
                status: 'PASS',
                template_id: 'S01',
                template_version: '1.0.0',
                execution_mode: 'STANDARD',
                evidence_mode: 'EXISTENCE',
                parameter_sources: { path: 'host', filetype: 'template' },
                evidence: [],
              },
            ],
            failures: [],
          },
        ],
        cleanup_items: [
          {
            cleanup_id: 'cleanup-1',
            host_id: 'node-a',
            status: 'IGNORED',
            remote_path: '/tmp/readyspec-run',
            audit_reason: 'operator verified no cleanup was needed',
          },
        ],
        failures: [],
      }),
      { status: 200 },
    )
  })
  vi.stubGlobal('fetch', fetchMock)
  render(<RunHistory />)
  fireEvent.click(await screen.findByRole('button', { name: '查看报告' }))
  expect(await screen.findByText(/参数来源：filetype=template · path=host/)).toBeInTheDocument()
  expect(
    screen.getByText(
      'node-a · IGNORED · /tmp/readyspec-run · operator verified no cleanup was needed',
    ),
  ).toBeInTheDocument()
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
})

test('report warns about verified UID zero independently of the login name', async () => {
  const run = {
    run_id: 'root-run',
    plan_id: 'root-plan',
    plan_name: 'Root fixture',
    plan_version: '1.0.0',
    status: 'COMPLETED',
    accepted_at: '2026-11-08T12:00:00Z',
    run_mode: 'GATE',
    run_scope: 'FULL',
    executor_backend: 'native_ssh',
  }
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async (input: RequestInfo | URL) =>
        new Response(
          JSON.stringify(
            String(input).endsWith('/runs')
              ? { items: [run] }
              : {
                  ...run,
                  run_status: 'COMPLETED',
                  verdict: 'READY',
                  started_at: run.accepted_at,
                  finished_at: run.accepted_at,
                  topology_results: [],
                  hosts: [
                    {
                      host_id: 'admin-alias',
                      status: 'COMPLETED',
                      login_uid: 0,
                      high_risk_root_audit: true,
                      roles: ['app'],
                      item_results: [],
                      failures: [],
                    },
                  ],
                  cleanup_items: [],
                  failures: [],
                },
          ),
          { status: 200 },
        ),
    ),
  )
  render(<RunHistory />)
  fireEvent.click(await screen.findByRole('button', { name: '查看报告' }))
  expect(await screen.findByText(/root（UID 0）身份执行检查/)).toHaveTextContent('admin-alias')
})
