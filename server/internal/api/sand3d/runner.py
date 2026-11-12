"""跑一段 sand3d 程序，产出 GLB。

由 asset-studio 后端调用，两种运行时都走这个脚本：
    python  runner.py <program.py> <workdir> <out.glb>
    blender --background --factory-startup --python runner.py -- <program.py> <workdir> <out.glb>

程序以 exec 执行，命名空间里 __name__ 是 "__main__"。程序自己调 s3d.export 就把它
写出的文件搬过来；没调 export 时，兜底导出场景里第一个网格 —— 这样「写了几行
verb 却没写 export」的程序也能直接看到结果，而不是只拿到一句报错。

运行结果写进 <workdir>/meta.json，不走 stdout：stdout 是程序自己的输出通道，
用户程序打印一行 "S3D_META {...}" 不应该能改写我们报告的面数与零件表。
"""
import json
import os
import shutil
import sys
import time
import traceback

META_FILE = "meta.json"


def script_args() -> list[str]:
    """Blender 会把 blender.exe 和 --background 之类一起塞进 sys.argv，
    真正的脚本参数在 `--` 之后；直接跑 python 时就是 argv[1:]。"""
    argv = sys.argv
    if "--" in argv:
        return argv[argv.index("--") + 1:]
    return argv[1:]


def main() -> int:
    args = script_args()
    script = os.path.abspath(args[0])
    workdir = os.path.abspath(args[1])
    target = os.path.abspath(args[2])
    os.chdir(workdir)

    # 源码形态的 sand3d（没有 pip install）靠这个目录进来，必须在 import 之前挂上。
    src = os.environ.get("SAND3D_SRC", "").strip()
    if src:
        sys.path.insert(0, src)

    t0 = time.time()
    import sand3d as s3d
    import bpy

    exported = []
    real_export = s3d.export

    def export(root, path):
        absolute = os.path.abspath(str(path))
        result = real_export(root, absolute)
        exported.append(absolute)
        return result

    s3d.export = export

    code = open(script, encoding="utf-8").read()
    namespace = {"__name__": "__main__", "__file__": script}

    s3d.empty_scene()
    exec(compile(code, script, "exec"), namespace)

    source = None
    for path in reversed(exported):
        if os.path.exists(path):
            source = path
            break

    meshes = [ob for ob in bpy.context.scene.objects if ob.type == "MESH"]
    if source is None:
        if not meshes:
            raise RuntimeError(
                "程序没有生成任何网格：确认调用了 s3d.box / s3d.cylinder / s3d.extrude 这类建模 verb"
            )
        s3d.export(meshes[0], target)
        source = target

    if os.path.abspath(source) != target:
        shutil.copyfile(source, target)

    faces = int(sum(len(ob.data.polygons) for ob in meshes))
    verts = int(sum(len(ob.data.vertices) for ob in meshes))
    meta = {
        "ok": True,
        "version": s3d.__version__,
        "fingerprint": s3d.fingerprint(),
        "units": s3d.units(),
        "objects": [ob.get("s3d_name", ob.name) for ob in meshes],
        "faces": faces,
        "vertices": verts,
        "bytes": os.path.getsize(target),
        "seconds": round(time.time() - t0, 3),
    }
    with open(os.path.join(workdir, META_FILE), "w", encoding="utf-8", newline="\n") as fh:
        json.dump(meta, fh, ensure_ascii=False)
    return 0

if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except BaseException:
        traceback.print_exc()
        sys.exit(1)
