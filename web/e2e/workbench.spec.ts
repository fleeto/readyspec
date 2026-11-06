import { expect, test } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import { parse } from 'yaml'

test('builtin path check can be authored, compiled and exported without execution', async ({
  page,
}) => {
  await page.goto('/')
  await page.getByLabel('访问令牌').fill('readyspec-browser-test-token-only-000000000000000000')
  await page.getByRole('button', { name: '连接工作区' }).click()
  await page.getByRole('button', { name: '新建检查方案', exact: true }).click()
  await page.getByRole('button', { name: /浏览模板/ }).click()
  await page.getByRole('button', { name: '添加 路径存在与类型' }).click()
  await page.getByLabel('path', { exact: true }).fill('/data/orders')
  await page.getByLabel('filetype', { exact: true }).selectOption('directory')
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
  await page.getByLabel('清单格式').selectOption('ini')
  await page
    .getByLabel('主机清单', { exact: true })
    .fill('[all:vars]\nansible_user=root\nansible_port=2222\n[app]\nnode ansible_host=192.0.2.1\n')
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
  await page.getByLabel('打开已保存方案').selectOption('web-browser-demo@1.0.0')
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
  await page.getByLabel('打开已保存方案').selectOption('incomplete-draft@1.0.0')
  await expect(page.getByLabel('方案名称', { exact: true })).toHaveValue('')
  await page.getByRole('button', { name: '校验草稿' }).click()
  await expect(
    page.getByText('PLAN_SCHEMA_ERROR: document does not match readyspec.plan/v1'),
  ).toBeVisible()
  await expect(page.locator('.digest-line')).not.toBeVisible()
  await expect(page.getByLabel('方案标识', { exact: true })).toHaveValue('incomplete-draft')
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
