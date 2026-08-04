package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// APIError 表示模型服务端返回的非 2xx HTTP 响应。
// 携带状态码与响应体,便于调用方(verify、TUI)区分鉴权/路径/服务端错误,
// 而无需再解析格式化后的字符串。
type APIError struct {
	StatusCode int
	Status     string // 形如 "404 Not Found"
	Body       []byte
}

func newAPIError(status int, statusText string, body []byte) *APIError {
	return &APIError{StatusCode: status, Status: statusText, Body: body}
}

func (e *APIError) Error() string {
	preview := strings.TrimSpace(string(e.Body))
	// 按 rune 截断,避免切断 UTF-8 多字节字符产生乱码。
	if r := []rune(preview); len(r) > 160 {
		preview = string(r[:160]) + "…"
	}
	if preview == "" {
		preview = e.Status
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, preview)
}

// 哨兵错误:成功响应但内容异常的两种情况,便于 errors.Is 精确判定。
var (
	// ErrEmptyModelList 表示服务端返回成功,但模型列表为空。
	ErrEmptyModelList = errors.New("empty model list")
	// ErrUnrecognizedModels 表示响应体无法按已知格式解析出模型 id。
	ErrUnrecognizedModels = errors.New("unrecognized models response")
)

// ErrorKind 是对模型配置相关错误的归类,供 UI 分支与排查建议使用。
type ErrorKind int

const (
	KindUnknown ErrorKind = iota
	KindNetwork
	KindAuth
	KindNotFound
	KindRateLimit
	KindServer
	KindEmptyList
	KindUnrecognized
	KindBadHeader
	// KindModelNotFound 表示 HTTP 404 的响应体指明"模型名不存在"
	// (OpenAI 兼容网关的 model_not_found),而非接口路径错误。
	KindModelNotFound
)

// ClassifyError 把 ListRemoteModels / verify 过程中的错误归类,
// 并给出面向终端用户的中文说明(含排查建议)。
func ClassifyError(err error) (ErrorKind, string) {
	if err == nil {
		return KindUnknown, ""
	}

	// 成功响应但内容异常(哨兵)
	if errors.Is(err, ErrEmptyModelList) {
		return KindEmptyList, "服务端返回成功,但模型列表为空。该网关可能不提供 /models 列表接口,可手动填写模型名后重试。"
	}
	if errors.Is(err, ErrUnrecognizedModels) {
		return KindUnrecognized, "无法解析模型列表响应格式。请确认 base_url 指向 OpenAI 兼容接口(应返回 {\"data\":[{\"id\":...}]})。"
	}

	// 非 2xx HTTP 状态(类型化)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:
			return KindAuth, fmt.Sprintf("鉴权失败(HTTP %d)。请检查 api_key 是否正确、是否过期或额度不足。", apiErr.StatusCode)
		case apiErr.StatusCode == 404:
			// 404 有两种含义,需靠响应体区分:
			//   1. 模型名不存在(网关能正常受理请求,只是没有该模型)→ 引导改模型名;
			//   2. 接口路径不存在(base_url 前缀错误)→ 引导改 base_url。
			// 若二者都误报为路径问题,会把用户推向"换协议/换 API"的错误方向。
			if isModelNotFoundBody(apiErr.Body) {
				return KindModelNotFound, "模型名不存在(HTTP 404,model_not_found)。接口与鉴权均正常,只是网关上没有该模型。请运行 `wikify config` 从可用模型列表中选择,或核对模型名后重填。"
			}
			return KindNotFound, "接口路径不存在(HTTP 404)。请检查 base_url 前缀是否正确(多数服务需要以 /v1 结尾)。"
		case apiErr.StatusCode == 429:
			return KindRateLimit, "请求过于频繁(HTTP 429)。请稍后重试,或检查账号限流/额度。"
		case apiErr.StatusCode >= 500:
			return KindServer, fmt.Sprintf("服务端错误(HTTP %d)。可能是网关或上游模型服务暂时不可用,请稍后重试。", apiErr.StatusCode)
		default:
			return KindUnknown, apiErr.Error()
		}
	}

	// 非法请求头(客户端预检失败):api_key 或 base_url 含有控制字符
	// (如 NUL、换行),Go 的 net/http 会在发出请求前拒绝,包装为 *url.Error。
	// 该分支必须先于 net.Error 判定,否则会被误报为"网络连接失败"。
	if strings.Contains(err.Error(), "invalid header field value") {
		return KindBadHeader, "请求头非法:api_key 含有不可见字符(如空格、换行或 NUL)。请重新粘贴 api_key 后再试。"
	}

	// 网络层(超时 / 连接失败 / DNS)
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return KindNetwork, "网络超时。请检查网络连通性、代理设置,或 base_url 主机是否可达。"
		}
		return KindNetwork, "网络连接失败。请检查 base_url 是否正确、主机是否可达或是否需要代理。"
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return KindNetwork, "网络请求失败。请检查 base_url 是否正确、主机是否可达或是否需要代理。"
	}

	return KindUnknown, err.Error()
}

// isModelNotFoundBody 判断一个 HTTP 404 的响应体是否表示"模型名不存在",
// 而非"接口路径不存在"。OpenAI 兼容网关(one-api、new-api 等)在模型名
// 拼写错误或未开通时会返回 404,响应体形如:
//
//	{"error":{"message":"model 'gpt-x' not found","type":"invalid_request_error","code":"model_not_found"}}
//
// 判定采用大小写无关的子串匹配,覆盖各家网关的措辞差异,避免误把
// "路径不存在"的普通 404 也归为模型错误。
func isModelNotFoundBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	s := strings.ToLower(string(body))
	// 明确的机器可读码,最可靠。
	if strings.Contains(s, "model_not_found") {
		return true
	}
	// 文案兜底:必须同时提到"模型"与"不存在/未找到"语义,
	// 才认定为模型问题,避免命中纯路径类 404。
	mentionsModel := strings.Contains(s, "model") || strings.Contains(s, "模型")
	if !mentionsModel {
		return false
	}
	for _, kw := range []string{
		"not found", "does not exist", "not exist",
		"no such model", "unknown model", "invalid model",
		"不存在", "未找到", "无效",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}
