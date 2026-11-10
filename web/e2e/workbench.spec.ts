import { expect, test } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import { parse } from 'yaml'

const nonLoopbackRequests = new WeakMap<object, string[]>()

for (const width of [1366, 768, 375])
  for (const theme of ['dark', 'light']) {
    test(`report actions and readable results at ${width}px ${theme}`, async ({
      page,
    }, testInfo) => {
      const run = {
        run_id: 'layout-fixture',
        plan_id: 'fixture',
        plan_name: '中文长方案名称与报告操作布局验证',
        plan_version: '1',
        status: 'PARTIAL',
        accepted_at: '2026-11-10T00:00:00Z',
        run_mode: 'GATE',
        run_scope: 'FULL',
        executor_backend: 'native_ssh',
        verdict: 'UNKNOWN',
      }
      await page.route('**/api/v1/runs', (route) =>
        route.fulfill({
          json: {
            items: [
              run,
              { ...run, run_id: 'active-fixture', status: 'RUNNING', verdict: undefined },
            ],
          },
        }),
      )
      await page.route('**/api/v1/runs/layout-fixture', (route) =>
        route.fulfill({ json: { run, sequence: 1, stage_transitions: [] } }),
      )
      await page.route('**/api/v1/runs/layout-fixture/display-metadata', (route) =>
        route.fulfill({
          json: {
            run_id: run.run_id,
            items: [
              {
                role_id: 'app',
                item_id: 'memory',
                item_name: '主机最低内存',
                template_name: '总内存',
                importance: 'REQUIRED',
                enabled: true,
              },
            ],
          },
        }),
      )
      await page.route('**/api/v1/runs/layout-fixture/report', (route) =>
        route.fulfill({
          json: {
            ...run,
            run_status: 'PARTIAL',
            started_at: run.accepted_at,
            finished_at: run.accepted_at,
            topology_results: [],
            cleanup_items: [],
            failures: [],
            hosts: [
              {
                host_id: 'node-a',
                roles: ['app'],
                status: 'COMPLETED',
                failures: [],
                item_results: [
                  {
                    role_id: 'app',
                    item_id: 'memory',
                    status: 'FAIL',
                    template_id: 'H03',
                    template_version: '1',
                    execution_mode: 'STANDARD',
                    evidence_mode: 'METADATA',
                    evidence: [
                      {
                        resource: 'memory',
                        property: 'stdout',
                        expected: { min_memory: 16000000000 },
                        actual: { mem_total_bytes: 15959113728 },
                      },
                    ],
                  },
                ],
              },
            ],
          },
        }),
      )
      await page.goto('/')
      await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
      await page.getByRole('button', { name: '连接工作区' }).click()
      await page
        .locator('.sidebar')
        .getByRole('button', { name: /检查与报告/ })
        .click()
      if (theme === 'light') await page.getByRole('button', { name: '切换浅色主题' }).click()
      await page.setViewportSize({ width, height: 1000 })
      const actions = page.locator('.run-actions').first()
      const buttons = actions.getByRole('button')
      await expect(buttons).toHaveCount(3)
      await expect(page.locator('.run-actions').nth(1).getByRole('button')).toHaveCount(4)
      await actions.scrollIntoViewIfNeeded()
      const boxes = await buttons.evaluateAll((elements) =>
        elements.map((element) => {
          const b = element.getBoundingClientRect()
          return { x: b.x, y: b.y, right: b.right, bottom: b.bottom }
        }),
      )
      for (let a = 0; a < boxes.length; a++)
        for (let b = a + 1; b < boxes.length; b++)
          expect(
            boxes[a].right <= boxes[b].x ||
              boxes[b].right <= boxes[a].x ||
              boxes[a].bottom <= boxes[b].y ||
              boxes[b].bottom <= boxes[a].y,
          ).toBeTruthy()
      await buttons.first().focus()
      await expect(buttons.first()).toBeFocused()
      await buttons.first().click()
      const summary = page.getByRole('region', { name: '主机检查汇总', exact: true })
      await expect(summary.locator('details')).toHaveAttribute('open', '')
      await expect(summary.getByText('0/1', { exact: true })).toBeVisible()
      await expect(summary.getByText('0/0', { exact: true })).toBeVisible()
      for (const title of ['拓扑核验', '检查项明细', '执行阶段', '远端清理审计']) {
        const region = page.getByRole('region', { name: title, exact: true })
        const details = region.locator(':scope > details')
        await expect(details).not.toHaveAttribute('open', '')
        await details.locator(':scope > summary').click()
        await expect(details).toHaveAttribute('open', '')
      }
      await expect(page.getByText('主机最低内存', { exact: true })).toBeVisible()
      await expect(page.getByText(/实际可见内存：15.96 GB/)).toBeVisible()
      await page.screenshot({
        path: testInfo.outputPath(`report-${width}-${theme}.png`),
        fullPage: true,
      })
    })
  }

test.beforeEach(async ({ page }) => {
  const externalRequests: string[] = []
  nonLoopbackRequests.set(page, externalRequests)
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (!['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) {
      externalRequests.push(url.origin)
      await route.abort()
      return
    }
    await route.continue()
  })
})

test.afterEach(async ({ page }) => {
  expect(nonLoopbackRequests.get(page), 'browser attempted a non-loopback network request').toEqual(
    [],
  )
})

test('builtin path check can be authored, compiled and exported without execution', async ({
  page,
}) => {
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await page.getByRole('button', { name: '新建检查方案', exact: true }).click()
  await page.getByRole('button', { name: /浏览模板/ }).click()
  await page.getByRole('button', { name: '添加 路径存在与类型' }).click()
  await page.getByLabel('目标路径', { exact: true }).fill('/data/orders')
  await page.getByLabel('路径类型', { exact: true }).selectOption('directory')
  await page.getByRole('button', { name: '静态编译', exact: true }).click()
  await expect(
    page.getByText('静态编译通过。未连接目标主机，不代表目标环境已经就绪。'),
  ).toBeVisible()
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('草稿已保存到当前工作区。')).toBeVisible()
  const event = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 YAML' }).click()
  const download = await event
  expect(download.suggestedFilename()).toMatch(/\.yaml$/)
  await page.getByRole('button', { name: /检查与报告/ }).click()
  await page.getByRole('button', { name: '下一步：主机清单' }).click()
  await page.getByLabel('清单格式').selectOption('ini')
  await page
    .getByLabel('主机清单', { exact: true })
    .fill('[all:vars]\nansible_user=root\nansible_port=2222\n[app]\nnode ansible_host=192.0.2.1\n')
  await page.getByRole('button', { name: '下一步：角色与认证' }).click()
  await page.getByLabel('绑定主机 · app').fill('node')
  await page.getByRole('button', { name: '生成静态预览' }).click()
  await expect(page.getByText('候选检查项：1', { exact: true })).toBeVisible()
  await expect(page.getByText('192.0.2.1:2222', { exact: true })).toBeVisible()
  await expect(page.getByText(/配置为 root 登录：node/)).toBeVisible()
})

test('browser can author, validate, persist, export and strictly reimport a plan', async ({
  page,
}, testInfo) => {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await expect(page.getByRole('heading', { name: '总览', exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('overview-dark.png'), fullPage: true })
  await page.getByRole('button', { name: '新建检查方案', exact: true }).click()
  await page.getByLabel('方案名称', { exact: true }).fill('订单平台部署前检查')
  await page.getByLabel('方案标识', { exact: true }).fill('web-browser-demo')
  await page.getByRole('button', { name: '自定义 Shell', exact: true }).click()
  await page.getByLabel('检查项名称', { exact: true }).fill('数据目录可访问')
  await page.getByRole('button', { name: '添加检查参数' }).click()
  await page.getByLabel('参数名', { exact: true }).fill('DATA_PATH')
  await page.getByRole('button', { name: '添加参数', exact: true }).click()
  await page.getByLabel('DATA_PATH', { exact: true }).fill('/data/orders')
  await page.getByLabel('现场可调', { exact: true }).check()
  await page.getByLabel('Shell 脚本', { exact: true }).fill('test -d "$RS_PARAM_DATA_PATH"')
  await page.getByRole('button', { name: '校验草稿' }).click()
  await expect(
    page.getByText('静态校验通过。此结果仅验证方案文件，不代表目标环境已经就绪。'),
  ).toBeVisible()
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('草稿已保存到当前工作区。')).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('workbench-dark.png'), fullPage: true })
  const event = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 YAML' }).click()
  const download = await event
  const filename = testInfo.outputPath('exported-plan.yaml')
  await download.saveAs(filename)
  const exported = parse(await readFile(filename, 'utf8'))
  expect(exported.plan_id).toBe('web-browser-demo')
  expect(exported.roles[0].items[0].overridable_params).toEqual(['DATA_PATH'])
  expect(exported.embedded_templates[0].digest).toMatch(/^sha256:[0-9a-f]{64}$/)
  await page.reload()
  const savedPlans = page.getByLabel('打开已保存方案')
  const savedPlan = savedPlans.locator('option').filter({ hasText: '订单平台部署前检查' }).first()
  await savedPlans.selectOption((await savedPlan.getAttribute('value')) ?? '')
  await expect(page.getByLabel('方案名称', { exact: true })).toHaveValue('订单平台部署前检查')
  await page.getByRole('button', { name: '导入', exact: true }).click()
  await page.getByLabel('选择方案文件').setInputFiles(filename)
  await page.getByRole('button', { name: '校验并导入' }).click()
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await expect(page.getByLabel('方案名称', { exact: true })).toHaveValue('订单平台部署前检查')
  await page.getByRole('button', { name: '切换浅色主题' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await page.screenshot({ path: testInfo.outputPath('workbench-light.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '切换导航' }).click()
  await page.getByRole('button', { name: /模板与清理/ }).click()
  await expect(page.getByRole('heading', { name: '模板与清理', exact: true })).toBeVisible()
  await expect(page.getByRole('complementary', { name: '主导航' })).not.toBeInViewport()
  await page.getByLabel('搜索模板').fill('A03')
  await expect(page.getByRole('button', { name: /A03.*SSH 服务端生效配置/ })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.screenshot({ path: testInfo.outputPath('templates-mobile.png'), fullPage: true })
  await page.setViewportSize({ width: 1024, height: 900 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.setViewportSize({ width: 720, height: 900 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
  await page.emulateMedia({ reducedMotion: 'reduce' })
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(
    true,
  )
  expect(
    await page.evaluate(() => getComputedStyle(document.body).transitionDuration === '0s'),
  ).toBe(true)
  expect(errors).toEqual([])
})

test('incomplete draft can be saved and reopened but cannot be exported as validated', async ({
  page,
}) => {
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await page.getByRole('button', { name: '新建检查方案', exact: true }).click()
  await page.getByLabel('方案标识', { exact: true }).fill('incomplete-draft')
  await page.getByLabel('方案名称', { exact: true }).fill('')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('草稿已保存到当前工作区。')).toBeVisible()
  await expect(page.locator('.digest-line')).not.toBeVisible()
  await page.reload()
  const savedPlans = page.getByLabel('打开已保存方案')
  const savedDraft = savedPlans
    .locator('option')
    .filter({ hasText: '未命名方案 · 1.0.0 · 草稿' })
    .first()
  await savedPlans.selectOption((await savedDraft.getAttribute('value')) ?? '')
  await expect(page.getByLabel('方案名称', { exact: true })).toHaveValue('')
  await page.getByRole('button', { name: '校验草稿' }).click()
  await expect(page.getByText('请填写方案名称。', { exact: true })).toBeVisible()
  await expect(page.getByLabel('方案名称', { exact: true })).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByLabel('方案名称', { exact: true })).toBeFocused()
  await expect(page.locator('.digest-line')).not.toBeVisible()
  await expect(page.getByLabel('方案标识', { exact: true })).toHaveValue('incomplete-draft')
})

test('released plan can be archived and forked into a new version draft', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await page.getByRole('button', { name: '新建检查方案', exact: true }).click()
  await page.getByLabel('方案名称', { exact: true }).fill('Lifecycle browser plan')
  await page.getByLabel('方案标识', { exact: true }).fill('lifecycle-browser-plan')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('草稿已保存到当前工作区。')).toBeVisible()
  await page.getByRole('button', { name: '发布版本' }).click()
  await expect(page.getByText(/方案版本已发布并冻结/)).toBeVisible()
  await expect(page.getByLabel('方案版本', { exact: true })).toHaveValue('1.0.0')
  await page.getByRole('button', { name: '停用版本' }).click()
  await expect(page.getByText('方案版本已停用，历史检查任务保持不变。')).toBeVisible()
  await page.getByRole('button', { name: '创建新版本草稿' }).click()
  await expect(page.getByLabel('方案版本', { exact: true })).toHaveValue('1.0.1')
  await expect(page.getByText('草稿', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('草稿已保存到当前工作区。')).toBeVisible()
})

test('offline simulator run streams progress and archives the profile result', async ({
  page,
}, testInfo) => {
  const pageErrors: string[] = []
  page.on('pageerror', (error) => pageErrors.push(`${error.message}\n${error.stack ?? ''}`))
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await page.getByRole('button', { name: '新建检查方案', exact: true }).click()
  await page.getByLabel('方案名称', { exact: true }).fill('Offline simulator plan')
  await page.getByLabel('方案标识', { exact: true }).fill('offline-simulator-plan')
  await page.getByRole('button', { name: '自定义 Shell', exact: true }).click()
  await page.getByLabel('检查项名称', { exact: true }).fill('Profile result')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByText('草稿已保存到当前工作区。')).toBeVisible()

  const exportEvent = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 YAML' }).click()
  const planDownload = await exportEvent
  const planPath = testInfo.outputPath('simulator-plan.yaml')
  await planDownload.saveAs(planPath)
  const exported = parse(await readFile(planPath, 'utf8'))
  const itemID = exported.roles[0].items[0].item_id as string

  await page.getByRole('button', { name: /检查与报告/ }).click()
  await page.getByRole('button', { name: '下一步：主机清单' }).click()
  await page.getByLabel('主机清单', { exact: true }).fill('node=192.0.2.1')
  await page.getByRole('button', { name: '下一步：角色与认证' }).click()
  await page.getByLabel('绑定主机 · app').fill('node')
  await page.getByLabel('执行器').selectOption('mock_simulator')
  await page.getByLabel('模拟器画像（JSON）').fill(
    JSON.stringify({
      hosts: {
        node: {
          preflight: 'READY',
          machine_id: 'machine-node',
          boot_id: 'boot-node',
          host_key_fingerprint: 'SHA256:offline-profile',
          arch: 'x86_64',
          uid: 1000,
          results: { [itemID]: 'FAIL' },
        },
      },
    }),
  )
  await page.getByRole('button', { name: '生成静态预览' }).click()
  await expect(page.getByText('候选检查项：1', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '接受并执行任务' }).click()
  const wizard = page
    .locator('section.panel')
    .filter({ has: page.getByRole('heading', { name: '四步检查向导' }) })
  const check = wizard.locator('.check-summary').filter({ hasText: itemID })
  await expect(check).toContainText('Profile result', { timeout: 15000 })
  await expect(check).toContainText('FAIL')
  await expect(page.getByText(/COMPLETED \/ NOT_READY/)).toBeVisible()
  await expect(page.getByText(/SIMULATING/).first()).toBeVisible()
  await expect(page.getByText(/尝试 1 · 开始 .*结束 .*用时/).first()).toBeVisible()
  await page.getByRole('button', { name: '刷新' }).click()
  const historyReportResponse = page.waitForResponse((response) =>
    /\/api\/v1\/runs\/[^/]+\/report$/.test(response.url()),
  )
  const simulatorRunRow = page.getByRole('row').filter({ hasText: 'Offline simulator plan' })
  await simulatorRunRow.getByRole('button', { name: '查看报告' }).click()
  const reportResponse = await historyReportResponse
  expect(reportResponse.status()).toBe(200)
  const archivedReport = await reportResponse.json()
  const originalPlanDigest = archivedReport.plan_digest
  expect(archivedReport.run_id).toBeTruthy()
  expect(Array.isArray(archivedReport.hosts)).toBe(true)
  expect(Array.isArray(archivedReport.cleanup_items)).toBe(true)
  expect(Array.isArray(archivedReport.failures)).toBe(true)
  expect(Array.isArray(archivedReport.topology_results)).toBe(true)
  expect(
    archivedReport.hosts.every((host: { item_results: unknown }) =>
      Array.isArray(host.item_results),
    ),
  ).toBe(true)
  expect(pageErrors).toEqual([])
  await expect(page.getByRole('button', { name: '下载 JSON' })).toBeVisible()
  await page
    .getByRole('region', { name: '检查项明细', exact: true })
    .locator(':scope > details > summary')
    .click()
  await expect(page.getByText(/登录 UID 1000 · 架构 x86_64 · 认证方式 offline/)).toBeVisible()
  await expect(page.getByRole('cell').filter({ hasText: itemID }).first()).toBeVisible()

  const partialConfigResponse = page.waitForResponse((response) =>
    /\/api\/v1\/runs\/[^/]+\/rerun-config$/.test(response.url()),
  )
  await simulatorRunRow.getByRole('button', { name: '复测异常主机' }).click()
  const partialConfig = await partialConfigResponse
  expect(partialConfig.status()).toBe(200)
  const partialConfigBody = await partialConfig.json()
  expect(partialConfigBody.partial_hosts).toEqual(['node'])
  expect(typeof partialConfigBody.plan).toBe('string')
  expect(JSON.parse(partialConfigBody.plan).state).toBe('DRAFT')
  expect(partialConfigBody.plan_digest).toBe(originalPlanDigest)
  expect(pageErrors).toEqual([])
  await expect(page.getByText(/已载入最近一次非秘密配置/)).toBeVisible()
  await expect(page.getByLabel('执行范围')).toHaveValue('PARTIAL')
  await expect(page.getByLabel('主机清单', { exact: true })).toContainText('node')
  await page.getByRole('button', { name: '下一步：主机清单' }).click()
  await page.getByRole('button', { name: '下一步：角色与认证' }).click()
  const partialProfiles = JSON.parse(await page.getByLabel('模拟器画像（JSON）').inputValue())
  expect(Object.keys(partialProfiles.hosts)).toEqual(['node'])
  partialProfiles.hosts.node.results[itemID] = 'PASS'
  await page.getByLabel('模拟器画像（JSON）').fill(JSON.stringify(partialProfiles))
  const partialPreviewResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/runs/preview') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '生成静态预览' }).click()
  const partialPreview = await partialPreviewResponse
  expect(partialPreview.status(), await partialPreview.text()).toBe(200)
  const partialPreviewBody = await partialPreview.json()
  expect(partialPreviewBody.validated_plan).toEqual(JSON.parse(partialConfigBody.plan))
  expect(partialPreviewBody.plan_digest).toBe(originalPlanDigest)
  await expect(page.getByText('候选检查项：1', { exact: true })).toBeVisible()
  const partialSubmitResponse = page.waitForResponse(
    (response) => response.url().endsWith('/api/v1/runs') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '接受并执行任务' }).click()
  const partialAccepted = await partialSubmitResponse
  expect(partialAccepted.request().postDataJSON().partial_source_run_id).toBe(archivedReport.run_id)
  expect(partialAccepted.status(), await partialAccepted.text()).toBe(202)
  const passedCheck = wizard
    .locator('.check-summary')
    .filter({ hasText: itemID })
    .filter({ hasText: 'PASS' })
  await expect(passedCheck).toContainText('Profile result', { timeout: 15000 })
  await expect(passedCheck).toHaveCount(1)
  await expect(page.getByText(/COMPLETED \/ UNKNOWN/)).toBeVisible()

  await page.getByRole('button', { name: '刷新' }).click()
  const originalRunRow = page.getByRole('row').filter({ hasText: archivedReport.run_id })
  await originalRunRow.getByRole('button', { name: '使用最近配置重跑' }).click()
  await expect(page.getByLabel('执行范围')).toHaveValue('FULL')
  await expect(page.getByLabel('主机清单', { exact: true })).toContainText('node')
  await page.getByRole('button', { name: '下一步：主机清单' }).click()
  await page.getByRole('button', { name: '下一步：角色与认证' }).click()
  const rerunProfiles = JSON.parse(await page.getByLabel('模拟器画像（JSON）').inputValue())
  expect(Object.keys(rerunProfiles.hosts)).toEqual(['node'])
  await page.getByRole('button', { name: '生成静态预览' }).click()
  await expect(page.getByText('候选检查项：1', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '接受并执行任务' }).click()
  await expect(check).toContainText('Profile result', { timeout: 15000 })
  await expect(check).toContainText('FAIL')
})

test('cleanup process confirmation can be cancelled and IGNORE is recorded in the live audit', async ({
  page,
}) => {
  let cleanRequests = 0
  await page.route('**/api/v1/runs/*/cleanup/*', async (route) => {
    const request = route.request()
    if (request.method() === 'POST' && request.postDataJSON().action === 'CLEAN') {
      cleanRequests++
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          cleanup_id: 'cleanup-browser-item',
          host_id: 'cleanup-node',
          machine_id: 'fixture-machine',
          boot_id: 'fixture-boot',
          status: 'UNKNOWN',
          reason: 'associated processes are active; explicit termination confirmation is required',
          remote_path: '/tmp/readyspec-cleanup-browser-fixture',
        }),
      })
      return
    }
    await route.continue()
  })

  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await page.getByRole('button', { name: /模板与清理/ }).click()
  await page.getByRole('tab', { name: /远端残留清理/ }).click()

  const row = page.getByRole('row').filter({ hasText: 'cleanup-browser-item' })
  await expect(row).toContainText('PENDING')
  await row.getByRole('button', { name: '重新核验并清理' }).click()
  await expect(page.getByRole('dialog', { name: '检测到关联进程' })).toBeVisible()
  await expect(page.getByText(/尚未尝试终止进程或删除目录/)).toBeVisible()
  await page.getByRole('button', { name: '取消' }).click()
  await expect(page.getByRole('dialog')).not.toBeVisible()
  expect(cleanRequests).toBe(1)
  await expect(row).toContainText('PENDING')

  const reason = 'operator retained the directory for investigation'
  await row.getByLabel('cleanup-node 忽略原因').fill(reason)
  const ignoreResponsePromise = page.waitForResponse(
    (response) =>
      response.url().includes('/api/v1/runs/') &&
      response.url().endsWith('/cleanup/cleanup-browser-item') &&
      response.request().method() === 'POST',
  )
  await row.getByRole('button', { name: '记录忽略' }).click()
  const ignoreResponse = await ignoreResponsePromise
  expect(ignoreResponse.status()).toBe(200)
  expect(ignoreResponse.request().postDataJSON()).toMatchObject({
    action: 'IGNORE',
    audit_reason: reason,
  })
  expect(await ignoreResponse.json()).toMatchObject({ status: 'IGNORED', audit_reason: reason })

  await page.getByLabel('清理审计筛选').selectOption('all')
  await expect(page.getByRole('row').filter({ hasText: 'cleanup-browser-item' })).toContainText(
    reason,
  )
  expect(cleanRequests).toBe(1)
})

test('closed mobile navigation cannot take keyboard focus and Escape returns focus to its toggle', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await expect(page.getByRole('heading', { name: '总览', exact: true })).toBeVisible()
  const toggle = page.getByRole('button', { name: '切换导航' })
  await toggle.focus()
  await page.keyboard.press('Shift+Tab')
  expect(
    await page.evaluate(
      () => document.activeElement?.closest('aside[aria-label="主导航"]') !== null,
    ),
  ).toBe(false)
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(
    await page.evaluate(
      () => document.activeElement?.closest('aside[aria-label="主导航"]') !== null,
    ),
  ).toBe(true)
  await page.keyboard.press('Escape')
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(toggle).toBeFocused()
})
