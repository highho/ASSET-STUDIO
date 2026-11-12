/**
 * 缩略图补齐的失败次数记账。
 *
 * 缩略图是浏览器离屏渲染出来的，WebGL 不可用、显存吃紧、文件损坏都会失败。
 * 以前失败后资产仍被当作「缺图」，每次进资产库都重试一遍，用户看到的是永远转不完的补齐条；
 * 这里按资产 id 记次数（存 localStorage），到上限就停手，让卡片明确显示「预览失败」并提供手动重试。
 */

export const MAX_THUMB_ATTEMPTS = 3
const STORAGE_KEY = 'assetstudio.thumbAttempts'

type AttemptMap = Record<string, number>

function readMap(): AttemptMap {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const map: AttemptMap = {}
    for (const [id, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof value === 'number' && Number.isFinite(value) && value > 0) map[id] = Math.floor(value)
    }
    return map
  } catch {
    // 隐私模式禁用 localStorage 时退化成「本次会话不记账」，不影响主流程
    return {}
  }
}

function writeMap(map: AttemptMap): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(map))
  } catch {
    /* 同上 */
  }
}

export function thumbAttempts(id: string): number {
  return readMap()[id] ?? 0
}

export function canAttemptThumb(id: string): boolean {
  return thumbAttempts(id) < MAX_THUMB_ATTEMPTS
}

export function noteThumbAttempt(id: string): number {
  const map = readMap()
  const next = (map[id] ?? 0) + 1
  map[id] = next
  writeMap(map)
  return next
}

export function clearThumbAttempts(id: string): void {
  const map = readMap()
  if (!(id in map)) return
  delete map[id]
  writeMap(map)
}

/** 清空本浏览器所有资产的失败计数；卡片仍可单独手动重试。 */
export function clearAllThumbAttempts(): void {
  writeMap({})
}
