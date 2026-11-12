package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"assetstudio/server/internal/ai"
)

func TestAIConfigMasksKeyAndPersists(t *testing.T) {
	a, dir := newTestAPI(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen-test"},{"name":"other-model"}]}`))
	}))
	defer server.Close()

	body := `{"provider":"ollama","endpoint":"` + server.URL + `","model":"qwen-test","apiKey":"sk-secret-test-1234"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存配置状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-secret-test-1234") {
		t.Fatalf("保存响应泄露了密钥: %s", rec.Body.String())
	}
	var view aiConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("解析配置响应: %v", err)
	}
	if !view.HasKey || view.KeyHint != "••••1234" {
		t.Fatalf("密钥提示 = %+v", view)
	}

	// 文件里确实保存了完整密钥，但 GET 不得回传它。
	raw, err := os.ReadFile(filepath.Join(dir, "ai-config.json"))
	if err != nil {
		t.Fatalf("读取配置文件: %v", err)
	}
	if !strings.Contains(string(raw), "sk-secret-test-1234") {
		t.Fatalf("配置文件没有保存密钥: %s", raw)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/ai/config", nil)
	getRec := httptest.NewRecorder()
	a.Routes().ServeHTTP(getRec, getReq)
	if strings.Contains(getRec.Body.String(), "sk-secret-test-1234") {
		t.Fatalf("GET 配置泄露了密钥: %s", getRec.Body.String())
	}

	// 不带 apiKey 的 PUT 保留已有密钥；clearKey 才清掉。
	preserve := `{"provider":"ollama","endpoint":"` + server.URL + `","model":"qwen-test"}`
	req = httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(preserve))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保留密钥保存失败: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"hasKey":true`) {
		t.Fatalf("空密钥字段不该清掉已有密钥: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(`{"clearKey":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"hasKey":true`) {
		t.Fatalf("清除密钥失败: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAITestAndChatUseSavedConfig(t *testing.T) {
	a, dir := newTestAPI(t)
	var chatBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen-test"}]}`))
		case "/api/chat":
			_ = json.NewDecoder(r.Body).Decode(&chatBody)
			_, _ = w.Write([]byte(`{"message":{"content":"这是助手回答"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	body := `{"provider":"ollama","endpoint":"` + server.URL + `","model":"qwen-test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存配置失败: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/ai/test", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("测试连接状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
	var result aiTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("解析测试结果: %v", err)
	}
	if !result.OK || len(result.Models) != 1 || result.Models[0] != "qwen-test" {
		t.Fatalf("测试结果 = %+v", result)
	}

	seedObjAsset(t, a, dir, "a-chat", objV1)
	req = httptest.NewRequest(http.MethodPost, "/api/ai/chat", bytes.NewBufferString(
		`{"messages":[{"role":"user","content":"这个资产怎么分类？"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("对话状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
	var reply aiChatReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("解析对话响应: %v", err)
	}
	if reply.Reply != "这是助手回答" || reply.Provider != "ollama/qwen-test" {
		t.Fatalf("对话响应 = %+v", reply)
	}

	// 服务端必须把资产库上下文塞进 system 消息，而不是原样转发用户消息。
	messages, ok := chatBody["messages"].([]any)
	if !ok || len(messages) < 2 {
		t.Fatalf("发给模型的 messages = %#v", chatBody["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if role, _ := first["role"].(string); role != "system" {
		t.Fatalf("第一条不是 system: %#v", messages[0])
	}
	if content, _ := first["content"].(string); !strings.Contains(content, "测试件") || !strings.Contains(content, "当前资产库概况") {
		t.Fatalf("system 没有资产库上下文: %q", content)
	}
}

func TestAIConfigRejectsUnknownProvider(t *testing.T) {
	a, _ := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(
		`{"provider":"evil","endpoint":"http://127.0.0.1:11434","model":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知 provider 状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
}

func TestAIChatRejectsUnconfiguredState(t *testing.T) {
	a, _ := newTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/ai/chat", bytes.NewBufferString(
		`{"messages":[{"role":"user","content":"你好"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "尚未配置") {
		t.Fatalf("未配置状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
}

func TestAIConfigDoesNotUseClassifierOverrideForSavedConfig(t *testing.T) {
	a, _ := newTestAPI(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"real"}]}`))
	}))
	defer server.Close()
	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"fake"}]}`))
	}))
	defer aiServer.Close()
	a.classifier = ai.New(aiServer.URL, "fake")
	body := `{"provider":"ollama","endpoint":"` + server.URL + `","model":"real"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	// 覆盖 classifier 只用于分类单测；配置测试应该走表单值，不能误报 fake。
	req = httptest.NewRequest(http.MethodPost, "/api/ai/test", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	var result aiTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("解析测试结果: %v", err)
	}
	if !result.OK || len(result.Models) != 1 || result.Models[0] != "real" {
		t.Fatalf("配置测试没有使用保存的配置，结果 = %+v", result)
	}
}

func TestAIChatChineseMessageUsesRuneLimit(t *testing.T) {
	a, _ := newTestAPI(t)
	var chatBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&chatBody)
		_, _ = w.Write([]byte(`{"message":{"content":"收到"}}`))
	}))
	defer server.Close()

	body := `{"provider":"ollama","endpoint":"` + server.URL + `","model":"qwen-test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/ai/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存配置失败: %d %s", rec.Code, rec.Body.String())
	}

	// 4000 个汉字按字节是 12000，按 rune 才是 4000；旧实现会在这里把消息丢掉。
	long := strings.Repeat("字", 4000)
	payload, _ := json.Marshal(aiChatRequest{
		Messages: []ai.Message{{Role: "user", Content: long}},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/ai/chat", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	a.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("中文长消息状态码 = %d，正文 %s", rec.Code, rec.Body.String())
	}
	messages, ok := chatBody["messages"].([]any)
	if !ok || len(messages) < 2 {
		t.Fatalf("模型收到的 messages = %#v", chatBody["messages"])
	}
	last, _ := messages[len(messages)-1].(map[string]any)
	if content, _ := last["content"].(string); content != long {
		t.Fatalf("长消息被截断或丢失: got %d runes", utf8.RuneCountInString(content))
	}
}
