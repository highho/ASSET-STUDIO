package api

// sand3d 运行时：把检视台里写下的 Python 程序交给本机装了 bpy + sand3d 的解释器
// 真跑一遍，导出的 GLB 直接回传。浏览器里没有 Blender，所以几何一律以这次真跑为准。

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed sand3d/runner.py
var sand3dRunner []byte

const (
	// 运行结果（版本、指纹、零件表、面数）写在 <workdir>/meta.json 里，
	// 不从 stdout 读：stdout 是用户程序的地盘，程序打印什么都可能。
	sand3dMetaFile = "meta.json"

	sand3dTimeout = 180 * time.Second

	// 探测：每个候选最多等这么久，整轮探测有总预算，避免点了状态接口却挂几分钟。
	sand3dProbeTimeout = 20 * time.Second
	sand3dProbeBudget  = 45 * time.Second
	// 探测结论的缓存时长：成功缓存久一点（装好了不会轻易消失），
	// 失败只缓存一分钟（用户 pip install 完不用重启服务就能用上）。
	sand3dSuccessTTL = 10 * time.Minute
	sand3dFailureTTL = time.Minute

	// 单次运行保留的 stdout / stderr 上限。程序里 while True: print() 也不会把内存打爆。
	sand3dOutputLimit = 256 << 10
)

// 每个 bpy 进程要几百 MB 内存，同时最多跑两个。
var sand3dSlots = make(chan struct{}, 2)

var (
	sand3dProbeMu   sync.Mutex
	sand3dCache     *sand3dStatus
	sand3dCacheTime time.Time
)

type sand3dRequest struct {
	Code string `json:"code"`
}

type sand3dStatus struct {
	Available bool   `json:"available"`
	// Runtime 是跑这段程序的方式：`blender` 用 Blender 自带的 bpy（sand3d 源码挂在
	// PYTHONPATH 上），`python` 用 pip 装的 bpy 解释器。两种都要能跑。
	Runtime string `json:"runtime,omitempty"`
	// Python 是真正被执行的可执行文件路径：python 模式下是解释器，blender 模式下是 blender.exe。
	Python  string `json:"python,omitempty"`
	Version string `json:"version,omitempty"`
	// Source 是 sand3d 源码目录（其下要有 sand3d/__init__.py）。
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

type sand3dMeta struct {
	Version     string   `json:"version"`
	Fingerprint string   `json:"fingerprint"`
	Units       string   `json:"units"`
	Objects     []string `json:"objects"`
	Faces       int      `json:"faces"`
	Vertices    int      `json:"vertices"`
	Bytes       int      `json:"bytes"`
	Seconds     float64  `json:"seconds"`
	ElapsedMs   int64    `json:"elapsedMs"`
}

// sand3dPythonCandidates 按优先级给出可能装了 bpy + sand3d 的解释器。
// 只留真正可能存在的几条：挨个起进程探测一次要几十秒，候选越多越慢。
func sand3dPythonCandidates() []string {
	list := []string{}
	if custom := strings.TrimSpace(os.Getenv("SAND3D_PYTHON")); custom != "" {
		list = append(list, custom)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		list = append(list, filepath.Join(local, "Programs", "Python", "Python311", "python.exe"))
		list = append(list, filepath.Join(local, "Programs", "Python", "Python313", "python.exe"))
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		list = append(list, filepath.Join(home, "tools", "python311", "python.exe"))
	}
	// 用户自己往非默认盘装的解释器也要认，否则「明明装了却探测不到」。
	list = append(list, "E:\\python\\python.exe")
	list = append(list, "python3.11", "python3.13", "python")
	return list
}

// sand3dBlenderCandidates 给出可能的 Blender 可执行文件。
// 很多机器上并没有 pip 装 bpy（那个包 300 MB 且只认特定 Python 版本），
// 但装了 Blender —— bpy 就在 Blender 进程里，走 `blender --background` 一样能跑 sand3d。
func sand3dBlenderCandidates() []string {
	list := []string{}
	if custom := strings.TrimSpace(os.Getenv("SAND3D_BLENDER")); custom != "" {
		list = append(list, custom)
	}
	for _, pf := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if pf == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(pf, "Blender Foundation", "Blender*", "blender.exe"))
		if err != nil {
			continue
		}
		sort.Sort(sort.Reverse(sort.StringSlice(matches))) // 版本号大的排前面
		list = append(list, matches...)
	}
	// UPBGE 这种 Blender 分支常被直接解压到某个盘根目录，先按已知落点找，再退回 PATH。
	list = append(list,
		"D:\\upbge-0.50-windows-x64\\blender.exe",
		"D:\\upbge\\blender.exe",
		"blender",
	)
	return list
}

// sand3dSourceDir 找一份能 import 的 sand3d 源码目录：它下面必须有 sand3d/__init__.py。
// 有的机器是 pip install sand3d（包在 site-packages 里，这里返回空即可），
// 有的是把源码挂进来（如 H:\sand3d-plugin\_probe\sand3d_src，需要别名成 sand3d）。
func sand3dSourceDir() string {
	var list []string
	if custom := strings.TrimSpace(os.Getenv("SAND3D_SRC")); custom != "" {
		list = append(list, custom)
	}
	if wd, err := os.Getwd(); err == nil {
		list = append(list, filepath.Join(wd, "runtime"), filepath.Join(wd, "..", "runtime"))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		list = append(list, filepath.Join(dir, "runtime"), filepath.Join(dir, "..", "runtime"))
	}
	for _, dir := range list {
		abs, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "sand3d", "__init__.py")); err == nil {
			return abs
		}
	}
	return ""
}

type sand3dCandidate struct {
	kind string // "blender" | "python"
	path string
}

func sand3dCandidates() []sand3dCandidate {
	list := []sand3dCandidate{}
	for _, path := range sand3dBlenderCandidates() {
		list = append(list, sand3dCandidate{kind: "blender", path: path})
	}
	for _, path := range sand3dPythonCandidates() {
		list = append(list, sand3dCandidate{kind: "python", path: path})
	}
	return list
}

func resolvePython(candidate string) string {
	if !strings.Contains(candidate, string(os.PathSeparator)) {
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
		return ""
	}
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}

// sand3dProbeExpr 是探测用的那段 Python：先挂源码目录，再 import，最后打一行带标记的版本号。
// Blender 不读 PYTHONPATH（除非加 --python-use-system-env），所以统一用 sys.path 注入。
func sand3dProbeExpr(src string) string {
	prefix := ""
	if src != "" {
		prefix = "sys.path.insert(0, r'" + src + "'); "
	}
	return "import sys; " + prefix + "import sand3d; print('S3D_VERSION', sand3d.__version__)"
}

func sand3dProbeCommand(ctx context.Context, c sand3dCandidate, src, expr string) *exec.Cmd {
	if c.kind == "blender" {
		return exec.CommandContext(ctx, c.path, "--background", "--factory-startup", "--python-expr", expr)
	}
	return exec.CommandContext(ctx, c.path, "-c", expr)
}

var sand3dVersionRe = regexp.MustCompile(`S3D_VERSION\s+(\S+)`)

// probeSand3d 探一次「哪个运行时能 import sand3d」。
// 结论缓存：成功 10 分钟、失败 1 分钟，用户装完依赖不用重启。
func probeSand3d() *sand3dStatus {
	sand3dProbeMu.Lock()
	defer sand3dProbeMu.Unlock()
	if sand3dCache != nil {
		ttl := sand3dFailureTTL
		if sand3dCache.Available {
			ttl = sand3dSuccessTTL
		}
		if time.Since(sand3dCacheTime) < ttl {
			return sand3dCache
		}
	}

	src := sand3dSourceDir()
	expr := sand3dProbeExpr(src)
	deadline := time.Now().Add(sand3dProbeBudget)
	problems := []string{}
	for _, candidate := range sand3dCandidates() {
		remain := time.Until(deadline)
		if remain <= 0 {
			problems = append(problems, "探测总时长已到上限")
			break
		}
		path := resolvePython(candidate.path)
		if path == "" {
			continue
		}
		if remain > sand3dProbeTimeout {
			remain = sand3dProbeTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), remain)
		var stdout, stderr bytes.Buffer
		cmd := sand3dProbeCommand(ctx, sand3dCandidate{kind: candidate.kind, path: path}, src, expr)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		cancel()
		if runErr != nil {
			// 失败原因优先看 stderr：Python 的 ImportError / SyntaxError 都在那里，
			// 只读 stdout 会得到一句没用的 "exit status 1"。
			problems = append(problems, filepath.Base(path)+": "+probeProblem(stderr.String(), stdout.String(), runErr))
			continue
		}
		version := ""
		if match := sand3dVersionRe.FindStringSubmatch(stdout.String()); len(match) == 2 {
			version = match[1]
		}
		if version == "" {
			problems = append(problems, filepath.Base(path)+": 能 import sand3d 但没读到版本号")
			continue
		}
		sand3dCache = &sand3dStatus{
			Available: true,
			Runtime:   candidate.kind,
			Python:    path,
			Version:   version,
			Source:    src,
		}
		sand3dCacheTime = time.Now()
		return sand3dCache
	}

	message := "本机没有能跑 sand3d 的运行时：既没找到装了 bpy 的 Python（pip install sand3d），也没找到 Blender。" +
		"若 sand3d 源码挂在某个目录，设 SAND3D_SRC 指向它；若用的是 Blender，设 SAND3D_BLENDER 指向 blender.exe。"
	if len(problems) > 0 {
		shown := problems
		if len(shown) > 3 {
			shown = shown[:3]
		}
		message += "。已尝试：" + strings.Join(shown, "；")
	}
	sand3dCache = &sand3dStatus{Available: false, Error: message, Source: src}
	sand3dCacheTime = time.Now()
	return sand3dCache
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// probeProblem 从 stderr 里挑一句能说明问题的话，挑不到再看 stdout，最后退到退出状态。
func probeProblem(stderr, stdout string, err error) string {
	if line := lastLine(stderr); line != "" {
		return line
	}
	if line := lastLine(stdout); line != "" {
		return line
	}
	if err != nil {
		return err.Error()
	}
	return "启动失败"
}

func (a *API) sand3dStatusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, probeSand3d())
}

func (a *API) sand3dRun(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req sand3dRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeError(w, http.StatusBadRequest, "程序为空")
		return
	}

	status := probeSand3d()
	if !status.Available {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": status.Error,
			"detail": "真跑要落到本机装了 bpy 的运行时（pip 的 bpy，或 Blender 自带的 bpy）；" +
				"浏览器内只有解释器前端，几何结果以真跑为准。",
		})
		return
	}

	work, err := os.MkdirTemp("", "sand3d-run-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建临时目录失败："+err.Error())
		return
	}
	defer os.RemoveAll(work)

	programPath := filepath.Join(work, "program.py")
	runnerPath := filepath.Join(work, "runner.py")
	outputPath := filepath.Join(work, "out.glb")
	if err := os.WriteFile(programPath, []byte(code), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "写入程序失败："+err.Error())
		return
	}
	if err := os.WriteFile(runnerPath, sand3dRunner, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "写入执行器失败："+err.Error())
		return
	}

	// 先把请求自己的 ctx 建起来，再去抢运行槽位：
	// 客户端已经断开时不必再排队，也不该让一个死连接占着槽位。
	ctx, cancel := context.WithTimeout(r.Context(), sand3dTimeout)
	defer cancel()

	select {
	case sand3dSlots <- struct{}{}:
		defer func() { <-sand3dSlots }()
	default:
		// 两个槽位都占着时立刻返回 503，而不是把请求挂在这里等。
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":  "已有两个 sand3d 程序在跑，等其中一个结束再试",
			"detail": "bpy 每个进程要几百 MB 内存，故意限制成同时两个。",
		})
		return
	}

	started := time.Now()
	var cmd *exec.Cmd
	if status.Runtime == "blender" {
		// `--` 之后的参数才是脚本参数；`--factory-startup` 跳过用户配置里的插件，
		// 既快也避免把别人的 addon 带进一次几何计算。
		cmd = exec.CommandContext(ctx, status.Python, "--background", "--factory-startup",
			"--python", runnerPath, "--", programPath, work, outputPath)
	} else {
		cmd = exec.CommandContext(ctx, status.Python, runnerPath, programPath, work, outputPath)
	}
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "SAND3D_SRC="+status.Source, "PYTHONIOENCODING=utf-8")
	stdout := newLimitedBuffer(sand3dOutputLimit)
	stderr := newLimitedBuffer(sand3dOutputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	elapsed := time.Since(started)

	switch ctx.Err() {
	case context.DeadlineExceeded:
		writeError(w, http.StatusGatewayTimeout, "运行超过 "+strconv.Itoa(int(sand3dTimeout.Seconds()))+" 秒，已中止")
		return
	case context.Canceled:
		// 客户端自己断开（或页面关掉），没有可写的接收方，直接结束。
		return
	}
	if runErr != nil {
		message, line := sand3dDiagnose(stderr.String(), stdout.String())
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  message,
			"line":   line,
			"detail": truncate(stderr.String(), 6000),
		})
		return
	}

	glb, err := os.ReadFile(outputPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "程序跑完了但没有产出 GLB："+err.Error())
		return
	}

	meta := readSand3dMeta(filepath.Join(work, sand3dMetaFile))
	meta.Bytes = len(glb)
	meta.ElapsedMs = elapsed.Milliseconds()
	if meta.Version == "" {
		meta.Version = status.Version
	}
	patch, err := json.Marshal(meta)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "序列化运行结果失败")
		return
	}

	w.Header().Set("Content-Type", "model/gltf-binary")
	w.Header().Set("Content-Length", strconv.Itoa(len(glb)))
	w.Header().Set("X-Sand3D-Meta", url.PathEscape(string(patch)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(glb)
}

// readSand3dMeta 读执行器写下的 meta.json。文件缺失或不是合法 JSON 时返回零值：
// 少一份统计不该把一次成功的运行变成失败。
func readSand3dMeta(path string) sand3dMeta {
	meta := sand3dMeta{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return sand3dMeta{}
	}
	if len(meta.Objects) > 24 {
		meta.Objects = append(meta.Objects[:24], "…")
	}
	return meta
}

// sand3dDiagnose 把 bpy 的日志和 Python 的 traceback 压成一句人话 + 行号。
//
// 两个细节决定了它准不准：
//   - 行号取**最后**一个 program.py 帧。traceback 是「外→内」的顺序，异常链还会让
//     program.py 出现两次；取第一个会指到被包装的外层位置。
//   - 异常那一句只认 `XxxError: ...` 这种形状，不靠关键词猜 —— 用户程序自己打印的
//     一句 "Error: 随便什么" 不该被当成失败原因。
func sand3dDiagnose(stderr, stdout string) (string, int) {
	text := stderr
	if strings.TrimSpace(text) == "" {
		text = stdout
	}
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")

	line := 0
	for _, match := range programFrame.FindAllStringSubmatch(text, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil {
			line = value
		}
	}

	message := exceptionLine(lines)
	if message == "" {
		message = lastLine(text)
	}
	if message == "" {
		message = "程序执行失败，没有返回错误信息"
	}
	if line > 0 {
		return fmt.Sprintf("第 %d 行：%s", line, message), line
	}
	return message, 0
}

// programFrame 匹配 traceback 里指向用户程序的帧，例如：
//   File "C:\...\program.py", line 12, in <module>
var programFrame = regexp.MustCompile(`File "[^"]*program\.py", line (\d+)`)

// exceptionLine 返回最后一条真正的异常行（`ValueError: 参数不合法`），
// 跳过 "Traceback"、源码行、`^^^` 指示行与空行。
func exceptionLine(lines []string) string {
	last := ""
	for _, raw := range lines {
		candidate := strings.TrimSpace(raw)
		if candidate == "" || strings.HasPrefix(candidate, "Traceback") ||
			strings.HasPrefix(candidate, "^") || strings.HasPrefix(candidate, "File \"") ||
			strings.HasPrefix(candidate, "During handling") || strings.HasPrefix(candidate, "The above exception") {
			continue
		}
		name, _, ok := strings.Cut(candidate, ": ")
		if !ok {
			continue
		}
		if index := strings.LastIndex(name, "."); index >= 0 {
			name = name[index+1:]
		}
		if isExceptionName(name) {
			last = candidate
		}
	}
	return last
}

func isExceptionName(name string) bool {
	if name == "" || strings.ContainsRune(name, ' ') {
		return false
	}
	return strings.HasSuffix(name, "Error") ||
		strings.HasSuffix(name, "Exception") ||
		strings.HasSuffix(name, "Interrupt") ||
		strings.HasSuffix(name, "Warning") ||
		strings.HasSuffix(name, "Exit")
}

// truncate 按字符（rune）截断，不按字节：按字节切会把一个汉字劈成两半，
// 界面上就会出现半个字。切不到整行时就退一格，尽量在行边界结束。
func truncate(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	if index := strings.LastIndex(cut, "\n"); index > 0 {
		cut = cut[:index]
	}
	return cut + "\n…（后面的输出已截断）"
}

// limitedBuffer 只保留前 limit 字节，超出部分照常「消费」但不留在内存里：
// 子进程往管道写数据时若没人读会被阻塞，所以不能简单拒绝写入。
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer { return &limitedBuffer{limit: limit} }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) <= room {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:room])
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	text := b.buf.String()
	if b.truncated {
		text += "\n…（输出超过 " + strconv.Itoa(b.limit>>10) + " KB，后面已截断）"
	}
	return text
}
