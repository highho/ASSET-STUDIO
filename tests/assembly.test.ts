import assert from 'node:assert/strict'
import test from 'node:test'
import * as THREE from 'three'
import {
  applyItem,
  canParent,
  captureLocalTransform,
  createItem,
  depthOf,
  dragWorldPosition,
  nextPlacement,
  parseAssembly,
  serializeAssembly,
  snapToStep,
} from '../src/lib/assembly.ts'

function near(actual: number, expected: number): void {
  assert.ok(Math.abs(actual - expected) < 1e-6, `${actual} 应接近 ${expected}`)
}

test('装配定义往返：位置、旋转、缩放与引用都原样保留', () => {
  const items = [
    createItem('a-1', '支架', [1, 2, 3]),
    createItem('a-2', '转子', [-0.5, 0, 0.25]),
  ]
  items[0].rotation = [0, 45, 0]
  items[1].scale = 2
  const restored = parseAssembly(serializeAssembly(items, 123))
  assert.equal(restored.length, 2)
  assert.deepEqual(restored[0].position, [1, 2, 3])
  assert.deepEqual(restored[0].rotation, [0, 45, 0])
  assert.equal(restored[1].assetId, 'a-2')
  assert.equal(restored[1].scale, 2)
  // uid 不复用：文件被重开两次，两次的条目必须是各自独立的列表项
  assert.notEqual(restored[0].uid, restored[1].uid)
  assert.notEqual(restored[0].uid, items[0].uid)
})

test('坏定义不炸：手改坏的 JSON / 缺字段 / 负缩放都按安全值处理', () => {
  assert.deepEqual(parseAssembly('{'), [])
  assert.deepEqual(parseAssembly('{"items": "nope"}'), [])
  // 缺 assetId 的条目跳过；坏的 scale、坏向量回落到默认
  const restored = parseAssembly(
    JSON.stringify({
      schema: 1,
      items: [{ name: '没有引用' }, { assetId: 'a-9', position: [1, 'x', 3], scale: -2 }],
    }),
  )
  assert.equal(restored.length, 1)
  assert.deepEqual(restored[0].position, [0, 0, 0])
  assert.equal(restored[0].scale, 1)
  assert.equal(restored[0].name, 'a-9')
})

test('applyItem 把角度换成弧度，正的缩放才生效', () => {
  const object = new THREE.Object3D()
  const item = createItem('a-1', '零件')
  item.position = [1, 2, 3]
  item.rotation = [0, 90, 0]
  item.scale = 1.5
  applyItem(object, item)
  near(object.position.x, 1)
  near(object.rotation.y, Math.PI / 2)
  near(object.scale.x, 1.5)
  item.scale = 0
  applyItem(object, item)
  near(object.scale.x, 1)
})

test('父子层级往返：父级按条目下标写盘，读回后指向正确的 uid', () => {
  const parent = createItem('a-1', '底座', [0, 0, 0])
  const child = createItem('a-2', '立柱', [0, 1, 0])
  child.parentUid = parent.uid
  const restored = parseAssembly(serializeAssembly([parent, child], 456))
  assert.equal(restored.length, 2)
  assert.equal(restored[0].parentUid, null)
  assert.equal(restored[1].parentUid, restored[0].uid)
  assert.equal(depthOf(restored, restored[1].uid), 1)
})

test('坏条目被跳过后，parent 下标仍按原文件位置对齐', () => {
  const restored = parseAssembly(
    JSON.stringify({
      schema: 2,
      items: [
        { name: '没有引用' },
        { assetId: 'a-1' },
        { assetId: 'a-2', parent: 1 },
      ],
    }),
  )
  assert.equal(restored.length, 2)
  assert.equal(restored[0].assetId, 'a-1')
  assert.equal(restored[1].parentUid, restored[0].uid)
})


test('坏父级不炸：越界、自指、成环都降级成顶层', () => {
  const restored = parseAssembly(
    JSON.stringify({
      schema: 2,
      items: [
        { assetId: 'a-1', parent: 1 },
        { assetId: 'a-2', parent: 0 },
        { assetId: 'a-3', parent: 2 },
        { assetId: 'a-4', parent: 99 },
      ],
    }),
  )
  // 0 与 1 成环，环上两条都回顶层；2 自指、3 越界也回顶层
  assert.equal(restored[0].parentUid, null)
  assert.equal(restored[1].parentUid, null)
  assert.equal(restored[2].parentUid, null)
  assert.equal(restored[3].parentUid, null)
  assert.equal(depthOf(restored, restored[0].uid), 0)
})

test('canParent 拦住自己与后代，depthOf 给出缩进层级', () => {
  const rootItem = createItem('a-1', '底座')
  const child = createItem('a-2', '立柱')
  const grandchild = createItem('a-3', '顶灯')
  child.parentUid = rootItem.uid
  grandchild.parentUid = child.uid
  const items = [rootItem, child, grandchild]
  assert.equal(canParent(items, rootItem.uid, child.uid), false)
  assert.equal(canParent(items, child.uid, grandchild.uid), false)
  assert.equal(canParent(items, child.uid, null), true)
  assert.equal(canParent(items, rootItem.uid, grandchild.uid), false)
  assert.equal(depthOf(items, grandchild.uid), 2)
})

test('captureLocalTransform 把 attach 后的局部矩阵写回条目', () => {
  const pivot = new THREE.Group()
  pivot.position.set(10, 0, 0)
  pivot.rotation.y = Math.PI / 2
  const child = new THREE.Mesh(new THREE.BoxGeometry(1, 1, 1))
  child.position.set(2, 0, 0)
  pivot.add(child)
  const item = createItem('a-1', '零件')
  captureLocalTransform(child, item)
  near(item.position[0], 2)
  near(item.rotation[1], 0)
  near(item.scale, 1)
})

test('视口拖拽：网格吸附 / Shift 锁水平轴 / Ctrl 垂直只改 Y', () => {
  const start: [number, number, number] = [1.71, 0.5, 0]
  const snapped = dragWorldPosition(start, [0.333, 0, -0.347], false, 0.1)
  assert.deepEqual(snapped, [2, 0.5, -0.3])
  // 横向位移更大：锁住 Z，只让 X 吸附
  const lockZ = dragWorldPosition(start, [0.5, 0, -0.2], true, 0.1)
  assert.deepEqual(lockZ, [2.2, 0.5, 0])
  // 纵向位移更大：锁住 X，只让 Z 吸附
  const lockX = dragWorldPosition(start, [0.2, 0, -0.5], true, 0.1)
  assert.deepEqual(lockX, [1.71, 0.5, -0.5])
  // Ctrl 垂直拖拽：X/Z 不动，Y 吸附到 0.1
  const vertical = dragWorldPosition(start, [0.4, 0.333, -0.2], false, 0.1, true)
  assert.deepEqual(vertical, [1.71, 0.8, 0])
  // 关掉吸附时保留 4 位小数，不返回浮点尾巴
  assert.equal(snapToStep(1.234567, 0), 1.2346)
})

test('nextPlacement 让新零件排在右边、底面落在地面上', () => {
  const first = new THREE.Mesh(new THREE.BoxGeometry(2, 1, 2))
  first.position.y = 0.5
  const firstPlacement = nextPlacement(first, 0, false)
  // 第一个零件左边缘落在 0，底面贴 y=0，前后居中到 0
  near(firstPlacement.position[0], 1)
  near(firstPlacement.position[1], -0.5 + 0.5)
  near(firstPlacement.position[2], 0)
  near(firstPlacement.rightEdge, 2)

  const second = new THREE.Mesh(new THREE.BoxGeometry(1, 1, 1))
  const secondPlacement = nextPlacement(second, firstPlacement.rightEdge, true)
  // 左边缘 = 已有右边界 + 0.4 的间隙
  near(secondPlacement.position[0], 2.9)
  near(secondPlacement.rightEdge, 3.4)
})
