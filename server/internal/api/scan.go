package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"assetstudio/server/internal/job"
	"assetstudio/server/internal/modelinfo"
	"assetstudio/server/internal/store"
)

const (
	// 单次扫描最多处理这么多个候选文件，避免误选系统盘根目录时卡死。
	defaultScanLimit = 2000
	maxScanLimit     = 5000

	// 外部资源上限：同目录里的 .bin / .mtl / 贴图只搬这些，避免把整个素材库拖进来。
	maxSidecarFiles = 64
	maxSidecarBytes = 200 << 20
)

// 需要跟着 .gltf / .obj 一起搬走的外部资源类型。
var sidecarImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true,
	".ktx2": true, ".basis": true, ".dds": true, ".ktx": true,
	".tga": true, ".bmp": true, ".hdr": true, ".exr": true,
}

var sidecarDataExts = map[string]bool{".bin": true, ".mtl": true}

// 扫描时可以直接接受的「源格式」：它们不能直接入库，先由服务端调用 Blender 转成 GLB。
// PMX 还需要 mmd_tools + opencc，.blend 只需要 Blender。
var scanSourceFormats = map[string]bool{"pmx": true, "blend": true}

// normalizeScanFormat 比 normalizeFormat 多认 pmx / blend，只用于扫描。
// 资产格式白名单仍是 allowedFormats，上传接口不会因此接受 PMX。
func normalizeScanFormat(raw string) string {
	format := strings.ToLower(strings.TrimSpace(raw))
	format = strings.TrimPrefix(format, ".")
	if allowedFormats[format] || scanSourceFormats[format] {
		return format
	}
	return ""
}

// 扫描时直接跳过的目录，避免把依赖、版本库、系统回收站里的杂物扫进来。
var ignoredScanDirs = map[string]bool{
	"node_modules":              true,
	"$recycle.bin":              true,
	"system volume information": true,
	"__macosx":                  true,
	"lost+found":                true,
	".git":                      true,
	".svn":                      true,
	".hg":                       true,
	".idea":                     true,
	".vscode":                   true,
	"windows":                   true,
	"$windows.~bt":              true,
	"$windows.~ws":              true,
}

type scanRequest struct {
	Path      string   `json:"path"`
	Recursive bool     `json:"recursive"`
	Category  string   `json:"category"`
	Formats   []string `json:"formats"`
	Limit     int      `json:"limit"`
}

// scanOptions 是校验后的扫描参数，作业 goroutine 里只读。
type scanOptions struct {
	root      string
	recursive bool
	category  string
	allowed   map[string]bool
	limit     int
}

// scanOutcome 是扫描作业的结果，完成后通过 /api/jobs/{id} 的 result 字段返回。
type scanOutcome struct {
	Scanned    int      `json:"scanned"`
	Created    int      `json:"created"`
	CreatedIDs []string `json:"createdIds"`
	Skipped    int      `json:"skipped"`
	Failed     int      `json:"failed"`
	Truncated  bool     `json:"truncated"`
	Formats    []string `json:"formats"`
}

// scanFolder 只做参数校验，真正的遍历与复制交给后台作业：
// HTTP 请求立刻返回 jobId，前端轮询进度，2000 个文件也不会把一次请求挂死。
func (a *API) scanFolder(w http.ResponseWriter, r *http.Request) {
	var req scanRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}

	root := strings.TrimSpace(req.Path)
	if root == "" {
		writeError(w, http.StatusBadRequest, "请填写要扫描的文件夹路径")
		return
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		writeError(w, http.StatusBadRequest, "路径不可访问: "+err.Error())
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "该路径不是文件夹")
		return
	}

	allowed := resolveScanFormats(req.Formats)
	limit := req.Limit
	if limit <= 0 {
		limit = defaultScanLimit
	}
	if limit > maxScanLimit {
		limit = maxScanLimit
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = "未分类"
	}

	opts := scanOptions{root: root, recursive: req.Recursive, category: category, allowed: allowed, limit: limit}
	j, err := a.jobs.Start("scan", func(work *job.Job) error {
		return a.runScan(work, opts)
	})
	if errors.Is(err, job.ErrRunning) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "已有扫描正在进行，等它跑完或先取消",
			"jobId": j.ID(),
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法创建扫描作业: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": j.ID()})
}

// runScan 在后台作业里遍历目录、复制文件、解析指标并入库，全程汇报进度。
func (a *API) runScan(work *job.Job, opts scanOptions) error {
	files, truncated, err := collectScanFiles(work.Context(), opts)
	if err != nil {
		return err
	}
	work.SetTotal(len(files))

	usedFormats := make([]string, 0, len(opts.allowed))
	for format := range opts.allowed {
		usedFormats = append(usedFormats, format)
	}
	outcome := scanOutcome{
		Scanned:    len(files),
		CreatedIDs: make([]string, 0, len(files)),
		Truncated:  truncated,
		Formats:    usedFormats,
	}

	for _, srcPath := range files {
		select {
		case <-work.Context().Done():
			work.Result(outcome)
			return context.Canceled
		default:
		}

		format := normalizeScanFormat(filepath.Ext(srcPath))
		if format == "" {
			work.Step(filepath.Base(srcPath))
			continue
		}
		if _, err := a.store.FindBySourcePath(work.Context(), srcPath); err == nil {
			outcome.Skipped++
			work.Skip(filepath.Base(srcPath))
			continue
		}

		fileInfo, statErr := os.Stat(srcPath)
		if statErr != nil {
			outcome.Failed++
			work.Fail(srcPath, statErr.Error())
			continue
		}

		work.Message(filepath.Base(srcPath))
		id := store.NewID("a")

		// PMX / .blend 不能直接入库：先转成 GLB。转换产物在临时目录里，
		// 复制进库后立刻清掉；源路径仍记原始文件，方便以后判重。
		sourceFormat := ""
		copySrc := srcPath
		copyFormat := format
		cleanupDir := ""
		if scanSourceFormats[format] {
			sourceFormat = format
			work.Message(filepath.Base(srcPath) + " · 正在转换")
			glbPath, tempDir, convErr := convertMMD(work.Context(), srcPath, format)
			if convErr != nil {
				if errors.Is(convErr, context.Canceled) {
					work.Result(outcome)
					return context.Canceled
				}
				outcome.Failed++
				work.Fail(srcPath, convErr.Error())
				continue
			}
			copySrc = glbPath
			copyFormat = "glb"
			cleanupDir = tempDir
		}

		dstPath := a.store.AbsModelPath(id, copyFormat)
		size, digest, copyErr := copyFileHashed(copySrc, dstPath)
		if cleanupDir != "" {
			_ = os.RemoveAll(cleanupDir)
		}
		if copyErr != nil {
			_ = os.Remove(dstPath)
			outcome.Failed++
			work.Fail(srcPath, copyErr.Error())
			continue
		}

		// 扫描入库的资产当场解析出指标：不再出现「扫进来一片 0，打开网页才补齐」。
		stats := store.Stats{}
		metricsState, metricsSource, lastError := "pending", "client", ""
		if parsed, parseErr := modelinfo.Parse(dstPath, copyFormat); parseErr == nil {
			stats = store.Stats{
				Vertices:   parsed.Vertices,
				Triangles:  parsed.Triangles,
				Meshes:     parsed.Meshes,
				Materials:  parsed.Materials,
				Dimensions: parsed.Dimensions,
			}
			metricsState, metricsSource = "ok", "server"
		} else if errors.Is(parseErr, modelinfo.ErrUnsupported) {
			metricsState, metricsSource = "pending", "client" // FBX：留给浏览器
		} else {
			metricsState, metricsSource = "error", "server"
			lastError = parseErr.Error()
		}

		name := strings.TrimSuffix(fileInfo.Name(), filepath.Ext(fileInfo.Name()))
		fileName := fileInfo.Name()
		description := ""
		tags := []string{"批量扫描"}
		if sourceFormat != "" {
			fileName = name + ".glb"
			tag := strings.ToUpper(sourceFormat)
			if sourceFormat == "blend" {
				tag = "Blender"
			}
			tags = append(tags, tag)
			description = "由 " + strings.ToUpper(sourceFormat) + " 在服务端自动转换为 GLB（源文件：" + fileInfo.Name() + "）"
		}

		now := time.Now().UnixMilli()
		sidecarRel, _ := a.attachSidecars(id, srcPath, copyFormat)
		asset := store.Asset{
			ID:            id,
			Name:          name,
			Description:   description,
			Category:      opts.category,
			Tags:          tags,
			Format:        copyFormat,
			FileName:      fileName,
			FileSize:      size,
			Stats:         stats,
			CreatedAt:     now,
			UpdatedAt:     now,
			SourcePath:    srcPath,
			FilePath:      a.store.RelativePath(dstPath),
			SidecarDir:    sidecarRel,
			HasSidecars:   sidecarRel != "",
			MetricsState:  metricsState,
			MetricsSource: metricsSource,
			ThumbState:    "missing",
			LastError:     lastError,
			SHA256:        digest,
		}
		if err := a.store.Create(work.Context(), &asset); err != nil {
			_ = os.Remove(dstPath)
			outcome.Failed++
			work.Fail(srcPath, err.Error())
			continue
		}
		outcome.Created++
		outcome.CreatedIDs = append(outcome.CreatedIDs, asset.ID)
		work.Step(fileInfo.Name())
	}

	work.Result(outcome)
	return nil
}

// collectScanFiles 先把候选文件收集成列表，才能在开始处理前给出总数与进度。
func collectScanFiles(ctx context.Context, opts scanOptions) ([]string, bool, error) {
	files := make([]string, 0, 256)
	truncated := false

	if opts.recursive {
		walkErr := filepath.WalkDir(opts.root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // 单个目录不可读时跳过，不中断整次扫描
			}
			if ctx.Err() != nil {
				return fs.SkipAll
			}
			name := entry.Name()
			if entry.IsDir() {
				if path == opts.root {
					return nil
				}
				if strings.HasPrefix(name, ".") || ignoredScanDirs[strings.ToLower(name)] {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return nil
			}
			format := normalizeScanFormat(filepath.Ext(name))
			if format == "" || !opts.allowed[format] {
				return nil
			}
			files = append(files, path)
			if len(files) >= opts.limit {
				truncated = true
				return fs.SkipAll
			}
			return nil
		})
		if walkErr != nil && walkErr != fs.SkipAll {
			return nil, false, walkErr
		}
		return files, truncated, nil
	}

	entries, readErr := os.ReadDir(opts.root)
	if readErr != nil {
		return nil, false, readErr
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		format := normalizeScanFormat(filepath.Ext(name))
		if format == "" || !opts.allowed[format] {
			continue
		}
		files = append(files, filepath.Join(opts.root, name))
		if len(files) >= opts.limit {
			truncated = true
			break
		}
	}
	return files, truncated, nil
}

// resolveScanFormats 把前端传的格式列表收敛成允许的扩展名集合，空列表表示全部支持。
func resolveScanFormats(raw []string) map[string]bool {
	allowed := make(map[string]bool, len(raw))
	for _, item := range raw {
		if format := normalizeScanFormat(item); format != "" {
			allowed[format] = true
		}
	}
	if len(allowed) == 0 {
		for format := range allowedFormats {
			allowed[format] = true
		}
		for format := range scanSourceFormats {
			allowed[format] = true
		}
	}
	return allowed
}

// attachSidecars 把主文件同目录的外部资源（.bin / .mtl / 贴图，以及一层子目录里的贴图）
// 一起复制进库，返回入库相对路径。只对 .gltf / .obj 生效，其余格式本身自包含。
func (a *API) attachSidecars(id, srcPath, format string) (string, int) {
	if format != "gltf" && format != "obj" {
		return "", 0
	}
	dir := filepath.Dir(srcPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	root := a.store.AbsSidecarDir(id)
	saved := 0
	var total int64

	for _, entry := range entries {
		if saved >= maxSidecarFiles || total >= maxSidecarBytes {
			break
		}
		name := entry.Name()
		full := filepath.Join(dir, name)
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") || ignoredScanDirs[strings.ToLower(name)] {
				continue
			}
			subs, readErr := os.ReadDir(full)
			if readErr != nil {
				continue
			}
			for _, sub := range subs {
				if sub.IsDir() || saved >= maxSidecarFiles || total >= maxSidecarBytes {
					continue
				}
				ext := strings.ToLower(filepath.Ext(sub.Name()))
				if !sidecarImageExts[ext] && !sidecarDataExts[ext] {
					continue
				}
				n, copyErr := copyInto(root, filepath.Join(full, sub.Name()), filepath.Join(name, sub.Name()))
				if copyErr != nil {
					continue
				}
				saved++
				total += n
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !sidecarImageExts[ext] && !sidecarDataExts[ext] {
			continue
		}
		n, copyErr := copyInto(root, full, name)
		if copyErr != nil {
			continue
		}
		saved++
		total += n
	}

	if saved == 0 {
		_ = os.RemoveAll(root)
		return "", 0
	}
	return a.store.RelativePath(root), saved
}

func copyInto(root, src, rel string) (int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if info.Size() > maxSidecarBytes {
		return 0, os.ErrInvalid
	}
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

// copyFileHashed 复制的同时算 sha256，省掉入库后再整份读一遍文件。
func copyFileHashed(src, dst string) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return 0, "", err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), in)
	closeErr := out.Close()
	if copyErr != nil {
		return written, "", copyErr
	}
	if closeErr != nil {
		return written, "", closeErr
	}
	return written, hex.EncodeToString(hasher.Sum(nil)), nil
}
