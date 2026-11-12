import type { ConvertTarget } from '../types'

/**
 * 转换后写进资产的说明文字，交代这次转换损失了什么。
 * 单独放一个文件：它只是文案，不该把导出器（以及 three）一起拉进资产库首屏的依赖图。
 */
export function convertLossNote(target: ConvertTarget): string {
  switch (target) {
    case 'glb':
      return 'GLB 保留网格、材质与贴图，是复用性最好的格式。'
    case 'gltf':
      return 'GLTF 为单文件内嵌资源，部分外部贴图会以数据块内嵌，文件偏大。'
    case 'obj':
      return 'OBJ 只保留几何，材质、贴图与层级名称会丢失。'
    case 'stl':
      return 'STL 只保留三角面片，不带材质、颜色与顶点法线分组。'
    default:
      return ''
  }
}
