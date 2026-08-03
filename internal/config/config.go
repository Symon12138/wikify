package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config mirrors the structure of ~/.wikify/config.yaml as used by the previous wiki tools CLI.
//
// YAML layout:
//
// language: zh
// doc_language: zh
// llm:
//
//	provider: custom
//	model: deepseek-chat
//	api_key: sk-xxx
//	base_url: https://api.deepseek.com/v1
//
// concurrency:
//
//	max_concurrent: 1
//	max_retries: 1
type Config struct {
	Language    string        `yaml:"language"`
	DocLanguage string        `yaml:"doc_language"`
	LLM         LLMConfig     `yaml:"llm"`
	Concurrency ConcurrConfig `yaml:"concurrency"`
}

// LLMConfig holds the model provider settings.
type LLMConfig struct {
	Provider        string   `yaml:"provider"`
	Model           string   `yaml:"model"`
	APIKey          string   `yaml:"api_key"`
	BaseURL         string   `yaml:"base_url"`
	ReasoningEffort string   `yaml:"reasoning_effort,omitempty"`
	Temperature     *float32 `yaml:"temperature,omitempty"`
}

// ConcurrConfig holds concurrency settings.
type ConcurrConfig struct {
	MaxConcurrent int `yaml:"max_concurrent"`
	MaxRetries    int `yaml:"max_retries"`
}

// Flat returns a flat view of Config used by the rest of the codebase.
type Flat struct {
	APIKey          string
	BaseURL         string
	Model           string
	Language        string
	Workers         int
	Retries         int
	ReasoningEffort string
	Temperature     *float32
}

// ConfigPath returns the path of the config file (~/.wikify/config.yaml).
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".wikify", "config.yaml")
}

// Load reads ~/.wikify/config.yaml, applies env-var overrides, and returns a Flat view.
func Load() (*Flat, error) {
	cfg := defaults()

	data, err := os.ReadFile(ConfigPath())
	if err == nil {
		if err2 := yaml.Unmarshal(data, cfg); err2 != nil {
			return nil, fmt.Errorf("解析配置文件失败: %w", err2)
		}
	}

	// Ensure nested defaults if sections are empty
	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = "https://api.deepseek.com/v1"
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = "deepseek-chat"
	}
	if cfg.Language == "" {
		cfg.Language = "zh"
	}
	if cfg.DocLanguage == "" {
		cfg.DocLanguage = cfg.Language
	}
	if cfg.Concurrency.MaxConcurrent <= 0 {
		cfg.Concurrency.MaxConcurrent = 1
	}
	if cfg.Concurrency.MaxRetries <= 0 {
		cfg.Concurrency.MaxRetries = 1
	}

	// Environment variable overrides (backwards compat)
	if v := os.Getenv("WIKIFY_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("WIKIFY_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("WIKIFY_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("WIKIFY_REASONING_EFFORT"); v != "" {
		cfg.LLM.ReasoningEffort = v
	}

	// Strip stray control/NUL bytes and surrounding whitespace from credentials
	// so they cannot poison the outgoing Authorization header.
	sanitizeLLM(&cfg.LLM)

	return cfg.Flat(), nil
}

// LoadRaw returns the full structured Config (used by the config subcommand).
func LoadRaw() *Config {
	cfg := defaults()
	data, err := os.ReadFile(ConfigPath())
	if err == nil {
		_ = yaml.Unmarshal(data, cfg)
	}
	sanitizeLLM(&cfg.LLM)
	return cfg
}

// sanitizeCredential strips surrounding whitespace and any control characters
// (including stray NUL bytes that can sneak in from copy/paste or editors).
// Such bytes are illegal in HTTP header values and cause Go's net/http to
// reject the request client-side, which previously surfaced as a misleading
// "网络连接失败" error before the request ever left the machine.
func sanitizeCredential(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		// Drop control chars (NUL, CR, LF, tabs, etc.); keep printable content.
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// sanitizeLLM cleans the credential-bearing fields of an LLMConfig in place.
func sanitizeLLM(llm *LLMConfig) {
	llm.APIKey = sanitizeCredential(llm.APIKey)
	llm.BaseURL = sanitizeCredential(llm.BaseURL)
	llm.Model = sanitizeCredential(llm.Model)
}

// Save writes the Config to ~/.wikify/config.yaml.
func Save(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// SaveFlat converts a Flat config back to Config and saves it.
func SaveFlat(f *Flat) error {
	raw := LoadRaw()
	raw.LLM.APIKey = f.APIKey
	raw.LLM.BaseURL = f.BaseURL
	raw.LLM.Model = f.Model
	raw.LLM.ReasoningEffort = f.ReasoningEffort
	raw.LLM.Temperature = f.Temperature
	raw.Language = f.Language
	raw.DocLanguage = f.Language
	raw.Concurrency.MaxConcurrent = f.Workers
	raw.Concurrency.MaxRetries = f.Retries
	return Save(raw)
}

func defaults() *Config {
	return &Config{
		Language:    "zh",
		DocLanguage: "zh",
		LLM: LLMConfig{
			Provider: "custom",
			Model:    "deepseek-chat",
			BaseURL:  "https://api.deepseek.com/v1",
		},
		Concurrency: ConcurrConfig{
			MaxConcurrent: 1,
			MaxRetries:    1,
		},
	}
}

// Flat builds a Flat from the structured Config.
func (c *Config) Flat() *Flat {
	return &Flat{
		APIKey:          c.LLM.APIKey,
		BaseURL:         NormalizeBaseURL(c.LLM.BaseURL),
		Model:           c.LLM.Model,
		Language:        docLang(c),
		Workers:         c.Concurrency.MaxConcurrent,
		Retries:         c.Concurrency.MaxRetries,
		ReasoningEffort: c.LLM.ReasoningEffort,
		Temperature:     c.LLM.Temperature,
	}
}

// NormalizeBaseURL ensures OpenAI-compatible chat clients hit …/v1 when the
// user only configured an origin host (common for relay gateways).
// Leaves paths that already include /v1 or a non-root path unchanged.
func NormalizeBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "https://api.deepseek.com/v1"
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	// Already OpenAI-style root.
	if strings.HasSuffix(base, "/v1") || strings.Contains(u.Path, "/v1/") {
		return base
	}
	// Origin-only → append /v1 (go-openai appends /chat/completions to BaseURL).
	if u.Path == "" || u.Path == "/" {
		return u.Scheme + "://" + u.Host + "/v1"
	}
	return base
}

// docLang returns the effective documentation language string.
// wikify accepts "zh"/"en" locale codes; we map to display names for prompts.
func docLang(c *Config) string {
	lang := c.DocLanguage
	if lang == "" {
		lang = c.Language
	}
	switch lang {
	case "zh", "zh-CN", "zh-TW":
		return "Chinese"
	case "en", "en-US", "en-GB":
		return "English"
	default:
		if lang == "" {
			return "Chinese"
		}
		return lang
	}
}
