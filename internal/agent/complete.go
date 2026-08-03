package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net"
	"strings"
	"sync/atomic"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// chatTurnRetries is how many times a single ReAct turn may retry on
// transient gateway errors (524/502/429/timeout) before failing the page.
const chatTurnRetries = 3

// Sentinel errors from the streaming path. errors.Is-able so retry/fallback
// classification survives fmt.Errorf("%w") wrapping up the call chain.
var (
	// errStreamEmpty: the SSE stream ended without any content or tool calls.
	// Typical causes: a relay that ignores stream=true and returns a plain
	// JSON body (go-openai silently discards it), or a momentarily
	// overloaded gateway emitting a role-only delta.
	errStreamEmpty = errors.New("empty stream response (no content or tool calls)")
	// errStreamTruncated: the stream ended cleanly (io.EOF) before any chunk
	// carried a finish_reason. Cloudflare/relay edges cut long SSE streams
	// with a clean close that go-openai cannot distinguish from [DONE], so
	// the accumulated partial message must be retried, never returned.
	errStreamTruncated = errors.New("stream truncated: connection closed before finish_reason")
)

// streamEverSucceeded flips to true after the first fully-delivered stream.
// While false, transient/empty stream failures also probe the non-stream
// path once (recovers relays that reject or ignore SSE). Once true, a
// transient stream error is retried as a stream only, so a gateway outage
// is not hammered with doubled stream+non-stream request pairs.
var streamEverSucceeded atomic.Bool

// completeChat runs one chat completion turn.
// Prefer streaming so reverse proxies (Cloudflare etc.) keep the connection
// warm with SSE chunks instead of waiting for a single non-stream body
// (a common cause of HTTP 524 on long wiki pages). Falls back to non-stream
// when the relay rejects or ignores streaming.
func completeChat(ctx context.Context, client *openai.Client, req openai.ChatCompletionRequest) (openai.ChatCompletionMessage, error) {
	msg, err := completeChatStream(ctx, client, req)
	if err == nil {
		return msg, nil
	}
	// Hard API errors (auth, bad request unrelated to streaming) do not fall back.
	if !isStreamFallbackCandidate(err) {
		return openai.ChatCompletionMessage{}, err
	}
	// Non-stream fallback for relays that disable SSE / tool streaming.
	req.Stream = false
	resp, err2 := client.CreateChatCompletion(ctx, req)
	if err2 != nil {
		// Auth failures must never be retried: keep the (possibly
		// transient-looking) stream error text out of the returned string so
		// IsTransientAPIError classifies on the decisive auth error alone.
		if isAuthAPIError(err2) {
			return openai.ChatCompletionMessage{}, fmt.Errorf("non-stream fallback failed: %w", err2)
		}
		return openai.ChatCompletionMessage{}, fmt.Errorf("stream failed (%v); non-stream failed: %w", err, err2)
	}
	if len(resp.Choices) == 0 {
		return openai.ChatCompletionMessage{}, fmt.Errorf("no choices in non-stream response")
	}
	return resp.Choices[0].Message, nil
}

// completeChatWithRetry wraps completeChat with short exponential backoff on
// transient transport/gateway failures.
func completeChatWithRetry(ctx context.Context, client *openai.Client, req openai.ChatCompletionRequest) (openai.ChatCompletionMessage, error) {
	var last error
	for attempt := 1; attempt <= chatTurnRetries; attempt++ {
		msg, err := completeChat(ctx, client, req)
		if err == nil {
			return msg, nil
		}
		last = err
		if !IsTransientAPIError(err) || attempt == chatTurnRetries {
			return openai.ChatCompletionMessage{}, err
		}
		delay := JitterDelay(backoffDelay(attempt))
		select {
		case <-ctx.Done():
			return openai.ChatCompletionMessage{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return openai.ChatCompletionMessage{}, last
}

func completeChatStream(ctx context.Context, client *openai.Client, req openai.ChatCompletionRequest) (openai.ChatCompletionMessage, error) {
	req.Stream = true
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return openai.ChatCompletionMessage{}, err
	}
	defer stream.Close()

	var (
		role      string
		sawFinish bool
		content   strings.Builder
		// tool index → accumulated call (OpenAI streams tool_calls by index)
		byIdx = map[int]*openai.ToolCall{}
		order []int
	)

	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return openai.ChatCompletionMessage{}, err
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			sawFinish = true
		}
		delta := choice.Delta
		if delta.Role != "" {
			role = delta.Role
		}
		if delta.Content != "" {
			content.WriteString(delta.Content)
		}
		for _, tc := range delta.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			} else if len(order) > 0 {
				// Some relays stream whole tool calls without an index. A
				// fragment with no ID continues the latest call; a new
				// non-empty ID different from the latest call starts a new
				// synthetic slot instead of corrupting the previous call.
				last := order[len(order)-1]
				if tc.ID != "" && byIdx[last] != nil && byIdx[last].ID != "" && byIdx[last].ID != tc.ID {
					idx = last + 1
				} else {
					idx = last
				}
			}
			acc, ok := byIdx[idx]
			if !ok {
				acc = &openai.ToolCall{
					Index: tc.Index,
					ID:    tc.ID,
					Type:  tc.Type,
					Function: openai.FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
				byIdx[idx] = acc
				order = append(order, idx)
				continue
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Type != "" {
				acc.Type = tc.Type
			}
			if tc.Function.Name != "" {
				acc.Function.Name += tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Function.Arguments += tc.Function.Arguments
			}
		}
	}

	if role == "" {
		role = openai.ChatMessageRoleAssistant
	}
	msg := openai.ChatCompletionMessage{
		Role:    role,
		Content: content.String(),
	}
	if len(order) > 0 {
		msg.ToolCalls = make([]openai.ToolCall, 0, len(order))
		for _, idx := range order {
			tc := byIdx[idx]
			if tc == nil {
				continue
			}
			if tc.Type == "" {
				tc.Type = openai.ToolTypeFunction
			}
			msg.ToolCalls = append(msg.ToolCalls, *tc)
		}
	}
	// Empty assistant with neither content nor tools is invalid.
	if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
		return openai.ChatCompletionMessage{}, errStreamEmpty
	}
	// A clean EOF without any finish_reason means the edge/relay cut the
	// connection mid-generation; returning the partial message would save
	// half-written pages or execute tools with truncated argument JSON.
	if !sawFinish {
		return openai.ChatCompletionMessage{}, fmt.Errorf("%w (got %d content bytes, %d tool calls)", errStreamTruncated, content.Len(), len(msg.ToolCalls))
	}
	streamEverSucceeded.Store(true)
	return msg, nil
}

// IsTransientAPIError reports whether err is worth retrying (gateway timeout,
// rate limit, connection blip). Permanent errors (401/400) return false.
func IsTransientAPIError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errStreamTruncated) || errors.Is(err, errStreamEmpty) ||
		errors.Is(err, openai.ErrTooManyEmptyStreamMessages) {
		return true
	}
	// net timeouts / resets
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	s := err.Error()
	lower := strings.ToLower(s)
	// HTTP status embedded by go-openai: "status code: 524"
	for _, code := range []string{
		"status code: 524",
		"status code: 504",
		"status code: 502",
		"status code: 503",
		"status code: 429",
		"status code: 408",
		"status code: 520",
		"status code: 521",
		"status code: 522",
		"status code: 523",
	} {
		if strings.Contains(lower, code) {
			return true
		}
	}
	// Cloudflare / proxy / HTTP2 transport / CJK relay wording.
	// In-stream "data: {error...}" frames carry no HTTP status (go-openai
	// leaves HTTPStatusCode=0), so message wording is all we get there.
	for _, frag := range []string{
		"timeout",
		"timed out",
		"i/o timeout",
		"connection reset",
		"connection refused",
		"connection lost",
		"broken pipe",
		"eof",
		"tls handshake",
		"temporary failure",
		"try again",
		"overloaded",
		"rate limit",
		"too many requests",
		"cloudflare",
		"a timeout occurred",
		"goaway",
		"stream error",
		"internal_error",
		"超时",
		"负载",
		"稍后",
		"繁忙",
		"限流",
	} {
		if strings.Contains(lower, frag) {
			// Avoid treating clear auth failures as transient even if message is long.
			if strings.Contains(lower, "status code: 401") || strings.Contains(lower, "status code: 403") {
				return false
			}
			if strings.Contains(lower, "status code: 400") || strings.Contains(lower, "invalid_api_key") {
				return false
			}
			return true
		}
	}
	return false
}

// isAuthAPIError reports a definitive authentication/authorization failure.
func isAuthAPIError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "status code: 401") ||
		strings.Contains(s, "status code: 403") ||
		strings.Contains(s, "invalid_api_key")
}

func isStreamFallbackCandidate(err error) bool {
	if err == nil {
		return false
	}
	// Empty/ignored-stream responses: the non-stream fallback recovers relays
	// that ignore stream=true and answer with a plain JSON body (go-openai
	// discards it, yielding an empty stream). Once streaming has worked on
	// this run, treat emptiness as a blip and just retry the stream.
	if errors.Is(err, errStreamEmpty) || errors.Is(err, openai.ErrTooManyEmptyStreamMessages) {
		return !streamEverSucceeded.Load()
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "stream") && (strings.Contains(s, "not support") || strings.Contains(s, "unsupported")) {
		return true
	}
	// CJK "streaming not supported" wording from Chinese relays.
	if strings.Contains(s, "流式") {
		return true
	}
	// Any 4xx rejection of the stream request (except auth/rate-limit, which
	// fail non-stream identically) is worth one non-stream probe: relays
	// reject stream+tools with 400/404/422/501 bodies that never say "stream".
	for _, code := range []string{
		"status code: 400",
		"status code: 404",
		"status code: 405",
		"status code: 415",
		"status code: 422",
		"status code: 501",
	} {
		if strings.Contains(s, code) {
			return true
		}
	}
	// Transient stream errors probe non-stream only until streaming has ever
	// succeeded; after that the retry loop re-streams instead, so an outage
	// is not hit with doubled request pairs.
	if IsTransientAPIError(err) {
		return !streamEverSucceeded.Load()
	}
	return false
}

func backoffDelay(attempt int) time.Duration {
	// attempt 1 → 2s, 2 → 4s, 3 → 8s … cap 30s
	sec := 1 << attempt // 2, 4, 8, 16…
	if sec > 30 {
		sec = 30
	}
	if sec < 2 {
		sec = 2
	}
	return time.Duration(sec) * time.Second
}

// JitterDelay randomizes d into [d/2, d] so concurrent workers that failed at
// the same instant (e.g. a shared gateway outage) do not retry in lockstep
// waves against a per-second rate limiter.
func JitterDelay(d time.Duration) time.Duration {
	if d <= time.Millisecond {
		return d
	}
	half := d / 2
	return half + mrand.N(half+1)
}
