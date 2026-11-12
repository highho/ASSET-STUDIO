import * as THREE from 'three'
import { DRACOLoader } from 'three/examples/jsm/loaders/DRACOLoader.js'
import { FBXLoader } from 'three/examples/jsm/loaders/FBXLoader.js'
import { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js'
import { KTX2Loader } from 'three/examples/jsm/loaders/KTX2Loader.js'
import { MTLLoader } from 'three/examples/jsm/loaders/MTLLoader.js'
import { OBJLoader } from 'three/examples/jsm/loaders/OBJLoader.js'
import { STLLoader } from 'three/examples/jsm/loaders/STLLoader.js'
import { MeshoptDecoder } from 'three/examples/jsm/libs/meshopt_decoder.module.js'
import { ASSET_FORMATS, type AssetFormat, type AssetStats } from '../types'
import { applyPreviewOffset } from './placement'

export interface LoadOptions {
  /** 资产 id：配合 hasSidecars 把 .bin / 贴图 / .mtl 映射到后端的 sidecars 接口 */
  assetId?: string
  hasSidecars?: boolean
}

export function detectFormat(fileName: string): AssetFormat | null {
  const extension = fileName.split('.').pop()?.toLowerCase() ?? ''
  return (ASSET_FORMATS as string[]).includes(extension) ? (extension as AssetFormat) : null
}

/** GLTF 的 Draco / KTX2 / meshopt 解码器都按需初始化一次，避免每次导入重复拉取。 */
let dracoLoader: DRACOLoader | null = null
let ktx2Loader: KTX2Loader | null = null
let ktx2Renderer: THREE.WebGLRenderer | null = null

function getDracoLoader(): DRACOLoader {
  if (!dracoLoader) {
    dracoLoader = new DRACOLoader()
    dracoLoader.setDecoderPath('/draco/')
  }
  return dracoLoader
}

function getKtx2Loader(): KTX2Loader | null {
  if (ktx2Loader) return ktx2Loader
  try {
    ktx2Renderer = new THREE.WebGLRenderer({ antialias: false })
    ktx2Renderer.setSize(1, 1)
    ktx2Loader = new KTX2Loader().setTranscoderPath('/basis/').detectSupport(ktx2Renderer)
    return ktx2Loader
  } catch (error) {
    console.warn('KTX2 解码器初始化失败，压缩纹理将不可用', error)
    return null
  }
}

function sidecarBase(options?: LoadOptions): string {
  if (!options?.assetId || !options.hasSidecars) return ''
  return `/api/assets/${encodeURIComponent(options.assetId)}/sidecars/`
}

/** 把模型里写的外部引用（model.bin、textures/wood.png）改写到库内的 sidecars 接口。 */
function rewriteSidecarUrl(url: string, base: string): string {
  if (!base) return url
  if (/^(data:|blob:|http:|https:|\/)/i.test(url)) return url
  const clean = url.replace(/^\.\//, '').replace(/^\/+/, '')
  return base + clean.split('/').map((segment) => encodeURIComponent(segment)).join('/')
}

function createManager(base: string): THREE.LoadingManager {
  const manager = new THREE.LoadingManager()
  manager.setURLModifier((url) => rewriteSidecarUrl(url, base))
  return manager
}

function configureGltfLoader(loader: GLTFLoader): void {
  loader.setDRACOLoader(getDracoLoader())
  loader.setMeshoptDecoder(MeshoptDecoder)
  const ktx = getKtx2Loader()
  if (ktx) loader.setKTX2Loader(ktx)
}

/** 等外部贴图加载完（最多等 timeout），否则缩略图可能渲染出无贴图的版本。 */
async function settleLoading(manager: THREE.LoadingManager, timeout: number): Promise<void> {
  await new Promise<void>((resolve) => {
    let finished = false
    const finish = () => {
      if (finished) return
      finished = true
      resolve()
    }
    manager.onLoad = finish
    manager.onError = finish
    window.setTimeout(finish, timeout)
  })
}

function findMtlRef(text: string): string | null {
  const match = /\bmtllib\s+([^\r\n]+)/i.exec(text)
  if (!match) return null
  const first = match[1].trim().split(/\s+/)[0]
  return first || null
}

function stlMesh(geometry: THREE.BufferGeometry): THREE.Object3D {
  geometry.computeVertexNormals()
  const material = new THREE.MeshStandardMaterial({
    color: 0xc7ccd6,
    metalness: 0.18,
    roughness: 0.5,
  })
  const mesh = new THREE.Mesh(geometry, material)
  mesh.name = 'STL Mesh'
  return mesh
}

export async function loadModelFromBlob(
  blob: Blob,
  format: AssetFormat,
  options?: LoadOptions,
): Promise<THREE.Object3D> {
  const base = sidecarBase(options)
  const manager = createManager(base)
  let object: THREE.Object3D

  switch (format) {
    case 'glb':
    case 'gltf': {
      const loader = new GLTFLoader(manager)
      configureGltfLoader(loader)
      const payload: ArrayBuffer | string = format === 'glb' ? await blob.arrayBuffer() : await blob.text()
      const gltf = await loader.parseAsync(payload, '')
      object = gltf.scene
      break
    }
    case 'obj': {
      const text = await blob.text()
      const loader = new OBJLoader(manager)
      const mtlRef = findMtlRef(text)
      if (mtlRef) {
        try {
          const response = await fetch(rewriteSidecarUrl(mtlRef, base))
          if (response.ok) {
            const materials = new MTLLoader(manager).parse(await response.text(), '')
            materials.preload()
            loader.setMaterials(materials)
            await settleLoading(manager, 3000)
          }
        } catch (error) {
          // 找不到 MTL 就退回只有几何的预览，不影响入库
          console.warn('读取 MTL 失败，按无材质渲染', mtlRef, error)
        }
      }
      object = loader.parse(text)
      break
    }
    case 'stl': {
      const buffer = await blob.arrayBuffer()
      object = stlMesh(new STLLoader().parse(buffer))
      break
    }
    case 'fbx': {
      const buffer = await blob.arrayBuffer()
      object = new FBXLoader(manager).parse(buffer, '')
      break
    }
    default: {
      throw new Error(`暂不支持 ${format} 格式`)
    }
  }

  if (!object) throw new Error('文件里没有可预览的模型数据')
  if (!object.name) object.name = 'asset-root'
  applyPreviewOffset(object)
  return object
}

export function computeStats(object: THREE.Object3D): AssetStats {
  let vertices = 0
  let triangles = 0
  let meshes = 0
  const materialIds = new Set<string>()

  object.traverse((child) => {
    const mesh = child as THREE.Mesh
    if (!mesh.isMesh || !mesh.geometry) return
    meshes += 1
    const position = mesh.geometry.getAttribute('position')
    if (position) vertices += position.count
    const index = mesh.geometry.getIndex()
    if (index) triangles += index.count / 3
    else if (position) triangles += position.count / 3
    const material = mesh.material
    if (Array.isArray(material)) material.forEach((item) => materialIds.add(item.uuid))
    else if (material) materialIds.add(material.uuid)
  })

  object.updateMatrixWorld(true)
  const box = new THREE.Box3().setFromObject(object)
  const size = box.isEmpty() ? new THREE.Vector3(0, 0, 0) : box.getSize(new THREE.Vector3())

  return {
    vertices,
    triangles: Math.round(triangles),
    meshes,
    materials: materialIds.size,
    dimensions: [size.x, size.y, size.z],
  }
}

/** 让相机刚好装下整个模型 */
export function fitCameraToObject(
  camera: THREE.PerspectiveCamera,
  object: THREE.Object3D,
  padding = 1.2,
): THREE.Vector3 {
  const box = new THREE.Box3().setFromObject(object)
  const sphere = box.isEmpty()
    ? new THREE.Sphere(new THREE.Vector3(), 1)
    : box.getBoundingSphere(new THREE.Sphere())
  const radius = Math.max(sphere.radius, 0.001)
  const distance = (radius / Math.sin((camera.fov * Math.PI) / 360)) * padding
  const direction = new THREE.Vector3(0.95, 0.62, 1).normalize()

  camera.position.copy(sphere.center).add(direction.multiplyScalar(distance))
  camera.near = Math.max(distance / 400, 0.001)
  camera.far = Math.max(distance * 25, 10)
  camera.lookAt(sphere.center)
  camera.updateProjectionMatrix()
  return sphere.center
}

export function disposeObject(root: THREE.Object3D): void {
  const materials = new Set<THREE.Material>()
  root.traverse((child) => {
    const mesh = child as THREE.Mesh
    if (!mesh.isMesh) return
    mesh.geometry?.dispose()
    const material = mesh.material
    if (Array.isArray(material)) material.forEach((item) => materials.add(item))
    else if (material) materials.add(material)
  })
  materials.forEach((material) => {
    Object.values(material).forEach((value) => {
      if (value && typeof value === 'object' && 'isTexture' in value) {
        ;(value as THREE.Texture).dispose()
      }
    })
    material.dispose()
  })
}

/** 离屏渲染一张缩略图，失败时返回 null（浏览器不支持 WebGL 的情况） */
export async function createThumbnail(
  object: THREE.Object3D,
  width = 512,
  height = 320,
): Promise<Blob | null> {
  let renderer: THREE.WebGLRenderer | null = null
  try {
    renderer = new THREE.WebGLRenderer({
      antialias: true,
      preserveDrawingBuffer: true,
      alpha: false,
    })
    renderer.setPixelRatio(1)
    renderer.setSize(width, height, false)
    renderer.outputColorSpace = THREE.SRGBColorSpace
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    renderer.toneMappingExposure = 1.15

    const scene = new THREE.Scene()
    scene.background = new THREE.Color(0x12151b)

    const clone = object.clone(true)
    scene.add(clone)

    scene.add(new THREE.HemisphereLight(0xb9c7de, 0x1b1f26, 1.2))
    const keyLight = new THREE.DirectionalLight(0xffffff, 2.6)
    keyLight.position.set(3.5, 5.5, 4)
    scene.add(keyLight)
    const rimLight = new THREE.DirectionalLight(0x86a8ff, 0.85)
    rimLight.position.set(-4, 2.5, -3.5)
    scene.add(rimLight)

    const camera = new THREE.PerspectiveCamera(34, width / height, 0.01, 1e6)
    fitCameraToObject(camera, clone, 1.18)

    renderer.render(scene, camera)

    const blob = await new Promise<Blob | null>((resolve) => {
      renderer!.domElement.toBlob(resolve, 'image/webp', 0.88)
    })
    scene.remove(clone)
    return blob
  } catch (error) {
    console.warn('缩略图生成失败', error)
    return null
  } finally {
    renderer?.dispose()
    renderer?.forceContextLoss()
  }
}
