/**
 * 浏览器本地的界面偏好。
 *
 * 这些偏好只影响当前浏览器，不属于服务端资产数据；换浏览器或清缓存会回到默认值。
 * 读写都做容错，隐私模式下 localStorage 被禁用时退化成默认行为，不阻塞页面。
 */

export type LibraryViewMode = 'grid' | 'list'

const VIEW_KEY = 'assetstudio.defaultView'
const THUMB_AUTO_KEY = 'assetstudio.thumbAuto'

export function defaultViewMode(): LibraryViewMode {
  try {
    return window.localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'grid'
  } catch {
    return 'grid'
  }
}

export function setDefaultViewMode(value: LibraryViewMode): void {
  try {
    window.localStorage.setItem(VIEW_KEY, value)
  } catch {
    /* 隐私模式下不生效 */
  }
}

export function thumbAutoEnabled(): boolean {
  try {
    return window.localStorage.getItem(THUMB_AUTO_KEY) !== 'off'
  } catch {
    return true
  }
}

export function setThumbAutoEnabled(value: boolean): void {
  try {
    window.localStorage.setItem(THUMB_AUTO_KEY, value ? 'on' : 'off')
  } catch {
    /* 同上 */
  }
}
