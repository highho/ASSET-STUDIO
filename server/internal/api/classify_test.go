package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"assetstudio/server/internal/ai"
	"assetstudio/server/internal/store"
)

func TestClassifyAssetPersistsAISuggestion(t *testing.T) {
	a, dir := newTestAPI(t)

	var gotModel string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		_, _ = w.Write([]byte(`{"message":{"content":"{\"category\":\"角色\",\"confidence\":0.91,\"tags\":[\"人物\",\"MMD\"]}"}}`))
	}))
	defer ollama.Close()
	a.classifier = ai.New(ollama.URL, "qwen-test")

	seedObjAsset(t, a, dir, "a-classify", objV1)

	req := httptest.NewRequest(http.MethodPost, "/api/assets/a-classify/classify", nil)
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("分类状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
	var updated store.Asset
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("解析分类响应失败: %v", err)
	}
	if updated.AI == nil {
		t.Fatal("响应里没有 ai 字段")
	}
	if updated.AI.Category != "角色" || updated.AI.Confidence != 0.91 {
		t.Fatalf("AI 结果 = %+v", updated.AI)
	}
	if updated.AI.Provider != "ollama/qwen-test" {
		t.Fatalf("provider = %q，想要 ollama/qwen-test", updated.AI.Provider)
	}
	if gotModel != "qwen-test" {
		t.Fatalf("传给 Ollama 的模型 = %q", gotModel)
	}

	// GET 应能读到落库的 ai_category / ai_confidence / ai_tags；provider 不落库。
	getReq := httptest.NewRequest(http.MethodGet, "/api/assets/a-classify", nil)
	getRec := httptest.NewRecorder()
	a.Routes().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("读取资产状态码 = %d", getRec.Code)
	}
	var fetched store.Asset
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("解析资产失败: %v", err)
	}
	if fetched.AI == nil || fetched.AI.Category != "角色" || len(fetched.AI.Tags) != 2 {
		t.Fatalf("落库后的 ai = %+v", fetched.AI)
	}
	if fetched.AI.Provider != "" {
		t.Fatalf("provider 不该被持久化，实际 %q", fetched.AI.Provider)
	}
}
