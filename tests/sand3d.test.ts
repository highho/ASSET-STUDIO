import assert from 'node:assert/strict'
import test from 'node:test'
import * as THREE from 'three'
import { modelToSand3d, worldBoxToSand3d } from '../src/lib/sand3d.ts'

/** 0.1 米乘 1000 在浮点上是 100.00000000000001，尺寸比较给一点容差 */
function near(actual: number, expected: number): void {
  assert.ok(Math.abs(actual - expected) < 1e-6, `${actual} 应接近 ${expected}`)
}

test('世界包围盒换轴：three 的 X/Y/Z 变成 sand3d 的 X/Z/Y', () => {
  // 一块 0.1 × 0.2 × 0.3 米（X 宽、Y 高、Z 前后）的盒子，从原点往正方向长
  const box = worldBoxToSand3d({ x: 0, y: 0, z: 0 }, { x: 0.1, y: 0.2, z: 0.3 })
  near(box.width, 100) // 宽取 three 的 X
  near(box.depth, 300) // 进深取 three 的 Z
  near(box.height, 200) // 高度取 three 的 Y
  near(box.x, 50) // three 的 X 中心
  near(box.y, 150) // sand3d 的 Y 是 three 的 Z 中心
  near(box.z, 0) // sand3d 的 Z 取底面，也就是 three 的 Y 最小处
})

test('平移过的盒子：底面贴 three 的最小 Y，中心跟着走', () => {
  const box = worldBoxToSand3d({ x: 0.5, y: 0.1, z: -0.2 }, { x: 0.6, y: 0.3, z: 0.1 })
  near(box.width, 100)
  near(box.depth, 300)
  near(box.height, 200)
  near(box.x, 550)
  near(box.y, -50)
  near(box.z, 100)
})

test('由模型推代码：100×200×300 的盒子生成的参数顺序是 宽 / 进深 / 高', () => {
  const mesh = new THREE.Mesh(new THREE.BoxGeometry(0.1, 0.2, 0.3))
  mesh.name = 'part'
  const scene = new THREE.Scene()
  scene.add(mesh)
  scene.updateMatrixWorld(true)

  const { code, parts } = modelToSand3d(scene, '盒子')
  assert.equal(parts, 1)
  assert.match(code, /s3d\.box\("part", 100\.0, 300\.0, 200\.0\)/)
  // BoxGeometry 以原点为中心，所以底面在 -0.1 米处 → sand3d 的 -100
  assert.match(code, /s3d\.at\(part_1_part, 0\.0, 0\.0, -100\.0\)/)
})

test('绕 X 转 90° 之后，高与进深互换，X 方向不受影响', () => {
  const mesh = new THREE.Mesh(new THREE.BoxGeometry(0.1, 0.2, 0.3))
  mesh.rotation.x = Math.PI / 2
  mesh.name = 'tilted'
  const scene = new THREE.Scene()
  scene.add(mesh)
  scene.updateMatrixWorld(true)

  const { code } = modelToSand3d(scene, '倒盒子')
  assert.match(code, /s3d\.box\("tilted", 100\.0, 200\.0, 300\.0\)/)
})

test('单个高面数网格：反推要自带警告，不能让人以为盒子就是形状', () => {
  // 球面 64×32 段约 4000 个三角面，和「黄铜环形结」那类单网格资产同构
  const mesh = new THREE.Mesh(new THREE.SphereGeometry(0.2, 64, 32))
  mesh.name = 'knot'
  const scene = new THREE.Scene()
  scene.add(mesh)
  scene.updateMatrixWorld(true)

  const { parts, warning, code } = modelToSand3d(scene, '环形结')
  assert.equal(parts, 1)
  assert.ok(warning && warning.includes('形状'), '应当有警告文案')
  assert.match(code, /^# 注意：/m, '警告要写进生成的代码里')
})

test('多零件模型不给误报（零件多于一个就没有这条警告）', () => {
  const scene = new THREE.Scene()
  for (const [index, size] of [0.1, 0.3].entries()) {
    const mesh = new THREE.Mesh(new THREE.BoxGeometry(size, size, size))
    mesh.position.x = index * 0.5
    mesh.name = `part${index}`
    scene.add(mesh)
  }
  scene.updateMatrixWorld(true)

  const { parts, warning } = modelToSand3d(scene, '两个零件')
  assert.equal(parts, 2)
  assert.equal(warning, undefined)
})
