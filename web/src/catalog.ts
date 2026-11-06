import type { EvidenceMode } from './model'

export const domains = [
  ['H', '硬件与计算'],
  ['O', '操作系统与内核'],
  ['S', '存储与文件系统'],
  ['N', '网络与通信'],
  ['R', '系统资源与基础服务'],
  ['A', '安全与访问'],
  ['D', '软件依赖与运行时'],
  ['L', '系统健康与生命周期'],
] as const
export interface CatalogEntry {
  id: string
  name: string
  summary: string
  evidence: EvidenceMode
  privileged: boolean
}
