package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CheckResult 汇总一次模型配置检查的结果,便于 CLI / TUI 统一呈现。
type CheckResult struct {
	// Endpoint 是最终探针实际命中的 …/chat/completions 地址(成功时)。
	Endpoint string
	// Models 是从 /models 拉取到的模型 id 列表(可能为空,例如网关不提供该接口)。
	Models []string
	// ModelListErr 为拉取模型列表时的原始错误(nil 表示成功)。
	ModelListErr error
	// ModelPresent 表示配置的 Model 是否出现在拉取到的列表中。
	// 当 Models 为空(网关无 /models 接口)时,该字段无参考意义,统一置为 true 以跳过判定。
	ModelPresent bool
	// ProbeErr 为真实 chat 探针的原始错误(nil 表示模型成功应答)。
	ProbeErr error
	// ProbeReply 是探针返回的一小段模型应答文本(截断),便于人工确认确实是该模型在回话。
	ProbeReply string
}

// OK 表示模型可用:探针成功应答,且(在能拉到列表时)模型存在于列表中。
func (r *CheckResult) OK() bool {
	if r.ProbeErr != nil {
		return false
	}
	if len(r.Models) > 0 && !r.ModelPresent {
		return false
	}
	return true
}

// chatEndpoints 推导出可能的 …/chat/completions 探针地址,规则与 modelsEndpoints 对齐:
// 优先使用 NormalizeBaseURL 归一化后的 base 直接拼 /chat/completions,再补 origin/v1 兜底变体。
func chatEndpoints(baseURL string) ([]string, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		return nil, fmt.Errorf("base URL is empty")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")

	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL")
	}

	var out []string
	seen := map[string]bool{}
	add := func(e string) {
		if e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}

	// 已经指向 chat/completions,直接用。
	if strings.HasSuffix(base, "/chat/completions") {
		add(base)
		return out, nil
	}

	origin := u.Scheme + "://" + u.Host

	// base 以 /v1 结尾,或路径中含 /v1/ → 认为 base 已是正确前缀。
	if strings.HasSuffix(base, "/v1") || strings.Contains(u.Path, "/v1/") {
		add(base + "/chat/completions")
		return out, nil
	}

	// 仅有 origin(无路径)→ 标准 OpenAI 形态 origin/v1/chat/completions,并兜底 origin/chat/completions。
	if u.Path == "" || u.Path == "/" {
		add(origin + "/v1/chat/completions")
		add(origin + "/chat/completions")
		return out, nil
	}

	// 其他带路径的自定义网关:先按 base 直接拼,再补 origin/v1 兜底。
	add(base + "/chat/completions")
	add(origin + "/v1/chat/completions")
	add(origin + "/chat/completions")
	return out, nil
}

// chatProbeRequest 是发给 …/chat/completions 的最小探针请求体。
type chatProbeRequest struct {
	Model           string             `json:"model"`
	Messages        []chatProbeMessage `json:"messages"`
	MaxTokens       int                `json:"max_tokens,omitempty"`
	Temperature     *float32           `json:"temperature,omitempty"`
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
}

type chatProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatProbeResponse 仅解析我们关心的应答文本片段。
type chatProbeResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// probeChat 向配置的模型发起一次最小真实对话,确认模型确实可应答。
// 返回实际命中的 endpoint、一小段应答文本,以及原始错误(nil 表示成功)。
// 错误统一为可被 ClassifyError 识别的类型:非 2xx 返回 *APIError,网络错误保留 %w 链。
func probeChat(ctx context.Context, cfg LLMConfig) (endpoint, reply string, err error) {
	endpoints, err := chatEndpoints(cfg.BaseURL)
	if err != nil {
		return "", "", err
	}

	payload := chatProbeRequest{
		Model:     cfg.Model,
		Messages:  []chatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	}
	// 与 agent.go 的条件字段模式对齐:仅在显式设置时才带上。
	if cfg.Temperature != nil {
		payload.Temperature = cfg.Temperature
	}
	if cfg.ReasoningEffort != "" {
		payload.ReasoningEffort = cfg.ReasoningEffort
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal probe payload: %w", err)
	}

	client := &http.Client{Timeout: 20 * time.Second}

	var lastErr error
	for _, ep := range endpoints {
		rep, perr := probeOnce(ctx, client, ep, cfg.APIKey, body)
		if perr == nil {
			return ep, rep, nil
		}
		lastErr = perr
		// 404 需区分两种含义:
		//   - 模型名不存在(model_not_found):路径与鉴权都正常,只是模型不对,
		//     继续盲试其它候选路径只会白费,且可能把真实的模型错误覆盖掉,应立即返回;
		//   - 接口路径不存在:才是"这个路径不对",继续尝试下一个候选。
		if apiErr, ok := asAPIError(perr); ok && apiErr.StatusCode == http.StatusNotFound {
			if isModelNotFoundBody(apiErr.Body) {
				return ep, "", perr
			}
			continue
		}
		return ep, "", perr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no chat endpoint tried")
	}
	return "", "", lastErr
}

// probeOnce 对单个 endpoint 发一次探针请求。
func probeOnce(ctx context.Context, client *http.Client, endpoint, apiKey string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "wikify/0.1")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", newAPIError(resp.StatusCode, resp.Status, respBody)
	}

	var parsed chatProbeResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// 2xx 但无法解析:视为格式无法识别,交给 ClassifyError 统一提示。
		return "", ErrUnrecognizedModels
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		// 少数网关以 200 包裹 error 字段。
		return "", fmt.Errorf("%s", parsed.Error.Message)
	}

	reply := ""
	if len(parsed.Choices) > 0 {
		reply = strings.TrimSpace(parsed.Choices[0].Message.Content)
	}
	return reply, nil
}

// asAPIError 是 errors.As 到 *APIError 的小封装,便于内部分支判断。
func asAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if err != nil && errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// CheckModel 编排一次完整的模型配置检查:
//  1. 拉取远端模型列表(ListRemoteModels);
//  2. 判定配置的 Model 是否在列表中(列表为空时跳过判定);
//  3. 发起真实 chat 探针,确认模型可应答。
//
// 每一步的原始错误都保留在 CheckResult 中,调用方可用 ClassifyError 生成中文提示。
func CheckModel(ctx context.Context, cfg LLMConfig) *CheckResult {
	res := &CheckResult{ModelPresent: true}

	models, err := ListRemoteModels(cfg.BaseURL, cfg.APIKey)
	res.Models = models
	res.ModelListErr = err

	if err == nil && len(models) > 0 {
		res.ModelPresent = modelInList(cfg.Model, models)
	}

	ep, reply, perr := probeChat(ctx, cfg)
	res.Endpoint = ep
	res.ProbeReply = reply
	res.ProbeErr = perr

	return res
}

// modelInList 判定 model 是否命中列表(精确匹配,或忽略常见 provider 前缀后匹配)。
func modelInList(model string, models []string) bool {
	if model == "" {
		return false
	}
	for _, m := range models {
		if m == model {
			return true
		}
	}
	return false
}

