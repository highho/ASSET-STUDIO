// Package ai 提供本地 AI 运行时：Ollama / OpenAI 兼容接口的配置、分类、对话与
// 连通性探测。配置只存在服务端，分类失败时退回规则分类，对话失败则明确报错。
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	providerRules = "local-rules"
	maxTags       = 6
	maxTagRunes   = 20
)

// Input 是一次分类需要的资产信息。SourceCode 只放 source.py 的开头部分，
// 不让一条超长程序把提示词撑爆。
type Input struct {
	Name        string
	Description string
	Category    string
	Tags        []string
	Format      string
	SourceName  string
	SourceCode  string
	Meshes      int
	Triangles   int
	Dimensions  [3]float64
}

// Result 是对外的分类结果。Provider 只用于本次响应，数据库当前只存
// category / confidence / tags 三列，所以重载后不会保留 provider。
type Result struct {
	Category   string   `json:"category"`
	Confidence float64  `json:"confidence"`
	Tags       []string `json:"tags"`
	Provider   string   `json:"provider,omitempty"`
}

// Message 是对话消息。分类时也复用它，避免两套 message 结构。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Classifier 是一个按配置构造的轻量客户端；每次请求新建，配置更新后立即生效。
type Classifier struct {
	Provider string
	Endpoint string
	Model    string
	APIKey   string
	Client   *http.Client
}

func New(endpoint, model string) *Classifier {
	return NewFromSettings(Settings{Provider: ProviderOllama, Endpoint: endpoint, Model: model})
}

func NewFromSettings(s Settings) *Classifier {
	s = s.Normalized()
	return &Classifier{
		Provider: s.Provider,
		Endpoint: s.Endpoint,
		Model:    s.Model,
		APIKey:   s.APIKey,
		Client:   &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Classifier) Label() string {
	if c.Provider == "" || c.Model == "" {
		return "未配置"
	}
	return c.Provider + "/" + c.Model
}

type chatRequest struct {
	Model    string         `json:"model"`
	Stream   bool           `json:"stream"`
	Format   string         `json:"format,omitempty"`
	Messages []Message      `json:"messages"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error string `json:"error"`
}

type openAIRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stream         bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type modelsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Chat 只做普通对话；失败直接返回错误，不用规则假装 AI 回答。
func (c *Classifier) Chat(ctx context.Context, messages []Message) (string, error) {
	return c.complete(ctx, messages, false, 512, 0.2)
}

// Classify 先问模型；网络错误、模型没装、返回不是合法 JSON 时都退回本地规则。
// 只有调用方主动取消（例如浏览器关闭）才把错误交给上层，避免已经断开还写库。
func (c *Classifier) Classify(ctx context.Context, in Input) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	messages := []Message{
		{Role: "system", Content: classifySystemPrompt},
		{Role: "user", Content: userPrompt(in)},
	}
	content, err := c.complete(ctx, messages, true, 96, 0)
	if err == nil {
		result, decodeErr := decodeResult(content)
		if decodeErr == nil {
			result.Provider = c.Label()
			return result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return ruleClassify(in), nil
}

func (c *Classifier) complete(ctx context.Context, messages []Message, jsonMode bool, maxTokens int, temperature float64) (string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	if ProviderUsesOpenAI(c.Provider) {
		return c.openAIComplete(ctx, client, messages, jsonMode, maxTokens, temperature)
	}
	return c.ollamaComplete(ctx, client, messages, jsonMode, maxTokens, temperature)
}

func (c *Classifier) ollamaComplete(ctx context.Context, client *http.Client, messages []Message, jsonMode bool, maxTokens int, temperature float64) (string, error) {
	payload := chatRequest{
		Model:    c.Model,
		Stream:   false,
		Messages: messages,
		Options: map[string]any{
			"temperature": temperature,
			"num_predict": maxTokens,
			"top_p":       0.9,
		},
	}
	if jsonMode {
		payload.Format = "json"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	url := ollamaURL(c.Endpoint, "/api/chat")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("ollama: %s", parsed.Error)
	}
	if strings.TrimSpace(parsed.Message.Content) == "" {
		return "", fmt.Errorf("ollama 返回了空内容")
	}
	return parsed.Message.Content, nil
}

func (c *Classifier) openAIComplete(ctx context.Context, client *http.Client, messages []Message, jsonMode bool, maxTokens int, temperature float64) (string, error) {
	payload := openAIRequest{
		Model:       c.Model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Stream:      false,
	}
	if jsonMode {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIChatURL(c.Endpoint), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.addAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI 服务 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("AI 服务: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("AI 服务返回了空内容")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Models 用于配置页的「测试连接」：Ollama 读 /api/tags，OpenAI 兼容读 /models。
func (c *Classifier) Models(ctx context.Context) ([]string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	url := ollamaURL(c.Endpoint, "/api/tags")
	if ProviderUsesOpenAI(c.Provider) {
		url = openAIModelsURL(c.Endpoint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.addAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed modelsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(parsed.Models)+len(parsed.Data))
	for _, item := range parsed.Models {
		if name := strings.TrimSpace(item.Name); name != "" {
			models = append(models, name)
		}
	}
	for _, item := range parsed.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, nil
}

func (c *Classifier) addAuth(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
}

func ollamaURL(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

func openAIChatURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1/") {
		base += "/v1"
	}
	return base + "/chat/completions"
}

func openAIModelsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/models") {
		return base
	}
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1/") {
		base += "/v1"
	}
	return base + "/models"
}

const classifySystemPrompt = `你是三维资产库的智能分类器。根据资产元数据判断它最适合的主分类，并给出简短中文标签。
只输出一个 JSON 对象，不要输出解释、Markdown 或代码围栏：
{"category":"角色|机械|自然|几何体|场景|装配|代码生成|未分类","confidence":0.0到1.0之间的数字,"tags":["2到4个短标签"]}
分类口径：
- 角色：人形、人物、二次元角色、MMD/PMX 人物。
- 机械：支架、转子、齿轮、轴、夹具、工业零件等硬表面机械件。
- 自然：植物、水晶、岩石、地形、自然生成物。
- 几何体：球、环、立方、圆柱、环形结等抽象几何/参数化形状。
- 场景：房间、建筑、环境、陈设组合。
- 装配：由多个零件组合成的装配体。
- 代码生成：明显以 sand3d/Python 程序生成的参数化模型；若内容明显属于其它类，优先内容。
- 未分类：信息不足。
confidence 必须反映信息充分程度，不要一律给 1。`

func userPrompt(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "名称：%s\n", clip(in.Name, 120))
	fmt.Fprintf(&b, "现有分类：%s\n", clip(in.Category, 40))
	fmt.Fprintf(&b, "现有标签：%s\n", clip(strings.Join(in.Tags, ", "), 200))
	fmt.Fprintf(&b, "描述：%s\n", clip(in.Description, 500))
	fmt.Fprintf(&b, "格式：%s；网格 %d；三角面 %d；尺寸 %.3f × %.3f × %.3f\n",
		in.Format, in.Meshes, in.Triangles, in.Dimensions[0], in.Dimensions[1], in.Dimensions[2])
	if in.SourceName != "" {
		fmt.Fprintf(&b, "来源文件名：%s\n", clip(in.SourceName, 120))
	}
	if strings.TrimSpace(in.SourceCode) != "" {
		fmt.Fprintf(&b, "生成源码（截断）：\n%s\n", clip(in.SourceCode, 1400))
	}
	b.WriteString("请分类。")
	return b.String()
}

func decodeResult(content string) (Result, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return Result{}, fmt.Errorf("模型没有返回 JSON 对象")
	}
	var result Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return Result{}, err
	}
	result.Category = normalizeCategory(result.Category)
	if result.Category == "" {
		return Result{}, fmt.Errorf("模型没有给出分类")
	}
	result.Confidence = normalizeConfidence(result.Confidence)
	result.Tags = normalizeTags(result.Tags)
	return result, nil
}

func extractJSONObject(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(content[start : end+1])
}

func normalizeCategory(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	aliases := map[string]string{
		"人物": "角色", "人物模型": "角色", "角色模型": "角色", "character": "角色", "anime": "角色",
		"hard-surface": "机械", "hard surface": "机械", "mechanical": "机械", "machine": "机械",
		"nature": "自然", "plant": "自然",
		"geometry": "几何体", "geometric": "几何体",
		"scene": "场景", "environment": "场景",
		"assembly": "装配", "装配体": "装配",
		"code": "代码生成", "generated": "代码生成", "sand3d": "代码生成",
		"unknown": "未分类",
	}
	if mapped, ok := aliases[strings.ToLower(value)]; ok {
		return mapped
	}
	for _, allowed := range []string{"角色", "机械", "自然", "几何体", "场景", "装配", "代码生成", "未分类"} {
		if value == allowed {
			return allowed
		}
	}
	return clip(value, 32)
}

func normalizeConfidence(raw float64) float64 {
	if raw > 1 && raw <= 100 {
		raw /= 100
	}
	if raw <= 0 {
		return 0.55
	}
	if raw > 1 {
		return 1
	}
	return raw
}

func normalizeTags(raw []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, maxTags)
	for _, tag := range raw {
		tag = strings.TrimSpace(tag)
		tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
		if tag == "" || utf8.RuneCountInString(tag) > maxTagRunes {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, tag)
		if len(out) >= maxTags {
			break
		}
	}
	return out
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

// ruleClassify 是离线兜底：不依赖模型也能给出一个可用的分类，
// 但 provider 明确写 local-rules，避免把规则结果伪装成模型结果。
func ruleClassify(in Input) Result {
	text := strings.ToLower(strings.Join([]string{
		in.Name, in.Description, in.Category, strings.Join(in.Tags, " "), in.SourceName, in.SourceCode,
	}, " "))

	rules := []struct {
		category string
		words    []string
		tags     []string
	}{
		{"装配", []string{"装配", "assembly", "组合体", "多零件组合"}, []string{"装配", "组合"}},
		{"角色", []string{"角色", "人物", "人偶", "二次元", "mmd", "pmx", "蕾米埃尔", "character", "anime", "girl", "boy", "面部", "头发"}, []string{"角色", "人物"}},
		{"机械", []string{"机械", "支架", "转子", "齿轮", "轴", "轴承", "夹具", "螺栓", "螺旋", "工业", "零件", "机械臂"}, []string{"机械", "硬表面"}},
		{"自然", []string{"自然", "水晶", "植物", "树", "岩石", "花", "草", "珊瑚", "云", "地形", "山"}, []string{"自然"}},
		{"场景", []string{"场景", "建筑", "房间", "环境", "城市", "陈设", "室内"}, []string{"场景", "环境"}},
		{"几何体", []string{"几何", "环形结", "球", "立方", "圆柱", "圆锥", "网格", "torus", "knot", "sphere", "cube", "cylinder"}, []string{"几何体", "参数化"}},
		{"代码生成", []string{"代码生成", "sand3d", "参数化", "python", "程序", "源码"}, []string{"sand3d", "代码生成"}},
	}
	scores := make(map[string]int)
	matchedTags := make(map[string][]string)
	for _, rule := range rules {
		for _, word := range rule.words {
			if strings.Contains(text, word) {
				scores[rule.category]++
				matchedTags[rule.category] = append(matchedTags[rule.category], rule.tags...)
			}
		}
	}
	if in.Category != "" {
		scores[in.Category]++
	}
	best, bestScore := "", 0
	for _, category := range []string{"角色", "机械", "自然", "几何体", "场景", "装配", "代码生成"} {
		if scores[category] > bestScore {
			best, bestScore = category, scores[category]
		}
	}
	if best == "" {
		if in.Category != "" {
			best = normalizeCategory(in.Category)
		} else {
			best = "未分类"
		}
	}
	confidence := 0.45 + float64(bestScore)*0.05
	if confidence > 0.8 {
		confidence = 0.8
	}
	if bestScore == 0 {
		confidence = 0.35
	}
	tags := normalizeTags(append(matchedTags[best], in.Tags...))
	if len(tags) == 0 {
		tags = []string{best}
	}
	return Result{Category: best, Confidence: confidence, Tags: tags, Provider: providerRules}
}
