import * as THREE from 'three'
import { GLTFExporter } from 'three/examples/jsm/exporters/GLTFExporter.js'
import type { UploadPayload } from '../types'
import { computeStats, createThumbnail, disposeObject, loadModelFromBlob } from './model'

interface SeedSpec {
  slug: string
  name: string
  category: string
  description: string
  tags: string[]
  build: () => THREE.Object3D
}

function brassMaterial(): THREE.MeshStandardMaterial {
  return new THREE.MeshStandardMaterial({ color: 0xd8a154, metalness: 0.82, roughness: 0.3 })
}

function slateMaterial(): THREE.MeshStandardMaterial {
  return new THREE.MeshStandardMaterial({ color: 0x8791a3, metalness: 0.42, roughness: 0.56 })
}

function crystalMaterial(): THREE.MeshStandardMaterial {
  return new THREE.MeshStandardMaterial({
    color: 0x9ce0d6,
    metalness: 0.25,
    roughness: 0.16,
    transparent: true,
    opacity: 0.92,
  })
}

function buildTorusKnot(): THREE.Object3D {
  const mesh = new THREE.Mesh(new THREE.TorusKnotGeometry(1, 0.32, 240, 40), brassMaterial())
  mesh.name = 'torus-knot'
  return mesh
}

function buildCrystalCluster(): THREE.Object3D {
  const group = new THREE.Group()
  const shards: Array<[number, number, number, number, number]> = [
    [0, 0, 0, 1.05, 0],
    [0.75, 0, 0.35, 0.62, 0.5],
    [-0.68, 0, 0.52, 0.55, -0.35],
    [0.2, 0, -0.72, 0.48, 0.9],
    [-0.3, 0, -0.3, 0.4, -0.7],
  ]
  shards.forEach(([x, y, z, radius, tilt], index) => {
    const mesh = new THREE.Mesh(new THREE.OctahedronGeometry(radius, 0), crystalMaterial())
    mesh.position.set(x, y + 0.9, z)
    mesh.rotation.set(tilt * 0.4, index * 0.7, tilt)
    mesh.scale.set(1, 2.1, 1)
    mesh.name = `shard-${index}`
    group.add(mesh)
  })
  return group
}

function buildMountBracket(): THREE.Object3D {
  const group = new THREE.Group()

  const base = new THREE.Mesh(new THREE.CylinderGeometry(1.5, 1.65, 0.24, 56), slateMaterial())
  base.position.y = 0
  base.name = 'base'
  group.add(base)

  const post = new THREE.Mesh(new THREE.CylinderGeometry(0.26, 0.34, 2.1, 40), slateMaterial())
  post.position.y = 1.17
  post.name = 'post'
  group.add(post)

  const ring = new THREE.Mesh(new THREE.TorusGeometry(0.58, 0.11, 28, 72), brassMaterial())
  ring.position.y = 2.32
  ring.rotation.x = Math.PI / 2
  ring.name = 'clamp-ring'
  group.add(ring)

  for (let index = 0; index < 3; index += 1) {
    const angle = (index / 3) * Math.PI * 2
    const bolt = new THREE.Mesh(new THREE.CylinderGeometry(0.1, 0.1, 0.34, 16), brassMaterial())
    bolt.position.set(Math.cos(angle) * 1.16, 0.16, Math.sin(angle) * 1.16)
    bolt.name = `bolt-${index}`
    group.add(bolt)
  }

  return group
}

function buildSpiralRotor(): THREE.Object3D {
  const group = new THREE.Group()
  const count = 18
  for (let index = 0; index < count; index += 1) {
    const angle = (index / count) * Math.PI * 2
    const radius = 0.95 + Math.sin(index * 0.6) * 0.12
    const blade = new THREE.Mesh(new THREE.BoxGeometry(0.14, 1.06, 0.42), slateMaterial())
    blade.position.set(Math.cos(angle) * radius, 0.53 + index * 0.085, Math.sin(angle) * radius)
    blade.rotation.set(0, -angle, 0.45)
    blade.name = `blade-${index}`
    group.add(blade)
  }
  const hub = new THREE.Mesh(new THREE.CylinderGeometry(0.34, 0.34, 0.3, 32), brassMaterial())
  hub.position.y = 0.15
  hub.name = 'hub'
  group.add(hub)
  return group
}

const SEED_SPECS: SeedSpec[] = [
  {
    slug: 'torus-knot',
    name: '黄铜环形结',
    category: '几何体',
    description: '参数化环形结，240 × 40 段细分，用于检查曲面法线与高光走向。',
    tags: ['参数化', '曲面', '黄铜'],
    build: buildTorusKnot,
  },
  {
    slug: 'crystal-cluster',
    name: '水晶簇',
    category: '自然',
    description: '五枚拉长的八面体组成的水晶簇，半透明材质，适合测试透明排序。',
    tags: ['低模', '透明', '自然'],
    build: buildCrystalCluster,
  },
  {
    slug: 'mount-bracket',
    name: '检视台支架',
    category: '机械',
    description: '底座、立轴与卡箍组成的硬表面组件，附三枚固定螺栓。',
    tags: ['硬表面', '装配', '支架'],
    build: buildMountBracket,
  },
  {
    slug: 'spiral-rotor',
    name: '螺旋转子',
    category: '机械',
    description: '18 片沿轴线抬升的叶片，绕中心轮毂排布，用于检查阵列与间距。',
    tags: ['阵列', '螺旋', '机械'],
    build: buildSpiralRotor,
  },
]

async function exportGlb(object: THREE.Object3D): Promise<Blob> {
  const exporter = new GLTFExporter()
  const result = await exporter.parseAsync(object, { binary: true })
  if (!(result instanceof ArrayBuffer)) throw new Error('示例模型导出失败')
  return new Blob([result], { type: 'model/gltf-binary' })
}

/** 内置示例资产，走与用户导入完全相同的解析、缩略图与上传管线 */
export async function createSeedPayloads(): Promise<UploadPayload[]> {
  const payloads: UploadPayload[] = []

  for (const spec of SEED_SPECS) {
    const built = spec.build()
    const blob = await exportGlb(built)
    disposeObject(built)

    const fileName = `${spec.slug}.glb`
    const loaded = await loadModelFromBlob(blob, 'glb')
    const stats = computeStats(loaded)
    const thumb = await createThumbnail(loaded)
    disposeObject(loaded)

    payloads.push({
      file: blob,
      fileName,
      thumb,
      meta: {
        name: spec.name,
        description: spec.description,
        category: spec.category,
        tags: spec.tags,
        format: 'glb',
        stats,
      },
    })
  }

  return payloads
}
