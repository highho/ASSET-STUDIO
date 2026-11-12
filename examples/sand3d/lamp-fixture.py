import sand3d as s3d

# 一盏工作灯：法兰底板（4 沉孔）+ 立柱 + 悬臂 + 黄铜灯头。
# 底板 120×80×10，立柱 φ16 高 130，悬臂 96×20×14，灯头 φ52 高 26。
def lamp():
    base = s3d.box("base", 120, 80, 10)
    s3d.fillet(base, 6, edges="top")
    s3d.hole(base, 8.2, on=s3d.grid(2, 2, 92, 56), cbore=(14, 3))
    s3d.material(base, "steel", color="#8a93a0")

    post = s3d.cylinder("post", 16, 130)
    s3d.at(post, 0, 0, 10)
    s3d.material(post, "brushed_steel")

    arm = s3d.box("arm", 96, 20, 14)
    s3d.fillet(arm, 4, edges="top")
    s3d.at(arm, 40, 0, 140)
    s3d.material(arm, "steel", color="#8a93a0")

    head = s3d.cylinder("head", 52, 26)
    s3d.at(head, 92, 0, 146)
    s3d.material(head, "brass")

    return s3d.group("lamp", base, post, arm, head)


s3d.export(lamp(), "lamp.glb")
