import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import App from './App'
import { newPlan } from './model'
import { Workbench } from './Workbench'
import { parse } from 'yaml'

function stubWorkspace() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string) => {
      const data = input.endsWith('/auth/session')
        ? { csrf_token: 'test-csrf' }
        : input.endsWith('/system/info')
          ? { version: '0.2.0-dev', executor_available: false }
          : input.endsWith('/templates')
            ? {
                items: [],
                definitions: [
                  {
                    id: 'A03',
                    name: 'SSH 服务端生效配置',
                    summary: 'sshd -T',
                    evidence: 'EFFECTIVE_STATE',
                    privileged: true,
                  },
                ],
              }
            : { items: [] }
      return new Response(JSON.stringify(data), { status: 200 })
    }),
  )
}

test('incomplete JSON parameter text stays in the draft instead of saving the old value', async () => {
  const document = newPlan()
  document.embedded_templates.push({
    schema_version: 'readyspec.template/v1',
    template_id: 'fixture',
    version: '1',
    name: 'JSON fixture',
    source: 'BUILTIN',
    domain: 'S',
    execution_mode: 'STANDARD',
    evidence_mode: 'CONFIG',
    digest: 'fixture',
    params_schema: { properties: { options: { type: 'array' } } },
    implementation: { kind: 'BUILTIN' },
  })
  document.roles[0].items.push({
    item_id: 'json',
    name: 'JSON check',
    template_id: 'fixture',
    template_version: '1',
    template_digest: 'fixture',
    importance: 'REQUIRED',
    enabled: true,
    params: { options: ['old'] },
    explicit_null_params: [],
    overridable_params: [],
  })
  render(
    <Workbench
      document={document}
      plans={[]}
      dirty={false}
      digest=""
      busy={false}
      saveEpoch={0}
      mutate={(fn) => fn(document)}
      onNew={() => {}}
      onImport={() => {}}
      onOpen={() => {}}
      onAction={() => {}}
    />,
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'JSON check' }))
  await user.clear(screen.getByLabelText('options'))
  await user.click(screen.getByLabelText('options'))
  await user.paste('[')
  expect(screen.getByRole('alert')).toHaveTextContent('请输入有效 JSON')
  expect(document.roles[0].items[0].params.options).toBe('[')
})

test('static compilation diagnostics identify the conflicting check', async () => {
  stubWorkspace()
  const initial = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if (String(input).endsWith('/plans/compile')) {
      const document = parse(JSON.parse(String(init?.body)).source)
      return new Response(
        JSON.stringify({
          error: 'RESOURCE_CONFLICT: 同一资源属性存在不同要求',
          diagnostics: [
            {
              code: 'RESOURCE_CONFLICT',
              message: '同一资源属性存在不同要求',
              role_id: 'app',
              item_id: document.roles[0].items[0].item_id,
              property: 'exists',
            },
          ],
        }),
        { status: 422 },
      )
    }
    return initial(input, init)
  })
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '自定义 Shell' }))
  await user.click(screen.getByRole('button', { name: '静态编译' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('RESOURCE_CONFLICT')
  expect(await screen.findByRole('button', { name: /定位检查项/ })).toBeInTheDocument()
})

test('remote HTTP startup and editing work when crypto.randomUUID is unavailable', async () => {
  const getRandomValues = crypto.getRandomValues.bind(crypto)
  vi.stubGlobal('crypto', { getRandomValues })
  stubWorkspace()
  const user = userEvent.setup()
  render(<App />)
  expect(await screen.findByRole('heading', { name: '总览' })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '自定义 Shell' }))
  await user.click(screen.getByRole('button', { name: '复制 自定义 Shell 检查' }))
  await user.click(screen.getByRole('button', { name: '添加节点角色' }))
  expect(screen.getByRole('button', { name: /新节点角色/ })).toBeInTheDocument()
})

test('new drafts declare CPU architecture without an OS allowlist', () => {
  expect(newPlan().topology_policy).toEqual({ allowed_arch: ['x86_64', 'aarch64'] })
})

test('available builtin template can be added to the selected role', async () => {
  stubWorkspace()
  const initial = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (input, init) =>
    String(input).endsWith('/templates')
      ? new Response(
          JSON.stringify({
            items: [
              {
                schema_version: 'readyspec.template/v1',
                template_id: 'S01',
                version: '1.0.0',
                name: '路径存在与类型',
                source: 'BUILTIN',
                domain: 'S',
                execution_mode: 'STANDARD',
                evidence_mode: 'EXISTENCE',
                digest: 'test-digest',
                params_schema: {
                  properties: {
                    path: { type: 'string' },
                    exists: { type: 'boolean', default: true },
                  },
                },
                implementation: { kind: 'BUILTIN', compiler: 'path' },
              },
            ],
          }),
          { status: 200 },
        )
      : initial(input, init),
  )
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: /浏览模板/ }))
  await user.click(await screen.findByRole('button', { name: '添加 路径存在与类型' }))
  expect(screen.getByLabelText('path')).toHaveValue('')
  expect(screen.getByLabelText('exists')).toHaveValue('true')
  expect(screen.queryByLabelText('Shell 脚本')).not.toBeInTheDocument()
})

test('authenticated user can navigate from overview to a new draft', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string) => {
      const data = input.endsWith('/auth/session')
        ? { csrf_token: 'test-csrf' }
        : input.endsWith('/system/info')
          ? { version: '0.2.0-dev', executor_available: false }
          : { items: [] }
      return new Response(JSON.stringify(data), { status: 200 })
    }),
  )
  render(<App />)
  await screen.findByRole('heading', { name: '总览' })
  await userEvent.click(screen.getByRole('button', { name: '新建检查方案' }))
  expect(await screen.findByRole('heading', { name: '方案工作台' })).toBeInTheDocument()
  expect(screen.getByLabelText('方案名称')).toHaveValue('未命名方案')
  expect(screen.queryByText('目标操作系统')).not.toBeInTheDocument()
  expect(screen.getByText('CPU 架构')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '校验草稿' })).toBeEnabled()
})

test('custom check can be copied, edited independently, deleted with confirmation and restored', async () => {
  stubWorkspace()
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '自定义 Shell' }))
  expect(screen.getByLabelText('Shell 脚本')).toHaveValue('test -d /data')
  await user.click(screen.getByRole('button', { name: '复制 自定义 Shell 检查' }))
  await user.clear(screen.getByLabelText('Shell 脚本'))
  await user.type(screen.getByLabelText('Shell 脚本'), 'test -d /var/data')
  await user.click(screen.getByRole('button', { name: '自定义 Shell 检查' }))
  expect(screen.getByLabelText('Shell 脚本')).toHaveValue('test -d /data')
  await user.click(screen.getByRole('button', { name: '删除检查项' }))
  const dialog = screen.getByRole('dialog', { name: '删除检查项？' })
  await user.click(within(dialog).getByRole('button', { name: '删除检查项' }))
  expect(screen.queryByRole('button', { name: '自定义 Shell 检查' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '撤销删除' }))
  expect(screen.getByRole('button', { name: '自定义 Shell 检查' })).toBeInTheDocument()
})

test('template filtering shows definitions without offering unimplemented execution', async () => {
  stubWorkspace()
  const user = userEvent.setup()
  render(<App />)
  await screen.findByRole('heading', { name: '总览' })
  await user.click(screen.getByRole('button', { name: /模板与清理/ }))
  await user.type(screen.getByLabelText('搜索模板'), 'A03')
  expect(screen.getByRole('button', { name: /A03.*SSH 服务端生效配置/ })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /H01.*CPU 架构/ })).not.toBeInTheDocument()
  expect(screen.getByText('特权执行')).toBeInTheDocument()
  expect(screen.getByText('规格定义')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '执行检查' })).not.toBeInTheDocument()
})

test('invalid import preserves source and shows the server validation error', async () => {
  stubWorkspace()
  const initial = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (input, init) =>
    String(input).endsWith('/plans/validate')
      ? new Response(JSON.stringify({ error: 'YAML_DUPLICATE_KEY: line 2' }), { status: 422 })
      : initial(input, init),
  )
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '导入方案' }))
  await user.type(screen.getByLabelText('方案 YAML'), 'name: first\nname: second')
  await user.click(screen.getByRole('button', { name: '校验并导入' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('YAML_DUPLICATE_KEY')
  expect(screen.getByLabelText('方案 YAML')).toHaveValue('name: first\nname: second')
})

test('workspace read failure can be retried without asking for the consumed bootstrap token', async () => {
  stubWorkspace()
  const initial = vi.mocked(fetch).getMockImplementation()!
  let failed = false
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if (String(input).endsWith('/plans') && !failed) {
      failed = true
      return new Response(JSON.stringify({ error: '工作区读取失败' }), { status: 500 })
    }
    return initial(input, init)
  })
  render(<App />)
  await screen.findByRole('heading', { name: '工作区暂不可用' })
  await userEvent.click(screen.getByRole('button', { name: '重试读取工作区' }))
  expect(await screen.findByRole('heading', { name: '总览' })).toBeInTheDocument()
  expect(screen.queryByLabelText('访问令牌')).not.toBeInTheDocument()
})

test('saving a snapshot does not erase edits made while the request is in flight', async () => {
  stubWorkspace()
  const initial = vi.mocked(fetch).getMockImplementation()!
  let finish!: () => void
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if (String(input).endsWith('/plans') && init?.method === 'POST') {
      const document = parse(JSON.parse(String(init.body)).source)
      return new Promise<Response>((resolve) => {
        finish = () =>
          resolve(
            new Response(
              JSON.stringify({ document, draft_key: 'snapshot-key', validated: false }),
              { status: 200 },
            ),
          )
      })
    }
    return initial(input, init)
  })
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '保存草稿' }))
  await user.clear(screen.getByLabelText('方案名称'))
  await user.type(screen.getByLabelText('方案名称'), '请求期间的新修改')
  finish()
  await screen.findByText('操作已完成，但你又修改了方案。当前修改仍需重新校验或保存。')
  expect(screen.getByLabelText('方案名称')).toHaveValue('请求期间的新修改')
  expect(screen.getByText('未保存')).toBeInTheDocument()
})

test('undo restores only the deleted check and preserves subsequent plan and check edits', async () => {
  stubWorkspace()
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '自定义 Shell' }))
  await user.click(screen.getByRole('button', { name: '删除检查项' }))
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '删除检查项' }))
  await user.clear(screen.getByLabelText('方案名称'))
  await user.type(screen.getByLabelText('方案名称'), '删除后的修改')
  await user.click(screen.getByRole('button', { name: '自定义 Shell' }))
  await user.clear(screen.getByLabelText('检查项名称'))
  await user.type(screen.getByLabelText('检查项名称'), '后添加的检查项')
  await user.click(screen.getByRole('button', { name: '撤销删除' }))
  expect(screen.getByLabelText('方案名称')).toHaveValue('删除后的修改')
  expect(screen.getByRole('button', { name: '后添加的检查项' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '自定义 Shell 检查' })).toBeInTheDocument()
})

test('switching to a new draft clears deletion history even with the same workbench mounted', async () => {
  stubWorkspace()
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '自定义 Shell' }))
  await user.click(screen.getByRole('button', { name: '删除检查项' }))
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '删除检查项' }))
  await user.click(screen.getByRole('button', { name: '新建方案' }))
  await user.click(screen.getByRole('button', { name: '放弃修改' }))
  expect(screen.queryByRole('button', { name: '撤销删除' })).not.toBeInTheDocument()
  expect(screen.getByLabelText('方案名称')).toHaveValue('未命名方案')
})

test('undoing a deleted role preserves later changes to another role', async () => {
  stubWorkspace()
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.click(screen.getByRole('button', { name: '添加节点角色' }))
  await user.click(screen.getByRole('button', { name: '删除此角色' }))
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '删除角色' }))
  await user.clear(screen.getByLabelText('角色名称'))
  await user.type(screen.getByLabelText('角色名称'), '更新后的应用节点')
  await user.click(screen.getByRole('button', { name: '撤销删除' }))
  expect(screen.getByRole('button', { name: /更新后的应用节点/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /新节点角色/ })).toBeInTheDocument()
})

test('expired session can be reauthenticated without discarding the unsaved draft', async () => {
  stubWorkspace()
  const initial = vi.mocked(fetch).getMockImplementation()!
  let firstSave = true
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    if (String(input).endsWith('/plans') && init?.method === 'POST') {
      if (firstSave) {
        firstSave = false
        return new Response(JSON.stringify({ error: 'SESSION_EXPIRED' }), { status: 401 })
      }
      const document = parse(JSON.parse(String(init.body)).source)
      return new Response(JSON.stringify({ document, draft_key: 'recovered', validated: false }), {
        status: 200,
      })
    }
    return initial(input, init)
  })
  const user = userEvent.setup()
  render(<App />)
  await user.click(await screen.findByRole('button', { name: '新建检查方案' }))
  await user.clear(screen.getByLabelText('方案名称'))
  await user.type(screen.getByLabelText('方案名称'), '认证期间必须保留')
  await user.click(screen.getByRole('button', { name: '保存草稿' }))
  const dialog = await screen.findByRole('dialog', { name: '会话已过期' })
  expect(screen.getByLabelText('方案名称')).toHaveValue('认证期间必须保留')
  await user.type(within(dialog).getByLabelText('新的访问令牌'), 'replacement-test-token')
  await user.click(within(dialog).getByRole('button', { name: '重新认证' }))
  expect(screen.queryByRole('dialog', { name: '会话已过期' })).not.toBeInTheDocument()
  expect(screen.getByLabelText('方案名称')).toHaveValue('认证期间必须保留')
  await user.click(screen.getByRole('button', { name: '保存草稿' }))
  expect(await screen.findByText('草稿已保存到当前工作区。')).toBeInTheDocument()
})
