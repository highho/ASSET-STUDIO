package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"assetstudio/server/internal/ai"
	"assetstudio/server/internal/job"
	"assetstudio/server/internal/modelinfo"
	"assetstudio/server/internal/store"
)

var ErrUnsupportedFormat = errors.New("不支持的模型格式")

var allowedFormats = map[string]bool{
	"glb": true, "gltf": true, "obj": true, "stl": true, "fbx": true,
}

var formatMIME = map[string]string{
	"glb":  "model/gltf-binary",
	"gltf": "model/gltf+json",
	"obj":  "text/plain; charset=utf-8",
	"stl":  "model/stl",
	"fbx":  "application/octet-stream",
}

type API struct {
	store      *store.Store
	jobs       *job.Manager
	classifier *ai.Classifier // 测试/嵌入用覆盖；正常请求走 aiConfig
	aiConfig   *ai.ConfigStore
}

type uploadMeta struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Category    string      `json:"category"`
	Tags        []string    `json:"tags"`
	Format      string      `json:"format"`
	Stats       store.Stats `json:"stats"`
}

func New(st *store.Store) *API {
	return &API{
		store:    st,
		jobs:     job.NewManager(24),
		aiConfig: ai.NewConfigStore(filepath.Join(st.DataDir, "ai-config.json")),
	}
}

// aiRuntime 每次按当前配置新建客户端，保存配置后不需要重启服务。
// classifier 字段优先，给测试注入假服务用。
func (a *API) aiRuntime() *ai.Classifier {
	if a.classifier != nil {
		return a.classifier
	}
	if a.aiConfig != nil {
		return ai.NewFromSettings(a.aiConfig.Settings())
	}
	return ai.NewFromSettings(ai.DefaultSettings())
}

// Shutdown 让在跑的作业在文件边界停下来。服务退出前调用一次。
func (a *API) Shutdown() int {
	return a.jobs.CancelAll()
}

// RunningJobs 返回还在跑的作业数，供退出时等待用。
func (a *API) RunningJobs() int {
	return a.jobs.Running()
}

func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/assets", a.listAssets)
	mux.HandleFunc("POST /api/assets", a.createAsset)
	mux.HandleFunc("GET /api/assets/{id}", a.getAsset)
	mux.HandleFunc("PATCH /api/assets/{id}", a.updateAsset)
	mux.HandleFunc("DELETE /api/assets/{id}", a.deleteAsset)
	mux.HandleFunc("GET /api/assets/{id}/file", a.serveFile)
	mux.HandleFunc("POST /api/assets/{id}/replace", a.replaceAsset)
	mux.HandleFunc("GET /api/assets/{id}/versions", a.listVersions)
	mux.HandleFunc("GET /api/assets/{id}/versions/{version}/file", a.serveVersionFile)
	mux.HandleFunc("POST /api/assets/{id}/versions/{version}/restore", a.restoreVersion)
	mux.HandleFunc("GET /api/assets/{id}/sidecars/{path...}", a.serveSidecar)
	mux.HandleFunc("GET /api/assets/{id}/thumbnail", a.serveThumb)
	mux.HandleFunc("PUT /api/assets/{id}/thumbnail", a.putThumb)
	mux.HandleFunc("PUT /api/assets/{id}/metrics", a.putMetrics)
	mux.HandleFunc("POST /api/assets/{id}/classify", a.classifyAsset)
	mux.HandleFunc("GET /api/ai/config", a.getAIConfig)
	mux.HandleFunc("PUT /api/ai/config", a.putAIConfig)
	mux.HandleFunc("POST /api/ai/test", a.testAIConfig)
	mux.HandleFunc("POST /api/ai/chat", a.aiChat)
	mux.HandleFunc("POST /api/scan", a.scanFolder)
	mux.HandleFunc("GET /api/jobs", a.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", a.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", a.cancelJob)
	mux.HandleFunc("GET /api/stats", a.summary)
	mux.HandleFunc("GET /api/sand3d/status", a.sand3dStatusHandler)
	mux.HandleFunc("POST /api/sand3d/run", a.sand3dRun)
	mux.HandleFunc("GET /api/mmd/status", a.mmdStatusHandler)

	return mux
}

// WithCORS 只放行本机来源（端口不限，方便开发时前端直连 :8080 调试），
// 并拒绝浏览器跨站发起的写操作：跨站页面无法伪造 Sec-Fetch-Site，同源页面与
// 本机命令行（不带这个头）则照常放行。
func WithCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if OriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if isWriteMethod(r.Method) {
			switch r.Header.Get("Sec-Fetch-Site") {
			case "", "same-origin", "none":
			default:
				writeError(w, http.StatusForbidden, "拒绝跨站写入：只接受同源页面或本机命令行发起的请求。")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		_, _ = io.WriteString(w, `{"error":"写入响应失败"}`)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"dataDir": a.store.DataDir,
		"time":    time.Now().UnixMilli(),
	})
}

func (a *API) listAssets(w http.ResponseWriter, r *http.Request) {
	assets, err := a.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取资产列表失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

func (a *API) getAsset(w http.ResponseWriter, r *http.Request) {
	asset, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

func normalizeFormat(raw string) string {
	format := strings.ToLower(strings.TrimSpace(raw))
	format = strings.TrimPrefix(format, ".")
	if !allowedFormats[format] {
		return ""
	}
	return format
}

func (a *API) createAsset(w http.ResponseWriter, r *http.Request) {
	// 单个文件上限 1GB，足够本地资产管理使用。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "解析上传表单失败: "+err.Error())
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少模型文件字段 file")
		return
	}
	defer file.Close()

	var meta uploadMeta
	if raw := r.FormValue("meta"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			writeError(w, http.StatusBadRequest, "meta 字段不是合法 JSON")
			return
		}
	}

	format := normalizeFormat(meta.Format)
	if format == "" {
		format = normalizeFormat(filepath.Ext(header.Filename))
	}
	if format == "" {
		writeError(w, http.StatusBadRequest, ErrUnsupportedFormat.Error()+"，仅支持 GLB / GLTF / OBJ / STL / FBX")
		return
	}

	id := store.NewID("a")
	modelPath := a.store.AbsModelPath(id, format)
	out, err := os.Create(modelPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "写入模型文件失败: "+err.Error())
		return
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), file)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(modelPath)
		writeError(w, http.StatusInternalServerError, "保存模型文件失败")
		return
	}
	digest := hex.EncodeToString(hasher.Sum(nil))

	// 外部资源（GLTF 的 .bin、贴图，OBJ 的 .mtl）跟着主文件一起入库，
	// 前端解析时按 sidecars 路径重写 URL，避免「外链资源一律 404」。
	sidecarRel := ""
	if sidecarFiles := r.MultipartForm.File["sidecar"]; len(sidecarFiles) > 0 {
		var paths []string
		if raw := r.FormValue("sidecarPaths"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &paths)
		}
		sidecarDir := a.store.AbsSidecarDir(id)
		if err := os.MkdirAll(sidecarDir, 0o755); err == nil {
			saved := 0
			for index, header := range sidecarFiles {
				rel := ""
				if index < len(paths) {
					rel = sanitizeSidecarPath(paths[index])
				}
				if rel == "" {
					rel = sanitizeSidecarPath(header.Filename)
				}
				if rel == "" {
					continue
				}
				dstPath := filepath.Join(sidecarDir, rel)
				if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
					continue
				}
				src, err := header.Open()
				if err != nil {
					continue
				}
				dst, err := os.Create(dstPath)
				if err != nil {
					_ = src.Close()
					continue
				}
				_, _ = io.Copy(dst, src)
				_ = dst.Close()
				_ = src.Close()
				saved++
			}
			if saved > 0 {
				sidecarRel = a.store.RelativePath(sidecarDir)
			} else {
				_ = os.RemoveAll(sidecarDir)
			}
		}
	}

	thumbRel := ""
	if thumbFile, _, err := r.FormFile("thumb"); err == nil {
		ext := thumbExtension(meta.Format, r.FormValue("thumbExt"))
		thumbPath := a.store.AbsThumbPath(id, ext)
		dst, err := os.Create(thumbPath)
		if err == nil {
			if _, err := io.Copy(dst, thumbFile); err == nil {
				thumbRel = a.store.RelativePath(thumbPath)
			}
			_ = dst.Close()
			if thumbRel == "" {
				_ = os.Remove(thumbPath)
			}
		}
		_ = thumbFile.Close()
	}

	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	}
	category := strings.TrimSpace(meta.Category)
	if category == "" {
		category = "未分类"
	}

	// 几何真相落在服务端：文件一落盘就自己解析，不再等浏览器打开网页来补。
	stats := meta.Stats
	metricsState, metricsSource, lastError := "ok", "client", ""
	if parsed, parseErr := modelinfo.Parse(modelPath, format); parseErr == nil {
		stats = store.Stats{
			Vertices:   parsed.Vertices,
			Triangles:  parsed.Triangles,
			Meshes:     parsed.Meshes,
			Materials:  parsed.Materials,
			Dimensions: parsed.Dimensions,
		}
		metricsState, metricsSource = "ok", "server"
	} else if errors.Is(parseErr, modelinfo.ErrUnsupported) {
		// FBX 服务端不解析：浏览器若在 meta 里带了指标就记为 client 值，否则留 pending 等它补。
		if meta.Stats.Triangles > 0 || meta.Stats.Vertices > 0 {
			metricsState = "ok"
		} else {
			metricsState = "pending"
		}
		metricsSource = "client"
	} else {
		metricsState, metricsSource = "error", "server"
		lastError = parseErr.Error()
	}

	now := time.Now().UnixMilli()
	asset := store.Asset{
		ID:            id,
		Name:          name,
		Description:   meta.Description,
		Category:      category,
		Tags:          meta.Tags,
		Format:        format,
		FileName:      filepath.Base(header.Filename),
		FileSize:      written,
		Stats:         stats,
		CreatedAt:     now,
		UpdatedAt:     now,
		FilePath:      a.store.RelativePath(modelPath),
		ThumbPath:     thumbRel,
		HasThumb:      thumbRel != "",
		SidecarDir:    sidecarRel,
		HasSidecars:   sidecarRel != "",
		MetricsState:  metricsState,
		MetricsSource: metricsSource,
		ThumbState:    "missing",
		LastError:     lastError,
		SHA256:        digest,
	}
	if asset.ThumbState == "" {
		asset.ThumbState = "missing"
	}
	if thumbRel != "" {
		asset.ThumbState = "ok"
	}
	if asset.Tags == nil {
		asset.Tags = []string{}
	}

	if err := a.store.Create(r.Context(), &asset); err != nil {
		_ = os.Remove(modelPath)
		if thumbRel != "" {
			_ = os.Remove(filepath.Join(a.store.DataDir, thumbRel))
		}
		writeError(w, http.StatusInternalServerError, "写入数据库失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, asset)
}

// 缩略图统一存 webp；只有前端明确要求时才按 png 存。
func thumbExtension(_ string, requested string) string {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "png":
		return "png"
	case "jpg", "jpeg":
		return "jpg"
	default:
		return "webp"
	}
}

func (a *API) updateAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := a.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var payload struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Category    *string   `json:"category"`
		Tags        *[]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}

	name := asset.Name
	if payload.Name != nil {
		trimmed := strings.TrimSpace(*payload.Name)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "名称不能为空")
			return
		}
		name = trimmed
	}
	description := asset.Description
	if payload.Description != nil {
		description = *payload.Description
	}
	category := asset.Category
	if payload.Category != nil {
		category = strings.TrimSpace(*payload.Category)
		if category == "" {
			category = "未分类"
		}
	}
	tags := asset.Tags
	if payload.Tags != nil {
		tags = *payload.Tags
		if tags == nil {
			tags = []string{}
		}
	}

	if err := a.store.UpdateMeta(r.Context(), id, name, description, category, tags); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
	updated, err := a.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteAsset 分三步走：立墓碑（立刻从列表消失）→ 把文件挪进 trash → 删库行。
// 任何一步失败都停在那里，墓碑留到下次启动由 Reconcile 收尾，不会出现「行删了文件还在」。
func (a *API) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := a.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取资产失败: "+err.Error())
		return
	}
	if err := a.store.MarkDeleted(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "资产不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "标记删除失败: "+err.Error())
		return
	}
	if err := a.store.PurgeFiles(asset); err != nil {
		writeError(w, http.StatusInternalServerError,
			"已标记删除，但文件移动失败（可能被其它程序占用），重启服务后会自动重试: "+err.Error())
		return
	}
	if err := a.store.HardDelete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "已标记删除，删除记录失败，重启服务后会自动重试: "+err.Error())
		return
	}
	_ = os.RemoveAll(filepath.Join(a.store.TrashDir, id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

func (a *API) serveFile(w http.ResponseWriter, r *http.Request) {
	asset, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	abs := filepath.Join(a.store.DataDir, asset.FilePath)
	if _, err := os.Stat(abs); err != nil {
		writeError(w, http.StatusNotFound, "模型文件已丢失")
		return
	}
	if mimeType := formatMIME[asset.Format]; mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(asset.FileName))
	http.ServeFile(w, r, abs)
}

func (a *API) serveThumb(w http.ResponseWriter, r *http.Request) {
	asset, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if asset.ThumbPath == "" {
		writeError(w, http.StatusNotFound, "该资产还没有缩略图")
		return
	}
	abs := filepath.Join(a.store.DataDir, asset.ThumbPath)
	if _, err := os.Stat(abs); err != nil {
		writeError(w, http.StatusNotFound, "缩略图文件已丢失")
		return
	}
	if mimeType := mime.TypeByExtension(filepath.Ext(abs)); mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, abs)
}

func (a *API) putThumb(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := a.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "解析缩略图表单失败: "+err.Error())
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("thumb")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少缩略图字段 thumb")
		return
	}
	defer file.Close()

	ext := thumbExtension("", r.FormValue("thumbExt"))
	thumbPath := a.store.AbsThumbPath(id, ext)
	dst, err := os.Create(thumbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "写入缩略图失败: "+err.Error())
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		_ = dst.Close()
		_ = os.Remove(thumbPath)
		writeError(w, http.StatusInternalServerError, "写入缩略图失败")
		return
	}
	_ = dst.Close()

	if asset.ThumbPath != "" {
		old := filepath.Join(a.store.DataDir, asset.ThumbPath)
		if old != thumbPath {
			_ = os.Remove(old)
		}
	}
	rel := a.store.RelativePath(thumbPath)
	if err := a.store.SetThumb(r.Context(), id, rel); err != nil {
		writeError(w, http.StatusInternalServerError, "更新缩略图记录失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "thumbPath": rel})
}

func (a *API) putMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := a.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var stats store.Stats
	if err := json.NewDecoder(r.Body).Decode(&stats); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}

	// 服务端已经解析过就是权威，浏览器回填直接忽略：返回库里真实的值，前端无需改动，
	// 也不会出现「浏览器算错一次就把库里的数字改坏」。
	if asset.MetricsSource == "server" && asset.MetricsState == "ok" {
		writeJSON(w, http.StatusOK, asset.Stats)
		return
	}
	if err := a.store.UpdateMetrics(r.Context(), id, stats); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "资产不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// serveSidecar 提供模型的外部资源：GLTF 的 .bin、贴图，OBJ 的 .mtl 与贴图。
func (a *API) serveSidecar(w http.ResponseWriter, r *http.Request) {
	asset, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "资产不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if asset.SidecarDir == "" {
		writeError(w, http.StatusNotFound, "该资产没有外部资源")
		return
	}
	rel := sanitizeSidecarPath(r.PathValue("path"))
	if rel == "" {
		writeError(w, http.StatusBadRequest, "资源路径不合法")
		return
	}
	root := filepath.Join(a.store.DataDir, asset.SidecarDir)
	abs := filepath.Join(root, rel)
	if !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		writeError(w, http.StatusForbidden, "资源路径越界")
		return
	}
	if _, err := os.Stat(abs); err != nil {
		writeError(w, http.StatusNotFound, "外部资源不存在: "+filepath.ToSlash(rel))
		return
	}
	if mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(abs))); mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Cache-Control", "private, max-age=600")
	http.ServeFile(w, r, abs)
}

// sanitizeSidecarPath 丢掉 ..、盘符与空段，只保留安全的相对路径。
func sanitizeSidecarPath(raw string) string {
	clean := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	for strings.HasPrefix(clean, "./") {
		clean = strings.TrimPrefix(clean, "./")
	}
	parts := strings.Split(clean, "/")
	safe := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		part = strings.ReplaceAll(part, ":", "_")
		safe = append(safe, part)
	}
	if len(safe) == 0 {
		return ""
	}
	return filepath.Join(safe...)
}

func (a *API) summary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.store.Summary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// classifyAsset 读资产元数据和 source.py，交给本机 Ollama 分类，再把
// category / confidence / tags 落到 ai_* 三列。Ollama 不可用时由 ai 包退回
// 本地规则分类，所以这个接口不会因为没装模型就变成 501。
func (a *API) classifyAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := a.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "资产不存在")
		} else {
			writeError(w, http.StatusInternalServerError, "读取资产失败: "+err.Error())
		}
		return
	}
	classifier := a.aiRuntime()
	result, err := classifier.Classify(r.Context(), ai.Input{
		Name:        asset.Name,
		Description: asset.Description,
		Category:    asset.Category,
		Tags:        asset.Tags,
		Format:      asset.Format,
		SourceName:  sourceName(asset),
		SourceCode:  a.sourceCode(asset),
		Meshes:      asset.Stats.Meshes,
		Triangles:   asset.Stats.Triangles,
		Dimensions:  asset.Stats.Dimensions,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "AI 分类失败: "+err.Error())
		return
	}

	if err := a.store.SetAI(r.Context(), id, store.AIResult{
		Category:   result.Category,
		Confidence: result.Confidence,
		Tags:       result.Tags,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "保存 AI 分类失败: "+err.Error())
		return
	}
	updated, err := a.store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取分类结果失败: "+err.Error())
		return
	}
	if updated.AI == nil {
		updated.AI = &store.AIResult{
			Category:   result.Category,
			Confidence: result.Confidence,
			Tags:       result.Tags,
		}
	}
	// provider 只随本次响应返回：数据库只保留三列，重载后不伪装成还有模型信息。
	updated.AI.Provider = result.Provider
	writeJSON(w, http.StatusOK, updated)
}

func sourceName(asset *store.Asset) string {
	if strings.TrimSpace(asset.SourcePath) != "" {
		return filepath.Base(asset.SourcePath)
	}
	return asset.FileName
}

// sourceCode 只读 source.py，不碰任意 sidecar；读不到就当没有源码。
func (a *API) sourceCode(asset *store.Asset) string {
	if asset.SidecarDir == "" {
		return ""
	}
	path := filepath.Join(a.store.DataDir, asset.SidecarDir, "source.py")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
