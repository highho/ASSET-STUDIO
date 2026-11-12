<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { TransformControls } from 'three/examples/jsm/controls/TransformControls.js'
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js'
import { downloadDataUrl } from '../lib/download'
import { fitCameraToObject } from '../lib/model'

const props = defineProps<{
  model: THREE.Object3D | null
  assetName?: string
  /** model = 只留几何（素模）；realistic = 保留贴图材质并投影到地面 */
  mode?: 'model' | 'realistic'
  /** 装配台用：这些根对象可以点选 / 拖动；不传则保持纯检视器行为 */
  dragTargets?: THREE.Object3D[]
  /** 打开后左键按在零件上拖动；关闭时左键仍然交给 OrbitControls 旋转 */
  dragEnabled?: boolean
  /** 装配台用：当前选中实例的三轴移动手柄 */
  gizmoTarget?: THREE.Object3D | null
  gizmoEnabled?: boolean
  /** 手柄模式：平移 / 旋转 / 缩放，默认平移 */
  gizmoMode?: 'translate' | 'rotate' | 'scale'
  /** 手柄参考轴：local 随父级旋转，world 固定世界轴 */
  gizmoSpace?: 'local' | 'world'
  /** 手柄的平移 / 缩放吸附步长；<= 0 或省略表示不吸附 */
  gizmoSnap?: number
  /** 手柄旋转吸附角度（度）；<= 0 或省略表示不吸附 */
  gizmoRotateSnapDeg?: number
}>()

const emit = defineEmits<{
  (event: 'pick', object: THREE.Object3D): void
  (event: 'drag-start', object: THREE.Object3D, point: THREE.Vector3, vertical: boolean): void
  (event: 'drag', object: THREE.Object3D, point: THREE.Vector3, shiftKey: boolean): void
  (event: 'drag-end', object: THREE.Object3D): void
  (event: 'gizmo-start', object: THREE.Object3D): void
  (event: 'gizmo-change', object: THREE.Object3D): void
  (event: 'gizmo-end', object: THREE.Object3D): void
}>()

const viewport = ref<HTMLDivElement | null>(null)
const hud = ref<HTMLDivElement | null>(null)
const autoRotate = ref(false)
const wireframe = ref(false)
const showGrid = ref(true)
const errorMessage = ref('')

const holder = shallowRef<THREE.Group | null>(null)

let renderer: THREE.WebGLRenderer | null = null
let scene: THREE.Scene | null = null
let camera: THREE.PerspectiveCamera | null = null
let controls: OrbitControls | null = null
let transformControls: TransformControls | null = null
let grid: THREE.GridHelper | null = null
let envTexture: THREE.Texture | null = null
let ground: THREE.Mesh | null = null
let keyLight: THREE.DirectionalLight | null = null
let clayMaterial: THREE.MeshStandardMaterial | null = null
const materialStore = new Map<THREE.Mesh, THREE.Material | THREE.Material[]>()
let frameId = 0
let lastHudAt = 0
let resizeObserver: ResizeObserver | null = null

function applyWireframe(): void {
  holder.value?.traverse((child) => {
    const mesh = child as THREE.Mesh
    if (!mesh.isMesh) return
    const materials = Array.isArray(mesh.material) ? mesh.material : [mesh.material]
    materials.forEach((material) => {
      const candidate = material as THREE.MeshBasicMaterial
      if (candidate && 'wireframe' in candidate) candidate.wireframe = wireframe.value
    })
  })
}

function applyGrid(): void {
  if (grid) grid.visible = showGrid.value
}

function getClayMaterial(): THREE.MeshStandardMaterial {
  if (!clayMaterial) {
    clayMaterial = new THREE.MeshStandardMaterial({ color: 0xb9c0cc, roughness: 0.58, metalness: 0.06 })
  }
  return clayMaterial
}

/** 模型模式把材质换成统一的素模，关掉地面与阴影；实景模式恢复原材质与投影 */
function applyMode(): void {
  const realistic = (props.mode ?? 'realistic') === 'realistic'
  if (scene) {
    if (scene.background instanceof THREE.Color) scene.background.set(realistic ? 0x101217 : 0x161a21)
    ;(scene as unknown as { environmentIntensity?: number }).environmentIntensity = realistic ? 1 : 0.45
  }
  if (keyLight) keyLight.castShadow = realistic
  if (ground) ground.visible = realistic
  if (renderer) {
    renderer.shadowMap.enabled = realistic
    if (realistic) renderer.shadowMap.type = THREE.PCFShadowMap
  }
  holder.value?.traverse((child) => {
    const mesh = child as THREE.Mesh
    if (!mesh.isMesh) return
    if (realistic) {
      const original = materialStore.get(mesh)
      if (original) {
        mesh.material = original
        materialStore.delete(mesh)
      }
    } else {
      if (!materialStore.has(mesh)) materialStore.set(mesh, mesh.material)
      mesh.material = getClayMaterial()
    }
  })
  applyWireframe()
}

function fitView(): void {
  if (!camera || !controls || !holder.value || holder.value.children.length === 0) return
  const target = fitCameraToObject(camera, holder.value, 1.25)
  controls.target.copy(target)
  controls.minDistance = Math.max(target.distanceTo(camera.position) / 200, 0.01)
  controls.maxDistance = Math.max(target.distanceTo(camera.position) * 40, 10)
  controls.update()

  const box = new THREE.Box3().setFromObject(holder.value)
  const size = box.getSize(new THREE.Vector3())
  const span = Math.max(size.x, size.z, 0.5)
  grid?.scale.setScalar(span / 6)

  if (ground) {
    ground.scale.setScalar(span * 3.2)
    ground.position.y = box.min.y - span * 0.002
  }
  if (keyLight) {
    keyLight.position.set(span * 1.6, span * 2.2, span * 1.6)
    const shadowCamera = keyLight.shadow.camera
    shadowCamera.left = -span * 1.5
    shadowCamera.right = span * 1.5
    shadowCamera.top = span * 1.5
    shadowCamera.bottom = -span * 1.5
    shadowCamera.near = span * 0.1
    shadowCamera.far = span * 12
    shadowCamera.updateProjectionMatrix()
  }
}

function attachModel(): void {
  if (!scene) return
  if (!holder.value) {
    holder.value = new THREE.Group()
    scene.add(holder.value)
  }
  while (holder.value.children.length > 0) {
    holder.value.remove(holder.value.children[0] as THREE.Object3D)
  }
  materialStore.clear()
  if (!props.model) return
  holder.value.rotation.set(0, 0, 0)
  holder.value.add(props.model)
  fitView()
  applyMode()
}

function updateHud(time: number): void {
  if (!hud.value || !camera || !controls) return
  if (time - lastHudAt < 110) return
  lastHudAt = time
  const offset = camera.position.clone().sub(controls.target)
  const distance = offset.length()
  if (distance < 0.0001) return
  const azimuth = ((Math.atan2(offset.x, offset.z) * 180) / Math.PI + 360) % 360
  const elevation = (Math.asin(THREE.MathUtils.clamp(offset.y / distance, -1, 1)) * 180) / Math.PI
  const fov = camera.fov
  hud.value.textContent = `AZ ${azimuth.toFixed(0).padStart(3, '0')}°   EL ${elevation.toFixed(0).padStart(3, '0')}°   DIST ${distance.toFixed(2)}   FOV ${fov}°`
}

function animate(time: number): void {
  frameId = window.requestAnimationFrame(animate)
  if (!renderer || !scene || !camera) return
  controls?.update()
  renderer.render(scene, camera)
  updateHud(time)
}

function handleResize(): void {
  const container = viewport.value
  if (!container || !renderer || !camera) return
  const width = container.clientWidth
  const height = container.clientHeight
  if (width === 0 || height === 0) return
  renderer.setSize(width, height)
  camera.aspect = width / height
  camera.updateProjectionMatrix()
}

function capture(): string | null {
  if (!renderer || !scene || !camera) return null
  renderer.render(scene, camera)
  return renderer.domElement.toDataURL('image/png')
}

/** 装配台拖拽：只对传入的根对象做拾取，内部网格仍按世界坐标命中 */
const raycaster = new THREE.Raycaster()
const pointer = new THREE.Vector2()
let dragState: { target: THREE.Object3D; pointerId: number; plane: THREE.Plane; vertical: boolean } | null = null
let canvasElement: HTMLCanvasElement | null = null

function updatePointer(event: PointerEvent): boolean {
  if (!canvasElement) return false
  const rect = canvasElement.getBoundingClientRect()
  if (rect.width === 0 || rect.height === 0) return false
  pointer.set(((event.clientX - rect.left) / rect.width) * 2 - 1, -((event.clientY - rect.top) / rect.height) * 2 + 1)
  return true
}

function pickDragTarget(event: PointerEvent): THREE.Object3D | null {
  const targets = props.dragTargets
  if (!camera || !targets || targets.length === 0 || !updatePointer(event)) return null
  raycaster.setFromCamera(pointer, camera)
  const hit = raycaster.intersectObjects(targets, true)[0]
  if (!hit) return null
  let node: THREE.Object3D | null = hit.object
  while (node && !targets.includes(node)) node = node.parent
  return node
}

function dragPoint(): THREE.Vector3 | null {
  if (!camera || !dragState) return null
  raycaster.setFromCamera(pointer, camera)
  const point = new THREE.Vector3()
  return raycaster.ray.intersectPlane(dragState.plane, point) ? point : null
}

/** 捕获阶段拦截：命中零件时别让 OrbitControls 同时开始旋转 */
function handlePointerDown(event: PointerEvent): void {
  if (event.button !== 0) return
  // 手柄已经悬停 / 正在拖动时，把事件完整留给 TransformControls。
  if (props.gizmoEnabled && transformControls && (transformControls.dragging || transformControls.axis !== null)) return
  const target = pickDragTarget(event)
  if (!target) return
  emit('pick', target)
  if (!props.dragEnabled) return

  event.preventDefault()
  event.stopPropagation()
  const world = target.getWorldPosition(new THREE.Vector3())
  const vertical = event.ctrlKey || event.metaKey
  let plane: THREE.Plane
  if (vertical && camera) {
    // 垂直拖拽平面经过物体、包含世界 Y 轴，法线取相机水平右方向。
    const dir = camera.getWorldDirection(new THREE.Vector3())
    const normal = new THREE.Vector3(-dir.z, 0, dir.x)
    if (normal.lengthSq() < 1e-6) normal.set(1, 0, 0)
    plane = new THREE.Plane().setFromNormalAndCoplanarPoint(normal.normalize(), world)
  } else {
    plane = new THREE.Plane(new THREE.Vector3(0, 1, 0), -world.y)
  }
  dragState = {
    target,
    pointerId: event.pointerId,
    plane,
    vertical,
  }
  if (controls) controls.enabled = false
  try {
    canvasElement?.setPointerCapture?.(event.pointerId)
  } catch {
    // 合成事件 / 指针已释放时浏览器会拒绝捕获，不影响拖拽本身
  }
  const point = dragPoint()
  if (point) emit('drag-start', target, point, vertical)
}

function handlePointerMove(event: PointerEvent): void {
  if (!dragState || event.pointerId !== dragState.pointerId) return
  event.preventDefault()
  updatePointer(event)
  const point = dragPoint()
  if (point) emit('drag', dragState.target, point, event.shiftKey)
}

function finishDrag(): void {
  if (!dragState) return
  const target = dragState.target
  const pointerId = dragState.pointerId
  dragState = null
  if (controls) controls.enabled = true
  try {
    if (canvasElement?.hasPointerCapture?.(pointerId)) canvasElement.releasePointerCapture(pointerId)
  } catch {
    // 指针捕获可能已被浏览器释放，忽略即可
  }
  emit('drag-end', target)
}

function handlePointerUp(event: PointerEvent): void {
  if (dragState && event.pointerId === dragState.pointerId) finishDrag()
}

function syncGizmo(): void {
  if (!transformControls) return
  const mode = props.gizmoMode ?? 'translate'
  transformControls.setMode(mode)
  transformControls.setSpace(props.gizmoSpace ?? 'local')
  const target = props.gizmoEnabled ? props.gizmoTarget : null
  if (target && target.parent) transformControls.attach(target)
  else transformControls.detach()
  const snap = props.gizmoSnap && props.gizmoSnap > 0 ? props.gizmoSnap : null
  const rotateDeg = props.gizmoRotateSnapDeg && props.gizmoRotateSnapDeg > 0 ? props.gizmoRotateSnapDeg : null
  transformControls.translationSnap = snap
  transformControls.rotationSnap = mode === 'rotate' && rotateDeg ? THREE.MathUtils.degToRad(rotateDeg) : null
  transformControls.scaleSnap = mode === 'scale' && snap ? snap : null
}

function handleGizmoDragging(event: { value: unknown }): void {
  const dragging = Boolean(event.value)
  if (controls) controls.enabled = !dragging
  const object = transformControls?.object
  if (!object) return
  if (dragging) emit('gizmo-start', object)
  else emit('gizmo-end', object)
}

function handleGizmoChange(): void {
  const object = transformControls?.object
  if (object) emit('gizmo-change', object)
}

function saveScreenshot(): void {
  const dataUrl = capture()
  if (!dataUrl) return
  const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')
  downloadDataUrl(dataUrl, `${props.assetName ?? 'asset'}-${stamp}.png`)
}

watch(() => props.model, attachModel)
watch(() => props.mode, applyMode)
watch(autoRotate, (value) => {
  if (controls) controls.autoRotate = value
})
watch(wireframe, applyWireframe)
watch(showGrid, applyGrid)
watch(() => [props.gizmoEnabled, props.gizmoTarget, props.gizmoSnap, props.gizmoRotateSnapDeg, props.gizmoMode, props.gizmoSpace], syncGizmo, { flush: 'post' })

onMounted(() => {
  const container = viewport.value
  if (!container) return

  try {
    renderer = new THREE.WebGLRenderer({ antialias: true, preserveDrawingBuffer: true })
  } catch {
    errorMessage.value = '当前环境无法创建 WebGL 上下文，模型预览不可用'
    return
  }

  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2))
  renderer.setSize(container.clientWidth, container.clientHeight)
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.toneMapping = THREE.ACESFilmicToneMapping
  renderer.toneMappingExposure = 1.05
  renderer.shadowMap.enabled = true
  renderer.shadowMap.type = THREE.PCFShadowMap
  container.appendChild(renderer.domElement)

  scene = new THREE.Scene()
  scene.background = new THREE.Color(0x101217)

  const pmrem = new THREE.PMREMGenerator(renderer)
  const room = new RoomEnvironment()
  envTexture = pmrem.fromScene(room, 0.04).texture
  scene.environment = envTexture
  room.dispose()
  pmrem.dispose()

  camera = new THREE.PerspectiveCamera(38, container.clientWidth / Math.max(container.clientHeight, 1), 0.01, 1e6)
  camera.position.set(3, 2.4, 3)

  controls = new OrbitControls(camera, renderer.domElement)
  controls.enableDamping = true
  controls.dampingFactor = 0.08
  controls.autoRotateSpeed = 0.9
  controls.maxPolarAngle = Math.PI * 0.94

  canvasElement = renderer.domElement
  canvasElement.addEventListener('pointerdown', handlePointerDown, true)
  canvasElement.addEventListener('pointermove', handlePointerMove)
  canvasElement.addEventListener('pointerup', handlePointerUp)
  canvasElement.addEventListener('pointercancel', handlePointerUp)
  canvasElement.addEventListener('lostpointercapture', finishDrag)

  transformControls = new TransformControls(camera, renderer.domElement)
  transformControls.setSize(0.9)
  // 子级挂在带旋转 / 缩放的父级下时，local 轴才是它自己的局部轴。
  transformControls.setSpace('local')
  transformControls.addEventListener('dragging-changed', handleGizmoDragging)
  transformControls.addEventListener('objectChange', handleGizmoChange)
  scene.add(transformControls.getHelper())
  syncGizmo()

  scene.add(new THREE.HemisphereLight(0xbccbdf, 0x191c22, 0.85))
  keyLight = new THREE.DirectionalLight(0xffffff, 1.9)
  keyLight.position.set(4, 6, 4)
  keyLight.castShadow = true
  keyLight.shadow.mapSize.set(1024, 1024)
  keyLight.shadow.bias = -0.0006
  keyLight.shadow.normalBias = 0.02
  scene.add(keyLight)
  const rimLight = new THREE.DirectionalLight(0x88a8ff, 0.55)
  rimLight.position.set(-5, 3, -4)
  scene.add(rimLight)

  const groundGeometry = new THREE.CircleGeometry(1, 64)
  const groundMaterial = new THREE.MeshStandardMaterial({ color: 0x1b1f27, roughness: 0.95, metalness: 0.02 })
  ground = new THREE.Mesh(groundGeometry, groundMaterial)
  ground.rotation.x = -Math.PI / 2
  ground.position.y = -0.003
  ground.receiveShadow = true
  scene.add(ground)

  grid = new THREE.GridHelper(20, 20, 0x3d4553, 0x242932)
  const gridMaterial = grid.material as THREE.Material
  gridMaterial.transparent = true
  gridMaterial.opacity = 0.8
  scene.add(grid)
  applyGrid()

  holder.value = new THREE.Group()
  scene.add(holder.value)
  attachModel()

  resizeObserver = new ResizeObserver(handleResize)
  resizeObserver.observe(container)

  frameId = window.requestAnimationFrame(animate)
})

onBeforeUnmount(() => {
  window.cancelAnimationFrame(frameId)
  resizeObserver?.disconnect()
  resizeObserver = null
  if (canvasElement) {
    canvasElement.removeEventListener('pointerdown', handlePointerDown, true)
    canvasElement.removeEventListener('pointermove', handlePointerMove)
    canvasElement.removeEventListener('pointerup', handlePointerUp)
    canvasElement.removeEventListener('pointercancel', handlePointerUp)
    canvasElement.removeEventListener('lostpointercapture', finishDrag)
    canvasElement = null
  }
  dragState = null
  if (transformControls) {
    transformControls.removeEventListener('dragging-changed', handleGizmoDragging)
    transformControls.removeEventListener('objectChange', handleGizmoChange)
    transformControls.detach()
    scene?.remove(transformControls.getHelper())
    transformControls.dispose()
    transformControls = null
  }
  controls?.dispose()
  controls = null
  if (holder.value && scene) {
    while (holder.value.children.length > 0) {
      holder.value.remove(holder.value.children[0] as THREE.Object3D)
    }
  }
  grid?.geometry.dispose()
  const gridMaterial = grid?.material
  if (gridMaterial && !Array.isArray(gridMaterial)) gridMaterial.dispose()
  ground?.geometry.dispose()
  const groundMaterial = ground?.material
  if (groundMaterial && !Array.isArray(groundMaterial)) groundMaterial.dispose()
  ground = null
  clayMaterial?.dispose()
  clayMaterial = null
  materialStore.clear()
  keyLight = null
  envTexture?.dispose()
  envTexture = null
  scene?.clear()
  scene = null
  renderer?.dispose()
  renderer?.forceContextLoss()
  if (renderer?.domElement.parentNode) renderer.domElement.parentNode.removeChild(renderer.domElement)
  renderer = null
  camera = null
  holder.value = null
})

defineExpose({ capture, fitView })
</script>

<template>
  <div class="viewer">
    <div ref="viewport" class="canvas-host"></div>

    <div class="toolbar">
      <button
        class="chip"
        type="button"
        :class="{ 'is-active': autoRotate }"
        :aria-pressed="autoRotate"
        @click="autoRotate = !autoRotate"
      >
        自动旋转
      </button>
      <button
        class="chip"
        type="button"
        :class="{ 'is-active': wireframe }"
        :aria-pressed="wireframe"
        @click="wireframe = !wireframe"
      >
        线框
      </button>
      <button
        class="chip"
        type="button"
        :class="{ 'is-active': showGrid }"
        :aria-pressed="showGrid"
        @click="showGrid = !showGrid"
      >
        网格
      </button>
      <span class="toolbar-sep"></span>
      <button class="chip" type="button" @click="fitView">复位视角</button>
      <button class="chip" type="button" @click="saveScreenshot">导出 PNG</button>
    </div>

    <div ref="hud" class="hud mono">AZ —   EL —   DIST —</div>

    <div class="hint mono">
      <span v-if="dragEnabled">左键 拖零件</span>
      <span>左键 旋转</span>
      <span>滚轮 缩放</span>
      <span>右键 平移</span>
      <span v-if="dragEnabled">Shift 锁单轴</span>
      <span v-if="dragEnabled">Ctrl 垂直</span>
    </div>

    <div v-if="errorMessage" class="viewer-error">{{ errorMessage }}</div>
  </div>
</template>

<style scoped>
.viewer {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #101217;
}

.canvas-host {
  position: absolute;
  inset: 0;
}

.canvas-host :deep(canvas) {
  display: block;
  width: 100%;
  height: 100%;
}

.toolbar {
  position: absolute;
  top: 6px;
  right: 6px;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px;
  border: 2px solid;
  border-color: var(--bevel-raised);
  border-radius: 0;
  background: var(--surface);
  box-shadow: 1px 1px 0 #000000;
}

.toolbar .chip {
  border-color: transparent;
  background: transparent;
  box-shadow: none;
}

.toolbar .chip.is-active {
  border-color: var(--bevel-sunken);
  background: #000080;
  color: #ffffff;
}

.toolbar-sep {
  width: 1px;
  height: 16px;
  margin: 0 2px;
  background: #808080;
  box-shadow: 1px 0 0 #ffffff;
}

.hud {
  position: absolute;
  left: 8px;
  bottom: 8px;
  padding: 1px 4px;
  border: 1px solid #808080;
  border-radius: 0;
  background: #000000;
  color: #00ff00;
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: 0;
  white-space: pre;
  pointer-events: none;
}

.hint {
  position: absolute;
  right: 8px;
  bottom: 8px;
  display: flex;
  gap: 8px;
  color: #c0c0c0;
  font-size: 10px;
  letter-spacing: 0;
  text-shadow: 1px 1px 0 #000000;
  pointer-events: none;
}

.viewer-error {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  padding: 24px;
  color: var(--danger);
  font-size: 13px;
  text-align: center;
}
</style>
