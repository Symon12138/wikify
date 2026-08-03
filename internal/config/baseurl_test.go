package config

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                            "https://api.deepseek.com/v1",
		"https://api.deepseek.com/v1": "https://api.deepseek.com/v1",
		"https://api.deepseek.com/v1/": "https://api.deepseek.com/v1",
		"https://sub.chccc.xyz":       "https://sub.chccc.xyz/v1",
		"sub.chccc.xyz":               "https://sub.chccc.xyz/v1",
		"https://example.com/openai":  "https://example.com/openai",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}
