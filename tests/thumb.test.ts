import assert from 'node:assert/strict'
import test from 'node:test'

/**
 * thumb.ts 用 localStorage 记账，Node 里没有 window 也没有 localStorage。
 * 这里塞一个最小可用的替身，验证「到上限就停」「手动重试能清零」两条策略本身。
 */
class MemoryStorage {
  private map = new Map<string, string>()
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key) ?? null : null
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value)
  }
  removeItem(key: string): void {
    this.map.delete(key)
  }
  clear(): void {
    this.map.clear()
  }
}

Object.defineProperty(globalThis, 'window', {
  value: { localStorage: new MemoryStorage() },
  configurable: true,
})

const { MAX_THUMB_ATTEMPTS, canAttemptThumb, clearThumbAttempts, noteThumbAttempt, thumbAttempts } = await import(
  '../src/lib/thumb.ts'
)

test('失败累计到上限后不再自动重试', () => {
  const id = 'asset-cap'
  assert.equal(thumbAttempts(id), 0)
  assert.equal(canAttemptThumb(id), true)
  for (let i = 1; i <= MAX_THUMB_ATTEMPTS; i += 1) {
    assert.equal(noteThumbAttempt(id), i)
    assert.equal(thumbAttempts(id), i)
  }
  assert.equal(canAttemptThumb(id), false, '第三次失败后应该停手，等用户手动重试')
  assert.equal(noteThumbAttempt(id), MAX_THUMB_ATTEMPTS + 1, '手动重试仍然要能记账')
  assert.equal(canAttemptThumb(id), false)
})

test('清空计数后回到可重试状态', () => {
  const id = 'asset-reset'
  noteThumbAttempt(id)
  noteThumbAttempt(id)
  noteThumbAttempt(id)
  assert.equal(canAttemptThumb(id), false)
  clearThumbAttempts(id)
  assert.equal(thumbAttempts(id), 0)
  assert.equal(canAttemptThumb(id), true)
})

test('localStorage 内容坏掉时按 0 次处理，不抛错', () => {
  const id = 'asset-corrupt'
  globalThis.window.localStorage.setItem('assetstudio.thumbAttempts', 'not-json')
  assert.equal(thumbAttempts(id), 0)
  assert.equal(canAttemptThumb(id), true)
})

test('数组或非法计数值不会被当成有效次数', () => {
  const id = 'asset-bad-values'
  globalThis.window.localStorage.setItem(
    'assetstudio.thumbAttempts',
    JSON.stringify({ [id]: -2, other: 'x', good: 2 }),
  )
  assert.equal(thumbAttempts(id), 0)
  assert.equal(thumbAttempts('other'), 0)
  assert.equal(thumbAttempts('good'), 2)
})
