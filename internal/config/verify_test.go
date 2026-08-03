package config

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestChatEndpoints(t *testing.T) {
	cases := map[string][]string{
		"https://api.deepseek.com/v1":  {"https://api.deepseek.com/v1/chat/completions"},
		"https://api.deepseek.com/v1/": {"https://api.deepseek.com/v1/chat/completions"},
		"https://api.deepseek.com/v1/chat/completions": {"https://api.deepseek.com/v1/chat/completions"},
		"https://sub.example.com": {
			"https://sub.example.com/v1/chat/completions",
			"https://sub.example.com/chat/completions",
		},
		"sub.example.com": {
			"https://sub.example.com/v1/chat/completions",
			"https://sub.example.com/chat/completions",
		},
		"https://sub.example.com/openai": {
			"https://sub.example.com/openai/chat/completions",
			"https://sub.example.com/v1/chat/completions",
			"https://sub.example.com/chat/completions",
		},
	}
	for in, want := range cases {
		got, err := chatEndpoints(in)
		if err != nil {
			t.Fatalf("chatEndpoints(%q) unexpected error: %v", in, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("chatEndpoints(%q) = %v, want %v", in, got, want)
		}
	}

	if _, err := chatEndpoints("   "); err == nil {
		t.Error("chatEndpoints(blank) expected error, got nil")
	}
}

func TestClassifyErrorKinds(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"nil", nil, KindUnknown},
		{"empty-list", ErrEmptyModelList, KindEmptyList},
		{"unrecognized", ErrUnrecognizedModels, KindUnrecognized},
		{"auth-401", newAPIError(401, "401 Unauthorized", []byte(`{"error":"bad key"}`)), KindAuth},
		{"auth-403", newAPIError(403, "403 Forbidden", nil), KindAuth},
		{"notfound-404", newAPIError(404, "404 Not Found", nil), KindNotFound},
		{"model-not-found-404", newAPIError(404, "404 Not Found", []byte(`{"error":{"message":"The model 'gpt-x' does not exist","type":"invalid_request_error","code":"model_not_found"}}`)), KindModelNotFound},
		{"model-not-found-404-zh", newAPIError(404, "404 Not Found", []byte(`{"error":{"message":"模型不存在"}}`)), KindModelNotFound},
		{"ratelimit-429", newAPIError(429, "429 Too Many Requests", nil), KindRateLimit},
		{"server-500", newAPIError(500, "500 Internal Server Error", nil), KindServer},
		{"server-503", newAPIError(503, "503 Service Unavailable", nil), KindServer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, msg := ClassifyError(c.err)
			if kind != c.want {
				t.Errorf("ClassifyError(%v) kind = %v, want %v", c.err, kind, c.want)
			}
			if c.err != nil && c.want != KindUnknown && msg == "" {
				t.Errorf("ClassifyError(%v) returned empty message", c.err)
			}
		})
	}
}

func TestClassifyErrorNetwork(t *testing.T) {
	// *url.Error 包装的连接失败应归类为网络问题。
	netErr := &url.Error{
		Op:  "Post",
		URL: "https://unreachable.invalid/v1/chat/completions",
		Err: errors.New("dial tcp: lookup unreachable.invalid: no such host"),
	}
	kind, msg := ClassifyError(netErr)
	if kind != KindNetwork {
		t.Errorf("ClassifyError(url.Error) kind = %v, want %v", kind, KindNetwork)
	}
	if msg == "" {
		t.Error("ClassifyError(url.Error) returned empty message")
	}
}

// TestClassifyErrorBadHeader 锁定顺序约束:非法请求头(api_key 含控制字符)
// 会被 Go 的 net/http 包装成 *url.Error,而 *url.Error 满足 net.Error 接口。
// 该分支必须先于网络分支命中,否则会把客户端预检失败误报为"网络连接失败"。
func TestClassifyErrorBadHeader(t *testing.T) {
	// 模拟 net/http 在发出请求前拒绝非法 Authorization 头时产生的错误。
	badHdr := &url.Error{
		Op:  "Post",
		URL: "https://api.example.com/v1/chat/completions",
		Err: errors.New(`net/http: invalid header field value for "Authorization"`),
	}
	kind, msg := ClassifyError(badHdr)
	if kind != KindBadHeader {
		t.Errorf("ClassifyError(bad header) kind = %v, want %v (must not fall through to net.Error branch)", kind, KindBadHeader)
	}
	if msg == "" {
		t.Error("ClassifyError(bad header) returned empty message")
	}
}

func TestProbeChatSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer probe-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer probe-key")
		}
		var req chatProbeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode probe request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("probe model = %q, want %q", req.Model, "test-model")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer srv.Close()

	ep, reply, err := probeChat(context.Background(), LLMConfig{
		Model:   "test-model",
		APIKey:  "probe-key",
		BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("probeChat unexpected error: %v", err)
	}
	if reply != "pong" {
		t.Errorf("probe reply = %q, want %q", reply, "pong")
	}
	if ep != srv.URL+"/v1/chat/completions" {
		t.Errorf("probe endpoint = %q, want %q", ep, srv.URL+"/v1/chat/completions")
	}
}

func TestProbeChatAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	_, _, err := probeChat(context.Background(), LLMConfig{
		Model:   "test-model",
		BaseURL: srv.URL + "/v1",
	})
	if err == nil {
		t.Fatal("probeChat expected error, got nil")
	}
	if kind, _ := ClassifyError(err); kind != KindAuth {
		t.Errorf("ClassifyError(probe err) kind = %v, want %v", kind, KindAuth)
	}
}

// TestProbeChatModelNotFoundStopsProbing 保证:当首个候选路径已返回 model_not_found 的
// 404(路径与鉴权都正常,只是模型名不对)时,probeChat 立即返回该错误,不再盲试
// origin/chat/completions 等后续候选——否则真实的模型错误会被"路径不存在"覆盖掉。
func TestProbeChatModelNotFoundStopsProbing(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"The model 'gpt-x' does not exist","type":"invalid_request_error","code":"model_not_found"}}`))
	}))
	defer srv.Close()

	// 仅传 origin(无 /v1):chatEndpoints 会生成
	//   origin/v1/chat/completions 与 origin/chat/completions 两个候选。
	ep, _, err := probeChat(context.Background(), LLMConfig{
		Model:   "gpt-x",
		BaseURL: srv.URL,
	})
	if err == nil {
		t.Fatal("probeChat expected model_not_found error, got nil")
	}
	if kind, _ := ClassifyError(err); kind != KindModelNotFound {
		t.Errorf("ClassifyError(probe err) kind = %v, want %v", kind, KindModelNotFound)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server hit %d times, want 1 (must not fall through to other candidates)", got)
	}
	if ep != srv.URL+"/v1/chat/completions" {
		t.Errorf("probe endpoint = %q, want %q", ep, srv.URL+"/v1/chat/completions")
	}
}

func TestCheckModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"test-model"},{"id":"other"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	res := CheckModel(context.Background(), LLMConfig{
		Model:   "test-model",
		APIKey:  "k",
		BaseURL: srv.URL + "/v1",
	})
	if !res.OK() {
		t.Fatalf("CheckModel OK() = false, want true (modelListErr=%v probeErr=%v)", res.ModelListErr, res.ProbeErr)
	}
	if !res.ModelPresent {
		t.Error("ModelPresent = false, want true")
	}
	if res.ProbeReply != "pong" {
		t.Errorf("ProbeReply = %q, want %q", res.ProbeReply, "pong")
	}
	if res.Endpoint != srv.URL+"/v1/chat/completions" {
		t.Errorf("Endpoint = %q, want %q", res.Endpoint, srv.URL+"/v1/chat/completions")
	}
}

func TestCheckModelMissingModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"other"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	res := CheckModel(context.Background(), LLMConfig{
		Model:   "test-model",
		BaseURL: srv.URL + "/v1",
	})
	if res.ModelPresent {
		t.Error("ModelPresent = true, want false (model not in list)")
	}
	if res.OK() {
		t.Error("OK() = true, want false (model absent from non-empty list)")
	}
}
