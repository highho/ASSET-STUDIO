package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"assetstudio/server/internal/store"
)

const objV1 = "o one\nv 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"
const objV2 = "o two\nv 0 0 0\nv 2 0 0\nv 0 2 0\nv 0 0 2\nf 1 2 3\nf 1 3 4\n"

func newTestAPI(t *testing.T) (*API, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "assetstudio.db"), dir)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st), dir
}

func seedObjAsset(t *testing.T, a *API, dir, id, content string) {
	t.Helper()
	modelPath := filepath.Join(dir, "models", id+".obj")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(modelPath, []byte(content), 0o644); err != nil {
		t.Fatalf("写模型失败: %v", err)
	}
	asset := &store.Asset{
		ID: id, Name: "测试件", Format: "obj", FileName: id + ".obj",
		FileSize: int64(len(content)), FilePath: "models/" + id + ".obj",
		Stats:       store.Stats{Vertices: 3, Triangles: 1, Meshes: 1, Dimensions: [3]float64{1, 1, 0}},
		CreatedAt:   1000,
		UpdatedAt:   1000,
		MetricsState: "ok", MetricsSource: "server",
	}
	if err := a.store.Create(context.Background(), asset); err != nil {
		t.Fatalf("写资产失败: %v", err)
	}
}

func multipartBody(t *testing.T, filename string, content string, sidecars map[string]string) (io.Reader, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("建表单字段失败: %v", err)
	}
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatalf("写表单内容失败: %v", err)
	}
	if len(sidecars) > 0 {
		paths := make([]string, 0, len(sidecars))
		for path, text := range sidecars {
			paths = append(paths, path)
			sidePart, err := writer.CreateFormFile("sidecar", filepath.Base(path))
			if err != nil {
				t.Fatalf("建 sidecar 字段失败: %v", err)
			}
			if _, err := io.WriteString(sidePart, text); err != nil {
				t.Fatalf("写 sidecar 失败: %v", err)
			}
		}
		raw, _ := json.Marshal(paths)
		_ = writer.WriteField("sidecarPaths", string(raw))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("收尾表单失败: %v", err)
	}
	return buf, writer.FormDataContentType()
}

func doJSON(t *testing.T, a *API, method, target string, body io.Reader, contentType string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	request := httptest.NewRequest(method, target, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	a.Routes().ServeHTTP(recorder, request)
	return recorder, recorder.Body.Bytes()
}

// 端到端：替换 → 旧版可查可下 → 回滚 → 被回滚的那版也在表里。三条都由同一个 handler 走通。
func TestReplaceRestoreRoundTrip(t *testing.T) {
	a, dir := newTestAPI(t)
	seedObjAsset(t, a, dir, "a-swap", objV1)

	body, contentType := multipartBody(t, "new.obj", objV2, map[string]string{"source.py": "print('v2')\n"})
	recorder, payload := doJSON(t, a, http.MethodPost, "/api/assets/a-swap/replace", body, contentType)
	if recorder.Code != http.StatusOK {
		t.Fatalf("替换应 200，实际 %d: %s", recorder.Code, payload)
	}
	var updated store.Asset
	if err := json.Unmarshal(payload, &updated); err != nil {
		t.Fatalf("解析返回失败: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("替换后版本应是 2，实际 %d", updated.Version)
	}

	mainPath := filepath.Join(dir, "models", "a-swap.obj")
	if data, _ := os.ReadFile(mainPath); string(data) != objV2 {
		t.Fatalf("主文件没换成新版: %q", string(data))
	}
	archive := filepath.Join(dir, "versions", "a-swap", "v1.obj")
	if data, err := os.ReadFile(archive); err != nil || string(data) != objV1 {
		t.Fatalf("旧版没被归档: err=%v data=%q", err, string(data))
	}
	sourcePath := filepath.Join(dir, "sidecars", "a-swap", "source.py")
	if data, _ := os.ReadFile(sourcePath); string(data) != "print('v2')\n" {
		t.Fatalf("伴随程序没被覆盖: %q", string(data))
	}

	recorder, payload = doJSON(t, a, http.MethodGet, "/api/assets/a-swap/versions", nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("版本列表应 200，实际 %d: %s", recorder.Code, payload)
	}
	var list struct {
		Version int                 `json:"version"`
		Items   []store.AssetVersion `json:"items"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		t.Fatalf("解析版本列表失败: %v", err)
	}
	if list.Version != 2 || len(list.Items) != 1 || list.Items[0].Version != 1 {
		t.Fatalf("版本列表不对: %+v", list)
	}

	recorder, payload = doJSON(t, a, http.MethodGet, "/api/assets/a-swap/versions/1/file", nil, "")
	if recorder.Code != http.StatusOK || string(payload) != objV1 {
		t.Fatalf("下载旧版应拿到 v1 内容，实际 %d: %q", recorder.Code, string(payload))
	}

	recorder, payload = doJSON(t, a, http.MethodPost, "/api/assets/a-swap/versions/1/restore", nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("回滚应 200，实际 %d: %s", recorder.Code, payload)
	}
	var restored store.Asset
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatalf("解析回滚结果失败: %v", err)
	}
	if restored.Version != 3 {
		t.Fatalf("回滚后版本应是 3（只增不减），实际 %d", restored.Version)
	}
	if data, _ := os.ReadFile(mainPath); string(data) != objV1 {
		t.Fatalf("回滚后主文件应是 v1 内容: %q", string(data))
	}
	// 被回滚掉的 v2 也要进版本表，否则这次回滚就丢了
	versions, err := a.store.Versions(context.Background(), "a-swap")
	if err != nil {
		t.Fatalf("读版本失败: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("回滚后版本表应含 2,1，实际 %+v", versions)
	}

	recorder, payload = doJSON(t, a, http.MethodPost, "/api/assets/a-swap/versions/2/restore", nil, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("再回滚到 v2 应 200，实际 %d: %s", recorder.Code, payload)
	}
	if data, _ := os.ReadFile(mainPath); string(data) != objV2 {
		t.Fatalf("再回滚后主文件应是 v2 内容: %q", string(data))
	}
}

// 替换只允许同格式：换格式会把主文件扩展名也换掉，是另一件事（导入新资产）。
func TestReplaceRejectsFormatChange(t *testing.T) {
	a, dir := newTestAPI(t)
	seedObjAsset(t, a, dir, "a-fmt", objV1)

	body, contentType := multipartBody(t, "other.glb", "glTF-not-really", nil)
	recorder, payload := doJSON(t, a, http.MethodPost, "/api/assets/a-fmt/replace", body, contentType)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("换格式应 400，实际 %d: %s", recorder.Code, payload)
	}
	if !strings.Contains(string(payload), "同格式") {
		t.Fatalf("报错应说明同格式限制: %s", payload)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "models", "a-fmt.obj")); string(data) != objV1 {
		t.Fatalf("被拒的替换不该动原文件: %q", string(data))
	}
}

func TestVersionsOnMissingAsset(t *testing.T) {
	a, _ := newTestAPI(t)
	recorder, _ := doJSON(t, a, http.MethodGet, "/api/assets/a-none/versions", nil, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("不存在的资产应 404，实际 %d", recorder.Code)
	}
	recorder, _ = doJSON(t, a, http.MethodPost, "/api/assets/a-none/versions/1/restore", nil, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("不存在的资产回滚应 404，实际 %d", recorder.Code)
	}
}
