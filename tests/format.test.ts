import assert from 'node:assert/strict'
import test from 'node:test'
import { formatBytes, formatCount } from '../src/lib/format.ts'

test('体积显示：三档单位与小数位', () => {
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(999), '999 B')
  assert.equal(formatBytes(1536), '1.5 KB')
  // 超过 100 就不显示小数位，读起来更稳
  assert.equal(formatBytes(620600), '606 KB')
  assert.equal(formatBytes(5 * 1024 * 1024), '5.0 MB')
  assert.equal(formatBytes(2.5 * 1024 * 1024 * 1024), '2.5 GB')
})

test('面数显示：万级缩写，千级加千位分隔，非法值退回 0', () => {
  assert.equal(formatCount(19200), '19.2k')
  assert.equal(formatCount(3456), '3,456')
  assert.equal(formatCount(Number.NaN), '0')
})
