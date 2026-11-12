package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyUsesOllamaJSON(t *testing.T) {
	requests := make(chan chatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("请求路径 = %s，想要 /api/chat", r.URL.Path)
		}
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求失败: %v", err)
		}
		requests <- body
		_, _ = w.Write([]byte(`{"message":{"content":"{\"category\":\"机械\",\"confidence\":0.88,\"tags\":[\"支架\",\"硬表面\",\"支架\"]}"}}`))
	}))
	defer server.Close()

	c := New(server.URL, "test-model")
	result, err := c.Classify(context.Background(), Input{
		Name:        "检视台支架",
		Category:    "未分类",
		Tags:        []string{"硬表面"},
		Format:      "glb",
		Meshes:      6,
		Triangles:   2012,
		Dimensions:  [3]float64{120, 80, 60},
		Description: "用于支撑检视台的金属支架",
	})
	if err != nil {
		t.Fatalf("分类失败: %v", err)
	}
	if result.Category != "机械" || result.Provider != "ollama/test-model" {
		t.Fatalf("结果 = %+v，想要 机械 / ollama/test-model", result)
	}
	if len(result.Tags) != 2 || result.Tags[0] != "支架" || result.Tags[1] != "硬表面" {
		t.Fatalf("标签去重不正确: %+v", result.Tags)
	}
	select {
	case body := <-requests:
		if body.Model != "test-model" || body.Stream || body.Format != "json" {
			t.Fatalf("请求参数不对: %+v", body)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "检视台支架") {
			t.Fatalf("提示词没有带上资产信息: %+v", body.Messages)
		}
	default:
		t.Fatal("没有收到 Ollama 请求")
	}
}

func TestClassifyFallsBackToRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	defer server.Close()

	c := New(server.URL, "missing-model")
	result, err := c.Classify(context.Background(), Input{
		Name:        "螺旋转子",
		Category:    "未分类",
		Tags:        []string{"阵列", "螺旋", "机械"},
		Format:      "glb",
		Description: "一组螺旋排列的机械转子",
	})
	if err != nil {
		t.Fatalf("兜底分类不应失败: %v", err)
	}
	if result.Category != "机械" || result.Provider != providerRules {
		t.Fatalf("兜底结果 = %+v，想要 机械 / local-rules", result)
	}
	if result.Confidence <= 0 || result.Confidence > 0.8 {
		t.Fatalf("兜底 confidence = %v，超出范围", result.Confidence)
	}
}

func TestDecodeResultHandlesFenceAndNormalizes(t *testing.T) {
	result, err := decodeResult("```json\n{\"category\":\"人物模型\",\"confidence\":92,\"tags\":[\" #角色 \",\"角色\",\"\",\"这是一个超过二十个字符的标签所以会被丢掉这是一个超过二十个字符的标签所以会被丢掉\"]}\n```")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if result.Category != "角色" || result.Confidence != 0.92 {
		t.Fatalf("归一化结果 = %+v", result)
	}
	if len(result.Tags) != 1 || result.Tags[0] != "角色" {
		t.Fatalf("标签归一化 = %+v", result.Tags)
	}
}

func TestRuleClassifyCharacter(t *testing.T) {
	result := ruleClassify(Input{
		Name:     "蕾米埃尔-白",
		Category: "角色",
		Tags:     []string{"mmd", "人物"},
		Format:   "glb",
	})
	if result.Category != "角色" || result.Provider != providerRules {
		t.Fatalf("规则分类 = %+v", result)
	}
}

func TestClassifyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New("http://127.0.0.1:1", "test").Classify(ctx, Input{Name: "支架"})
	if err == nil {
		t.Fatal("已取消的 context 应该返回错误，而不是继续走规则兜底")
	}
}

func TestOpenAICompatibleChatAndModels(t *testing.T) {
	var gotAuth, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/chat/completions":
			var body openAIRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("解析 OpenAI 请求失败: %v", err)
			}
			if body.Model != "gpt-test" || body.Stream || body.Temperature != 0.2 {
				t.Errorf("OpenAI 请求参数不对: %+v", body)
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"兼容接口回答"}}]}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-test"},{"id":"gpt-other"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewFromSettings(Settings{
		Provider: ProviderOpenAI,
		Endpoint: server.URL + "/v1",
		Model:    "gpt-test",
		APIKey:   "sk-openai-test",
	})
	reply, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "你好"}})
	if err != nil {
		t.Fatalf("OpenAI 对话失败: %v", err)
	}
	if reply != "兼容接口回答" || gotPath != "/v1/chat/completions" || gotAuth != "Bearer sk-openai-test" {
		t.Fatalf("reply=%q path=%q auth=%q", reply, gotPath, gotAuth)
	}
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("OpenAI 模型探测失败: %v", err)
	}
	if gotPath != "/v1/models" || len(models) != 2 || models[0] != "gpt-test" {
		t.Fatalf("models=%+v path=%q", models, gotPath)
	}
}

func TestConfigStorePersistsAndMasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai-config.json")
	store := NewConfigStore(path)
	saved, err := store.Save(Settings{
		Provider: ProviderOpenAI,
		Endpoint: "https://api.example.com/v1/",
		Model:    "gpt-test",
		APIKey:   "sk-config-test-abcd",
	})
	if err != nil {
		t.Fatalf("保存配置失败: %v", err)
	}
	if saved.Endpoint != "https://api.example.com/v1" || saved.Model != "gpt-test" {
		t.Fatalf("归一化配置 = %+v", saved)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置文件失败: %v", err)
	}
	if !strings.Contains(string(raw), "sk-config-test-abcd") {
		t.Fatalf("配置文件没有保存密钥: %s", raw)
	}
	reloaded := NewConfigStore(path)
	if got := reloaded.Settings(); got.APIKey != "sk-config-test-abcd" || got.Provider != ProviderOpenAI {
		t.Fatalf("重新加载配置 = %+v", got)
	}
	if hint := KeyHint("sk-config-test-abcd"); hint != "••••abcd" {
		t.Fatalf("密钥尾号 = %q", hint)
	}
}

func TestSettingsValidateRejectsUnknownProviderAndScheme(t *testing.T) {
	if err := (Settings{Provider: "other", Endpoint: "http://127.0.0.1:1234", Model: "x"}).Validate(); err == nil {
		t.Fatal("未知 provider 应该被拒绝")
	}
	if err := (Settings{Provider: ProviderOllama, Endpoint: "file:///etc/passwd", Model: "x"}).Validate(); err == nil {
		t.Fatal("非 http scheme 应该被拒绝")
	}
}

func TestCloudProviderUsesOpenAIProtocolAndLabel(t *testing.T) {
	settings := Settings{
		Provider: ProviderDeepSeek,
		Endpoint: "https://api.deepseek.com/v1",
		Model:    "deepseek-chat",
	}
	if !ProviderUsesOpenAI(settings.Provider) {
		t.Fatal("DeepSeek 应该走 OpenAI 兼容协议")
	}
	if label := settings.ProviderLabel(); label != "DeepSeek / deepseek-chat" {
		t.Fatalf("provider label = %q", label)
	}
	if !settings.Configured() {
		t.Fatal("完整配置应视为已配置")
	}

	unconfigured := DefaultSettings()
	if unconfigured.Configured() {
		t.Fatalf("默认配置不应自动填 provider: %+v", unconfigured)
	}
	if label := unconfigured.ProviderLabel(); label != "未配置" {
		t.Fatalf("空配置 label = %q", label)
	}
}
