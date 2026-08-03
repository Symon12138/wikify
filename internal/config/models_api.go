package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ListRemoteModels queries an OpenAI-compatible models endpoint derived from baseURL
// and returns model ids (sorted). apiKey may be empty for open gateways.
// Tries several common path variants (…/v1/models, …/models) until one succeeds.
func ListRemoteModels(baseURL, apiKey string) ([]string, error) {
	endpoints, err := modelsEndpoints(baseURL)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 12 * time.Second}
	var lastErr error
	for _, endpoint := range endpoints {
		ids, err := fetchModelsOnce(client, endpoint, apiKey)
		if err == nil {
			return ids, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, fmt.Errorf("no models endpoint tried")
	}
	return nil, lastErr
}

func fetchModelsOnce(client *http.Client, endpoint, apiKey string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// Some reverse proxies reject empty / default Go User-Agent with 403.
	req.Header.Set("User-Agent", "wikify/0.1")
	req.Header.Set("Accept", "application/json")
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Return a typed error carrying the HTTP status so callers (verify.go,
		// the TUI) can classify network vs auth vs path vs server without
		// re-parsing a formatted string.
		return nil, newAPIError(resp.StatusCode, resp.Status, body)
	}

	ids, err := parseModelsJSON(body)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrEmptyModelList
	}
	return ids, nil
}

// modelsEndpoint returns the preferred single endpoint (tests / callers).
func modelsEndpoint(baseURL string) (string, error) {
	eps, err := modelsEndpoints(baseURL)
	if err != nil {
		return "", err
	}
	return eps[0], nil
}

// modelsEndpoints returns ordered candidates for GET models.
func modelsEndpoints(baseURL string) ([]string, error) {
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

	var cands []string
	add := func(s string) {
		for _, x := range cands {
			if x == s {
				return
			}
		}
		cands = append(cands, s)
	}

	if strings.HasSuffix(base, "/models") {
		add(base)
		return cands, nil
	}

	// Prefer path-preserving …/models first when base already includes /v1.
	if strings.HasSuffix(base, "/v1") || strings.Contains(u.Path, "/v1/") {
		add(base + "/models")
	}

	// Origin-only (or bare host): many Chinese relay APIs use /v1/models.
	origin := u.Scheme + "://" + u.Host
	if u.Path == "" || u.Path == "/" {
		add(origin + "/v1/models")
		add(origin + "/models")
	} else {
		add(base + "/models")
		// Also try origin/v1/models if base path is something else.
		if !strings.HasSuffix(base, "/v1") {
			add(origin + "/v1/models")
			add(origin + "/models")
		}
	}

	if len(cands) == 0 {
		add(base + "/models")
	}
	return cands, nil
}

func parseModelsJSON(body []byte) ([]string, error) {
	// OpenAI: {"data":[{"id":"..."}, ...]}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Data) > 0 {
		return uniqueSortedIDs(func(yield func(string)) {
			for _, m := range envelope.Data {
				yield(m.ID)
			}
		}), nil
	}

	// Alternate: {"models":["a","b"]} or {"models":[{"id":"a"}]}
	var alt struct {
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &alt); err == nil && len(alt.Models) > 0 {
		return uniqueSortedIDs(func(yield func(string)) {
			for _, raw := range alt.Models {
				var s string
				if json.Unmarshal(raw, &s) == nil && s != "" {
					yield(s)
					continue
				}
				var o struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &o) == nil {
					yield(o.ID)
				}
			}
		}), nil
	}

	// Bare array: ["a","b"] or [{"id":"a"}]
	var arr []json.RawMessage
	if err := json.Unmarshal(body, &arr); err == nil && len(arr) > 0 {
		return uniqueSortedIDs(func(yield func(string)) {
			for _, raw := range arr {
				var s string
				if json.Unmarshal(raw, &s) == nil && s != "" {
					yield(s)
					continue
				}
				var o struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &o) == nil {
					yield(o.ID)
				}
			}
		}), nil
	}

	return nil, ErrUnrecognizedModels
}

func uniqueSortedIDs(iter func(func(string))) []string {
	seen := map[string]struct{}{}
	var out []string
	iter(func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	})
	sort.Strings(out)
	return out
}
