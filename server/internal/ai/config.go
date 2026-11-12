package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	ProviderOllama      = "ollama"
	ProviderOpenAI      = "openai"
	ProviderDeepSeek    = "deepseek"
	ProviderMoonshot    = "moonshot"
	ProviderZhipu       = "zhipu"
	ProviderDashScope   = "dashscope"
	ProviderSiliconFlow = "siliconflow"
	ProviderGroq        = "groq"
	ProviderLMStudio    = "lmstudio"
	ProviderCustom      = "custom"

	DefaultOllamaEndpoint = "http://127.0.0.1:11434"
	DefaultOllamaModel    = "qwen2.5:7b-instruct"
	DefaultOpenAIEndpoint = "https://api.openai.com/v1"
	DefaultOpenAIModel    = "gpt-4o-mini"
)

type providerSpec struct {
	Label  string
	OpenAI bool
}

// providerSpecs 是所有允许保存的 provider。除 Ollama 外都走 OpenAI 兼容协议；
// 这里不放 endpoint/model 默认值，避免“没配置却悄悄用了某个预设模型”。
var providerSpecs = map[string]providerSpec{
	ProviderOllama:      {Label: "Ollama"},
	ProviderOpenAI:      {Label: "OpenAI", OpenAI: true},
	ProviderDeepSeek:    {Label: "DeepSeek", OpenAI: true},
	ProviderMoonshot:    {Label: "Kimi", OpenAI: true},
	ProviderZhipu:       {Label: "智谱 GLM", OpenAI: true},
	ProviderDashScope:   {Label: "通义千问", OpenAI: true},
	ProviderSiliconFlow: {Label: "SiliconFlow", OpenAI: true},
	ProviderGroq:        {Label: "Groq", OpenAI: true},
	ProviderLMStudio:    {Label: "LM Studio", OpenAI: true},
	ProviderCustom:      {Label: "自定义兼容", OpenAI: true},
}

func providerName(provider string) string {
	p, ok := providerSpecs[strings.ToLower(strings.TrimSpace(provider))]
	if !ok {
		return "未配置"
	}
	return p.Label
}

// ProviderUsesOpenAI 说明这个 provider 是否走 /v1/chat/completions。
// 未知 provider 一律按 Ollama 处理，宁可失败也不把密钥发到错误协议上。
func ProviderUsesOpenAI(provider string) bool {
	p, ok := providerSpecs[strings.ToLower(strings.TrimSpace(provider))]
	return ok && p.OpenAI
}

// Settings 是 AI 运行时的完整配置。APIKey 只留在服务端配置文件里，
// GET 接口永远不回传明文。
type Settings struct {
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey,omitempty"`
}

// DefaultSettings 是“尚未配置”的状态。首次安装由引导页选择 provider 与模型，
// 不再默认落到 qwen2.5，避免用户没注意就以为本机已经配好。
func DefaultSettings() Settings {
	return Settings{}
}

// SettingsFromEnv 让第一次启动可以通过环境变量给出默认配置；
// 一旦用户保存过 ai-config.json，文件内容优先。
func SettingsFromEnv() Settings {
	s := Settings{}
	if p := strings.TrimSpace(os.Getenv("ASSET_STUDIO_AI_PROVIDER")); p != "" {
		s.Provider = p
	}
	if e := strings.TrimSpace(os.Getenv("ASSET_STUDIO_AI_URL")); e != "" {
		s.Endpoint = e
	}
	if m := strings.TrimSpace(os.Getenv("ASSET_STUDIO_AI_MODEL")); m != "" {
		s.Model = m
	}
	if k := strings.TrimSpace(os.Getenv("ASSET_STUDIO_AI_KEY")); k != "" {
		s.APIKey = k
	}
	return s.Normalized()
}

// Normalized 只做清理，不补默认模型；空配置原样保留，交给引导页处理。
func (s Settings) Normalized() Settings {
	s.Provider = strings.ToLower(strings.TrimSpace(s.Provider))
	s.Endpoint = strings.TrimRight(strings.TrimSpace(s.Endpoint), "/")
	if !strings.HasPrefix(s.Endpoint, "http://") && !strings.HasPrefix(s.Endpoint, "https://") {
		s.Endpoint = ""
	}
	s.Model = strings.TrimSpace(s.Model)
	s.APIKey = strings.TrimSpace(s.APIKey)
	return s
}

func (s Settings) Configured() bool {
	return s.Provider != "" && s.Endpoint != "" && s.Model != ""
}

func (s Settings) Validate() error {
	provider := strings.ToLower(strings.TrimSpace(s.Provider))
	if _, ok := providerSpecs[provider]; !ok {
		return fmt.Errorf("不支持的 provider: %s", s.Provider)
	}
	normalized := s.Normalized()
	if normalized.Endpoint == "" {
		return fmt.Errorf("Endpoint 必须是 http:// 或 https:// 地址")
	}
	if normalized.Model == "" {
		return fmt.Errorf("模型名不能为空")
	}
	if len(normalized.Endpoint) > 500 || len(normalized.Model) > 200 || len(normalized.APIKey) > 4096 {
		return fmt.Errorf("配置过长")
	}
	return nil
}

// ProviderLabel 用于接入层展示，不包含密钥。
func (s Settings) ProviderLabel() string {
	if s.Provider == "" {
		return "未配置"
	}
	label := providerName(s.Provider)
	if s.Model == "" {
		return label
	}
	return label + " / " + s.Model
}

// ConfigStore 把配置写到 data/ai-config.json。写文件用临时文件 + rename，
// 避免半截 JSON；密钥文件权限尽量收窄到 0600（Windows 上由 ACL 兜底）。
type ConfigStore struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

func NewConfigStore(path string) *ConfigStore {
	store := &ConfigStore{path: path, cur: SettingsFromEnv()}
	if raw, err := os.ReadFile(path); err == nil {
		var saved Settings
		if json.Unmarshal(raw, &saved) == nil {
			// 文件存在就以文件为准，缺省字段再用 default 补齐。
			store.cur = saved.Normalized()
		}
	}
	return store
}

func (c *ConfigStore) Settings() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cur
}

func (c *ConfigStore) Save(next Settings) (Settings, error) {
	if err := next.Validate(); err != nil {
		return Settings{}, err
	}
	normalized := next.Normalized()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return Settings{}, err
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return Settings{}, err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		_ = os.Remove(tmp)
		return Settings{}, err
	}
	c.mu.Lock()
	c.cur = normalized
	c.mu.Unlock()
	return normalized, nil
}

// KeyHint 只返回尾部四位，供页面确认「已保存的是哪一把钥匙」。
func KeyHint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= 4 {
		return strings.Repeat("•", len(runes))
	}
	return "••••" + string(runes[len(runes)-4:])
}
