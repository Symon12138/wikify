package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// --- helpers ---------------------------------------------------------------

func newTestClient(t *testing.T, handler http.HandlerFunc) (*openai.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := openai.DefaultConfig("test-key")
	cfg.BaseURL = srv.URL + "/v1"
	return openai.NewClientWithConfig(cfg), srv
}

func isStreamRequest(r *http.Request) bool {
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body.Stream
}

func sseHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
}

func sseWrite(w http.ResponseWriter, payload string) {
	fmt.Fprintf(w, "data: %s\n\n", payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func contentChunk(text string) string {
	return fmt.Sprintf(`{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":%q}}]}`, text)
}

const finishChunk = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`

var testReq = openai.ChatCompletionRequest{
	Model:    "m",
	Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
}

// --- streaming happy path & merge ------------------------------------------

func TestCompleteChatStreamMergesIndexedToolCalls(t *testing.T) {
	streamEverSucceeded.Store(false)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		sseHeader(w)
		sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"list","arguments":""}}]}}]}`)
		sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":"}}]}}]}`)
		sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}},{"index":1,"id":"call_b","type":"function","function":{"name":"view","arguments":"{}"}}]}}]}`)
		sseWrite(w, finishChunk)
		sseWrite(w, "[DONE]")
	})

	msg, err := completeChatStream(context.Background(), client, testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2: %+v", len(msg.ToolCalls), msg.ToolCalls)
	}
	a, b := msg.ToolCalls[0], msg.ToolCalls[1]
	if a.ID != "call_a" || a.Function.Name != "list" || a.Function.Arguments != `{"x":1}` {
		t.Fatalf("call_a merged wrong: %+v", a)
	}
	if b.ID != "call_b" || b.Function.Name != "view" || b.Function.Arguments != "{}" {
		t.Fatalf("call_b merged wrong: %+v", b)
	}
	if !streamEverSucceeded.Load() {
		t.Fatal("streamEverSucceeded should be set after a complete stream")
	}
}

func TestCompleteChatStreamNilIndexToolCalls(t *testing.T) {
	streamEverSucceeded.Store(false)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		sseHeader(w)
		// No "index" on any fragment: continuation by ID, new ID → new call.
		sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_a","type":"function","function":{"name":"list","arguments":"{\"p\":"}}]}}]}`)
		sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"\"src\"}"}}]}}]}`)
		sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_b","type":"function","function":{"name":"view","arguments":"{}"}}]}}]}`)
		sseWrite(w, finishChunk)
		sseWrite(w, "[DONE]")
	})

	msg, err := completeChatStream(context.Background(), client, testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2 (new ID must open a new slot): %+v", len(msg.ToolCalls), msg.ToolCalls)
	}
	if msg.ToolCalls[0].ID != "call_a" || msg.ToolCalls[0].Function.Arguments != `{"p":"src"}` {
		t.Fatalf("call_a corrupted: %+v", msg.ToolCalls[0])
	}
	if msg.ToolCalls[1].ID != "call_b" || msg.ToolCalls[1].Function.Name != "view" {
		t.Fatalf("call_b corrupted: %+v", msg.ToolCalls[1])
	}
}

// --- truncation detection ---------------------------------------------------

func TestCompleteChatStreamDetectsTruncation(t *testing.T) {
	streamEverSucceeded.Store(false)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		sseHeader(w)
		sseWrite(w, contentChunk("partial page text"))
		// Handler returns: clean connection close, no finish_reason, no [DONE]
		// — exactly what a Cloudflare edge cut looks like to go-openai.
	})

	_, err := completeChatStream(context.Background(), client, testReq)
	if !errors.Is(err, errStreamTruncated) {
		t.Fatalf("want errStreamTruncated, got %v", err)
	}
	if !IsTransientAPIError(err) {
		t.Fatal("truncated stream must classify as transient so the turn retries")
	}
	if streamEverSucceeded.Load() {
		t.Fatal("truncated stream must not mark streamEverSucceeded")
	}
}

func TestCompleteChatWithRetryRestreamsAfterTruncation(t *testing.T) {
	streamEverSucceeded.Store(true) // gate OFF the non-stream fallback probe
	var calls atomic.Int32
	var nonStream atomic.Int32
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !isStreamRequest(r) {
			nonStream.Add(1)
			http.Error(w, "unexpected non-stream call", http.StatusBadRequest)
			return
		}
		n := calls.Add(1)
		sseHeader(w)
		if n == 1 {
			sseWrite(w, contentChunk("cut off"))
			return // truncated
		}
		sseWrite(w, contentChunk("full answer"))
		sseWrite(w, finishChunk)
		sseWrite(w, "[DONE]")
	})

	msg, err := completeChatWithRetry(context.Background(), client, testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "full answer" {
		t.Fatalf("content = %q", msg.Content)
	}
	if calls.Load() != 2 {
		t.Fatalf("stream attempts = %d, want 2", calls.Load())
	}
	if nonStream.Load() != 0 {
		t.Fatal("transient truncation after a stream success must NOT hit the non-stream fallback")
	}
}

// --- empty stream & fallback ------------------------------------------------

func TestCompleteChatFallsBackOnEmptyStream(t *testing.T) {
	streamEverSucceeded.Store(false)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isStreamRequest(r) {
			// Relay "accepts" the stream but delivers nothing useful.
			sseHeader(w)
			sseWrite(w, `{"id":"1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
			sseWrite(w, "[DONE]")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello from non-stream"},"finish_reason":"stop"}]}`)
	})

	msg, err := completeChat(context.Background(), client, testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "hello from non-stream" {
		t.Fatalf("content = %q, want non-stream fallback body", msg.Content)
	}
}

func TestEmptyStreamNotFallbackAfterStreamSuccess(t *testing.T) {
	streamEverSucceeded.Store(true)
	if isStreamFallbackCandidate(errStreamEmpty) {
		t.Fatal("empty stream should retry as stream once streaming has succeeded")
	}
	streamEverSucceeded.Store(false)
	if !isStreamFallbackCandidate(errStreamEmpty) {
		t.Fatal("empty stream should probe non-stream before any stream success")
	}
	if !isStreamFallbackCandidate(openai.ErrTooManyEmptyStreamMessages) {
		t.Fatal("ErrTooManyEmptyStreamMessages should probe non-stream before any stream success")
	}
}

// --- classification ---------------------------------------------------------

func TestIsTransientAPIError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("error, status code: 524, message: A Timeout Occurred"), true},
		{fmt.Errorf("error, status code: 429, message: rate limit"), true},
		{fmt.Errorf("error, status code: 502, message: bad gateway"), true},
		{fmt.Errorf("error, status code: 401, message: unauthorized"), false},
		{fmt.Errorf("error, status code: 400, message: invalid request"), false},
		{errors.New("Post \"https://x\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)"), true},
		{errors.New("read tcp: connection reset by peer"), true},
		{errors.New("invalid character '<' looking for beginning of value"), false},
		// wrapped sentinels survive %w chains
		{fmt.Errorf("turn failed: %w", errStreamTruncated), true},
		{fmt.Errorf("turn failed: %w", errStreamEmpty), true},
		{fmt.Errorf("turn failed: %w", openai.ErrTooManyEmptyStreamMessages), true},
		// http2 transport wording
		{errors.New("http2: server sent GOAWAY and closed the connection"), true},
		{errors.New("stream error: stream ID 5; INTERNAL_ERROR; received from peer"), true},
		// CJK relay wording (no status code available on in-stream errors)
		{errors.New("error, message: 当前分组上游负载已饱和，请稍后再试"), true},
		{errors.New("error, message: 请求超时"), true},
		// auth stays permanent even with transient-looking words
		{errors.New("error, status code: 403, message: request timeout, cloudflare"), false},
	}
	for _, tc := range cases {
		if got := IsTransientAPIError(tc.err); got != tc.want {
			t.Errorf("IsTransientAPIError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestIsStreamFallbackCandidate(t *testing.T) {
	streamEverSucceeded.Store(false)
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("error, status code: 400, message: stream is not supported for this model"), true},
		{errors.New("error, status code: 400, message: unsupported parameter"), true},
		{errors.New("该渠道不支持流式输出"), true},
		{errors.New("error, status code: 422, message: bad payload"), true},
		{errors.New("error, status code: 401, message: unauthorized"), false},
		{errors.New("error, status code: 403, message: forbidden"), false},
	}
	for _, tc := range cases {
		if got := isStreamFallbackCandidate(tc.err); got != tc.want {
			t.Errorf("isStreamFallbackCandidate(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// --- backoff ----------------------------------------------------------------

func TestBackoffDelayGrowsAndCaps(t *testing.T) {
	d1 := backoffDelay(1)
	d2 := backoffDelay(2)
	d3 := backoffDelay(3)
	if d1 >= d2 || d2 >= d3 {
		t.Fatalf("expected growing backoff, got %v %v %v", d1, d2, d3)
	}
	if d1 < 2*time.Second {
		t.Fatalf("d1 too small: %v", d1)
	}
	if backoffDelay(10) != 30*time.Second {
		t.Fatalf("cap: got %v want 30s", backoffDelay(10))
	}
}

func TestJitterDelayRange(t *testing.T) {
	base := 10 * time.Second
	for i := 0; i < 50; i++ {
		d := JitterDelay(base)
		if d < base/2 || d > base {
			t.Fatalf("JitterDelay(%v) = %v, want within [%v, %v]", base, d, base/2, base)
		}
	}
	if JitterDelay(0) != 0 {
		t.Fatal("zero delay must pass through")
	}
}
