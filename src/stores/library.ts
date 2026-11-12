import { computed, ref } from 'vue'
import { api } from '../lib/api'
import { readError } from '../lib/download'
import { canAttemptThumb, clearThumbAttempts, noteThumbAttempt, thumbAttempts } from '../lib/thumb'
import { thumbAutoEnabled } from '../lib/prefs'
import type { AssetFormat, AssetMeta, AssetStats, ConvertTarget, JobSnapshot, ScanFailure, ScanOptions, SidecarFile, UploadPayload } from '../types'
import { SIDECAR_FORMATS } from '../types'
import type * as THREE from 'three'
import { pushToast } from './toast'

/**
 * three.js 与导出器只在真正要解析/渲染/转换模型时才载入。
 * 资产库首屏大多数时候只需列表与缩略图 URL，不该为了三个函数背着 ~600 KB 的 three 启动。
 */
type ModelLib = typeof import('../lib/model')
type ExportLib = typeof import('../lib/export')
type PlacementLib = typeof import('../lib/placement')

let modelLibCache: Promise<ModelLib> | null = null
let exportLibCache: Promise<ExportLib> | null = null
let placementLibCache: Promise<PlacementLib> | null = null

function loadModelLib(): Promise<ModelLib> {
  if (!modelLibCache) modelLibCache = import('../lib/model')
  return modelLibCache
}

function loadExportLib(): Promise<ExportLib> {
  if (!exportLibCache) exportLibCache = import('../lib/export')
  return exportLibCache
}

function loadPlacementLib(): Promise<PlacementLib> {
  if (!placementLibCache) placementLibCache = import('../lib/placement')
  return placementLibCache
}

export const assets = ref<AssetMeta[]>([])
export const ready = ref(false)
export const busy = ref(false)
export const bootPhase = ref<'idle' | 'loading' | 'seeding'>('idle')
export const importProgress = ref<{ total: number; done: number; label: string } | null>(null)
export const backfillProgress = ref<{ total: number; done: number } | null>(null)
export const convertProgress = ref<{ total: number; done: number; label: string } | null>(null)
export const scanReport = ref<{
  created: number
  skipped: number
  failed: ScanFailure[]
  truncated: boolean
} | null>(null)

/** 正在跑的扫描作业进度。null 表示当前没有扫描。 */
export const scanJob = ref<{
  id: string
  status: JobSnapshot['status']
  total: number
  done: number
  skipped: number
  failed: number
  message: string
} | null>(null)
export const backendOnline = ref(false)

export const assetCount = computed(() => assets.value.length)
export const totalBytes = computed(() => assets.value.reduce((sum, item) => sum + item.fileSize, 0))
export const categories = computed(() => {
  const set = new Set<string>()
  assets.value.forEach((item) => set.add(item.category || '未分类'))
  return [...set].sort((a, b) => a.localeCompare(b, 'zh-CN'))
})

function stripExtension(fileName: string): string {
  const dot = fileName.lastIndexOf('.')
  return dot > 0 ? fileName.slice(0, dot) : fileName
}

export function getAsset(id: string): AssetMeta | undefined {
  return assets.value.find((item) => item.id === id)
}

/**
 * 缩略图直接由后端提供。更新过的资产 updatedAt 变化，URL 也随之变化，
 * 浏览器不会继续用旧的缓存图。
 */
export function getThumbUrl(id: string): string | undefined {
  const asset = getAsset(id)
  if (!asset?.hasThumb) return undefined
  return api.thumbUrl(asset.id, asset.updatedAt)
}

function upsert(asset: AssetMeta): void {
  const index = assets.value.findIndex((item) => item.id === asset.id)
  if (index >= 0) assets.value[index] = asset
  else assets.value.unshift(asset)
}

let initPromise: Promise<void> | null = null

export function initLibrary(): Promise<void> {
  if (!initPromise) initPromise = runInit()
  return initPromise
}

async function runInit(): Promise<void> {
  if (ready.value) return
  bootPhase.value = 'loading'

  try {
    assets.value = await api.list()
    backendOnline.value = true
  } catch (error) {
    backendOnline.value = false
    pushToast(`无法连接后端：${readError(error)}。服务恢复后会自动重连`, 'error')
    ready.value = true
    bootPhase.value = 'idle'
    watchBackend()
    return
  }

  ready.value = true
  bootPhase.value = 'idle'
  watchBackend()

  // 刷新页面或换标签页再回来时，把还在跑的扫描重新接上：进度在服务端，不在页面上。
  void reattachRunningJob()

  // 扫描入库、或中途中断的资产可能缺缩略图，进入资产库后自动补一次；
  // 已经失败到上限的不再重试，标成 error 让卡片给出「重试」入口。
  // 设置里关掉「自动补缩略图」后就不再在进库时批量跑，卡片仍可手动重试。
  const autoThumb = thumbAutoEnabled()
  const pending = autoThumb ? assets.value.filter((item) => !item.hasThumb).map((item) => item.id) : []
  for (const asset of assets.value) {
    if (!asset.hasThumb && !canAttemptThumb(asset.id)) asset.thumbState = 'error'
  }
  if (pending.length > 0) void backfillAssets(pending)
}

/**
 * 载入 4 个内置示例资产。
 *
 * 这一步以前是「库空就自动播种」，导致两个副作用：删空库再刷新会「复活」，
 * 多标签同时首启会各播一份。现在改成显式动作，只由用户点按钮触发。
 */
export async function seedExamples(): Promise<void> {
  if (bootPhase.value !== 'idle') return
  bootPhase.value = 'seeding'
  try {
    const { createSeedPayloads } = await import('../lib/seed')
    const payloads = await createSeedPayloads()
    // 倒序上传，让列表顺序与示例定义的顺序一致（后端按创建时间倒序返回）
    for (const payload of [...payloads].reverse()) {
      const asset = await api.upload(payload)
      assets.value.unshift(asset)
    }
    assets.value.sort((a, b) => b.createdAt - a.createdAt)
    pushToast(`已载入 ${payloads.length} 个示例资产`, 'success')
  } catch (error) {
    pushToast(`示例资产写入失败：${readError(error)}`, 'error')
  } finally {
    bootPhase.value = 'idle'
  }
}

let healthTimer: number | null = null

/**
 * 每 5 秒探一次健康接口，两个方向都要管：
 * - 启动时没连上：连上后把资产列表拉回来，不用手动刷新；
 * - 页面开着时后端挂了（重启、换端口）：标成离线，恢复后自动重连。
 * 以前的 initLibrary 只跑一次并缓存 promise，这两种情况都处理不了。
 */
function watchBackend(): void {
  if (healthTimer !== null) return
  healthTimer = window.setInterval(() => {
    void probeBackend()
  }, 5000)
}

async function probeBackend(): Promise<void> {
  let online: boolean
  try {
    await api.health()
    online = true
  } catch {
    online = false
  }

  if (!online) {
    if (backendOnline.value) {
      backendOnline.value = false
      pushToast('与后端的连接已断开，恢复后会自动重连', 'error')
    }
    return
  }
  if (backendOnline.value) return

  try {
    assets.value = await api.list()
    backendOnline.value = true
    pushToast('后端已恢复连接', 'success')
    void reattachRunningJob()
    const pending = assets.value.filter((item) => !item.hasThumb).map((item) => item.id)
    for (const asset of assets.value) {
      if (!asset.hasThumb && !canAttemptThumb(asset.id)) asset.thumbState = 'error'
    }
    if (pending.length > 0) void backfillAssets(pending)
  } catch (error) {
    console.warn('后端已响应，但列表读取失败', error)
  }
}


/** 找到正在运行的扫描作业就继续轮询，避免刷新后进度条消失、用户以为扫描没了。 */
async function reattachRunningJob(): Promise<void> {
  if (scanJob.value) return
  try {
    const list = await api.jobs()
    const running = list.find((item) => item.kind === 'scan' && item.status === 'running')
    if (running) watchScanJob(running.id)
  } catch {
    /* 拿不到作业列表不影响资产库使用 */
  }
}

async function buildPayload(file: File, format: AssetFormat): Promise<UploadPayload> {
  const { loadModelFromBlob, computeStats, createThumbnail, disposeObject } = await loadModelLib()
  const object = await loadModelFromBlob(file, format)
  try {
    const stats = computeStats(object)
    const thumb = await createThumbnail(object)
    return {
      file,
      fileName: file.name,
      thumb,
      meta: {
        name: stripExtension(file.name),
        description: '',
        category: '未分类',
        tags: [],
        format,
        stats,
      },
    }
  } finally {
    disposeObject(object)
  }
}

/** 可能是模型附属资源的扩展名 —— 与 types.ts 的 SIDECAR_FORMATS 同一份，不再各写一遍 */
const SIDECAR_EXTENSIONS = new Set<string>(SIDECAR_FORMATS)

function webkitPath(file: File): string {
  const path = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  return typeof path === 'string' ? path.replace(/\\/g, '/') : ''
}

function baseName(path: string): string {
  const clean = path.replace(/\\/g, '/')
  return clean.slice(clean.lastIndexOf('/') + 1).toLowerCase()
}

/** 从模型自身解析出它引用的外部资源：GLTF 的 buffers / images，OBJ 的 mtllib 与 MTL 里的贴图 */
async function collectReferences(
  assetFile: File,
  format: AssetFormat,
  lookup: Map<string, File>,
): Promise<string[]> {
  const refs: string[] = []
  try {
    if (format === 'gltf') {
      const json = JSON.parse(await assetFile.text()) as {
        buffers?: Array<{ uri?: string }>
        images?: Array<{ uri?: string }>
      }
      for (const item of [...(json.buffers ?? []), ...(json.images ?? [])]) {
        const uri = item?.uri
        if (uri && !/^(data:|blob:|http:|https:)/i.test(uri)) refs.push(uri.replace(/\\/g, '/'))
      }
    } else if (format === 'obj') {
      const text = await assetFile.text()
      const name = /\bmtllib\s+([^\r\n]+)/i.exec(text)?.[1]?.trim().split(/\s+/)[0]
      if (name) {
        refs.push(name.replace(/\\/g, '/'))
        const mtlFile = lookup.get(baseName(name))
        if (mtlFile) {
          const mtlText = await mtlFile.text()
          const mapRegex = /\b(map_[a-z0-9_]+|bump|disp|refl|decal)\s+([^\r\n]+)/gi
          let hit = mapRegex.exec(mtlText)
          while (hit) {
            const candidate = hit[2].trim().split(/\s+/).pop()
            if (candidate && !candidate.startsWith('-')) refs.push(candidate.replace(/\\/g, '/'))
            hit = mapRegex.exec(mtlText)
          }
        }
      }
    }
  } catch (error) {
    console.warn('解析模型外部引用失败', error)
  }
  return refs
}

/** 选目录时按相对目录配对；散选时按模型里写的 uri 还原子目录，让 textures/ 这类引用仍能命中 */
async function collectSidecars(
  assetFile: File,
  format: AssetFormat,
  extras: File[],
): Promise<SidecarFile[]> {
  const assetPath = webkitPath(assetFile)
  const dir = assetPath ? assetPath.slice(0, assetPath.lastIndexOf('/') + 1) : ''

  if (dir) {
    const list: SidecarFile[] = []
    extras.forEach((extra) => {
      const extraPath = webkitPath(extra)
      if (extraPath && extraPath.startsWith(dir)) {
        list.push({ path: extraPath.slice(dir.length), file: extra })
      }
    })
    return list
  }

  const lookup = new Map<string, File>()
  extras.forEach((extra) => lookup.set(baseName(extra.name), extra))

  const refs = await collectReferences(assetFile, format, lookup)
  const used = new Set<File>()
  const list: SidecarFile[] = []
  refs.forEach((ref) => {
    const file = lookup.get(baseName(ref))
    if (file && !used.has(file)) {
      used.add(file)
      list.push({ path: ref, file })
    }
  })
  extras.forEach((extra) => {
    if (!used.has(extra)) list.push({ path: extra.name, file: extra })
  })
  return list
}

const emptyStats = (): AssetStats => ({
  vertices: 0,
  triangles: 0,
  meshes: 0,
  materials: 0,
  dimensions: [0, 0, 0],
})

export async function importFiles(files: File[]): Promise<void> {
  if (!files.length || busy.value) return

  const accepted: Array<{ file: File; format: AssetFormat }> = []
  const extras: File[] = []
  const { detectFormat } = await loadModelLib()
  files.forEach((file) => {
    const format = detectFormat(file.name)
    if (format) {
      accepted.push({ file, format })
      return
    }
    const ext = file.name.split('.').pop()?.toLowerCase() ?? ''
    if (SIDECAR_EXTENSIONS.has(ext)) extras.push(file)
    else pushToast(`「${file.name}」格式不支持，仅支持 GLB / GLTF / OBJ / STL / FBX`, 'error')
  })
  if (!accepted.length) return

  busy.value = true
  importProgress.value = { total: accepted.length, done: 0, label: accepted[0]?.file.name ?? '' }
  let imported = 0
  const pendingBackfill: string[] = []

  for (const [index, item] of accepted.entries()) {
    importProgress.value = { total: accepted.length, done: index, label: item.file.name }
    const sidecars = await collectSidecars(item.file, item.format, extras)
    try {
      if (sidecars.length) {
        // 有外部资源时先入库、再按资产 id 解析：解析阶段要靠 sidecars 接口取 .bin 与贴图
        const asset = await api.upload({
          file: item.file,
          fileName: item.file.name,
          thumb: null,
          meta: {
            name: stripExtension(item.file.name),
            description: `含 ${sidecars.length} 个外部资源：${sidecars
              .map((sidecar) => sidecar.path)
              .slice(0, 5)
              .join('、')}${sidecars.length > 5 ? ' 等' : ''}`,
            category: '未分类',
            tags: ['带外部资源'],
            format: item.format,
            stats: emptyStats(),
          },
          sidecars,
        })
        upsert(asset)
        pendingBackfill.push(asset.id)
      } else {
        const payload = await buildPayload(item.file, item.format)
        const asset = await api.upload(payload)
        upsert(asset)
      }
      imported += 1
    } catch (error) {
      pushToast(`「${item.file.name}」导入失败：${readError(error)}`, 'error')
    }
  }

  importProgress.value = null
  if (imported > 0) pushToast(`已导入 ${imported} 个资产`, 'success')
  if (pendingBackfill.length) await backfillAssets(pendingBackfill)
  busy.value = false
}

export async function saveAssetMeta(
  meta: Pick<AssetMeta, 'id' | 'name' | 'description' | 'category' | 'tags'>,
): Promise<AssetMeta | null> {
  try {
    const updated = await api.update(meta.id, {
      name: meta.name.trim(),
      description: meta.description,
      category: meta.category,
      tags: meta.tags,
    })
    upsert(updated)
    return updated
  } catch (error) {
    pushToast(`保存失败：${readError(error)}`, 'error')
    return null
  }
}

/** AI 分类只回填 ai_* 三列；用户点「采纳建议」后才走 saveAssetMeta 改正式分类。 */
export async function classifyAssetMeta(id: string): Promise<AssetMeta> {
  const updated = await api.classify(id)
  upsert(updated)
  return updated
}

export async function removeAssets(ids: string[]): Promise<void> {
  if (!ids.length) return
  busy.value = true
  const failed: string[] = []
  try {
    for (const id of ids) {
      try {
        await api.remove(id)
      } catch {
        failed.push(id)
      }
    }
    assets.value = assets.value.filter((item) => !ids.includes(item.id) || failed.includes(item.id))
    if (failed.length) pushToast(`${failed.length} 个资产删除失败`, 'error')
    else pushToast(ids.length > 1 ? `已删除 ${ids.length} 个资产` : '资产已删除', 'success')
  } finally {
    busy.value = false
  }
}

export async function regenerateThumbnail(id: string): Promise<void> {
  const asset = getAsset(id)
  if (!asset) {
    pushToast('该资产已不存在', 'error')
    return
  }
  busy.value = true
  try {
    const { loadModelFromBlob, createThumbnail, disposeObject } = await loadModelLib()
    const blob = await api.fileBlob(id, asset.updatedAt)
    const object = await loadModelFromBlob(blob, asset.format, {
      assetId: id,
      hasSidecars: asset.hasSidecars,
    })
    const thumb = await createThumbnail(object)
    disposeObject(object)
    if (!thumb) {
      pushToast('缩略图生成失败，当前环境可能不支持 WebGL', 'error')
      return
    }
    await api.uploadThumb(id, thumb)
    clearThumbAttempts(id)
    // 用服务端返回的最新元数据替换：缩略图的缓存版本号取自 updatedAt，
    // 用本机 Date.now() 会和服务端的时间源不一致，缓存可能不刷新。
    const fresh = await api.get(id)
    upsert(fresh)
    pushToast('缩略图已重新渲染', 'success')
  } catch (error) {
    pushToast(`缩略图更新失败：${readError(error)}`, 'error')
  } finally {
    busy.value = false
  }
}

export async function readAssetFile(id: string): Promise<{ blob: Blob; meta: AssetMeta } | undefined> {
  const meta = getAsset(id)
  if (!meta) return undefined
  // 带上 updatedAt 做缓存键：就地更新后同一个 URL 的内容会变，不带就会被浏览器缓存住旧模型。
  const blob = await api.fileBlob(id, meta.updatedAt)
  return { blob, meta }
}

/**
 * 文件夹扫描：后端把这批文件当成一个后台作业来跑，HTTP 只负责取回 jobId。
 * 前端轮询进度，跑完后重新拉一次资产列表 —— 扫描期间用户可以继续删改资产、离开页面再回来。
 */
export async function scanFolder(options: ScanOptions): Promise<void> {
  try {
    const started = await api.scan(options)
    if (started.conflict) pushToast('已有一个扫描在进行，先接着看它的进度', 'info')
    watchScanJob(started.jobId)
  } catch (error) {
    pushToast(`扫描失败：${readError(error)}`, 'error')
  }
}

let scanTimer: ReturnType<typeof setTimeout> | null = null

function watchScanJob(id: string): void {
  stopPolling()
  scanJob.value = { id, status: 'running', total: 0, done: 0, skipped: 0, failed: 0, message: '' }

  const tick = async (): Promise<void> => {
    try {
      const shot = await api.job(id)
      scanJob.value = {
        id: shot.id,
        status: shot.status,
        total: shot.total,
        done: shot.done,
        skipped: shot.skipped,
        failed: shot.failed,
        message: shot.message ?? '',
      }
      if (shot.status === 'running') {
        scanTimer = setTimeout(() => void tick(), 600)
        return
      }
      await finishScanJob(shot)
    } catch (error) {
      scanJob.value = null
      pushToast(`读取扫描进度失败：${readError(error)}`, 'error')
    }
  }

  scanTimer = setTimeout(() => void tick(), 400)
}

function stopPolling(): void {
  if (scanTimer) {
    clearTimeout(scanTimer)
    scanTimer = null
  }
}

/** 请求取消扫描：后端在处理下一个文件前停手，已入库的保留。 */
export function cancelScan(): void {
  const id = scanJob.value?.id
  if (!id) return
  void api.cancelJob(id).catch((error) => pushToast(`取消失败：${readError(error)}`, 'error'))
}

/** 扫描入库后重新拉一次列表，比逐个 unshift 更省事，也不会把顺序弄乱。 */
async function reloadAssets(): Promise<void> {
  try {
    assets.value = await api.list()
    backendOnline.value = true
  } catch (error) {
    pushToast(`刷新资产列表失败：${readError(error)}`, 'error')
  }
}

async function finishScanJob(shot: JobSnapshot): Promise<void> {
  scanJob.value = null
  const result = shot.result

  if (shot.status === 'failed') {
    pushToast(`扫描失败：${shot.error ?? '后端起作业出错'}`, 'error')
    return
  }

  await reloadAssets()

  const created = result?.created ?? 0
  const skipped = result?.skipped ?? 0
  scanReport.value = {
    created,
    skipped,
    failed: shot.failures ?? [],
    truncated: result?.truncated ?? false,
  }

  if (shot.status === 'canceled') {
    pushToast(created > 0 ? `扫描已取消，已入库 ${created} 个模型` : '扫描已取消', 'info')
  } else if (created > 0) {
    pushToast(`已入库 ${created} 个模型（跳过 ${skipped} 个已导入文件）`, 'success')
  } else if (skipped > 0) {
    pushToast('目录内的模型都已导入过', 'info')
  } else {
    pushToast('该目录里没有发现符合筛选的模型', 'info')
  }
  if (shot.failed > 0) pushToast(`${shot.failed} 个文件处理失败，详见扫描报告`, 'error')
  if (result?.truncated) pushToast('候选文件超过单次上限，只处理了前面一部分', 'info')

  const createdIds = result?.createdIds ?? []
  if (createdIds.length > 0) void backfillAssets(createdIds)
}

/** 扫描入库、或中途中断的资产缺几何统计与缩略图，这里逐个补上 */
async function backfillAssets(ids: string[]): Promise<void> {
  const { loadModelFromBlob, computeStats, createThumbnail, disposeObject } = await loadModelLib()
  backfillProgress.value = { total: ids.length, done: 0 }
  let filled = 0
  let failed = 0
  for (const [index, id] of ids.entries()) {
    backfillProgress.value = { total: ids.length, done: index }
    const asset = getAsset(id)
    if (!asset) continue
    if (!canAttemptThumb(id)) {
      // 已经失败到上限：不再自动重试，等用户手动点「重试」
      asset.thumbState = 'error'
      continue
    }
    try {
      const blob = await api.fileBlob(id)
      const object = await loadModelFromBlob(blob, asset.format, {
        assetId: id,
        hasSidecars: asset.hasSidecars,
      })
      const stats: AssetStats = computeStats(object)
      const thumb = await createThumbnail(object)
      disposeObject(object)
      // 后端已经解析过指标的资产不再回写（后端才是权威，回写会被忽略）：
      // 这里解析只为渲染缩略图，不必再发一次无用的 PUT。
      const serverOwnsMetrics = asset.metricsState === 'ok' && asset.metricsSource === 'server'
      if (!serverOwnsMetrics) await api.putMetrics(id, stats)
      if (thumb) await api.uploadThumb(id, thumb)
      if (!serverOwnsMetrics) asset.stats = stats
      asset.metricsState = 'ok'
      asset.metricsSource = serverOwnsMetrics ? 'server' : 'client'
      asset.hasThumb = Boolean(thumb)
      asset.thumbState = thumb ? 'ok' : 'missing'
      if (thumb) {
        clearThumbAttempts(id)
        // 同样以服务端 updatedAt 为准，避免缩略图缓存版本号与后端时间源不一致
        const fresh = await api.get(id)
        upsert(fresh)
        filled += 1
      } else {
        noteThumbAttempt(id)
        asset.thumbState = 'error'
        failed += 1
      }
    } catch (error) {
      console.warn('补全资产失败', id, error)
      noteThumbAttempt(id)
      asset.thumbState = 'error'
      failed += 1
    }
  }
  backfillProgress.value = null
  if (filled > 0) pushToast(`${filled} 个资产的统计与缩略图已补齐`, 'success')
  if (failed > 0) {
    const capped = ids.filter((id) => !canAttemptThumb(id)).length
    pushToast(
      capped > 0
        ? `${failed} 个资产缩略图生成失败（${capped} 个已达重试上限，可在卡片上手动重试）`
        : `${failed} 个资产缩略图生成失败，下次进入资产库会自动再试`,
      'error',
    )
  }
}

/** 用户手动重试：清掉失败计数再跑一次，成功一次就把计数归零 */
export async function retryThumbnail(id: string): Promise<void> {
  const asset = getAsset(id)
  if (!asset) return
  if (asset.hasThumb) return
  if (thumbAttempts(id) >= 3) clearThumbAttempts(id)
  asset.thumbState = 'missing'
  await backfillAssets([id])
}

/**
 * 批量格式转换：逐个取源文件、解析、导出目标格式，作为新资产入库（保留原件）。
 * 转换在浏览器完成，后端只接收结果，因此不需要额外的服务端接口。
 */
export async function convertAssets(ids: string[], target: ConvertTarget): Promise<void> {
  if (!ids.length || busy.value) return
  const [{ loadModelFromBlob, computeStats, createThumbnail, disposeObject }, { convertObject }, { withOriginalPlacement }] =
    await Promise.all([loadModelLib(), loadExportLib(), loadPlacementLib()])
  busy.value = true
  convertProgress.value = { total: ids.length, done: 0, label: '' }
  let converted = 0
  const failures: string[] = []

  for (const [index, id] of ids.entries()) {
    const asset = getAsset(id)
    convertProgress.value = { total: ids.length, done: index, label: asset?.name ?? id }
    if (!asset) continue
    try {
      const source = await api.fileBlob(id)
      const object = await loadModelFromBlob(source, asset.format, {
        assetId: id,
        hasSidecars: asset.hasSidecars,
      })
      const stats = computeStats(object)
      const thumb = await createThumbnail(object)
      const baseName = stripExtension(asset.fileName).replace(/[\\/:*?"<>|]/g, '_')
      const result = await withOriginalPlacement(object, () => convertObject(object, target, baseName))
      disposeObject(object)

      const note = `来源：${asset.name}（${asset.format.toUpperCase()} → ${target.toUpperCase()}）`
      // 名字里已带格式后缀的，先剥掉再拼，避免反复转换叠成「xxx (STL) (OBJ) (GLTF)」
      const nameBase = asset.name.replace(/\s*\((?:GLB|GLTF|OBJ|STL|FBX)\)$/i, '').trim() || asset.name
      const payload: UploadPayload = {
        file: result.blob,
        fileName: result.fileName,
        thumb,
        meta: {
          name: `${nameBase} (${target.toUpperCase()})`,
          description: asset.description ? `${asset.description}\n${note}` : note,
          category: asset.category || '未分类',
          tags: [...new Set([...asset.tags, '格式转换'])].slice(0, 12),
          format: target,
          stats,
        },
      }
      const created = await api.upload(payload)
      upsert(created)
      converted += 1
    } catch (error) {
      failures.push(`${asset.name}：${readError(error)}`)
    }
  }

  convertProgress.value = null
  busy.value = false
  if (converted > 0) pushToast(`已转换并入库 ${converted} 个 ${target.toUpperCase()} 资产`, 'success')
  if (failures.length) pushToast(`${failures.length} 个转换失败：${failures[0]}`, 'error')
}

/** 「代码」模式里跑出来的模型，导出成 GLB 直接入库，和别的资产一样能预览、转换、删除 */
export async function createAssetFromObject(object: THREE.Object3D, name: string, category = '代码生成'): Promise<AssetMeta> {
  const [{ computeStats, createThumbnail }, { convertObject }, { withOriginalPlacement }] = await Promise.all([
    loadModelLib(),
    loadExportLib(),
    loadPlacementLib(),
  ])
  const stats = computeStats(object)
  const thumb = await createThumbnail(object)
  const baseName = (name || '代码模型').replace(/[\\/:*?"<>|]/g, '_')
  const result = await withOriginalPlacement(object, () => convertObject(object, 'glb', baseName))
  const created = await api.upload({
    file: result.blob,
    fileName: result.fileName,
    thumb,
    meta: {
      name: name.trim() || '代码模型',
      description: '由检视台「代码」模式生成',
      category,
      tags: ['代码生成'],
      format: 'glb',
      stats,
    },
  })
  upsert(created)
  pushToast(`已把「${created.name}」存为资产`, 'success')
  return created
}

/**
 * Sand3D 程序真跑出来的 GLB 直接入库：几何就是 Blender 导出的那份，不再经过浏览器重建。
 * 生成它的源代码一起写进描述，过几天回来还能照着改。
 */
export async function createAssetFromGlb(blob: Blob, name: string, code: string): Promise<AssetMeta> {
  const { loadModelFromBlob, computeStats, createThumbnail, disposeObject } = await loadModelLib()
  const object = await loadModelFromBlob(blob, 'glb')
  const stats = computeStats(object)
  const thumb = await createThumbnail(object)
  disposeObject(object)

  const baseName = (name || 'sand3d-model').replace(/[\\/:*?"<>|]/g, '_')
  const lines = code.trim().split('\n').length
  const created = await api.upload({
    file: blob,
    fileName: `${baseName}.glb`,
    thumb,
    meta: {
      name: name.trim() || 'sand3d 模型',
      // 源码完整地存成附属文件 source.py，描述里只留一句指引：
      // 以前是把源码切片塞进描述，超过 4000 字就被静默截断，用户拿到的代码跑不起来。
      description: `由检视台 Sand3D 程序生成\n\n生成这段模型的源码共 ${lines} 行，完整保存在附属文件 source.py。`,
      category: '代码生成',
      tags: ['sand3d', '代码生成'],
      format: 'glb',
      stats,
    },
    sidecars: [{ path: 'source.py', file: new File([code], 'source.py', { type: 'text/x-python' }) }],
  })
  upsert(created)
  pushToast(`已把「${created.name}」存为资产`, 'success')
  return created
}

/**
 * 就地更新一个已有资产：把这段程序跑出来的 GLB 换成它的当前版。
 * 旧文件由后端归档，名字/分类/描述都不动 —— 这正是「改完能原地保存」这件事。
 */
export async function replaceAssetFromGlb(id: string, blob: Blob, code: string): Promise<AssetMeta> {
  const asset = getAsset(id)
  if (!asset) throw new Error('这个资产已经不在库里了')

  const { loadModelFromBlob, createThumbnail, disposeObject } = await loadModelLib()
  const object = await loadModelFromBlob(blob, 'glb')
  const thumb = await createThumbnail(object)
  disposeObject(object)

  const updated = await api.replace(id, {
    file: blob,
    fileName: asset.fileName || `${id}.glb`,
    thumb,
    sidecars: [{ path: 'source.py', file: new File([code], 'source.py', { type: 'text/x-python' }) }],
  })
  upsert(updated)
  pushToast(`已更新「${updated.name}」到 v${updated.version ?? 2}，旧版可在版本记录里找回`, 'success')
  return updated
}

/** 回滚到某一版：后端把那一版放回当前位，现在的这一版同样进版本记录，所以回滚也能撤回。 */
export async function restoreAssetVersion(id: string, version: number): Promise<AssetMeta> {
  const updated = await api.restoreVersion(id, version)
  upsert(updated)
  pushToast(`已回滚到 v${version}，当前是 v${updated.version ?? version + 1}`, 'success')
  return updated
}

/**
 * 装配体导出：把装配台上的多个实例合成一个 GLB 入库。
 * 装配定义（引用了哪些资产、各自的位置/旋转/缩放）作为 `assembly.json` 附属文件一起存，
 * 以后能从详情页重新打开装配台继续改 —— 单独合并出来的 GLB 是改不回去的。
 */
export async function createAssemblyFromObject(
  root: THREE.Object3D,
  name: string,
  definition: string,
  partCount: number,
): Promise<AssetMeta> {
  const [{ computeStats, createThumbnail }, { convertObject }] = await Promise.all([loadModelLib(), loadExportLib()])
  const stats = computeStats(root)
  const thumb = await createThumbnail(root)
  const baseName = (name || '装配体').replace(/[\\/:*?"<>|]/g, '_')
  const result = await convertObject(root, 'glb', baseName)
  const created = await api.upload({
    file: result.blob,
    fileName: result.fileName,
    thumb,
    meta: {
      name: name.trim() || '装配体',
      description: `由装配台合成：${partCount} 个零件的组合。装配定义保存在附属文件 assembly.json，可从详情页重新打开继续编辑。`,
      category: '装配',
      tags: ['装配'],
      format: 'glb',
      stats,
    },
    sidecars: [{ path: 'assembly.json', file: new File([definition], 'assembly.json', { type: 'application/json' }) }],
  })
  upsert(created)
  pushToast(`已把「${created.name}」存为资产（装配定义可重新打开）`, 'success')
  return created
}
