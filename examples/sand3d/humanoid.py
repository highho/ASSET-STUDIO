import math

import sand3d as s3d

# 参数化人偶：只改下面这几个数，就能改身高、肩宽、四肢粗细和站姿。
# 它不追求像真人——是用 box / cylinder / sphere 拼出的「可改比例的人形」。
# 人体网格没法反推成程序（那是逆问题，没有唯一解），能做的是用参数造一个人。
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

# 脚
for side, sign in (("left", -1), ("right", 1)):
    foot = s3d.box(f"foot_{side}", 110, 260, 45)
    s3d.at(foot, sign * STANCE / 2, 0, 0, anchor="bottom")
    s3d.material(foot, "plastic", color="#2f3440")
    parts.append(foot)

# 小腿、大腿
for side, sign in (("left", -1), ("right", 1)):
    calf = s3d.cylinder(f"calf_{side}", 110 * THICK, shin)
    s3d.at(calf, sign * STANCE / 2, 0, ankle, anchor="bottom")
    s3d.material(calf, "plastic", color="#4a5260")
    parts.append(calf)

    leg = s3d.cylinder(f"thigh_{side}", 150 * THICK, thigh)
    s3d.at(leg, sign * STANCE / 2, 0, knee, anchor="bottom")
    s3d.material(leg, "plastic", color="#3a4150")
    parts.append(leg)

# 胯、躯干
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

# 脖子、头
neck = s3d.cylinder("neck", 100, neck_h)
s3d.at(neck, 0, 0, shoulder_z, anchor="bottom")
s3d.material(neck, "plastic", color=SKIN)
parts.append(neck)

head_ball = s3d.sphere("head", head * 0.92)
s3d.at(head_ball, 0, 0, shoulder_z + neck_h + head * 0.40)
s3d.material(head_ball, "plastic", color=SKIN)
parts.append(head_ball)

# 手臂：肩点 → 上臂 → 前臂 → 手，一个外张角贯穿
upper = H * 0.175
fore = H * 0.16
a1 = math.radians(SPREAD)
a2 = math.radians(SPREAD * 0.6)

for side, sign in (("left", -1), ("right", 1)):
    joint_x = sign * (SHOULDER / 2 - 10)
    joint_z = shoulder_z - head * 0.05

    # 肩头：把躯干和上臂接上，不然肩是断的
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
