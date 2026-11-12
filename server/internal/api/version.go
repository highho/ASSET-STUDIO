package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"assetstudio/server/internal/modelinfo"
	"assetstudio/server/internal/store"
)

// replaceAsset 用一段新导出的模型文件替换当前资产的「当前版」。
//
// 顺序是有讲究的：先把旧主文件挪进 versions/<id>/v<n>.<ext>，再把新文件挪到主文件位置，
// 最后才改数据库。三步里任何一步失败都按相反顺序还原，所以不存在「库说 v2、文件还是 v1」
// 这种状态。旧版本不会被删，`GET /versions` 能看到，也能一键回滚。
func (a *API) replaceAsset(w http.ResponseWriter, r *http.Request) {
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

	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "解析表单失败: "+err.Error())
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

	format := normalizeFormat(filepath.Ext(header.Filename))
	if format == "" {
		writeError(w, http.StatusBadRequest, ErrUnsupportedFormat.Error())
		return
	}
	if format != asset.Format {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("替换要求同格式：当前是 %s，上传的是 %s。换格式请导入为新资产", asset.Format, format))
		return
	}

	newPath, written, digest, err := a.stageUpload(file, id, format)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 从这里开始，出错都要把 temp 文件删掉。
	staged := true
	defer func() {
		if staged {
			_ = os.Remove(newPath)
		}
	}()

	stats, metricsState, metricsSource, lastError := parseForStore(newPath, format, store.Stats{}, false)
	sidecarRel, err := a.applyReplaceSidecars(r, asset)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	replacedAt := time.Now().UnixMilli()
	updated, err := a.commitReplace(r, asset, newPath, store.ReplaceInput{
		NewFileSize:   written,
		NewSHA256:     digest,
		NewStats:      stats,
		MetricsState:  metricsState,
		MetricsSource: metricsSource,
		LastError:     lastError,
		ThumbState:    "missing",
		Old: store.AssetVersion{
			Version:   asset.Version,
			FileName:  asset.FileName,
			Format:    asset.Format,
			FileSize:  asset.FileSize,
			SHA256:    asset.SHA256,
			CreatedAt: asset.UpdatedAt,
		},
		ReplacedAt: replacedAt,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "资产不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "替换失败: "+err.Error())
		return
	}
	staged = false

	// 缩略图是可选的：换了模型，旧图就不代表新形状了，这里先标记待生成，
	// 前端拿到新模型会重拍一张并 PUT 回来。
	if thumbFile, _, err := r.FormFile("thumb"); err == nil {
		defer thumbFile.Close()
		ext := thumbExtension(asset.Format, r.FormValue("thumbExt"))
		thumbPath := a.store.AbsThumbPath(id, ext)
		if dst, err := os.Create(thumbPath); err == nil {
			if _, err := io.Copy(dst, thumbFile); err == nil {
				_ = a.store.SetThumb(r.Context(), id, a.store.RelativePath(thumbPath))
			}
			_ = dst.Close()
		}
	}

	if sidecarRel != "" && !updated.HasSidecars {
		updated.HasSidecars = true
	}
	writeJSON(w, http.StatusOK, updated)
}

// stageUpload 把上传内容写进 models/<id>.replace.<ext>，返回路径、字节数与 sha256。
// 用固定名字而不是随机名：同一个资产同时只允许一次替换，重名会立刻暴露并发。
func (a *API) stageUpload(src io.Reader, id, format string) (string, int64, string, error) {
	path := filepath.Join(a.store.ModelsDir, id+".replace."+format)
	out, err := os.Create(path)
	if err != nil {
		return "", 0, "", fmt.Errorf("写入临时文件失败: %w", err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), src)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", 0, "", fmt.Errorf("保存上传文件失败")
	}
	return path, written, hex.EncodeToString(hasher.Sum(nil)), nil
}

// parseForStore 把几何真相交给服务端解析；解析不了（FBX）就沿用调用方给的指标。
func parseForStore(path, format string, fallback store.Stats, hasFallback bool) (store.Stats, string, string, string) {
	parsed, err := modelinfo.Parse(path, format)
	if err == nil {
		return store.Stats{
			Vertices:   parsed.Vertices,
			Triangles:  parsed.Triangles,
			Meshes:     parsed.Meshes,
			Materials:  parsed.Materials,
			Dimensions: parsed.Dimensions,
		}, "ok", "server", ""
	}
	if errors.Is(err, modelinfo.ErrUnsupported) {
		if hasFallback {
			return fallback, "ok", "client", ""
		}
		return fallback, "pending", "client", ""
	}
	return fallback, "error", "server", err.Error()
}

// applyReplaceSidecars 覆盖式写入伴随文件（典型是 source.py）：先写同名 .tmp 再换名，
// 读的人不会看到写了一半的程序。返回最终的 sidecar 相对目录（没有就沿用原值）。
func (a *API) applyReplaceSidecars(r *http.Request, asset *store.Asset) (string, error) {
	files := r.MultipartForm.File["sidecar"]
	if len(files) == 0 {
		return asset.SidecarDir, nil
	}
	var paths []string
	if raw := r.FormValue("sidecarPaths"); raw != "" {
		_ = jsonUnmarshalQuiet(raw, &paths)
	}
	dir := a.store.AbsSidecarDir(asset.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建附属目录失败: %w", err)
	}
	for index, header := range files {
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
		dstPath := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return "", fmt.Errorf("创建附属文件目录失败: %w", err)
		}
		src, err := header.Open()
		if err != nil {
			return "", fmt.Errorf("读取附属文件失败: %w", err)
		}
		tmp := dstPath + ".tmp"
		dst, err := os.Create(tmp)
		if err != nil {
			_ = src.Close()
			return "", fmt.Errorf("写入附属文件失败: %w", err)
		}
		_, copyErr := io.Copy(dst, src)
		_ = dst.Close()
		_ = src.Close()
		if copyErr != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("写入附属文件失败")
		}
		if err := os.Rename(tmp, dstPath); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("落盘附属文件失败: %w", err)
		}
	}
	return a.store.RelativePath(dir), nil
}

// commitReplace 做「旧版归档 → 新版上位 → 记数据库」三步，失败逐步回退。
func (a *API) commitReplace(r *http.Request, asset *store.Asset, newPath string, in store.ReplaceInput) (*store.Asset, error) {
	mainPath := filepath.Join(a.store.DataDir, asset.FilePath)
	archive := a.store.VersionAbsPath(asset.ID, asset.Version, asset.Format)
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return nil, fmt.Errorf("创建版本目录失败: %w", err)
	}

	if err := os.Rename(mainPath, archive); err != nil {
		return nil, fmt.Errorf("归档旧版本失败（旧文件可能被占用）: %w", err)
	}
	if err := os.Rename(newPath, mainPath); err != nil {
		_ = os.Rename(archive, mainPath)
		return nil, fmt.Errorf("新文件上位失败: %w", err)
	}

	in.Old.FilePath = a.store.RelativePath(archive)
	updated, err := a.store.ApplyReplace(r.Context(), asset.ID, in)
	if err != nil {
		_ = os.Rename(mainPath, newPath)
		_ = os.Rename(archive, mainPath)
		return nil, err
	}
	return updated, nil
}

func (a *API) listVersions(w http.ResponseWriter, r *http.Request) {
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
	versions, err := a.store.Versions(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"version": asset.Version,
		"fileSize": asset.FileSize,
		"items":   versions,
	})
}

func (a *API) findVersion(r *http.Request) (*store.Asset, *store.AssetVersion, error) {
	id := r.PathValue("id")
	asset, err := a.store.Get(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || number <= 0 {
		return nil, nil, store.ErrNotFound
	}
	versions, err := a.store.Versions(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	for index := range versions {
		if versions[index].Version == number {
			return asset, &versions[index], nil
		}
	}
	return nil, nil, store.ErrNotFound
}

func (a *API) serveVersionFile(w http.ResponseWriter, r *http.Request) {
	_, version, err := a.findVersion(r)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "这个版本不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	abs := filepath.Join(a.store.DataDir, version.FilePath)
	if _, err := os.Stat(abs); err != nil {
		writeError(w, http.StatusNotFound, "这个版本的文件已丢失")
		return
	}
	if mimeType := formatMIME[version.Format]; mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Content-Disposition",
		"attachment; filename*=UTF-8''"+url.PathEscape(fmt.Sprintf("v%d-%s", version.Version, version.FileName)))
	http.ServeFile(w, r, abs)
}

// restoreVersion 把某一版旧文件放回当前位：现在的这一版照样归档，所以回滚也能再回滚。
func (a *API) restoreVersion(w http.ResponseWriter, r *http.Request) {
	asset, version, err := a.findVersion(r)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "这个版本不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	srcPath := filepath.Join(a.store.DataDir, version.FilePath)
	stage := filepath.Join(a.store.ModelsDir, asset.ID+".restore."+asset.Format)
	if _, err := copyFile(srcPath, stage); err != nil {
		writeError(w, http.StatusInternalServerError, "复制旧版本失败: "+err.Error())
		return
	}
	staged := true
	defer func() {
		if staged {
			_ = os.Remove(stage)
		}
	}()

	stats, metricsState, metricsSource, lastError := parseForStore(stage, asset.Format, store.Stats{}, false)
	replacedAt := time.Now().UnixMilli()
	updated, err := a.commitReplace(r, asset, stage, store.ReplaceInput{
		NewFileSize:   version.FileSize,
		NewSHA256:     version.SHA256,
		NewStats:      stats,
		MetricsState:  metricsState,
		MetricsSource: metricsSource,
		LastError:     lastError,
		ThumbState:    "missing",
		Old: store.AssetVersion{
			Version:   asset.Version,
			FileName:  asset.FileName,
			Format:    asset.Format,
			FileSize:  asset.FileSize,
			SHA256:    asset.SHA256,
			CreatedAt: asset.UpdatedAt,
		},
		ReplacedAt: replacedAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "回滚失败: "+err.Error())
		return
	}
	staged = false
	writeJSON(w, http.StatusOK, updated)
}

// jsonUnmarshalQuiet 解析失败就当没给：sidecarPaths 只是提示，不是必需字段。
func jsonUnmarshalQuiet(raw string, target any) error {
	return json.Unmarshal([]byte(raw), target)
}
