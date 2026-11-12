<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as THREE from 'three'
import ModelViewer from '../components/ModelViewer.vue'
import { api } from '../lib/api'
import {
  applyItem,
  ASSEMBLY_SIDECAR,
  canParent,
  captureLocalTransform,
  createItem,
  depthOf,
  dragWorldPosition,
  nextPlacement,
  parseAssembly,
  serializeAssembly,
  type AssemblyItem,
  type Vec3,
} from '../lib/assembly'
import { readError } from '../lib/download'
import { formatBytes } from '../lib/format'
import { disposeObject, loadModelFromBlob } from '../lib/model'
import { assets, createAssemblyFromObject, getAsset, initLibrary } from '../stores/library'
import { pushToast } from '../stores/toast'
import type { AssetMeta } from '../types'

const router = useRouter()
const route = useRoute()

/**
 * 装配台只做一件事：把库里的资产按「位置 / 旋转 / 缩放 + 父子层级」摆成一个组合，导出成一个新 GLB。
 * 装配屏幕上看的是实例的实时合成，导出的 GLB 就是这份合成；装配定义另存为 assembly.json，
 * 所以「摆好的东西」是可再编辑的，而不是一张烘焙过的图。
 */
const root = new THREE.Group()
root.name = 'assembly'
const stage = shallowRef<THREE.Group>(root)
/** 每个实例的 three 对象；几何与材质和 base 共享，删除实例只摘节点，不释放资源 */
const instances = new Map<string, THREE.Object3D>()
/** 每个资产只解析一次，多个实例共用同一份 base */
const bases = new Map<string, THREE.Object3D>()

const items = ref<AssemblyItem[]>([])
const selectedUid = ref<string | null>(null)
const name = ref('装配体')
const query = ref('')
const loading = ref(0)
const saving = ref(false)
const rightEdge = ref(0)
const viewer = ref<InstanceType<typeof ModelViewer> | null>(null)

const filtered = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  if (!keyword) return assets.value
  return assets.value.filter(
    (asset) =>
      asset.name.toLowerCase().includes(keyword) ||
      asset.format.toLowerCase().includes(keyword) ||
      asset.tags.some((tag) => tag.toLowerCase().includes(keyword)),
  )
})

const selected = computed(() => items.value.find((item) => item.uid === selectedUid.value) ?? null)

/** 传给 ModelViewer 的可拾取根对象；只含当前真实存在的实例 */
const dragTargets = computed(() => items.value.map((item) => instances.get(item.uid)).filter((object): object is THREE.Object3D => Boolean(object)))

const gizmoEnabled = ref(false)
const gizmoTarget = computed(() => (selectedUid.value ? instances.get(selectedUid.value) ?? null : null))
let gizmoBefore: { uid: string; before: TransformSnapshot } | null = null

const dragMode = ref(false)
const snapEnabled = ref(true)
const snapStep = ref(0.1)
const rotateSnapDeg = ref(5)
const SNAP_OPTIONS = [0.01, 0.05, 0.1, 0.25, 0.5, 1]
const ROTATE_OPTIONS = [1, 5, 15, 30]
const gizmoMode = ref<'translate' | 'rotate' | 'scale'>('translate')
const gizmoSpace = ref<'local' | 'world'>('local')
const snapLabel = computed(() =>
  gizmoEnabled.value && gizmoMode.value === 'rotate' ? `${rotateSnapDeg.value}°` : String(snapStep.value),
)

/** 一次拖拽 / 手柄操作只记一条；撤销/重做都按条目 uid 回写，不重放整份场景 */
interface TransformSnapshot {
  position: Vec3
  rotation: Vec3
  scale: number
}
interface TransformEdit {
  uid: string
  before: TransformSnapshot
  after: TransformSnapshot
}
const undoStack = ref<TransformEdit[]>([])
const redoStack = ref<TransformEdit[]>([])
const HISTORY_LIMIT = 100
const canUndo = computed(() => undoStack.value.length > 0)
const canRedo = computed(() => redoStack.value.length > 0)

let dragState: {
  item: AssemblyItem
  instance: THREE.Object3D
  startPoint: THREE.Vector3
  startWorld: THREE.Vector3
  vertical: boolean
  before: TransformSnapshot
} | null = null

function snapshotItem(item: AssemblyItem): TransformSnapshot {
  return {
    position: [item.position[0], item.position[1], item.position[2]],
    rotation: [item.rotation[0], item.rotation[1], item.rotation[2]],
    scale: item.scale,
  }
}

function sameVec3(a: Vec3, b: Vec3): boolean {
  return a[0] === b[0] && a[1] === b[1] && a[2] === b[2]
}

function sameSnapshot(a: TransformSnapshot, b: TransformSnapshot): boolean {
  return sameVec3(a.position, b.position) && sameVec3(a.rotation, b.rotation) && a.scale === b.scale
}

function recordTransformEdit(uid: string, before: TransformSnapshot, after: TransformSnapshot): void {
  if (sameSnapshot(before, after)) return
  undoStack.value.push({ uid, before, after })
  if (undoStack.value.length > HISTORY_LIMIT) undoStack.value.splice(0, undoStack.value.length - HISTORY_LIMIT)
  redoStack.value = []
}

let numericBefore: { uid: string; before: TransformSnapshot } | null = null

function beginNumericEdit(item: AssemblyItem): void {
  if (numericBefore?.uid === item.uid) return
  numericBefore = { uid: item.uid, before: snapshotItem(item) }
}

function commitNumericEdit(item: AssemblyItem): void {
  const state = numericBefore
  numericBefore = null
  if (!state || state.uid !== item.uid) return
  const current = items.value.find((entry) => entry.uid === state.uid)
  if (!current) return
  recordTransformEdit(state.uid, state.before, snapshotItem(current))
}

function applySnapshot(uid: string, snapshot: TransformSnapshot): boolean {
  const item = items.value.find((entry) => entry.uid === uid)
  const instance = item ? instances.get(item.uid) : null
  if (!item || !instance) return false
  item.position = [snapshot.position[0], snapshot.position[1], snapshot.position[2]]
  item.rotation = [snapshot.rotation[0], snapshot.rotation[1], snapshot.rotation[2]]
  item.scale = snapshot.scale
  applyItem(instance, item)
  selectedUid.value = uid
  return true
}

function undoEdit(): void {
  const edit = undoStack.value.pop()
  if (!edit) return
  if (!applySnapshot(edit.uid, edit.before)) {
    pushToast('该零件已不存在，这条撤销已跳过', 'info')
    return
  }
  redoStack.value.push(edit)
}

function redoEdit(): void {
  const edit = redoStack.value.pop()
  if (!edit) return
  if (!applySnapshot(edit.uid, edit.after)) {
    pushToast('该零件已不存在，这条重做已跳过', 'info')
    return
  }
  undoStack.value.push(edit)
}

function clearHistory(): void {
  undoStack.value = []
  redoStack.value = []
  numericBefore = null
}

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT' || target.isContentEditable
}

function onHistoryKeydown(event: KeyboardEvent): void {
  if (!(event.ctrlKey || event.metaKey) || event.altKey || isTypingTarget(event.target)) return
  const key = event.key.toLowerCase()
  if (key === 'z' && !event.shiftKey) {
    event.preventDefault()
    undoEdit()
  } else if ((key === 'z' && event.shiftKey) || key === 'y') {
    event.preventDefault()
    redoEdit()
  }
}

function itemForObject(object: THREE.Object3D): AssemblyItem | null {
  for (const [uid, instance] of instances) {
    if (instance === object) return items.value.find((item) => item.uid === uid) ?? null
  }
  return null
}

function onPick(object: THREE.Object3D): void {
  const item = itemForObject(object)
  if (item) selectedUid.value = item.uid
}

function onDragStart(object: THREE.Object3D, point: THREE.Vector3, vertical: boolean): void {
  const item = itemForObject(object)
  if (!item) return
  object.updateWorldMatrix(true, false)
  dragState = {
    item,
    instance: object,
    startPoint: point.clone(),
    startWorld: object.getWorldPosition(new THREE.Vector3()),
    vertical,
    before: snapshotItem(item),
  }
  selectedUid.value = item.uid
}

function onDrag(object: THREE.Object3D, point: THREE.Vector3, shiftKey: boolean): void {
  if (!dragState || dragState.instance !== object) return
  const delta = point.clone().sub(dragState.startPoint)
  const start: [number, number, number] = [dragState.startWorld.x, dragState.startWorld.y, dragState.startWorld.z]
  const target = dragWorldPosition(start, [delta.x, delta.y, delta.z], shiftKey, snapEnabled.value ? snapStep.value : 0, dragState.vertical)

  // 子级的条目是相对父级的局部变换，所以先把世界目标点换回父级空间。
  const parent = dragState.instance.parent
  parent?.updateWorldMatrix(true, false)
  const world = new THREE.Vector3(target[0], target[1], target[2])
  const local = parent ? parent.worldToLocal(world) : world
  dragState.item.position = [
    Math.round(local.x * 10000) / 10000,
    Math.round(local.y * 10000) / 10000,
    Math.round(local.z * 10000) / 10000,
  ]
  applyItem(dragState.instance, dragState.item)
}

function onDragEnd(object: THREE.Object3D): void {
  const state = dragState
  if (!state || state.instance !== object) {
    dragState = null
    return
  }
  dragState = null
  const item = items.value.find((entry) => entry.uid === state.item.uid)
  if (!item) return
  recordTransformEdit(item.uid, state.before, snapshotItem(item))
}

function onGizmoStart(object: THREE.Object3D): void {
  const item = itemForObject(object)
  if (!item) return
  gizmoBefore = { uid: item.uid, before: snapshotItem(item) }
  selectedUid.value = item.uid
}

function onGizmoChange(object: THREE.Object3D): void {
  const item = itemForObject(object)
  if (item) captureLocalTransform(object, item)
}

function onGizmoEnd(object: THREE.Object3D): void {
  const state = gizmoBefore
  gizmoBefore = null
  if (!state || state.uid !== itemForObject(object)?.uid) return
  const item = items.value.find((entry) => entry.uid === state.uid)
  if (!item) return
  recordTransformEdit(item.uid, state.before, snapshotItem(item))
}

async function ensureBase(asset: AssetMeta): Promise<THREE.Object3D> {
  const cached = bases.get(asset.id)
  if (cached) return cached
  const blob = await api.fileBlob(asset.id, asset.updatedAt)
  const object = await loadModelFromBlob(blob, asset.format, {
    assetId: asset.id,
    hasSidecars: asset.hasSidecars,
  })
  bases.set(asset.id, object)
  return object
}

async function addAsset(asset: AssetMeta): Promise<void> {
  loading.value += 1
  try {
    const base = await ensureBase(asset)
    const instance = base.clone(true)
    const placement = nextPlacement(instance, rightEdge.value, items.value.length > 0)
    rightEdge.value = placement.rightEdge
    const item = createItem(asset.id, asset.name, placement.position)
    applyItem(instance, item)
    root.add(instance)
    instances.set(item.uid, instance)
    items.value.push(item)
    selectedUid.value = item.uid
    viewer.value?.fitView()
  } catch (error) {
    pushToast(`加入失败：${readError(error)}`, 'error')
  } finally {
    loading.value -= 1
  }
}

function syncTransform(item: AssemblyItem): void {
  const instance = instances.get(item.uid)
  if (instance) applyItem(instance, item)
}

/**
 * 改父级时不改世界位置：用 three 的 attach 保住世界矩阵，再把新的局部变换抄回条目。
 * 之后父级移动 / 旋转 / 缩放，子级会自然跟随。
 */
function changeParent(item: AssemblyItem, value: string): void {
  const parentUid = value || null
  if (!canParent(items.value, item.uid, parentUid)) {
    pushToast('不能挂到自己的子级上', 'error')
    return
  }
  const instance = instances.get(item.uid)
  if (!instance) return
  const target = parentUid ? instances.get(parentUid) : root
  if (!target) {
    pushToast('父级零件已不存在', 'error')
    return
  }
  if (target === instance.parent) {
    item.parentUid = parentUid
    return
  }
  // 父级一变，子级的局部坐标系也变，旧的位置撤销记录不能再回放。
  clearHistory()
  target.attach(instance)
  item.parentUid = parentUid
  captureLocalTransform(instance, item)
}

function onParentChange(item: AssemblyItem, event: Event): void {
  if (event.target instanceof HTMLSelectElement) changeParent(item, event.target.value)
}

function indentFor(item: AssemblyItem): number {
  return 8 + depthOf(items.value, item.uid) * 12
}

function parentName(item: AssemblyItem): string {
  if (!item.parentUid) return '顶层'
  return items.value.find((entry) => entry.uid === item.parentUid)?.name ?? '缺失的父级'
}

function parentOptions(item: AssemblyItem): AssemblyItem[] {
  return items.value.filter((entry) => entry.uid !== item.uid)
}

function removeItem(item: AssemblyItem): void {
  const instance = instances.get(item.uid)
  undoStack.value = undoStack.value.filter((edit) => edit.uid !== item.uid)
  redoStack.value = redoStack.value.filter((edit) => edit.uid !== item.uid)
  // 子级不能跟着父级一起消失：先提回顶层并保住世界位置，再移除父级。
  for (const child of items.value.filter((entry) => entry.parentUid === item.uid)) {
    const childInstance = instances.get(child.uid)
    if (childInstance) {
      root.attach(childInstance)
      child.parentUid = null
      captureLocalTransform(childInstance, child)
    }
  }
  instance?.removeFromParent()
  instances.delete(item.uid)
  items.value = items.value.filter((entry) => entry.uid !== item.uid)
  if (selectedUid.value === item.uid) {
    selectedUid.value = null
    gizmoEnabled.value = false
  }
}

function clearAll(): void {
  for (const instance of instances.values()) instance.removeFromParent()
  instances.clear()
  items.value = []
  selectedUid.value = null
  rightEdge.value = 0
  gizmoEnabled.value = false
  gizmoMode.value = 'translate'
  gizmoSpace.value = 'local'
  gizmoBefore = null
  clearHistory()
}

async function save(): Promise<void> {
  if (items.value.length === 0) {
    pushToast('装配台还是空的，先加入几个零件', 'error')
    return
  }
  const trimmed = name.value.trim()
  if (!trimmed) {
    pushToast('先给这个装配体起个名字', 'error')
    return
  }
  saving.value = true
  try {
    const created = await createAssemblyFromObject(
      root,
      trimmed,
      serializeAssembly(items.value),
      items.value.length,
    )
    void router.push({ name: 'asset', params: { id: created.id } })
  } catch (error) {
    pushToast(`存入资产库失败：${readError(error)}`, 'error')
  } finally {
    saving.value = false
  }
}

/** 从已有装配体打开：读 assembly.json，按引用把零件重新摆一遍 */
async function openFrom(assetId: string): Promise<void> {
  await initLibrary()
  const text = await api.sidecarText(assetId, ASSEMBLY_SIDECAR)
  if (!text) {
    pushToast('这个资产没有装配定义，无法重新打开', 'error')
    return
  }
  const parsed = parseAssembly(text)
  if (parsed.length === 0) {
    pushToast('装配定义是空的或无法解析', 'error')
    return
  }
  clearHistory()
  gizmoEnabled.value = false
  gizmoMode.value = 'translate'
  gizmoSpace.value = 'local'
  gizmoBefore = null
  name.value = getAsset(assetId)?.name ?? '装配体'
  const missing: string[] = []
  for (const item of parsed) {
    const asset = getAsset(item.assetId)
    if (!asset) {
      missing.push(item.name)
      continue
    }
    try {
      const base = await ensureBase(asset)
      const instance = base.clone(true)
      applyItem(instance, item)
      root.add(instance)
      instances.set(item.uid, instance)
      items.value.push(item)
      rightEdge.value = Math.max(rightEdge.value, item.position[0] + 0.4)
    } catch {
      missing.push(item.name)
    }
  }

  // 父子关系在全部实例建好后再接：父级可能排在子级后面。
  let brokenParents = 0
  for (const item of items.value) {
    if (!item.parentUid) continue
    const childInstance = instances.get(item.uid)
    const parentInstance = instances.get(item.parentUid)
    if (childInstance && parentInstance && childInstance !== parentInstance) {
      parentInstance.add(childInstance)
    } else {
      item.parentUid = null
      brokenParents += 1
    }
  }

  viewer.value?.fitView()
  if (missing.length > 0) {
    pushToast(`${missing.length} 个零件已不在库里，已跳过：${missing.slice(0, 3).join('、')}`, 'error')
  } else if (brokenParents > 0) {
    pushToast(`有 ${brokenParents} 个零件的父级已缺失，已改为顶层`, 'info')
  } else {
    pushToast(`已读回 ${parsed.length} 个零件的装配定义`, 'success')
  }
}

function goBack(): void {
  void router.push({ name: 'library' })
}

onMounted(async () => {
  window.addEventListener('keydown', onHistoryKeydown)
  await initLibrary()
  const from = route.query.from
  if (typeof from === 'string' && from) void openFrom(from)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onHistoryKeydown)
  // 先摘实例再释放 base：实例与 base 共享几何，只有 base 该被释放
  clearAll()
  for (const base of bases.values()) disposeObject(base)
  bases.clear()
})
</script>

<template>
  <section class="assembly">
    <header class="head">
      <button class="btn btn-ghost" type="button" @click="goBack">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M10 3 5 8l5 5" />
        </svg>
        资产库
      </button>
      <div class="title">
        <p class="eyebrow mono">ASSEMBLY / 装配台</p>
        <h1>把几个资产摆成一个组合</h1>
      </div>
      <div class="head-actions">
        <span class="chip mono">{{ items.length }} 个零件</span>
        <button class="btn" type="button" :disabled="items.length === 0" @click="clearAll">清空</button>
        <button class="btn btn-primary" type="button" :disabled="saving || loading > 0 || items.length === 0" @click="save">
          {{ saving ? '导出中…' : '存为装配资产' }}
        </button>
      </div>
    </header>

    <div class="body">
      <aside class="picker">
        <label class="field-group">
          <span class="label">从资产库加入</span>
          <input v-model="query" class="field" type="search" placeholder="搜索名称或标签" aria-label="搜索要装配的资产" />
        </label>
        <p v-if="loading > 0" class="panel-note">正在解析 {{ loading }} 个模型…</p>
        <ul class="asset-list">
          <li v-for="asset in filtered" :key="asset.id">
            <div class="asset-main">
              <span class="asset-name" :title="asset.name">{{ asset.name }}</span>
              <span class="asset-meta mono">{{ asset.format.toUpperCase() }} · {{ formatBytes(asset.fileSize) }}</span>
            </div>
            <button class="btn btn-ghost btn-sm" type="button" @click="addAsset(asset)">加入</button>
          </li>
        </ul>
        <p v-if="filtered.length === 0" class="panel-note">没有匹配的资产。</p>
      </aside>

      <div class="viewport">
        <ModelViewer
          ref="viewer"
          :model="stage"
          asset-name="装配台"
          mode="realistic"
          :drag-targets="dragTargets"
          :drag-enabled="dragMode"
          :gizmo-target="gizmoTarget"
          :gizmo-enabled="gizmoEnabled"
          :gizmo-mode="gizmoMode"
          :gizmo-space="gizmoSpace"
          :gizmo-snap="snapEnabled ? snapStep : 0"
          :gizmo-rotate-snap-deg="snapEnabled ? rotateSnapDeg : 0"
          @pick="onPick"
          @drag-start="onDragStart"
          @drag="onDrag"
          @drag-end="onDragEnd"
          @gizmo-start="onGizmoStart"
          @gizmo-change="onGizmoChange"
          @gizmo-end="onGizmoEnd"
        />
        <div class="drag-tools">
          <button class="btn btn-sm" type="button" :class="{ 'is-active': dragMode }" :aria-pressed="dragMode" @click="dragMode = !dragMode">
            {{ dragMode ? '拖拽中' : '拖拽零件' }}
          </button>
          <button class="btn btn-sm" type="button" :class="{ 'is-active': snapEnabled }" :aria-pressed="snapEnabled" @click="snapEnabled = !snapEnabled">
            吸附 {{ snapLabel }}
          </button>
          <select
            v-if="snapEnabled && !(gizmoEnabled && gizmoMode === 'rotate')"
            v-model.number="snapStep"
            class="gizmo-mode"
            aria-label="吸附步长"
            title="移动 / 缩放吸附步长（场景单位）"
          >
            <option v-for="step in SNAP_OPTIONS" :key="step" :value="step">{{ step }}</option>
          </select>
          <select
            v-if="snapEnabled && gizmoEnabled && gizmoMode === 'rotate'"
            v-model.number="rotateSnapDeg"
            class="gizmo-mode"
            aria-label="旋转吸附角度"
            title="旋转吸附角度"
          >
            <option v-for="deg in ROTATE_OPTIONS" :key="deg" :value="deg">{{ deg }}°</option>
          </select>
          <button class="btn btn-sm" type="button" :class="{ 'is-active': gizmoEnabled }" :aria-pressed="gizmoEnabled" :disabled="!selected" title="选中零件的三轴手柄" @click="gizmoEnabled = !gizmoEnabled">
            手柄
          </button>
          <select v-if="gizmoEnabled" v-model="gizmoMode" class="gizmo-mode" :disabled="!selected" aria-label="手柄模式" title="手柄只作用于选中零件">
            <option value="translate">移动</option>
            <option value="rotate">旋转</option>
            <option value="scale">缩放</option>
          </select>
          <select v-if="gizmoEnabled" v-model="gizmoSpace" class="gizmo-mode" :disabled="!selected" aria-label="手柄参考轴" title="局部轴随父级旋转，世界轴固定">
            <option value="local">局部</option>
            <option value="world">世界</option>
          </select>
          <button class="btn btn-sm" type="button" :disabled="!canUndo" title="撤销上一步变换（Ctrl+Z）" @click="undoEdit">撤销</button>
          <button class="btn btn-sm" type="button" :disabled="!canRedo" title="重做上一步变换（Ctrl+Shift+Z / Ctrl+Y）" @click="redoEdit">重做</button>
          <span class="drag-tip mono">Shift 锁单轴 · Ctrl 垂直</span>
        </div>
      </div>

      <aside class="inspector">
        <section class="panel">
          <h2 class="panel-title">装配体</h2>
          <label class="field-group">
            <span class="label">名称</span>
            <input v-model="name" class="field" type="text" maxlength="64" placeholder="装配体名称" />
          </label>
          <p class="panel-note">
            存盘会导出成一个新的 GLB 资产，装配定义另存为 assembly.json，之后还能重新打开继续摆。
          </p>
        </section>

        <section class="panel">
          <h2 class="panel-title">零件（{{ items.length }}）</h2>
          <p v-if="items.length === 0" class="panel-note">左边点「加入」，第一个零件会自动落在地面上。</p>
          <ol v-else class="part-list">
            <li
              v-for="item in items"
              :key="item.uid"
              :class="{ 'is-selected': item.uid === selectedUid }"
              :style="{ paddingLeft: `${indentFor(item)}px` }"
            >
              <button class="part-main" type="button" @click="selectedUid = item.uid">
                <span class="part-name">
                  <span v-if="item.parentUid" class="branch mono">↳</span>
                  {{ item.name }}
                </span>
                <span class="part-meta mono">
                  {{ item.position[0].toFixed(2) }}, {{ item.position[1].toFixed(2) }}, {{ item.position[2].toFixed(2) }}
                </span>
              </button>
              <button class="link" type="button" @click="removeItem(item)">移除</button>
            </li>
          </ol>
        </section>

        <section v-if="selected" class="panel">
          <h2 class="panel-title">变换 · {{ selected.name }}</h2>
          <label class="field-group">
            <span class="label">父级（当前：{{ parentName(selected) }}）</span>
            <select class="field" :value="selected.parentUid ?? ''" @change="onParentChange(selected, $event)">
              <option value="">顶层（无父级）</option>
              <option
                v-for="candidate in parentOptions(selected)"
                :key="candidate.uid"
                :value="candidate.uid"
                :disabled="!canParent(items, selected.uid, candidate.uid)"
              >
                {{ candidate.name }}{{ canParent(items, selected.uid, candidate.uid) ? '' : '（子级，不可选）' }}
              </option>
            </select>
          </label>
          <div class="vec-grid">
            <label v-for="(axis, index) in ['X', 'Y', 'Z']" :key="`pos-${axis}`" class="field-group">
              <span class="label">位置 {{ axis }}</span>
              <input
                v-model.number="selected.position[index]"
                class="field field-sm mono"
                type="number"
                step="0.1"
                @focus="beginNumericEdit(selected)"
                @blur="commitNumericEdit(selected)"
                @input="syncTransform(selected)"
              />
            </label>
          </div>
          <div class="vec-grid">
            <label v-for="(axis, index) in ['X', 'Y', 'Z']" :key="`rot-${axis}`" class="field-group">
              <span class="label">旋转 {{ axis }}°</span>
              <input
                v-model.number="selected.rotation[index]"
                class="field field-sm mono"
                type="number"
                step="15"
                @focus="beginNumericEdit(selected)"
                @blur="commitNumericEdit(selected)"
                @input="syncTransform(selected)"
              />
            </label>
          </div>
          <label class="field-group">
            <span class="label">缩放</span>
            <input
              v-model.number="selected.scale"
              class="field field-sm mono"
              type="number"
              step="0.05"
              min="0.01"
              @focus="beginNumericEdit(selected)"
              @blur="commitNumericEdit(selected)"
              @input="syncTransform(selected)"
            />
          </label>
          <p class="panel-note">
            位置是场景单位（GLB 是米，OBJ / STL 是文件原始单位）；旋转填角度；缩放是统一缩放。
            挂到父级后，这里的变换是相对父级的，父级移动会带着子级一起走。
          </p>
        </section>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.assembly {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--surface);
}

.head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 7px;
  border: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
  background: var(--surface);
  color: #000000;
}

.title {
  flex: 1;
  min-width: 0;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
}

.head-actions .chip {
  border-color: #404040 #ffffff #ffffff #404040;
  background: #c0c0c0;
  color: #000000;
}

.head .btn-ghost {
  border: 2px solid #808080;
  border-color: var(--bevel-raised);
  background: var(--surface);
  color: #000000;
  text-decoration: none;
  box-shadow: 1px 1px 0 #000000;
}

.head .btn-ghost:hover {
  background: #e3e0d8;
}

.eyebrow {
  color: #333333;
  font-size: 9px;
  letter-spacing: 0;
}

h1 {
  margin-top: 1px;
  font-size: 13px;
  color: #000080;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.body {
  display: grid;
  grid-template-columns: 264px minmax(0, 1fr) 320px;
  flex: 1;
  min-height: 0;
  background: #808080;
}

.picker,
.inspector {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px;
  overflow-y: auto;
  background: var(--surface);
}

.picker {
  border-right: 2px solid #808080;
}

.inspector {
  gap: 1px;
  padding: 0;
  border-left: 2px solid #808080;
  background: var(--surface);
}

.viewport {
  position: relative;
  min-width: 0;
  min-height: 0;
  border: 2px solid;
  border-color: #808080 #ffffff #ffffff #808080;
  background: #000000;
}

.drag-tools {
  position: absolute;
  top: 6px;
  left: 6px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 3px;
  padding: 2px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: var(--surface);
  box-shadow: 1px 1px 0 #000000;
}

.drag-tools .is-active {
  border-color: var(--bevel-sunken);
  background: #b8b4ac;
  color: #000080;
  box-shadow: inset 1px 1px 0 #000000;
}

.gizmo-mode {
  height: 22px;
  padding: 0 2px;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  background: #ffffff;
  color: #000000;
  font: inherit;
  font-size: 11px;
}

.gizmo-mode:disabled {
  color: #808080;
  background: #d4d0c8;
}

.drag-tip {
  padding: 0 3px;
  color: #000000;
  font-size: 9px;
  letter-spacing: 0;
}

.field-group {
  display: block;
}

.field-group .label {
  margin-bottom: 5px;
}

.asset-list {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin: 0;
  padding: 0;
  list-style: none;
  background: #ffffff;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  overflow: hidden;
}

.asset-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  padding: 3px 4px;
  background: #ffffff;
  border-bottom: 1px dotted #c0c0c0;
}

.asset-list li:last-child {
  border-bottom: 0;
}

.asset-list li:hover {
  background: #e8e8e8;
}

.asset-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.asset-name {
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.asset-meta {
  color: #666666;
  font-size: 9px;
}

.panel {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 6px;
  background: var(--surface);
  border-bottom: 2px solid #808080;
  box-shadow: inset 0 1px 0 #ffffff;
}

.panel-title {
  padding-bottom: 2px;
  border-bottom: 1px solid #808080;
  color: #000080;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0;
  text-transform: none;
}

.panel-note {
  color: #333333;
  font-size: 10.5px;
  line-height: 1.45;
}

.part-list {
  display: flex;
  flex-direction: column;
  gap: 0;
  margin: 0;
  padding: 0;
  list-style: none;
  background: #ffffff;
  border: 2px solid;
  border-color: var(--bevel-sunken);
  border-radius: 0;
  overflow: hidden;
}

.part-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 2px;
  background: #ffffff;
  border-bottom: 1px dotted #c0c0c0;
}

.part-list li:last-child {
  border-bottom: 0;
}

.part-list li.is-selected {
  background: #000080;
  color: #ffffff;
}

.part-main {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 1px;
  min-width: 0;
  padding: 3px 4px;
  border: 0;
  background: transparent;
  color: inherit;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
}

.part-name {
  font-size: 11px;
  max-width: 190px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.branch {
  margin-right: 2px;
  color: #666666;
  font-size: 9px;
}

.part-list li.is-selected .branch,
.part-list li.is-selected .part-meta {
  color: #d4d0c8;
}

.part-meta {
  color: #555555;
  font-size: 9px;
}

.part-list .link {
  flex: none;
  padding: 0 4px;
  border: 0;
  background: transparent;
  color: #000080;
  font-family: inherit;
  font-size: 10px;
  text-decoration: underline;
  cursor: pointer;
}

.part-list li.is-selected .link {
  color: #ffffff;
}

.vec-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 3px;
}

.field-sm {
  height: 22px;
  padding: 1px 2px;
  font-size: 11px;
}

@media (max-width: 1100px) {
  .body {
    grid-template-columns: 1fr;
    grid-template-rows: 320px minmax(0, 1fr);
    overflow-y: auto;
  }

  .viewport {
    order: -1;
    border-bottom: 2px solid #808080;
  }

  .picker {
    border-right: none;
    border-top: 2px solid #808080;
  }

  .inspector {
    border-left: none;
    border-top: 2px solid #808080;
  }
}
</style>
