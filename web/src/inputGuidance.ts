import examples from './inventoryExamples.json'
export const inventoryExamples: Record<string, string> = examples
export function focusInvalidField() {
  Array.from(document.querySelectorAll<HTMLElement>('[aria-invalid="true"]'))
    .find((field) => !field.closest('[hidden]') && !field.hasAttribute('disabled'))
    ?.focus()
}
export interface Property {
  type?: string | string[]
  title?: string
  description?: string
  default?: unknown
  enum?: (string | number)[]
  items?: { enum?: string[] }
  minimum?: number
  maximum?: number
  'x-readyspec-type'?: string
}
export const capacityHint =
  '容量：数字 + 单位，例如 4 GiB、100 GB。支持 B/KB/MB/GB/TB/KiB/MiB/GiB/TiB，区分大小写；GB=10^9 字节，GiB=2^30 字节。精确范围由服务端校验。'
export function inputIssue(property: Property | undefined, value: unknown, required: boolean) {
  if (value === undefined || value === '') return required ? '请填写必填参数。' : ''
  if (value === null)
    return property?.type === 'null' ||
      (Array.isArray(property?.type) && property.type.includes('null'))
      ? ''
      : '此参数不允许显式 null。'
  if (
    property?.['x-readyspec-type'] === 'capacity' &&
    (typeof value !== 'string' ||
      !/^([0-9]+(?:\.[0-9]+)?)\s*(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)$/.test(value.trim()))
  )
    return '容量需要数字和单位，例如 4 GiB 或 100 GB。'
  if (property?.enum && !property.enum.includes(value as string | number))
    return '请从允许的选项中选择。'
  if (
    typeof value === 'number' &&
    (!Number.isFinite(value) ||
      (property?.type === 'integer' && !Number.isInteger(value)) ||
      (property?.minimum !== undefined && value < property.minimum) ||
      (property?.maximum !== undefined && value > property.maximum))
  )
    return '数值不满足允许的范围或整数要求。'
  return ''
}
export function requiredParameters(
  schema: Record<string, unknown>,
  values: Record<string, unknown>,
): Set<string> {
  const required = new Set(
    (Array.isArray(schema.required) ? schema.required : []).filter(
      (key): key is string => typeof key === 'string',
    ),
  )
  const rules = [schema, ...(Array.isArray(schema.allOf) ? schema.allOf : [])] as Record<
    string,
    unknown
  >[]
  for (const rule of rules) {
    const condition = rule.if as
      { properties?: Record<string, { const?: unknown }>; required?: string[] } | undefined
    if (
      !condition?.properties ||
      Object.values(condition.properties).some((property) => !Object.hasOwn(property, 'const'))
    )
      continue
    const matches =
      (condition.required ?? []).every((key) => Object.hasOwn(values, key)) &&
      Object.entries(condition.properties).every(
        ([key, property]) => !Object.hasOwn(values, key) || values[key] === property.const,
      )
    const branch = rule[matches ? 'then' : 'else'] as { required?: string[] } | undefined
    for (const key of branch?.required ?? []) required.add(key)
  }
  return required
}
