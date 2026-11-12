import type {
  AIChatMessage,
  AIChatReply,
  AIConfig,
  AIConfigPatch,
  AITestResult,
  AssetMeta,
  AssetStats,
  AssetVersionList,
  JobSnapshot,
  ScanOptions,
  UploadPayload,
} from '../types'

const BASE = '/api'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${BASE}${path}`, init)
  if (!response.ok) {
    throw new Error(await readErrorMessage(response))
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

async function readErrorMessage(response: Response): Promise<string> {
  try {
    const data = (await response.json()) as { error?: string }
    if (data?.error) return data.error
  } catch {
    /* 响应不是 JSON 时退回状态码描述 */
  }
  return `请求失败（HTTP ${response.status}）`
}

function jsonInit(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

export const api = {
  health: (): Promise<{ status: string }> => request<{ status: string }>('/health'),

  list: (): Promise<AssetMeta[]> => request<AssetMeta[]>('/assets'),

  get: (id: string): Promise<AssetMeta> => request<AssetMeta>(`/assets/${encodeURIComponent(id)}`),

  update: (
    id: string,
    patch: Partial<Pick<AssetMeta, 'name' | 'description' | 'category' | 'tags'>>,
  ): Promise<AssetMeta> => request<AssetMeta>(`/assets/${encodeURIComponent(id)}`, jsonInit('PATCH', patch)),

  /** 让当前 AI 运行时读元数据给出分类建议；只写 ai_* 三列，不改用户分类。 */
  classify: (id: string): Promise<AssetMeta> =>
    request<AssetMeta>(`/assets/${encodeURIComponent(id)}/classify`, { method: 'POST' }),

  /** AI 运行时配置；响应只带密钥尾号，不带明文。 */
  aiConfig: (): Promise<AIConfig> => request<AIConfig>('/ai/config'),

  saveAIConfig: (patch: AIConfigPatch): Promise<AIConfig> =>
    request<AIConfig>('/ai/config', jsonInit('PUT', patch)),

  /** 用表单当前值试连，密钥留空则沿用已保存的。失败也返回 200 + ok:false。 */
  testAIConfig: (patch: AIConfigPatch): Promise<AITestResult> =>
    request<AITestResult>('/ai/test', jsonInit('POST', patch)),

  aiChat: (messages: AIChatMessage[]): Promise<AIChatReply> =>
    request<AIChatReply>('/ai/chat', jsonInit('POST', { messages })),

  remove: (id: string): Promise<{ status: string; id: string }> =>
    request(`/assets/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  summary: (): Promise<SummaryPayload> => request<SummaryPayload>('/stats'),

  fileUrl: (id: string, version?: number): string =>
    `${BASE}/assets/${encodeURIComponent(id)}/file${version ? `?v=${version}` : ''}`,

  thumbUrl: (id: string, version: number): string =>
    `${BASE}/assets/${encodeURIComponent(id)}/thumbnail?v=${version}`,

  fileBlob: async (id: string, version?: number): Promise<Blob> => {
    const response = await fetch(api.fileUrl(id, version))
    if (!response.ok) throw new Error(await readErrorMessage(response))
    return response.blob()
  },

  /** 读附属文件（如 source.py）的纯文本；没有这个文件时返回 null，不当成错误。 */
  sidecarText: async (id: string, path: string): Promise<string | null> => {
    const response = await fetch(`${BASE}/assets/${encodeURIComponent(id)}/sidecars/${path}`)
    if (!response.ok) return null
    return response.text()
  },

  upload: (payload: UploadPayload): Promise<AssetMeta> => {
    const form = new FormData()
    form.append('meta', JSON.stringify(payload.meta))
    form.append('file', payload.file, payload.fileName)
    if (payload.thumb) {
      form.append('thumb', payload.thumb, 'thumb.webp')
      form.append('thumbExt', payload.thumb.type === 'image/png' ? 'png' : 'webp')
    }
    if (payload.sidecars?.length) {
      form.append('sidecarPaths', JSON.stringify(payload.sidecars.map((item) => item.path)))
      payload.sidecars.forEach((item) => {
        form.append('sidecar', item.file, item.path.split('/').pop() || item.file.name)
      })
    }
    return request<AssetMeta>('/assets', { method: 'POST', body: form })
  },

  uploadThumb: async (id: string, blob: Blob): Promise<void> => {
    const form = new FormData()
    form.append('thumb', blob, blob.type === 'image/png' ? 'thumb.png' : 'thumb.webp')
    form.append('thumbExt', blob.type === 'image/png' ? 'png' : 'webp')
    await request(`/assets/${encodeURIComponent(id)}/thumbnail`, { method: 'PUT', body: form })
  },

  /**
   * 就地更新：用一个新的主文件替换当前版。旧文件由后端归档进版本历史，
   * 所以这个操作是可撤销的 —— 详情页的「版本」面板能把它换回来。
   */
  replace: async (
    id: string,
    payload: { file: Blob; fileName: string; thumb?: Blob | null; sidecars?: Array<{ path: string; file: File }> },
  ): Promise<AssetMeta> => {
    const form = new FormData()
    form.append('file', payload.file, payload.fileName)
    if (payload.thumb) {
      form.append('thumb', payload.thumb, 'thumb.webp')
      form.append('thumbExt', payload.thumb.type === 'image/png' ? 'png' : 'webp')
    }
    if (payload.sidecars?.length) {
      form.append('sidecarPaths', JSON.stringify(payload.sidecars.map((item) => item.path)))
      payload.sidecars.forEach((item) => {
        form.append('sidecar', item.file, item.path.split('/').pop() || item.file.name)
      })
    }
    return request<AssetMeta>(`/assets/${encodeURIComponent(id)}/replace`, { method: 'POST', body: form })
  },

  versions: (id: string): Promise<AssetVersionList> =>
    request<AssetVersionList>(`/assets/${encodeURIComponent(id)}/versions`),

  versionFileUrl: (id: string, version: number): string =>
    `${BASE}/assets/${encodeURIComponent(id)}/versions/${version}/file`,

  restoreVersion: (id: string, version: number): Promise<AssetMeta> =>
    request<AssetMeta>(`/assets/${encodeURIComponent(id)}/versions/${version}/restore`, { method: 'POST' }),

  putMetrics: (id: string, stats: AssetStats): Promise<AssetStats> =>
    request<AssetStats>(`/assets/${encodeURIComponent(id)}/metrics`, jsonInit('PUT', stats)),

  /**
   * 扫描只负责发起作业：后端立刻返回 jobId，进度靠 /jobs/{id} 轮询。
   * 已有扫描在跑时后端返回 409 并带上那个作业的 id —— 接手上一次，而不是让用户干等。
   */
  scan: async (options: ScanOptions): Promise<{ jobId: string; conflict: boolean }> => {
    const response = await fetch(`${BASE}/scan`, jsonInit('POST', options))
    let payload: { jobId?: string; error?: string } = {}
    try {
      payload = (await response.json()) as { jobId?: string; error?: string }
    } catch {
      /* 响应不是 JSON 时按状态码报错 */
    }
    if (response.status === 202 || response.status === 409) {
      if (!payload.jobId) throw new Error(payload.error ?? '后端没有返回作业标识')
      return { jobId: payload.jobId, conflict: response.status === 409 }
    }
    throw new Error(payload.error ?? `请求失败（HTTP ${response.status}）`)
  },

  jobs: (): Promise<JobSnapshot[]> => request<JobSnapshot[]>('/jobs'),

  job: (id: string): Promise<JobSnapshot> => request<JobSnapshot>(`/jobs/${encodeURIComponent(id)}`),

  cancelJob: (id: string): Promise<{ status: string }> =>
    request(`/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),

  sand3dStatus: (): Promise<Sand3dStatus> => request<Sand3dStatus>('/sand3d/status'),

  /** PMX / .blend 扫描导入的运行时探测：PMX 需要 mmd_tools + opencc，.blend 只需要 Blender。 */
  mmdStatus: (): Promise<MmdStatus> => request<MmdStatus>('/mmd/status'),

  /** 把一段 sand3d Python 程序交给后端本机跑 bpy，拿回导出的 GLB。
   *  传 signal 可以中途放弃：浏览器断开后，后端会让那个 bpy 进程一起结束。 */
  sand3dRun: async (code: string, signal?: AbortSignal): Promise<{ blob: Blob; meta: Sand3dRunMeta }> => {
    const response = await fetch(`${BASE}/sand3d/run`, { ...jsonInit('POST', { code }), signal })
    if (!response.ok) {
      let message = `运行失败（HTTP ${response.status}）`
      let detail = ''
      let line = 0
      try {
        const data = (await response.json()) as { error?: string; detail?: string; line?: number }
        if (data.error) message = data.error
        if (data.detail) detail = data.detail
        if (typeof data.line === 'number') line = data.line
      } catch {
        /* 响应不是 JSON */
      }
      const failure = new Error(message) as Error & { detail?: string; line?: number }
      failure.detail = detail
      failure.line = line
      throw failure
    }
    const header = response.headers.get('X-Sand3D-Meta') ?? ''
    let meta: Sand3dRunMeta = {
      version: '',
      fingerprint: '',
      units: 'mm',
      objects: [],
      faces: 0,
      vertices: 0,
      bytes: 0,
      seconds: 0,
      elapsedMs: 0,
    }
    if (header) {
      try {
        meta = JSON.parse(decodeURIComponent(header)) as Sand3dRunMeta
      } catch {
        /* meta 解析失败不影响模型本身 */
      }
    }
    return { blob: await response.blob(), meta }
  },
}

export interface Sand3dStatus {
  available: boolean
  /** 跑程序的方式：`blender`（Blender 自带的 bpy）或 `python`（pip 装的 bpy） */
  runtime?: 'blender' | 'python'
  /** 实际被执行的可执行文件：blender 模式下是 blender.exe */
  python?: string
  version?: string
  /** sand3d 源码目录（源码形态安装时才有） */
  source?: string
  error?: string
}

export interface MmdStatus {
  /** 找到 Blender 即 true；.blend 只要求这一项 */
  available: boolean
  /** PMX 还要 mmd_tools + opencc */
  pmxReady: boolean
  blender?: string
  tools?: string
  opencc?: string
  error?: string
}

export interface Sand3dRunMeta {
  version: string
  fingerprint: string
  units: string
  objects: string[]
  faces: number
  vertices: number
  bytes: number
  seconds: number
  elapsedMs: number
}

export interface SummaryPayload {
  count: number
  totalBytes: number
  categories: Array<{ name: string; count: number }>
}
