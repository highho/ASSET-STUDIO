package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeScanFormat(t *testing.T) {
	cases := map[string]string{
		".pmx":   "pmx",
		".PMX":   "pmx",
		".blend": "blend",
		".glb":   "glb",
		".md":    "",
	}
	for raw, want := range cases {
		if got := normalizeScanFormat(raw); got != want {
			t.Errorf("normalizeScanFormat(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestResolveScanFormatsIncludesSources(t *testing.T) {
	defaults := resolveScanFormats(nil)
	for _, format := range []string{"glb", "pmx", "blend"} {
		if !defaults[format] {
			t.Errorf("默认扫描格式缺少 %s", format)
		}
	}
	if defaults["png"] {
		t.Error("默认扫描格式不应包含 png")
	}

	explicit := resolveScanFormats([]string{"pmx"})
	if len(explicit) != 1 || !explicit["pmx"] {
		t.Errorf("显式 PMX 扫描应只保留 pmx，得到 %v", explicit)
	}
}

func TestNormalizeMMDPackage(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "mmd_tools")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "__init__.py"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := normalizeMMDPackage(root, "mmd_tools", "__init__.py"); got != root {
		t.Errorf("父目录 = %q, want %q", got, root)
	}
	if got := normalizeMMDPackage(pkg, "mmd_tools", "__init__.py"); got != root {
		t.Errorf("包目录 = %q, want %q", got, root)
	}
	if got := normalizeMMDPackage(filepath.Join(root, "missing"), "mmd_tools", "__init__.py"); got != "" {
		t.Errorf("不存在的路径 = %q, want empty", got)
	}
}

func TestTailLines(t *testing.T) {
	got := tailLines("a\n\n b \nc", 6)
	if got != "a | b | c" {
		t.Errorf("tailLines = %q", got)
	}
	if got := tailLines("  \n ", 3); !strings.EqualFold(got, "") {
		t.Errorf("空白应返回空串，得到 %q", got)
	}
}
