# -*- coding: utf-8 -*-
"""把 PMX / .blend 人物模型转成 GLB，供 Asset Studio 导入。

由 Blender 无头执行（本机验证于 Blender 5.0.1 + mmd_tools 4.5.14）：

    blender --background --factory-startup --python scripts/mmd2glb.py -- ^
      pmx H:\mmd\蕾米埃尔-白\蕾米埃尔-白.pmx out\remiel-white.glb

    blender --background --factory-startup --python scripts/mmd2glb.py -- ^
      blend H:\mmd\模型\02_Claude_V3\model.blend out\claude.glb

PMX 依赖 mmd_tools 与 opencc 的**解包目录**（不是 .whl / .zip 本身）：
    $env:MMD_TOOLS_DIR  = ...\mmd_tools      # 目录里要有 __init__.py
    $env:MMD_OPENCC_DIR = ...\opencc         # 目录里要有 config\s2t.json
也可以用 --addons / --opencc 显式传入。

为什么不是直接 addon_utils.enable：
    Blender 5.0 上 enable('mmd_tools') 会去 wheels 目录找依赖并失败；
    addons 声明的最低版本是 4.2，直接 import + register() 才稳定。

为什么导出前要 convert_materials：
    PMX 的材质是 MMD 专有节点（mmd_base_tex 等），glTF 导出器只认
    Principled BSDF；不转的话贴图全部丢失，只剩下 baseColorFactor。

为什么要 use_visible：
    mmd_tools 会为每个刚体/关节建隐藏的代理网格（一个模型 700+ 个），
    glTF 导出器默认连隐藏对象一起导出，结果人物外面套了一层白模。
"""
import argparse
import os
import sys

import bpy


def parse_args():
    argv = sys.argv[sys.argv.index('--') + 1:] if '--' in sys.argv else []
    parser = argparse.ArgumentParser(description='PMX / .blend -> GLB')
    parser.add_argument('mode', choices=['pmx', 'blend'])
    parser.add_argument('src')
    parser.add_argument('dst')
    parser.add_argument('--addons', default=os.environ.get('MMD_TOOLS_DIR', ''))
    parser.add_argument('--opencc', default=os.environ.get('MMD_OPENCC_DIR', ''))
    parser.add_argument('--no-materials', action='store_true', help='跳过 Principled 材质转换')
    return parser.parse_args(argv)


def package_root(path, package, marker):
    """把「包目录本身」和「包含包的父目录」都收敛成可放进 sys.path 的父目录。"""
    path = os.path.abspath(path.rstrip('\\/'))
    if os.path.isfile(os.path.join(path, package, marker)):
        return path
    if os.path.basename(path) == package and os.path.isfile(os.path.join(path, marker)):
        return os.path.dirname(path)
    return ''


def import_pmx(src, addons_dir, opencc_dir):
    addons_root = package_root(addons_dir, 'mmd_tools', '__init__.py') if addons_dir else ''
    opencc_root = package_root(opencc_dir, 'opencc', os.path.join('config', 's2t.json')) if opencc_dir else ''
    if not addons_root:
        raise SystemExit('找不到 mmd_tools 目录，请设置 MMD_TOOLS_DIR 或传 --addons（指向包含 mmd_tools\\__init__.py 的目录）')
    if not opencc_root:
        raise SystemExit('找不到 opencc 解包目录，请设置 MMD_OPENCC_DIR 或传 --opencc（目录里要有 opencc\\config\\s2t.json）')

    bpy.ops.wm.read_factory_settings(use_empty=True)
    sys.path.insert(0, opencc_root)
    sys.path.insert(0, addons_root)
    import mmd_tools  # noqa: E402

    mmd_tools.register()
    bpy.ops.mmd_tools.import_model(
        filepath=src,
        scale=0.08,          # MMD 的 1 单位 ≈ 0.08 m，20 单位高的人物约 1.6 m
        clean_model=True,
        remove_doubles=False,
        fix_ik_links=True,
        rename_bones=True,
    )


def convert_materials():
    root = next(
        (o for o in bpy.context.scene.objects if o.type == 'EMPTY' and getattr(o, 'mmd_type', '') == 'ROOT'),
        None,
    )
    if root is None:
        raise SystemExit('导入后没有找到 mmd_tools 的 ROOT 空对象')

    bpy.ops.object.select_all(action='DESELECT')
    for obj in bpy.context.scene.objects:
        if obj.type == 'MESH' and getattr(obj, 'mmd_type', 'NONE') == 'NONE':
            obj.select_set(True)
    bpy.context.view_layer.objects.active = root
    bpy.ops.mmd_tools.convert_materials(use_principled=True, clean_nodes=True, subsurface=False)


def report():
    meshes = [o for o in bpy.context.scene.objects if o.type == 'MESH' and o.visible_get()]
    tris = 0
    for obj in meshes:
        obj.data.calc_loop_triangles()
        tris += len(obj.data.loop_triangles)
    mats = {m.name for o in meshes for m in o.data.materials if m}
    print('MESHES', len(meshes), 'TRIS', tris, 'MATS', len(mats))


def main():
    args = parse_args()
    if args.mode == 'pmx':
        import_pmx(args.src, args.addons, args.opencc)
        if not args.no_materials:
            convert_materials()
    else:
        bpy.ops.wm.open_mainfile(filepath=args.src)

    report()
    os.makedirs(os.path.dirname(os.path.abspath(args.dst)) or '.', exist_ok=True)
    # 隐藏的刚体 / 关节代理网格不能进 GLB，所以只导出可见对象。
    bpy.ops.export_scene.gltf(filepath=args.dst, export_format='GLB', export_yup=True, use_visible=True)
    print('EXPORTED', args.dst)


main()
