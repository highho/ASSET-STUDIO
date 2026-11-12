import * as THREE from 'three'

/** 预览用的摆正偏移，记在对象上，导出时再抵消掉 */
interface PreviewOffset {
  x: number
  y: number
  z: number
}

const PREVIEW_OFFSET_KEY = '__previewOffset'

/**
 * 把模型摆到原点、底面贴合 y=0，便于轨道相机围绕它旋转。
 *
 * 这层平移只服务于预览：它被记在对象上（userData.__previewOffset），
 * 导出/转换时由 withOriginalPlacement 抵消，导出的文件与世界坐标和原件一致。
 */
export function applyPreviewOffset(object: THREE.Object3D): void {
  object.updateMatrixWorld(true)
  const box = new THREE.Box3().setFromObject(object)
  if (box.isEmpty()) return
  const center = box.getCenter(new THREE.Vector3())
  const offset: PreviewOffset = { x: -center.x, y: -box.min.y, z: -center.z }
  object.userData[PREVIEW_OFFSET_KEY] = offset
  object.position.x += offset.x
  object.position.y += offset.y
  object.position.z += offset.z
  object.updateMatrixWorld(true)
}

/**
 * 在「源文件原始坐标」下执行导出/转换。
 *
 * 预览时对象被平移过（居中、底面落地），这层平移如果带进导出结果，
 * 转出来的文件和原件的位置就不一样了。这里临时把偏移减掉，做完再还原。
 */
export async function withOriginalPlacement<T>(
  object: THREE.Object3D,
  run: () => Promise<T> | T,
): Promise<T> {
  const offset = object.userData[PREVIEW_OFFSET_KEY] as PreviewOffset | undefined
  if (!offset) return await run()
  object.position.x -= offset.x
  object.position.y -= offset.y
  object.position.z -= offset.z
  object.updateMatrixWorld(true)
  try {
    return await run()
  } finally {
    object.position.x += offset.x
    object.position.y += offset.y
    object.position.z += offset.z
    object.updateMatrixWorld(true)
  }
}
