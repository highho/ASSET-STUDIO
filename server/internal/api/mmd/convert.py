# -*- coding: utf-8 -*-
"""Asset Studio 内嵌的 PMX / .blend -> GLB 转换脚本。

由 server/internal/api/mmd.go 写入临时文件，再交给 Blender 无头执行：

    blender --background --factory-startup --python convert.py -- ^
      pmx <src> <dst> <mmd_root> <opencc_root>

与仓库里的 scripts/mmd2glb.py 保持同一套做法（PMX 直接 import + register、
convert_materials、use_visible），改这里时同步改那边。
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
    parser.add_argument('--no-materials', action='store_true')
    return parser.parse_args(argv)


def package_root(path, package, marker):
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
        raise SystemExit('找不到 mmd_tools 目录')
    if not opencc_root:
        raise SystemExit('找不到 opencc 解包目录')

    bpy.ops.wm.read_factory_settings(use_empty=True)
    sys.path.insert(0, opencc_root)
    sys.path.insert(0, addons_root)
    import mmd_tools  # noqa: E402

    mmd_tools.register()
    bpy.ops.mmd_tools.import_model(
        filepath=src,
        scale=0.08,
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
    bpy.ops.export_scene.gltf(filepath=args.dst, export_format='GLB', export_yup=True, use_visible=True)
    print('EXPORTED', args.dst)


main()
