import assert from 'node:assert/strict'
import test from 'node:test'
import * as THREE from 'three'
import { OBJLoader } from 'three/examples/jsm/loaders/OBJLoader.js'
import { convertObject } from '../src/lib/export.ts'
import { withOriginalPlacement } from '../src/lib/placement.ts'

function near(actual: number, expected: number): void {
  assert.ok(Math.abs(actual - expected) < 1e-4, `${actual} 应接近 ${expected}`)
}

/** 造一个「摆正过」的对象：本体在 (1.5, 2, -0.75)，预览时又被平移了一圈 */
function positionedBox(): THREE.Object3D {
  const mesh = new THREE.Mesh(new THREE.BoxGeometry(1, 2, 3))
  mesh.position.set(1.5, 2, -0.75)
  const root = new THREE.Group()
  root.add(mesh)
  root.updateMatrixWorld(true)
  // 与 loadModelFromBlob 的摆正一致：居中到 XZ 原点、底面贴 y=0
  const box = new THREE.Box3().setFromObject(root)
  const center = box.getCenter(new THREE.Vector3())
  const offset = { x: -center.x, y: -box.min.y, z: -center.z }
  root.userData.__previewOffset = offset
  root.position.set(offset.x, offset.y, offset.z)
  root.updateMatrixWorld(true)
  return root
}

test('导出前抵消预览平移：转出来的 OBJ 与源文件位置一致', async () => {
  const root = positionedBox()
  const converted = await withOriginalPlacement(root, () => convertObject(root, 'obj', 'box'))
  const parsed = new OBJLoader().parse(await converted.blob.text())
  const box = new THREE.Box3().setFromObject(parsed)

  // 原始网格是 1×2×3 的盒子，中心在 (1.5, 2, -0.75)
  near(box.min.x, 1.0)
  near(box.max.x, 2.0)
  near(box.min.y, 1.0)
  near(box.max.y, 3.0)
  near(box.min.z, -2.25)
  near(box.max.z, 0.75)
})

test('导出后预览平移被还原：对象位置没有被子孙导出改坏', async () => {
  const root = positionedBox()
  const before = root.position.clone()
  await withOriginalPlacement(root, () => convertObject(root, 'obj', 'box'))
  near(root.position.x, before.x)
  near(root.position.y, before.y)
  near(root.position.z, before.z)

  // 还原之后再做一次预览用途的包围盒，仍是居中落地的
  const box = new THREE.Box3().setFromObject(root)
  near(box.min.y, 0)
})

test('没有预览偏移的对象（代码生成的模型）原样导出', async () => {
  const mesh = new THREE.Mesh(new THREE.BoxGeometry(1, 2, 3))
  mesh.position.set(0.5, 0.25, -0.5)
  const scene = new THREE.Scene()
  scene.add(mesh)
  scene.updateMatrixWorld(true)

  const converted = await withOriginalPlacement(scene, () => convertObject(scene, 'obj', 'raw'))
  const parsed = new OBJLoader().parse(await converted.blob.text())
  const box = new THREE.Box3().setFromObject(parsed)
  near(box.min.x, 0)
  near(box.max.y, 1.25)
})
