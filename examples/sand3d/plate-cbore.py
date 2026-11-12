import sand3d as s3d

# 一张带四个沉孔的法兰底板：120 × 80 × 10，顶边 R6 圆角，四孔 φ8.2 + 沉孔 φ14 深 3
base = s3d.box("base", 120, 80, 10)
s3d.fillet(base, 6, edges="top")
s3d.hole(base, 8.2, on=s3d.grid(2, 2, 92, 56), cbore=(14, 3))
s3d.material(base, "steel", color="#aab4c2")

s3d.export(base, "plate.glb")
