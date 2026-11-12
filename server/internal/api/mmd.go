package api

// PMX / .blend 产品内导入：扫描文件夹时遇到这两种源文件，服务端调用本机 Blender
// 无头转成 GLB，再走和普通 GLB 一样的入库路径。浏览器不解析这两类文件。
//
// 与 scripts/mmd2glb.py 共用同一套转换做法；依赖（mmd_tools + opencc）默认放在
// 仓库 tools/mmd 下，也可用 MMD_TOOLS_DIR / MMD_OPENCC_DIR 指向别处。

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed mmd/convert.py
var mmdConvertScript []byte

const (
	// 单个 PMX 36 MB 在本机约 1 分钟；留足余量，同时保证取消能杀掉 Blender。
	mmdConvertTimeout = 10 * time.Minute
	mmdOutputLimit    = 256 << 10
)

// mmdStatus 是 GET /api/mmd/status 的返回：PMX 与 .blend 的要求不同，
// .blend 只要有 Blender，PMX 还需要 mmd_tools + opencc。
type mmdStatus struct {
	Available bool   `json:"available"`
	PMXReady  bool   `json:"pmxReady"`
	Blender   string `json:"blender,omitempty"`
	Tools     string `json:"tools,omitempty"`
	OpenCC    string `json:"opencc,omitempty"`
	Error     string `json:"error,omitempty"`
}

type mmdPaths struct {
	blender string
	tools   string
	opencc  string
	status  mmdStatus
}

// normalizeMMDPackage 既接受包目录本身，也接受包含它的父目录，统一返回父目录。
func normalizeMMDPackage(path, pkg, marker string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if fileExists(filepath.Join(path, pkg, marker)) {
		return path
	}
	if filepath.Base(path) == pkg && fileExists(filepath.Join(path, marker)) {
		return filepath.Dir(path)
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// mmdRepoRoot 找仓库里的 tools/mmd。go run 的 cwd 是 server/，打包后的可执行文件
// 通常在 server/ 或仓库根，所以两个位置都试。
func mmdRepoRoot() string {
	var candidates []string
	for _, base := range []string{"", "..", "../.."} {
		candidates = append(candidates, filepath.Join(base, "tools", "mmd"))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "tools", "mmd"),
			filepath.Join(dir, "..", "tools", "mmd"),
		)
	}
	for _, candidate := range candidates {
		if fileExists(filepath.Join(candidate, "mmd_tools", "__init__.py")) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func resolveMMD() mmdPaths {
	result := mmdPaths{status: mmdStatus{}}

	blender := ""
	if custom := strings.TrimSpace(os.Getenv("MMD_BLENDER")); custom != "" {
		blender = resolvePython(custom)
	}
	if blender == "" {
		for _, candidate := range sand3dBlenderCandidates() {
			if resolved := resolvePython(candidate); resolved != "" {
				blender = resolved
				break
			}
		}
	}
	if blender != "" {
		result.blender = blender
		result.status.Blender = blender
	}

	root := mmdRepoRoot()
	tools := strings.TrimSpace(os.Getenv("MMD_TOOLS_DIR"))
	if tools == "" {
		tools = root
	}
	opencc := strings.TrimSpace(os.Getenv("MMD_OPENCC_DIR"))
	if opencc == "" {
		opencc = root
	}
	result.tools = normalizeMMDPackage(tools, "mmd_tools", "__init__.py")
	result.opencc = normalizeMMDPackage(opencc, "opencc", filepath.Join("config", "s2t.json"))
	result.status.Tools = result.tools
	result.status.OpenCC = result.opencc

	switch {
	case blender == "":
		result.status.Error = "没有找到 Blender：请安装 Blender，或把 MMD_BLENDER 指向 blender.exe"
	case result.tools == "":
		result.status.Error = "没有找到 mmd_tools：把依赖放到 tools/mmd/mmd_tools，或设置 MMD_TOOLS_DIR"
	case result.opencc == "":
		result.status.Error = "没有找到 opencc：把解包目录放到 tools/mmd/opencc，或设置 MMD_OPENCC_DIR"
	}

	result.status.Available = blender != ""
	result.status.PMXReady = result.status.Available && result.tools != "" && result.opencc != ""
	if result.status.Available && !result.status.PMXReady && result.status.Error == "" {
		result.status.Error = "PMX 需要 mmd_tools 与 opencc；.blend 只需 Blender"
	}
	return result
}

func (a *API) mmdStatusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, resolveMMD().status)
}

// convertMMD 把 src 转成 GLB 并返回临时文件路径；调用方负责在入库后删掉整个临时目录。
func convertMMD(ctx context.Context, src, mode string) (glbPath, cleanup string, err error) {
	if abs, absErr := filepath.Abs(src); absErr == nil {
		src = abs
	}
	paths := resolveMMD()
	if !paths.status.Available {
		return "", "", errors.New(paths.status.Error)
	}
	if mode == "pmx" && !paths.status.PMXReady {
		return "", "", errors.New(paths.status.Error)
	}

	work, err := os.MkdirTemp("", "mmd-convert-*")
	if err != nil {
		return "", "", fmt.Errorf("创建转换临时目录失败: %w", err)
	}
	scriptPath := filepath.Join(work, "convert.py")
	if err := os.WriteFile(scriptPath, mmdConvertScript, 0o600); err != nil {
		_ = os.RemoveAll(work)
		return "", "", fmt.Errorf("写入转换脚本失败: %w", err)
	}
	glbPath = filepath.Join(work, "model.glb")

	runCtx, cancel := context.WithTimeout(ctx, mmdConvertTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, paths.blender,
		"--background", "--factory-startup", "--python", scriptPath, "--",
		mode, src, glbPath, "--addons", paths.tools, "--opencc", paths.opencc,
	)
	cmd.Dir = filepath.Dir(src)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	stdout := newLimitedBuffer(mmdOutputLimit)
	stderr := newLimitedBuffer(mmdOutputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if runErr != nil {
		_ = os.RemoveAll(work)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", "", fmt.Errorf("转换超时（超过 %d 分钟）", int(mmdConvertTimeout.Minutes()))
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", "", context.Canceled
		}
		detail := tailLines(stderr.String(), 6)
		if detail == "" {
			detail = tailLines(stdout.String(), 6)
		}
		if detail == "" {
			detail = runErr.Error()
		}
		return "", "", fmt.Errorf("Blender 转换失败: %s", detail)
	}

	info, statErr := os.Stat(glbPath)
	if statErr != nil || info.Size() == 0 {
		_ = os.RemoveAll(work)
		return "", "", errors.New("Blender 没有产出 GLB，检查源文件是否有可导出的可见网格")
	}
	return glbPath, work, nil
}

// tailLines 只留最后几行，避免把 Blender 几百行警告全塞进作业失败原因。
func tailLines(raw string, n int) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " | ")
}
