// Full-text search (NAV-4) and the file→pages reverse index endpoint (NAV-5)
// for the live browse server. The search index is built lazily once from the
// served page bodies (works for any source: exported tree, drafts, legacy) —
// meta/search-index.json is a static-hosting artifact and is not required
// here. All matching is rune-based so snippets never split UTF-8 sequences.
package browse

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// searchResult is one hit returned by /api/search.
type searchResult struct {
	Title   string `json:"title"`
	Slug    string `json:"slug"`
	Section string `json:"section,omitempty"`
	Path    string `json:"path"`
	Snippet string `json:"snippet,omitempty"`
}

type searchDoc struct {
	title   string
	slug    string
	section string
	body    []rune // plain text (markdown noise stripped)
	titleLC []rune // rune-wise lowercase of title (1:1 with title runes)
	bodyLC  []rune // rune-wise lowercase of body (1:1 with body runes)
}

// searchIndex lazily builds the in-memory index on first query.
type searchIndex struct {
	src  *docSource
	wiki *wikiData
	once sync.Once
	docs []searchDoc
}

func newSearchIndex(src *docSource, wiki *wikiData) *searchIndex {
	return &searchIndex{src: src, wiki: wiki}
}

var (
	reSearchFence = regexp.MustCompile("(?s)```.*?```")
	reSearchCite  = regexp.MustCompile(`(?s)<cite>.*?</cite>`)
	reSearchLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reSearchFile  = regexp.MustCompile(`(?i)file://[^\s)"']+`)
	reSearchTag   = regexp.MustCompile(`<[^>\n]+>`)
	reSearchSpace = regexp.MustCompile(`\s+`)
)

// searchPlainText reduces a markdown body to plain searchable text.
func searchPlainText(body string) string {
	s := reSearchFence.ReplaceAllString(body, " ")
	s = reSearchCite.ReplaceAllString(s, " ")
	s = reSearchLink.ReplaceAllString(s, "$1")
	s = reSearchFile.ReplaceAllString(s, " ")
	s = reSearchTag.ReplaceAllString(s, " ")
	s = strings.NewReplacer("#", " ", "*", " ", "`", " ", ">", " ", "|", " ").Replace(s)
	return strings.TrimSpace(reSearchSpace.ReplaceAllString(s, " "))
}

// lowerRunes lowercases rune-by-rune, guaranteeing len(out) == len(in runes)
// so match offsets in the lowered text map 1:1 onto the original runes.
func lowerRunes(s string) []rune {
	rs := []rune(s)
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// runeIndex is a naive substring scan over rune slices starting at from.
// Returns the rune offset of the first occurrence, or -1.
func runeIndex(hay, needle []rune, from int) int {
	if len(needle) == 0 || from < 0 {
		return -1
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func (s *searchIndex) build() {
	for _, p := range s.wiki.Pages {
		raw, err := readPageBody(s.src, p.Slug, p.ContentPath)
		text := ""
		if err == nil {
			text = searchPlainText(string(raw))
		}
		s.docs = append(s.docs, searchDoc{
			title:   p.Title,
			slug:    p.Slug,
			section: p.Section,
			body:    []rune(text),
			titleLC: lowerRunes(p.Title),
			bodyLC:  lowerRunes(text),
		})
	}
}

// snippetAround extracts ±radius runes around [hit, hit+n) with ellipses.
// Rune-slicing only — never produces broken UTF-8.
func snippetAround(body []rune, hit, n, radius int) string {
	if len(body) == 0 {
		return ""
	}
	start := hit - radius
	if start < 0 {
		start = 0
	}
	end := hit + n + radius
	if end > len(body) {
		end = len(body)
	}
	out := string(body[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(body) {
		out += "…"
	}
	return strings.TrimSpace(out)
}

// search runs a case-insensitive token query over titles and bodies.
// A page matches when every token occurs in its title or body; pages whose
// TITLE contains every token rank before body-only matches (original page
// order preserved inside each rank). Results capped at limit.
func (s *searchIndex) search(q string, limit int) []searchResult {
	s.once.Do(s.build)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	var tokens [][]rune
	for _, t := range strings.Fields(strings.ToLower(q)) {
		tokens = append(tokens, []rune(t))
	}
	if len(tokens) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	type hit struct {
		doc     *searchDoc
		inTitle bool
		bodyPos int // rune offset of first body match (-1 when title-only)
		bodyLen int
	}
	var titleHits, bodyHits []hit
	for i := range s.docs {
		d := &s.docs[i]
		inTitle := true
		inDoc := true
		bodyPos, bodyLen := -1, 0
		for _, tok := range tokens {
			tIdx := runeIndex(d.titleLC, tok, 0)
			bIdx := runeIndex(d.bodyLC, tok, 0)
			if tIdx < 0 {
				inTitle = false
			}
			if tIdx < 0 && bIdx < 0 {
				inDoc = false
				break
			}
			if bIdx >= 0 && (bodyPos < 0 || bIdx < bodyPos) {
				bodyPos, bodyLen = bIdx, len(tok)
			}
		}
		if !inDoc {
			continue
		}
		h := hit{doc: d, inTitle: inTitle, bodyPos: bodyPos, bodyLen: bodyLen}
		if inTitle {
			titleHits = append(titleHits, h)
		} else {
			bodyHits = append(bodyHits, h)
		}
	}
	ordered := append(titleHits, bodyHits...)
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	results := make([]searchResult, 0, len(ordered))
	for _, h := range ordered {
		snip := ""
		if h.bodyPos >= 0 {
			snip = snippetAround(h.doc.body, h.bodyPos, h.bodyLen, 60)
		} else if len(h.doc.body) > 0 {
			snip = snippetAround(h.doc.body, 0, 0, 60)
		}
		results = append(results, searchResult{
			Title:   h.doc.title,
			Slug:    h.doc.slug,
			Section: h.doc.section,
			Path:    "/" + h.doc.slug,
			Snippet: snip,
		})
	}
	return results
}

// handleSearch serves GET /api/search?q=... as a JSON array of searchResult.
func (s *searchIndex) handleSearch(w http.ResponseWriter, r *http.Request) {
	results := s.search(r.URL.Query().Get("q"), 20)
	if results == nil {
		results = []searchResult{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(results)
}

// handleFilePages serves GET /api/file-pages: the meta/file-page-index.json
// written at export (NAV-5). Old wikis without the file get an empty object —
// the client treats that as "no popover data" and stays inert.
func handleFilePages(src *docSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		data, err := os.ReadFile(filepath.Join(src.catalogDir, "file-page-index.json"))
		if err != nil || len(data) == 0 || !json.Valid(data) {
			_, _ = w.Write([]byte("{}"))
			return
		}
		_, _ = w.Write(data)
	}
}
