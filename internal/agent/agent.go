// Package agent implements a generic ReAct (tool-calling) loop compatible
// with any OpenAI-compatible API (DeepSeek, OpenAI, etc.).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	openai "github.com/sashabaranov/go-openai"
)

const maxIterations = 50

// Config holds everything needed to run one agent invocation.
type Config struct {
	Client       *openai.Client
	Model        string
	SystemPrompt string
	UserPrompt   string
	Tools        []openai.Tool
	Handlers     map[string]func(args map[string]any) string
	// Verbose enables default fmt.Printf output for tool calls.
	Verbose bool
	// OnToolCall is called instead of (or in addition to) Verbose printing.
	// If set, Verbose fmt.Printf is suppressed.
	OnToolCall func(name, args, result string)
	// OnStatus is called with real-time status strings like "[requesting]",
	// "[tool: list_dir]", "[answering]" for display in the TUI.
	OnStatus func(status string)
	// Temperature controls randomness. nil = not sent (omitted by omitempty),
	// letting the API use its default. Explicit 0 is indistinguishable from
	// nil at the wire level due to float32+omitempty in go-openai.
	Temperature *float32
	// ReasoningEffort controls effort on reasoning models ("low"/"medium"/"high").
	// Empty string = not sent.
	ReasoningEffort string
}

// Run executes the multi-turn tool-calling ReAct loop and returns the final
// text content from the last AI message.
func Run(ctx context.Context, cfg Config) (string, error) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: cfg.SystemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: cfg.UserPrompt},
	}

	for i := 0; i < maxIterations; i++ {
		req := openai.ChatCompletionRequest{
			Model:    cfg.Model,
			Messages: messages,
		}
		if cfg.Temperature != nil {
			req.Temperature = *cfg.Temperature
		}
		if cfg.ReasoningEffort != "" {
			req.ReasoningEffort = cfg.ReasoningEffort
		}
		if len(cfg.Tools) > 0 {
			req.Tools = cfg.Tools
		}

		if cfg.OnStatus != nil {
			cfg.OnStatus("[requesting]")
		}
		// Stream-first completion with transient retry (524/502/429/timeout).
		// Non-stream CreateChatCompletion alone often trips Cloudflare 524 on
		// long wiki pages because the gateway waits for a single full body.
		msg, err := completeChatWithRetry(ctx, cfg.Client, req)
		if err != nil {
			return "", fmt.Errorf("API call failed (turn %d): %w", i+1, err)
		}
		messages = append(messages, msg)

		// No tool calls → final answer
		if len(msg.ToolCalls) == 0 {
			if cfg.OnStatus != nil {
				cfg.OnStatus("[answering]")
			}
			return msg.Content, nil
		}

		if cfg.OnStatus != nil {
			cfg.OnStatus("[thinking]")
		}

		// Execute all tool calls
		for _, tc := range msg.ToolCalls {
			if cfg.OnStatus != nil {
				cfg.OnStatus("[tool: " + tc.Function.Name + "]")
			}

			var result string
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				// Truncated/garbled argument JSON (e.g. a cut stream) must not
				// run the tool with empty args — that reads the wrong file or
				// lists the wrong dir and poisons the page. Surface the parse
				// error as the tool result so the model reissues the call.
				result = fmt.Sprintf("Error: tool arguments are not valid JSON (%v). Re-issue the tool call with complete JSON arguments.", err)
			} else if handler, ok := cfg.Handlers[tc.Function.Name]; ok {
				result = clampToolResult(handler(args))
			} else {
				result = fmt.Sprintf("Error: unknown tool '%s'", tc.Function.Name)
			}

			if cfg.OnToolCall != nil {
				cfg.OnToolCall(tc.Function.Name, tc.Function.Arguments, result)
			} else if cfg.Verbose {
				fmt.Printf("  \033[36m[tool]\033[0m %s(%s)\n",
					tc.Function.Name, abbrev(tc.Function.Arguments, 120))
				fmt.Printf("  \033[2m→ %s\033[0m\n", abbrev(result, 200))
			}

			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return "", fmt.Errorf("agent exceeded maximum iterations (%d)", maxIterations)
}

func abbrev(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "↵")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// maxToolResultBytes caps a single tool result appended to the conversation.
// Tool output is resent with the full history on every subsequent turn, so
// one oversized view_file/dir listing multiplies into every later request's
// prompt (token cost + gateway-timeout exposure).
const maxToolResultBytes = 24 * 1024

func clampToolResult(s string) string {
	if len(s) <= maxToolResultBytes {
		return s
	}
	cut := maxToolResultBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…[truncated: showing %d of %d bytes; request a narrower range for more]", cut, len(s))
}
