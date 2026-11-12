import type * as THREE from 'three'
import type { ConvertTarget } from '../types'

export interface ConvertedFile {
  blob: Blob
  fileName: string
}

/**
 * 把内存中的模型导出成目标格式。
 * GLB 保留网格与材质；GLTF 为单文件内嵌资源；OBJ / STL 只保留几何，
 * 材质与颜色信息会丢失 —— 界面上要如实提示。
 */
export async function convertObject(object: THREE.Object3D, target: ConvertTarget, baseName: string): Promise<ConvertedFile> {
  switch (target) {
    case 'glb': {
      const { GLTFExporter } = await import('three/examples/jsm/exporters/GLTFExporter.js')
      const exporter = new GLTFExporter()
      const buffer = (await exporter.parseAsync(object, { binary: true })) as ArrayBuffer
      return {
        blob: new Blob([buffer], { type: 'model/gltf-binary' }),
        fileName: `${baseName}.glb`,
      }
    }
    case 'gltf': {
      const { GLTFExporter } = await import('three/examples/jsm/exporters/GLTFExporter.js')
      const exporter = new GLTFExporter()
      const json = (await exporter.parseAsync(object, { binary: false, embedImages: true })) as Record<string, unknown>
      return {
        blob: new Blob([JSON.stringify(json)], { type: 'model/gltf+json' }),
        fileName: `${baseName}.gltf`,
      }
    }
    case 'obj': {
      const { OBJExporter } = await import('three/examples/jsm/exporters/OBJExporter.js')
      const text = new OBJExporter().parse(object)
      return {
        blob: new Blob([text], { type: 'text/plain' }),
        fileName: `${baseName}.obj`,
      }
    }
    case 'stl': {
      const { STLExporter } = await import('three/examples/jsm/exporters/STLExporter.js')
      const result: unknown = new STLExporter().parse(object, { binary: true })
      let part: BlobPart
      if (result instanceof DataView) {
        // 二进制 STL 返回的是 DataView，按实际视图范围拷一份，避免带上缓冲区里的多余字节
        const bytes = new Uint8Array(result.byteLength)
        bytes.set(new Uint8Array(result.buffer as ArrayBuffer, result.byteOffset, result.byteLength))
        part = bytes
      } else if (result instanceof ArrayBuffer) {
        part = result
      } else {
        part = new TextEncoder().encode(String(result))
      }
      return {
        blob: new Blob([part], { type: 'model/stl' }),
        fileName: `${baseName}.stl`,
      }
    }
    default: {
      throw new Error(`暂不支持导出 ${target} 格式`)
    }
  }
}

