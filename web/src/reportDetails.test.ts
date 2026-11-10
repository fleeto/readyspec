import { expect, test } from 'vitest'
import { itemCounts, stageStatus, verdictReasonTexts } from './reportDetails'
import type { DisplayMetadata } from './reportPresentation'

const metadata: DisplayMetadata = {
  run_id: 'run',
  items: [
    {
      role_id: 'app',
      item_id: 'required',
      item_name: '',
      template_name: '',
      importance: 'REQUIRED',
      enabled: true,
    },
    {
      role_id: 'app',
      item_id: 'warning',
      item_name: '',
      template_name: '',
      importance: 'WARNING',
      enabled: true,
    },
    {
      role_id: 'app',
      item_id: 'info',
      item_name: '',
      template_name: '',
      importance: 'INFO',
      enabled: true,
    },
  ],
}
const results = [
  { role_id: 'app', item_id: 'required', status: 'FAIL' },
  { role_id: 'app', item_id: 'warning', status: 'PASS' },
  { role_id: 'app', item_id: 'info', status: 'SKIP' },
]

test('summary separates required and optional results without counting skipped as passed', () => {
  expect(itemCounts(results, metadata, true)).toEqual({ passed: 0, checked: 1 })
  expect(itemCounts(results, metadata, false)).toEqual({ passed: 1, checked: 2 })
  expect(itemCounts(results, null, true)).toBeNull()
})

test('unknown importance never silently becomes optional', () => {
  expect(
    itemCounts([{ role_id: 'app', item_id: 'other', status: 'PASS' }], metadata, false),
  ).toBeNull()
})

test('old blank reasons use frozen plan evidence only when it has no required checks', () => {
  expect(verdictReasonTexts([''], 'UNKNOWN', metadata)).toEqual([])
  expect(
    verdictReasonTexts([''], 'UNKNOWN', {
      ...metadata,
      items: metadata.items.map((item) => ({ ...item, importance: 'WARNING' })),
    }),
  ).toEqual(['方案没有必检项，因此不能判断为满足放行要求。'])
  expect(verdictReasonTexts(['REQUIRED_CHECK_FAILED'], 'NOT_READY', metadata)).toEqual([
    '有必检项不满足要求。',
  ])
})

test('stage status distinguishes running, finished and omitted', () => {
  expect(stageStatus({ started_at: 'start' })).toBe('进行中')
  expect(stageStatus({ started_at: 'start', ended_at: 'end' })).toBe('已结束')
  expect(stageStatus({ omitted: true })).toBe('未执行')
  expect(stageStatus({})).toBe('')
})
