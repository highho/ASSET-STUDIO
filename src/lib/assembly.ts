import * as THREE from 'three'

/**
 * 装配体定义：只记「用了哪些资产、各自摆在哪」，不复制几何。
 * 它作为附属文件 `assembly.json` 跟导出的 GLB 一起入库，
 * 以后可以从详情页重新打开装配台继续改 —— 这是「装配」与「一次性合并」的区别。
 */

export const ASSEMBLY_SIDECAR = 'assembly.json'

export type Vec3 = [number, number, number]

export interface AssemblyItem {
  /** 页面内唯一，只用于列表选中与删除；不写进文件也没关系，但写进去更好排查 */
  uid: string
  assetId: string
  name: string
  /** 父级条目的 uid；null 表示顶层。子级的变换是相对父级的 */
  parentUid: string | null
  /** 场景单位。GLB 是米，OBJ / STL 是文件自己的原始单位 —— 与检视台的标注保持一致 */
  position: Vec3
  /** 欧拉角，单位是度（界面上填的就是度） */
  rotation: Vec3
  /** 统一缩放；单轴缩放会破坏大多数模型的装配关系，MVP 不给 */
  scale: number
}

export interface AssemblyDocItem {
  assetId: string
  name: string
  position: Vec3
  rotation: Vec3
  scale: number
  /** 父条目在 items 数组里的下标；缺省 / -1 表示顶层 */
  parent?: number
}

export interface AssemblyDoc {
  schema: 1 | 2
  items: AssemblyDocItem[]
  savedAt?: number
}

let counter = 0

export function nextUid(): string {
  counter += 1
  return `i${Date.now().toString(36)}${counter}`
}

export function createItem(assetId: string, name: string, position: Vec3 = [0, 0, 0]): AssemblyItem {
  return { uid: nextUid(), assetId, name, parentUid: null, position, rotation: [0, 0, 0], scale: 1 }
}

/** 把定义里的变换套到 three 对象上：位置用场景单位，旋转从度转弧度 */
export function applyItem(object: THREE.Object3D, item: AssemblyItem): void {
  object.position.set(item.position[0], item.position[1], item.position[2])
  object.rotation.set(
    THREE.MathUtils.degToRad(item.rotation[0]),
    THREE.MathUtils.degToRad(item.rotation[1]),
    THREE.MathUtils.degToRad(item.rotation[2]),
  )
  object.scale.setScalar(item.scale > 0 ? item.scale : 1)
}

export function serializeAssembly(items: AssemblyItem[], savedAt = Date.now()): string {
  const indexByUid = new Map(items.map((item, index) => [item.uid, index]))
  const doc: AssemblyDoc = {
    schema: 2,
    savedAt,
    items: items.map((item) => {
      const parent = item.parentUid === null ? -1 : indexByUid.get(item.parentUid) ?? -1
      return {
        assetId: item.assetId,
        name: item.name,
        position: item.position,
        rotation: item.rotation,
        scale: item.scale,
        ...(parent >= 0 ? { parent } : {}),
      }
    }),
  }
  return `${JSON.stringify(doc, null, 2)}\n`
}

function readVec3(raw: unknown, fallback: Vec3): Vec3 {
  if (!Array.isArray(raw) || raw.length < 3) return fallback
  const values = raw.slice(0, 3).map((value) => (typeof value === 'number' && Number.isFinite(value) ? value : null))
  if (values.some((value) => value === null)) return fallback
  return values as Vec3
}

/**
 * 读装配定义。宽容解析：字段缺失按默认值走、坏条目跳过 ——
 * 手改过的 JSON 不该让整个装配打不开，但也不该把 NaN 灌进 three。
 * `parent` 是 items 下标；越界、自指、成环的父级都会被降级成顶层。
 */
export function parseAssembly(text: string): AssemblyItem[] {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    return []
  }
  const doc = raw as Partial<AssemblyDoc> | null
  if (!doc || !Array.isArray(doc.items)) return []
  const items: AssemblyItem[] = []
  const parents: Array<number> = []
  /** 原文件下标 → 过滤后的下标：坏条目被跳过时，parent 不能跟着错位 */
  const originalToItem = new Map<number, number>()
  for (let sourceIndex = 0; sourceIndex < doc.items.length; sourceIndex += 1) {
    const entry = doc.items[sourceIndex]
    const source = entry as Partial<AssemblyDocItem>
    if (!source || typeof source.assetId !== 'string' || !source.assetId) continue
    const scale = typeof source.scale === 'number' && Number.isFinite(source.scale) && source.scale > 0 ? source.scale : 1
    originalToItem.set(sourceIndex, items.length)
    items.push({
      uid: nextUid(),
      assetId: source.assetId,
      name: typeof source.name === 'string' && source.name ? source.name : source.assetId,
      parentUid: null,
      position: readVec3(source.position, [0, 0, 0]),
      rotation: readVec3(source.rotation, [0, 0, 0]),
      scale,
    })
    parents.push(
      typeof source.parent === 'number' && Number.isInteger(source.parent) && source.parent >= 0
        ? source.parent
        : -1,
    )
  }

  // 先按下标找父级，再排除成环：A→B→A 这种手改坏的定义会让场景图挂死。
  for (let index = 0; index < items.length; index += 1) {
    const parentIndex = originalToItem.get(parents[index]) ?? -1
    if (parentIndex < 0 || parentIndex >= items.length || parentIndex === index) continue
    items[index].parentUid = items[parentIndex].uid
  }
  // 排除成环：A→B→A 这种手改坏的定义会让场景图挂死；环上的条目统一降级成顶层。
  for (const item of items) {
    const path: string[] = []
    let cursor: string | null = item.uid
    while (cursor) {
      const cycleStart = path.indexOf(cursor)
      if (cycleStart >= 0) {
        for (const uid of path.slice(cycleStart)) {
          const hit = items.find((entry) => entry.uid === uid)
          if (hit) hit.parentUid = null
        }
        break
      }
      path.push(cursor)
      cursor = items.find((entry) => entry.uid === cursor)?.parentUid ?? null
    }
  }
  return items
}

/** 子级不能挂到自己的后代上；self 也不行。 */
export function canParent(items: AssemblyItem[], childUid: string, parentUid: string | null): boolean {
  if (!parentUid) return true
  if (childUid === parentUid) return false
  let cursor: string | null = parentUid
  const seen = new Set<string>()
  while (cursor) {
    if (cursor === childUid) return false
    if (seen.has(cursor)) return false
    seen.add(cursor)
    cursor = items.find((item) => item.uid === cursor)?.parentUid ?? null
  }
  return true
}

/** 零件列表缩进用；坏掉的定义最多走 items.length 层，避免死循环。 */
export function depthOf(items: AssemblyItem[], uid: string): number {
  let depth = 0
  let cursor: string | null = uid
  const seen = new Set<string>()
  while (cursor && depth <= items.length) {
    if (seen.has(cursor)) return 0
    seen.add(cursor)
    const item = items.find((entry) => entry.uid === cursor)
    if (!item || !item.parentUid) break
    cursor = item.parentUid
    depth += 1
  }
  return depth
}

/** 把 three 对象当前的局部变换抄回条目；切换父级后用它保住世界位置。 */
export function captureLocalTransform(object: THREE.Object3D, item: AssemblyItem): void {
  item.position = [roundPlacement(object.position.x), roundPlacement(object.position.y), roundPlacement(object.position.z)]
  const euler = new THREE.Euler().setFromQuaternion(object.quaternion, 'XYZ')
  item.rotation = [
    roundPlacement(THREE.MathUtils.radToDeg(euler.x)),
    roundPlacement(THREE.MathUtils.radToDeg(euler.y)),
    roundPlacement(THREE.MathUtils.radToDeg(euler.z)),
  ]
  // 父级带非均匀缩放时局部矩阵会分解出非均匀缩放；条目只存统一缩放，取平均。
  item.scale = roundPlacement((object.scale.x + object.scale.y + object.scale.z) / 3)
  if (!(item.scale > 0)) item.scale = 1
}

/** 场景单位下 4 位小数足够；原始浮点会写出 -1.1e-16 这种没法读的数字 */
function roundPlacement(value: number): number {
  return Math.round(value * 10000) / 10000
}

/** 把世界坐标吸附到给定步长；step <= 0 表示不吸附。 */
export function snapToStep(value: number, step: number): number {
  if (!(step > 0)) return roundPlacement(value)
  return roundPlacement(Math.round(value / step) * step)
}

/**
 * 视口拖拽的世界目标点：默认只动 X/Z，Y 保持按下时的高度；
 * vertical = true 时反过来只动 Y，X/Z 保持。Shift 只锁水平轴。
 * 吸附只作用在可移动的轴上。
 */
export function dragWorldPosition(
  start: Vec3,
  delta: Vec3,
  shiftKey: boolean,
  snapStep: number,
  vertical = false,
): Vec3 {
  if (vertical) return [start[0], snapToStep(start[1] + delta[1], snapStep), start[2]]
  let x = start[0] + delta[0]
  let y = start[1]
  let z = start[2] + delta[2]
  const lockZ = shiftKey && Math.abs(delta[0]) >= Math.abs(delta[2])
  const lockX = shiftKey && Math.abs(delta[2]) > Math.abs(delta[0])
  if (lockZ) z = start[2]
  if (lockX) x = start[0]
  return [snapToStep(x, lockX ? 0 : snapStep), y, snapToStep(z, lockZ ? 0 : snapStep)]
}

/**
 * 新加入的实例排在已有实例右边、底面落在地面上，避免一加入就互相埋进去。
 * rightEdge 是当前已占用的最右端（场景单位），返回新的最右端供下一次使用。
 */
export function nextPlacement(
  object: THREE.Object3D,
  rightEdge: number,
  hasItems: boolean,
): { position: Vec3; rightEdge: number } {
  const box = new THREE.Box3().setFromObject(object)
  if (box.isEmpty()) return { position: [rightEdge + 1, 0, 0], rightEdge: rightEdge + 2 }
  const gap = hasItems ? 0.4 : 0
  const left = rightEdge + gap
  const x = left - box.min.x
  const y = -box.min.y
  const z = -((box.min.z + box.max.z) / 2)
  return {
    position: [roundPlacement(x), roundPlacement(y), roundPlacement(z)],
    rightEdge: roundPlacement(x + box.max.x),
  }
}
