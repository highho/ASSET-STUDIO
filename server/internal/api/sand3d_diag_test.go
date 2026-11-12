package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSand3dDiagnosePicksInnermostUserFrame(t *testing.T) {
	// 典型 traceback：runner.py 先，program.py 在中间，最里面是 sand3d 的报错。
	stderr := strings.Join([]string{
		"Traceback (most recent call last):",
		`  File "runner.py", line 45, in <module>`,
		"    exec(compile(code, script, \"exec\"), namespace)",
		`  File "C:\Users\x\AppData\Local\Temp\sand3d-run-1\program.py", line 7, in <module>`,
		"    s3d.hole(plate, 6.6, on=s3d.grid(2, 1, 50, 0))",
		`  File "sand3d\ops.py", line 210, in hole`,
		"    raise ValueError(msg)",
		"ValueError: hole diameter 6.6 is larger than plate thickness 6",
	}, "\n")

	message, line := sand3dDiagnose(stderr, "")
	if line != 7 {
		t.Fatalf("行号应为 7，实际 %d", line)
	}
	if !strings.Contains(message, "hole diameter 6.6") {
		t.Fatalf("诊断应带异常原话，实际 %q", message)
	}
	if strings.Contains(message, "runner.py") {
		t.Fatalf("诊断不该指向 runner 自己的帧：%q", message)
	}
}

func TestSand3dDiagnosePrefersLastProgramFrame(t *testing.T) {
	// 异常链：program.py 出现两次，最后一次才是用户真正出错的位置（第 12 行）。
	stderr := strings.Join([]string{
		"Traceback (most recent call last):",
		`  File "program.py", line 5, in rerun`,
		"    helper()",
		`  File "program.py", line 12, in helper`,
		"    s3d.fillet(plate, 3, edges=\"nope\")",
		"sand3d.errors.EdgeMissingError: plate has no edge named ['nope']",
	}, "\n")

	message, line := sand3dDiagnose(stderr, "")
	if line != 12 {
		t.Fatalf("应取最后一个 program.py 帧（12），实际 %d", line)
	}
	if !strings.Contains(message, "EdgeMissingError") {
		t.Fatalf("异常类型应保留，实际 %q", message)
	}
}

func TestSand3dDiagnoseIgnoresProgramNoise(t *testing.T) {
	// 程序自己打印的东西不当成失败原因：真正的异常才是最后那一行。
	stderr := strings.Join([]string{
		"building plate...",
		"Error: 我在程序里随手打印的一行",
		"Traceback (most recent call last):",
		`  File "program.py", line 3, in <module>`,
		"    raise RuntimeError(\"boom\")",
		"RuntimeError: boom",
	}, "\n")

	message, line := sand3dDiagnose(stderr, "")
	if line != 3 {
		t.Fatalf("行号应为 3，实际 %d", line)
	}
	if !strings.Contains(message, "RuntimeError: boom") {
		t.Fatalf("应取真正的异常行，实际 %q", message)
	}
}

func TestTruncateCutsByRune(t *testing.T) {
	text := strings.Repeat("中文报错", 10) // 40 个汉字
	got := truncate(text, 5)
	if !strings.HasPrefix(got, "中文报错中") {
		t.Fatalf("截断应按字符：%q", got)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("截断产生了半个字：%q", got)
	}
	if !strings.Contains(got, "已截断") {
		t.Fatalf("应带截断标记：%q", got)
	}
}

func TestReadSand3dMetaFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, sand3dMetaFile)
	payload := `{"version":"1.2.3","fingerprint":"abc","units":"mm","objects":["a"],"faces":12,"vertices":8}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := readSand3dMeta(path)
	if meta.Version != "1.2.3" || meta.Faces != 12 || len(meta.Objects) != 1 {
		t.Fatalf("meta 没读对：%+v", meta)
	}

	// 文件不存在时不该报错，返回零值即可。
	if meta := readSand3dMeta(filepath.Join(dir, "missing.json")); meta.Faces != 0 {
		t.Fatalf("缺失文件应返回零值：%+v", meta)
	}
}
