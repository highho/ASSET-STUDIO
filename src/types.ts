/** 支持的模型格式 —— 前端只在这一处写，其它地方（筛选条、文件选择器、后端）都从这里取 */
export const MODEL_FORMATS = ['glb', 'gltf', 'obj', 'stl', 'fbx'] as const

export type AssetFormat = (typeof MODEL_FORMATS)[number]

/** 兼容旧调用点；值就是 MODEL_FORMATS */
export const ASSET_FORMATS: AssetFormat[] = [...MODEL_FORMATS]

/** 扫描时可接受的源格式：不能直接入库，先由服务端调用 Blender 转成 GLB */
export const SCAN_SOURCE_FORMATS = ['pmx', 'blend'] as const

export type ScanSourceFormat = (typeof SCAN_SOURCE_FORMATS)[number]

export type ScanFormat = AssetFormat | ScanSourceFormat

/** 扫描弹层的格式 chip 用这一份，资产筛选仍用 ASSET_FORMATS */
export const SCAN_FORMATS: ScanFormat[] = [...MODEL_FORMATS, ...SCAN_SOURCE_FORMATS]

/** 模型的附属资源扩展名：glTF 的 .bin / 贴图，OBJ 的 .mtl 与其贴图 */
export const SIDECAR_FORMATS = [
  'bin',
  'mtl',
  'png',
  'jpg',
  'jpeg',
  'webp',
  'ktx2',
  'basis',
  'ktx',
  'dds',
  'tga',
  'bmp',
  'hdr',
  'exr',
] as const

export type SidecarFormat = (typeof SIDECAR_FORMATS)[number]

/** 文件选择器的 accept 串。资产库与侧栏两个入口共用同一份，避免一边能选附属文件一边不能。 */
export const MODEL_ACCEPT = MODEL_FORMATS.map((format) => `.${format}`).join(',')
export const MODEL_WITH_SIDECAR_ACCEPT = [...MODEL_FORMATS, ...SIDECAR_FORMATS]
  .map((format) => `.${format}`)
  .join(',')

/** 包围尺寸的单位：glTF / GLB 规范写死为米；OBJ / STL / FBX 文件里不写单位，只能按原值展示 */
export function dimensionUnitLabel(format: AssetFormat): string {
  return format === 'glb' || format === 'gltf' ? 'm' : '原始单位'
}

export interface AssetStats {
  vertices: number
  triangles: number
  meshes: number
  materials: number
  /** 模型包围盒尺寸，数值随源文件；单位见 dimensionUnitLabel(format) */
  dimensions: [number, number, number]
}

/** 后端预留的 AI 分类结果字段，接入模型后由 /api/assets/{id}/classify 回填 */
export interface AssetAI {
  category: string
  confidence: number
  tags: string[]
  provider?: string
}

/** AI 运行时配置，GET /api/ai/config 只返回密钥尾号，不返回明文 */
export type AIProvider =
  | 'ollama'
  | 'openai'
  | 'deepseek'
  | 'moonshot'
  | 'zhipu'
  | 'dashscope'
  | 'siliconflow'
  | 'groq'
  | 'lmstudio'
  | 'custom'

export interface AIConfig {
  provider: AIProvider | ''
  endpoint: string
  model: string
  hasKey: boolean
  keyHint?: string
  label: string
}

export interface AIConfigPatch {
  provider?: AIProvider
  endpoint?: string
  model?: string
  /** 留空/不传表示保留已保存的密钥 */
  apiKey?: string
  clearKey?: boolean
}

export interface AITestResult {
  ok: boolean
  message: string
  provider: string
  models?: string[]
}

export interface AIChatMessage {
  role: 'user' | 'assistant'
  content: string
}

export interface AIChatReply {
  reply: string
  provider: string
}

export interface AssetMeta {
  id: string
  name: string
  description: string
  category: string
  tags: string[]
  format: AssetFormat
  fileName: string
  fileSize: number
  createdAt: number
  updatedAt: number
  stats: AssetStats
  hasThumb: boolean
  /** 是否带着外部资源（GLTF 的 .bin、贴图，OBJ 的 .mtl）一起入库 */
  hasSidecars?: boolean
  /** 文件夹扫描入库时记录的磁盘来源路径 */
  sourcePath?: string
  ai?: AssetAI
  /** 指标状态：ok 已有数据；pending 等待浏览器补；error 解析失败（原因看 lastError） */
  metricsState?: 'ok' | 'pending' | 'error'
  /** 指标来源：server 表示后端自己解析（权威，浏览器不再覆盖）；client 表示浏览器回填 */
  metricsSource?: 'server' | 'client' | ''
  /** 缩略图状态 */
  thumbState?: 'ok' | 'missing' | 'error'
  /** 补齐尝试次数，用于停止无限重试 */
  attempts?: number
  /** 最近一次失败的原始原因 */
  lastError?: string
  /** 模型文件内容的 sha256，后端入库时计算 */
  sha256?: string
  /** 当前是第几版：1 是原始入库的那份，每次就地更新 +1（回滚也 +1） */
  version?: number
}

/** 一份已被替换掉的旧版本，来自 GET /api/assets/{id}/versions */
export interface AssetVersion {
  version: number
  fileName: string
  format: AssetFormat
  fileSize: number
  sha256?: string
  createdAt: number
  replacedAt: number
}

export interface AssetVersionList {
  id: string
  version: number
  fileSize: number
  items: AssetVersion[]
}

export interface UploadMeta {
  name: string
  description: string
  category: string
  tags: string[]
  format: AssetFormat
  stats: AssetStats
}

/** 模型的外部附属资源，path 是相对主文件目录的路径 */
export interface SidecarFile {
  path: string
  file: File
}

export interface UploadPayload {
  file: Blob
  fileName: string
  meta: UploadMeta
  thumb: Blob | null
  sidecars?: SidecarFile[]
}

export interface ScanFailure {
  path: string
  reason: string
}

/** 扫描作业跑完后的结果，对应后端 /api/jobs/{id} 的 result 字段 */
export interface ScanJobResult {
  scanned: number
  created: number
  createdIds: string[]
  skipped: number
  failed: number
  truncated: boolean
  formats: string[]
}

export type JobStatus = 'running' | 'done' | 'failed' | 'canceled'

/** 后台作业的进度快照。扫描不再占用一次 HTTP 请求，而是登记成作业让前端轮询。 */
export interface JobSnapshot {
  id: string
  kind: string
  status: JobStatus
  total: number
  done: number
  skipped: number
  failed: number
  message?: string
  error?: string
  failures?: ScanFailure[]
  result?: ScanJobResult
  startedAt: number
  updatedAt: number
  endedAt?: number
}

export interface ScanOptions {
  path: string
  recursive: boolean
  category: string
  /** 为空表示全部格式 */
  formats: ScanFormat[]
  limit?: number
}

/** 可导出的目标格式，FBX 只进不出（Three.js 没有 FBX 导出器） */
export type ConvertTarget = 'glb' | 'gltf' | 'obj' | 'stl'

export const CONVERT_TARGETS: ConvertTarget[] = ['glb', 'gltf', 'obj', 'stl']

export type SortKey = 'recent' | 'name' | 'size' | 'triangles'
