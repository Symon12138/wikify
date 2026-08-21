package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// AskResult contains the answer with evidence.
type AskResult struct {
	Answer  string
	Sources []Source
}

type Source struct {
	Title string
	Slug  string
	Path  string
}

// Ask queries the local .wikify wiki with RAG. Zero vector DB — simple keyword retrieval.
// It reuses the configured LLM (same base_url/api_key/model) and guarantees citations.
func Ask(ctx context.Context, client *openai.Client, model, workDir, question string) (*AskResult, error) {
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("question is empty")
	}
	root := filepath.Join(workDir, ".wikify")
	if _, err := os.Stat(filepath.Join(root, "meta", "wiki.json")); err != nil {
		return nil, fmt.Errorf("no .wikify found, run generate first: %w", err)
	}
	pages, contents, err := loadWikiPages(workDir)
	if err != nil {
		return nil, err
	}
	ranked := rankPages(pages, contents, question)
	if len(ranked) == 0 {
		return nil, fmt.Errorf("no relevant pages found")
	}
	// Top-k context (k=5, each truncated to ~3000 runes to fit context)
	k := 5
	if len(ranked) < k {
		k = len(ranked)
	}
	ctxPages := ranked[:k]
	var ctxBuilder strings.Builder
	var sources []Source
	for _, rp := range ctxPages {
		body := contents[rp.Slug]
		if len([]rune(body)) > 3000 {
			body = string([]rune(body)[:3000]) + "\n...(truncated)"
		}
		ctxBuilder.WriteString(fmt.Sprintf("### [%s] (slug: %s, path: %s)\n%s\n\n---\n\n", rp.Title, rp.Slug, rp.Path, body))
		sources = append(sources, Source{Title: rp.Title, Slug: rp.Slug, Path: rp.Path})
	}
	ctxText := ctxBuilder.String()
	sysPrompt := "You are a wiki assistant. Answer the user question based ONLY on the provided context. " +
		"Every factual statement MUST cite its source as [Title](slug) or file:// path from the context. " +
		"If the context does not contain the answer, say so and list the closest pages. " +
		"Language: match the user question language."
	userPrompt := fmt.Sprintf("Context:\n%s\n\nQuestion: %s\n\nAnswer with citations:", ctxText, question)
	// 推理模型（xhigh）可能需要较长时间；给足 5 分钟
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: sysPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty LLM response")
	}
	answer := strings.TrimSpace(resp.Choices[0].Message.Content)
	// Enforce citations: if answer has no citation markers, append sources explicitly
	if !hasCitation(answer) {
		var sb strings.Builder
		sb.WriteString(answer)
		sb.WriteString("\n\n---\n**依据 / Sources:**\n")
		for _, s := range sources {
			sb.WriteString(fmt.Sprintf("- [%s](%s) \u2014 %s\n", s.Title, s.Slug, s.Path))
		}
		answer = sb.String()
	}
	return &AskResult{Answer: answer, Sources: sources}, nil
}

func hasCitation(s string) bool {
	if strings.Contains(s, "file://") || (strings.Contains(s, "[") && strings.Contains(s, "](")) {
		return true
	}
	return false
}

type rankedPage struct {
	Title string
	Slug  string
	Path  string
	Score int
}

func loadWikiPages(workDir string) ([]rankedPage, map[string]string, error) {
	root := filepath.Join(workDir, ".wikify")
	data, err := os.ReadFile(filepath.Join(root, "meta", "wiki.json"))
	if err != nil {
		return nil, nil, err
	}
	type wikiFile struct {
		Pages []struct {
			Title string `json:"title"`
			Slug string `json:"slug"`
			ContentPath string `json:"content_path"`
		} `json:"pages"`
	}
	var wf wikiFile
	if err := jsonUnmarshalAsk(data, &wf); err != nil {
		return nil, nil, err
	}
	contents := map[string]string{}
	var pages []rankedPage
	for _, p := range wf.Pages {
		rel := p.ContentPath
		if rel == "" {
			rel = p.Title + ".md"
		}
		rel = filepath.ToSlash(rel)
		rel = strings.TrimPrefix(rel, "content/")
		b, err := os.ReadFile(filepath.Join(root, "content", filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		contents[p.Slug] = string(b)
		pages = append(pages, rankedPage{Title: p.Title, Slug: p.Slug, Path: rel})
	}
	return pages, contents, nil
}

func rankPages(pages []rankedPage, contents map[string]string, question string) []rankedPage {
	q := strings.ToLower(strings.TrimSpace(question))
	// Simple keyword split: by spaces and punctuation, plus whole query as keyword
	keywords := splitKeywords(q)
	for i := range pages {
		titleLow := strings.ToLower(pages[i].Title)
		bodyLow := strings.ToLower(contents[pages[i].Slug])
		score := 0
		for _, kw := range keywords {
			if kw == "" || len([]rune(kw)) < 2 {
				continue
			}
			if strings.Contains(titleLow, kw) {
				score += 5
			}
			if strings.Contains(bodyLow, kw) {
				score += 1
			}
		}
		// Whole query bonus
		if strings.Contains(titleLow, q) {
			score += 10
		}
		if strings.Contains(bodyLow, q) {
			score += 3
		}
		pages[i].Score = score
	}
	// Sort by score desc
	// Keep only pages with score > 0, or top 3 if none matched (fallback to all)
	var filtered []rankedPage
	for _, p := range pages {
		if p.Score > 0 {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		// Fallback: return first 3 pages sorted by title
		if len(pages) > 3 {
			return pages[:3]
		}
		return pages
	}
	sort.Slice(filtered, func(a, b int) bool { return filtered[a].Score > filtered[b].Score })
	return filtered
}

func splitKeywords(q string) []string {
	// Split by common delimiters; for Chinese, treat each 2-char window as keyword
	var kws []string
	parts := strings.FieldsFunc(q, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\uFF0C' || r == '?' || r == '\uFF1F' || r == '!' || r == '\uFF01' || r == '\u3002' || r == ':' || r == '\uFF1A'
	})
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			kws = append(kws, p)
			// For Chinese, also add 2-char bigrams for better matching
			runes := []rune(p)
			if len(runes) > 2 && isCJK(runes[0]) {
				for i := 0; i < len(runes)-1; i++ {
					kws = append(kws, string(runes[i:i+2]))
				}
			}
		}
	}
	kws = append(kws, q)
	return kws
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

func jsonUnmarshalAsk(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}