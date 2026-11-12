import * as THREE from 'three'

/**
 * sand3d 的默认程序：一块板、两个孔、顶边倒圆。
 * 这是 sand3d 官方 quickstart 里的 bracket，一字不改地放进来当模板 —— 它可以真跑。
 */
export const SAND3D_TEMPLATE = `import sand3d as s3d

# 一块板，两个孔，顶边倒圆。改数字就是改零件。
def bracket(w=80, d=40, t=6, hole_d=6.6, pitch=50):
    plate = s3d.extrude("plate", s3d.rect(w, d, r=4), t)
    s3d.hole(plate, hole_d, on=s3d.grid(2, 1, pitch, 0))
    s3d.fillet(plate, 1, edges="top")
    return plate

s3d.export(bracket(), "bracket.glb")
`

/**
 * 参数化人偶模板。人体没法从网格反推（那是一条死路），能做的是「用参数造一个人」：
 * 身高、肩宽、四肢粗细、站姿、头身比五个数，改完重跑就有新的人偶。
 */
export const SAND3D_HUMANOID_TEMPLATE = `import math

import sand3d as s3d

# 参数化人偶：只改下面这几个数，就能改身高、肩宽、四肢粗细和站姿。
# 它不追求像真人——是用 box / cylinder / sphere 拼出的「可改比例的人形」。
H = 1700          # 总身高，mm
SHOULDER = 430    # 肩宽
THICK = 1.0       # 四肢粗细系数：0.8 细、1.2 壮
SPREAD = 18       # 手臂外张角，度（0 = 贴身，A 字站姿用 15～25）
STANCE = 100      # 两脚间距
HEAD = 7.2        # 身高 / HEAD = 头高，越小头越大
SKIN = "#d8b49a"
CLOTH = "#59626f"

head = H / HEAD
shin = H * 0.24
thigh = H * 0.25
torso = H * 0.20
hip = H * 0.085
neck_h = H * 0.05
ankle = 45
knee = ankle + shin
hip_z = knee + thigh
shoulder_z = hip_z + hip + torso

parts = []

for side, sign in (("left", -1), ("right", 1)):
    foot = s3d.box(f"foot_{side}", 110, 260, 45)
    s3d.at(foot, sign * STANCE / 2, 0, 0, anchor="bottom")
    s3d.material(foot, "plastic", color="#2f3440")
    parts.append(foot)

for side, sign in (("left", -1), ("right", 1)):
    calf = s3d.cylinder(f"calf_{side}", 110 * THICK, shin)
    s3d.at(calf, sign * STANCE / 2, 0, ankle, anchor="bottom")
    s3d.material(calf, "plastic", color="#4a5260")
    parts.append(calf)

    leg = s3d.cylinder(f"thigh_{side}", 150 * THICK, thigh)
    s3d.at(leg, sign * STANCE / 2, 0, knee, anchor="bottom")
    s3d.material(leg, "plastic", color="#3a4150")
    parts.append(leg)

pelvis = s3d.box("pelvis", SHOULDER * 0.8, 220, hip)
s3d.at(pelvis, 0, 0, hip_z, anchor="bottom")
s3d.fillet(pelvis, 30, edges="top")
s3d.material(pelvis, "plastic", color="#2f3440")
parts.append(pelvis)

torso_box = s3d.box("torso", SHOULDER, 240, torso)
s3d.at(torso_box, 0, 0, hip_z + hip, anchor="bottom")
s3d.fillet(torso_box, 40, edges="top")
s3d.material(torso_box, "plastic", color=CLOTH)
parts.append(torso_box)

neck = s3d.cylinder("neck", 100, neck_h)
s3d.at(neck, 0, 0, shoulder_z, anchor="bottom")
s3d.material(neck, "plastic", color=SKIN)
parts.append(neck)

head_ball = s3d.sphere("head", head * 0.92)
s3d.at(head_ball, 0, 0, shoulder_z + neck_h + head * 0.40)
s3d.material(head_ball, "plastic", color=SKIN)
parts.append(head_ball)

upper = H * 0.175
fore = H * 0.16
a1 = math.radians(SPREAD)
a2 = math.radians(SPREAD * 0.6)

for side, sign in (("left", -1), ("right", 1)):
    joint_x = sign * (SHOULDER / 2 - 10)
    joint_z = shoulder_z - head * 0.05

    shoulder = s3d.sphere(f"shoulder_{side}", 118 * THICK)
    s3d.at(shoulder, joint_x, 0, joint_z)
    s3d.material(shoulder, "plastic", color=CLOTH)
    parts.append(shoulder)

    arm = s3d.cylinder(f"upper_arm_{side}", 100 * THICK, upper)
    s3d.at(arm, joint_x, 0, joint_z, ry=-sign * SPREAD, anchor="top")
    s3d.material(arm, "plastic", color=CLOTH)
    parts.append(arm)

    elbow_x = joint_x + sign * upper * math.sin(a1)
    elbow_z = joint_z - upper * math.cos(a1)

    lower = s3d.cylinder(f"forearm_{side}", 85 * THICK, fore)
    s3d.at(lower, elbow_x, 0, elbow_z, ry=-sign * SPREAD * 0.6, anchor="top")
    s3d.material(lower, "plastic", color=CLOTH)
    parts.append(lower)

    wrist_x = elbow_x + sign * fore * math.sin(a2)
    wrist_z = elbow_z - fore * math.cos(a2)

    palm = s3d.sphere(f"hand_{side}", 92 * THICK)
    s3d.at(palm, wrist_x, 0, wrist_z - 30)
    s3d.material(palm, "plastic", color=SKIN)
    parts.append(palm)

s3d.export(s3d.group("humanoid", *parts), "humanoid.glb")
`

/** sand3d 的长度单位是毫米，而 GLB / three.js 里是米 */
const MM = 1000

/** three.js 是 Y-up，bpy / sand3d 是 Z-up，所以尺寸和位置都要显式换轴：
 *  three.x → sand3d.x，three.z（前后）→ sand3d.y（进深），three.y（上下）→ sand3d.z（高度）。
 *  这一层只做换算，不碰场景，方便单测。 */
export interface Sand3dBox {
  width: number
  depth: number
  height: number
  x: number
  y: number
  z: number
}

export type Vec3 = { x: number; y: number; z: number }

/** 世界包围盒（米，Y-up）→ sand3d 的 box 参数与落位（毫米，Z-up）。 */
export function worldBoxToSand3d(min: Vec3, max: Vec3): Sand3dBox {
  const sizeX = max.x - min.x
  const sizeY = max.y - min.y
  const sizeZ = max.z - min.z
  return {
    width: sizeX * MM,
    depth: sizeZ * MM,
    height: sizeY * MM,
    // x / y 给中心，z 给底面 —— sand3d 的 at 以底面定位，和 s3d.box 的生长方向一致
    x: ((min.x + max.x) / 2) * MM,
    y: ((min.z + max.z) / 2) * MM,
    z: min.y * MM,
  }
}

export interface ReverseResult {
  code: string
  parts: number
  note: string
  /** 反推结果不可用时的提醒（单网格曲面模型）：UI 和生成代码里都要写出来 */
  warning?: string
}

function tidy(value: number): string {
  const rounded = Math.round(value * 10) / 10
  return Number.isInteger(rounded) ? `${rounded}.0` : String(rounded)
}

function identifier(raw: string, index: number): string {
  const cleaned = raw
    .normalize('NFKD')
    .replace(/[^\w\u4e00-\u9fa5]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .replace(/^\d/, 'p$&')
  return cleaned ? `part_${index}_${cleaned}`.slice(0, 40) : `part_${index}`
}

function hexOf(color: THREE.Color): string {
  return `#${color.getHexString()}`
}

/**
 * 由模型推代码。
 *
 * 网格是「结果」，sand3d 程序是「构造过程」，两者之间没有唯一解：一块带孔倒角的板，
 * 反推出来的程序有无数种写法。所以这里做的是**可改的起点**——按每个网格的世界包围盒
 * 生成一个 `s3d.box`，位置用 `s3d.at` 摆回去，颜色用 `s3d.material` 带上。
 * 曲面零件的形状会退化成盒子，这个边界写在生成代码的注释里，也写在检视台的提示里。
 */
export function modelToSand3d(root: THREE.Object3D, assetName: string): ReverseResult {
  root.updateMatrixWorld(true)

  interface Piece {
    name: string
    width: number
    depth: number
    height: number
    x: number
    y: number
    z: number
    color: string | null
    volume: number
  }

  const pieces: Piece[] = []
  const seenNames = new Map<string, number>()
  let meshCount = 0
  let triangleCount = 0

  root.traverse((child) => {
    const mesh = child as THREE.Mesh
    if (!mesh.isMesh || !mesh.geometry) return
    const geometry = mesh.geometry
    meshCount += 1
    triangleCount += geometry.index ? geometry.index.count / 3 : (geometry.attributes.position?.count ?? 0) / 3
    if (!geometry.boundingBox) geometry.computeBoundingBox()
    const local = geometry.boundingBox
    if (!local) return

    const world = local.clone().applyMatrix4(mesh.matrixWorld)
    const box = worldBoxToSand3d(world.min, world.max)
    if (box.width < 0.05 || box.depth < 0.05 || box.height < 0.05) return

    const baseName = (mesh.name || 'part').replace(/\s+/g, '_')
    const count = (seenNames.get(baseName) ?? 0) + 1
    seenNames.set(baseName, count)

    const material = Array.isArray(mesh.material) ? mesh.material[0] : mesh.material
    const standard = material as THREE.MeshStandardMaterial | undefined
    const color =
      standard && 'color' in standard && standard.color && !standard.color.equals(new THREE.Color(0xffffff))
        ? hexOf(standard.color)
        : null

    pieces.push({
      name: count > 1 ? `${baseName}_${count}` : baseName,
      width: box.width,
      depth: box.depth,
      height: box.height,
      x: box.x,
      y: box.y,
      z: box.z,
      color,
      volume: box.width * box.depth * box.height,
    })
  })

  pieces.sort((a, b) => b.volume - a.volume)
  const limit = 40
  const kept = pieces.slice(0, limit)
  const dropped = pieces.length - kept.length

  const lines: string[] = []
  lines.push('import sand3d as s3d')
  lines.push('')
  lines.push(`# 由模型「${assetName}」近似反推：${kept.length} 个零件，尺寸与位置取自各自的世界包围盒。`)
  // 单个网格、几千个三角面：这段程序只剩一个盒子，形状基本没了。写在文件里，别让下一个人当真。
  const warning =
    kept.length === 1 && triangleCount >= 2000
      ? `这个资产是 1 个网格、约 ${Math.round(triangleCount)} 个三角面，盒子只能还原外轮廓尺寸，形状（圆柱、环、曲面）会完全丢失。要改形状请到「用代码新建」重新写程序，别依赖这段。`
      : undefined
  if (warning) lines.push(`# 注意：${warning}`)
  lines.push('# 数字单位是毫米（sand3d 的默认长度单位）；three.js 里的米已乘 1000，Y-up 已换成 Z-up。')
  if (dropped > 0) lines.push(`# 另外 ${dropped} 个过小的网格没有写进来。`)
  lines.push('# 盒子只能还原占地与高度：球、圆柱、倒角这些曲面，形状会退化成方料。')
  lines.push('# 要精确，就把对应的 box 换成 s3d.cylinder / s3d.sphere / s3d.extrude。')
  lines.push('s3d.empty_scene()')
  lines.push('')

  const variables: string[] = []
  kept.forEach((piece, index) => {
    const variable = identifier(piece.name, index + 1)
    variables.push(variable)
    lines.push(
      `${variable} = s3d.box("${piece.name}", ${tidy(piece.width)}, ${tidy(piece.depth)}, ${tidy(piece.height)})`,
    )
    lines.push(`s3d.at(${variable}, ${tidy(piece.x)}, ${tidy(piece.y)}, ${tidy(piece.z)})`)
    if (piece.color) lines.push(`s3d.material(${variable}, "plastic", color="${piece.color}")`)
    lines.push('')
  })

  if (variables.length === 0) {
    lines.push('# 这个模型没有可用的网格，先补一个占位件再改')
    lines.push('placeholder = s3d.box("placeholder", 50.0, 30.0, 6.0)')
    lines.push('s3d.export(placeholder, "model.glb")')
    return { code: lines.join('\n'), parts: 0, note: '没有可用网格', warning }
  }

  if (variables.length === 1) {
    lines.push(`s3d.export(${variables[0]}, "model.glb")`)
  } else {
    lines.push(`assembly = s3d.group("model", ${variables.join(', ')})`)
    lines.push('s3d.export(assembly, "model.glb")')
  }

  return {
    code: lines.join('\n'),
    parts: variables.length,
    note: `按 ${variables.length} 个网格的包围盒近似生成`,
    warning,
  }
}
