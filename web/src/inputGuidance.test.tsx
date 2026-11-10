import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { Workbench } from './Workbench'
import { newPlan } from './model'
import fixtures from '../../internal/plan/testdata/capacity.json'
import { inputIssue, requiredParameters } from './inputGuidance'
import { useState } from 'react'

test('shared capacity examples distinguish syntax feedback from authoritative range validation', () => {
  for (const fixture of fixtures)
    expect(
      !!inputIssue({ 'x-readyspec-type': 'capacity' }, fixture.value, true),
      fixture.value,
    ).toBe(fixture.local_error)
  expect(inputIssue({ type: 'integer', minimum: 0 }, 0, true)).toBe('')
  expect(inputIssue({ type: 'boolean' }, false, true)).toBe('')
  expect(inputIssue({ type: ['string', 'null'] }, null, true)).toBe('')
  expect(inputIssue({ type: 'string', default: 'value' }, null, true)).not.toBe('')
  expect([
    ...requiredParameters(
      {
        allOf: [
          { if: { properties: { mode: { const: 'minimum' } } }, then: { required: ['min_swap'] } },
        ],
      },
      { mode: 'minimum' },
    ),
  ]).toEqual(['min_swap'])
  expect([
    ...requiredParameters(
      {
        allOf: [
          { if: { properties: { mode: { const: 'minimum' } } }, then: { required: ['min_swap'] } },
        ],
      },
      { mode: 'disabled' },
    ),
  ]).toEqual([])
})

test('capacity field explains units, validates precise ranges and still allows draft saving', async () => {
  let resolveOld: ((response: Response) => void) | undefined
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const plan = JSON.parse(JSON.parse(String(init?.body)).source)
      if (plan.roles[0].items[0].params.min_memory === '4 GB')
        return new Promise<Response>((resolve) => {
          resolveOld = resolve
        })
      return plan.roles[0].items[0].params.min_memory === '0.1 B'
        ? new Response(
            JSON.stringify({
              error: 'PLAN_PARAMETER_ERROR',
              diagnostics: [
                {
                  code: 'CAPACITY_RANGE',
                  role_id: 'app',
                  item_id: 'memory',
                  field: '/roles/0/items/0/params/min_memory',
                  message: '容量范围不符合要求。',
                },
              ],
            }),
            { status: 422 },
          )
        : new Response('{}')
    }),
  )
  const document = newPlan()
  document.embedded_templates.push({
    schema_version: 'readyspec.template/v1',
    template_id: 'H03',
    version: '1',
    name: '内存',
    source: 'BUILTIN',
    domain: 'H',
    execution_mode: 'STANDARD',
    evidence_mode: 'METADATA',
    digest: 'fixture',
    params_schema: {
      required: ['min_memory'],
      properties: { min_memory: { type: 'string', 'x-readyspec-type': 'capacity' } },
    },
    implementation: {},
  })
  document.roles[0].items.push({
    item_id: 'memory',
    name: '主机内存',
    template_id: 'H03',
    template_version: '1',
    template_digest: 'fixture',
    enabled: true,
    importance: 'REQUIRED',
    params: { min_memory: '4G' },
    explicit_null_params: [],
    overridable_params: [],
  })
  const action = vi.fn()
  function Editor() {
    const [current, setCurrent] = useState(document)
    return (
      <Workbench
        document={current}
        draftKey={null}
        plans={[]}
        dirty={true}
        digest=""
        busy={false}
        saveEpoch={0}
        mutate={(fn) =>
          setCurrent((previous) => {
            const next = structuredClone(previous)
            fn(next)
            return next
          })
        }
        onAction={action}
        onNew={() => {}}
        onImport={() => {}}
        onOpen={() => {}}
        onRelease={() => {}}
        onArchive={() => {}}
        onNewVersion={() => {}}
        onDeleteDraft={() => {}}
      />
    )
  }
  render(<Editor />)
  fireEvent.click(screen.getByRole('button', { name: '主机内存' }))
  const field = screen.getByLabelText(/min_memory/)
  expect(field).toHaveAttribute('aria-required', 'true')
  expect(screen.getByText(/GB=10/)).toBeInTheDocument()
  fireEvent.blur(field)
  expect(field).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('alert')).toHaveTextContent('例如 4 GiB')
  fireEvent.click(screen.getByRole('button', { name: '保存草稿' }))
  expect(action).toHaveBeenCalledWith('save')
  fireEvent.change(field, { target: { value: '4 GiB' } })
  fireEvent.blur(field)
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  fireEvent.change(field, { target: { value: '0.1 B' } })
  fireEvent.blur(field)
  expect((await screen.findAllByText('容量范围不符合要求。')).length).toBeGreaterThan(0)
  expect(field).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('button', { name: '保存草稿' })).not.toBeDisabled()
  fireEvent.change(field, { target: { value: '4 GiB' } })
  fireEvent.blur(field)
  expect(screen.queryAllByText('容量范围不符合要求。')).toHaveLength(0)
  fireEvent.change(field, { target: { value: '4 GB' } })
  fireEvent.blur(field)
  await waitFor(() => expect(resolveOld).toBeDefined())
  fireEvent.change(field, { target: { value: '8 GB' } })
  fireEvent.blur(field)
  await act(async () =>
    resolveOld!(
      new Response(
        JSON.stringify({
          error: 'PLAN_PARAMETER_ERROR',
          diagnostics: [
            {
              code: 'CAPACITY_RANGE',
              item_id: 'memory',
              field: '/roles/0/items/0/params/min_memory',
              message: '旧校验错误',
            },
          ],
        }),
        { status: 422 },
      ),
    ),
  )
  await waitFor(() =>
    expect(screen.queryByText('正在校验参数；草稿仍可保存。')).not.toBeInTheDocument(),
  )
  expect(screen.queryByText('旧校验错误')).not.toBeInTheDocument()
})
