package config

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com/v1":      "https://api.deepseek.com/v1/models",
		"https://api.deepseek.com/v1/":     "https://api.deepseek.com/v1/models",
		"https://api.openai.com/v1/models": "https://api.openai.com/v1/models",
		"https://sub.example.com":          "https://sub.example.com/v1/models",
		"sub.example.com":                  "https://sub.example.com/v1/models",
		"https://sub.example.com/openai":   "https://sub.example.com/openai/models",
	}
	for in, want := range cases {
		got, err := modelsEndpoint(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Fatalf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestParseModelsJSON(t *testing.T) {
	ids, err := parseModelsJSON([]byte(`{"data":[{"id":"b"},{"id":"a"},{"id":"a"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("%v", ids)
	}

	ids, err = parseModelsJSON([]byte(`{"models":["z","y"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "y" {
		t.Fatalf("%v", ids)
	}
}

func TestListRemoteModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"deepseek-chat"}]}`))
	}))
	defer srv.Close()

	ids, err := ListRemoteModels(srv.URL+"/v1", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("%v", ids)
	}
}
