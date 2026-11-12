// 对话框里的焦点工具：Esc 关闭、Tab 在框内绕回、打开时把焦点放进第一个可操作元素。
// 只在渲染层用，不依赖 three，所以可以单独被任何弹层复用。

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

export function focusableIn(root: HTMLElement | null): HTMLElement[] {
  if (!root) return []
  // 不能用 offsetParent 判可见：弹层是 position:fixed，fixed 元素的 offsetParent 为 null，
  // 会把所有候选都过滤掉（之前就是这样，打开弹层后焦点仍留在背景按钮上）。
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => el.getClientRects().length > 0,
  )
}

/** 打开弹层后把焦点放到第一个可操作元素，避免键盘用户落在背景页面上。 */
export function focusFirst(root: HTMLElement | null): void {
  const first = focusableIn(root)[0]
  first?.focus()
}

/** Tab / Shift+Tab 到边界时在弹层内部绕回。 */
export function trapTab(event: KeyboardEvent, root: HTMLElement | null): void {
  const items = focusableIn(root)
  if (!items.length) return
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement as HTMLElement | null
  if (event.shiftKey) {
    if (active === first || !root?.contains(active)) {
      event.preventDefault()
      last.focus()
    }
    return
  }
  if (active === last || !root?.contains(active)) {
    event.preventDefault()
    first.focus()
  }
}
