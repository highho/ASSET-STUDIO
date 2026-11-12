package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// sidecar 路径来自用户上传时的表单，是不可信输入：这里锁住「只能落到资产自己的目录里」。
func TestSanitizeSidecarPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通文件名", "texture.png", "texture.png"},
		{"子目录", "textures/albedo.png", filepath.Join("textures", "albedo.png")},
		{"反斜杠规范化", `textures\albedo.png`, filepath.Join("textures", "albedo.png")},
		{"开头的当前目录", "./texture.png", "texture.png"},
		{"上跳被丢掉", "../../secret.png", "secret.png"},
		{"夹在中间的上跳", "a/../../b.bin", filepath.Join("a", "b.bin")},
		{"盘符被替换", "C:/Windows/win.ini", filepath.Join("C_", "Windows", "win.ini")},
		{"只有当前目录", ".", ""},
		{"只有上跳", "..", ""},
		{"空串", "", ""},
		{"全是分隔符", "//\\", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeSidecarPath(c.in)
			if got != c.want {
				t.Fatalf("sanitizeSidecarPath(%q) = %q, want %q", c.in, got, c.want)
			}
			// 无论输入长什么样，结果都不能指到目录外，也不能是绝对路径
			if strings.Contains(got, "..") {
				t.Fatalf("结果里不该出现上跳：%q", got)
			}
			if len(got) > 0 && (got[0] == '/' || got[0] == '\\') {
				t.Fatalf("结果不该是绝对路径：%q", got)
			}
			if len(got) >= 2 && got[1] == ':' {
				t.Fatalf("结果不该带盘符：%q", got)
			}
		})
	}
}
