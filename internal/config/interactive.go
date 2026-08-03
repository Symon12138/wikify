package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// Interactive opens a TUI to edit ~/.wikify/config.yaml when stdin/stdout are TTYs.
// Returns true if the user saved, false if cancelled / non-TTY (caller should print instead).
func Interactive() (saved bool, err error) {
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return false, nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return false, nil
	}

	raw := LoadRaw()
	// Fill nested defaults so the form is never blank.
	if raw.LLM.BaseURL == "" {
		raw.LLM.BaseURL = "https://api.deepseek.com/v1"
	}
	if raw.LLM.Model == "" {
		raw.LLM.Model = "deepseek-chat"
	}
	if raw.Language == "" {
		raw.Language = "zh"
	}
	if raw.DocLanguage == "" {
		raw.DocLanguage = raw.Language
	}
	if raw.Concurrency.MaxConcurrent <= 0 {
		raw.Concurrency.MaxConcurrent = 1
	}
	if raw.Concurrency.MaxRetries <= 0 {
		raw.Concurrency.MaxRetries = 1
	}

	m := newConfigModel(raw)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return false, err
	}
	out, ok := final.(configModel)
	if !ok || !out.saved {
		return false, nil
	}
	if err := Save(out.toConfig()); err != nil {
		return false, err
	}
	fmt.Println("✓ Config saved →", ConfigPath())
	return true, nil
}

// ── field kinds ───────────────────────────────────────────────────────────────

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldEnum
	fieldModel // dynamic list from Base URL + free text
	fieldInt
)

type fieldDef struct {
	key   string
	label string
	kind  fieldKind
	// enum options (fieldEnum only)
	options []string
	// int bounds (fieldInt)
	min, max int
}

var configFields = []fieldDef{
	{key: "api_key", label: "API Key", kind: fieldText},
	{key: "base_url", label: "Base URL", kind: fieldText},
	{key: "model", label: "Model", kind: fieldModel},
	{key: "language", label: "Language", kind: fieldEnum, options: []string{"zh", "en"}},
	{key: "workers", label: "Workers", kind: fieldInt, min: 1, max: 32},
	{key: "retries", label: "Retries", kind: fieldInt, min: 0, max: 10},
	{key: "reasoning_effort", label: "Reasoning Effort", kind: fieldText},
}

// ── async messages ────────────────────────────────────────────────────────────

type modelsLoadedMsg struct {
	ids []string
	err error
	// fingerprint of base_url+api_key used for this request (ignore stale)
	fp string
}

// ── model ─────────────────────────────────────────────────────────────────────

type configModel struct {
	values map[string]string
	cursor int

	editing bool
	editBuf string

	// remote model list (from Base URL /models)
	modelOpts    []string
	modelsLoading bool
	modelsErr    string
	modelsFP     string // last successful / in-flight fingerprint

	saved  bool
	quit   bool
	dirty  bool
	status string
	width  int
}

func newConfigModel(raw *Config) configModel {
	lang := raw.DocLanguage
	if lang == "" {
		lang = raw.Language
	}
	switch strings.ToLower(lang) {
	case "chinese", "zh", "zh-cn", "zh-tw":
		lang = "zh"
	case "english", "en", "en-us", "en-gb":
		lang = "en"
	}
	model := raw.LLM.Model
	if model == "" {
		model = "deepseek-chat"
	}
	return configModel{
		values: map[string]string{
			"api_key":  raw.LLM.APIKey,
			"base_url": raw.LLM.BaseURL,
			"model":    model,
			"language": lang,
			"workers":  strconv.Itoa(raw.Concurrency.MaxConcurrent),
			"retries":          strconv.Itoa(raw.Concurrency.MaxRetries),
			"reasoning_effort": raw.LLM.ReasoningEffort,
		},
		modelsLoading: strings.TrimSpace(raw.LLM.BaseURL) != "",
		width:         80,
	}
}

func (m configModel) credentialFP() string {
	return strings.TrimSpace(m.values["base_url"]) + "\n" + strings.TrimSpace(m.values["api_key"])
}

func (m configModel) toConfig() *Config {
	cfg := LoadRaw()
	cfg.LLM.APIKey = strings.TrimSpace(m.values["api_key"])
	cfg.LLM.BaseURL = strings.TrimSpace(m.values["base_url"])
	model := strings.TrimSpace(m.values["model"])
	if model == "" {
		if cfg.LLM.Model != "" {
			model = cfg.LLM.Model
		} else {
			model = "deepseek-chat"
		}
	}
	cfg.LLM.Model = model
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "custom"
	}
	lang := strings.TrimSpace(m.values["language"])
	if lang == "" {
		lang = "zh"
	}
	cfg.Language = lang
	cfg.DocLanguage = lang
	w, _ := strconv.Atoi(m.values["workers"])
	if w < 1 {
		w = 1
	}
	r, _ := strconv.Atoi(m.values["retries"])
	if r < 0 {
		r = 0
	}
	cfg.Concurrency.MaxConcurrent = w
	cfg.Concurrency.MaxRetries = r
	cfg.LLM.ReasoningEffort = strings.TrimSpace(m.values["reasoning_effort"])
	return cfg
}

func fetchModelsCmd(baseURL, apiKey, fp string) tea.Cmd {
	return func() tea.Msg {
		ids, err := ListRemoteModels(baseURL, apiKey)
		return modelsLoadedMsg{ids: ids, err: err, fp: fp}
	}
}

func (m configModel) Init() tea.Cmd {
	if strings.TrimSpace(m.values["base_url"]) == "" {
		return nil
	}
	return fetchModelsCmd(m.values["base_url"], m.values["api_key"], m.credentialFP())
}

// refreshModels starts (or restarts) a remote model list fetch.
func (m *configModel) refreshModels() tea.Cmd {
	fp := m.credentialFP()
	if strings.TrimSpace(m.values["base_url"]) == "" {
		m.modelsLoading = false
		m.modelsErr = "Base URL 为空"
		m.modelOpts = nil
		m.modelsFP = fp
		return nil
	}
	m.modelsLoading = true
	m.modelsErr = ""
	m.modelsFP = fp
	m.status = "正在从 Base URL 拉取模型列表…"
	return fetchModelsCmd(m.values["base_url"], m.values["api_key"], fp)
}

func (m configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case modelsLoadedMsg:
		// Ignore stale responses (credentials changed mid-flight).
		if msg.fp != "" && msg.fp != m.credentialFP() {
			return m, nil
		}
		m.modelsLoading = false
		if msg.err != nil {
			_, friendly := ClassifyError(msg.err)
			if friendly == "" {
				friendly = msg.err.Error()
			}
			m.modelsErr = friendly
			// Keep any previous list if still same host? Drop to avoid wrong options.
			m.modelOpts = nil
			m.status = "模型列表加载失败 · Enter 可手动输入 · r 重试"
			return m, nil
		}
		m.modelOpts = msg.ids
		m.modelsErr = ""
		// If current model not in list, keep it (user free text) — cycle will still work from index.
		m.status = fmt.Sprintf("已加载 %d 个模型 · ←/→ 选择 · Enter 输入", len(msg.ids))
		return m, nil

	case tea.KeyMsg:
		if m.editing {
			return m.updateEditing(msg)
		}
		return m.updateNav(msg)
	}
	return m, nil
}

func (m configModel) updateNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := configFields[m.cursor]
	switch msg.String() {
	case "ctrl+c", "q":
		m.quit = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.status = ""
	case "down", "j":
		if m.cursor < len(configFields)-1 {
			m.cursor++
		}
		m.status = ""
	case "left", "h":
		m.cycle(f, -1)
	case "right", "l":
		m.cycle(f, +1)
	case "r":
		// Always allow refresh of model list.
		return m, m.refreshModels()
	case "enter", " ":
		switch f.kind {
		case fieldText:
			m.editing = true
			m.editBuf = m.values[f.key]
			m.status = "输入中 · Enter 确认 · Esc 取消"
		case fieldModel:
			// Free-text input always available for model.
			m.editing = true
			m.editBuf = m.values[f.key]
			m.status = "输入模型名 · Enter 确认 · Esc 取消 · ←/→ 从列表选择"
		case fieldEnum:
			m.cycle(f, +1)
		case fieldInt:
			m.cycle(f, +1)
		}
	case "s", "ctrl+s":
		m.saved = true
		return m, tea.Quit
	}
	return m, nil
}

func (m configModel) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := configFields[m.cursor]
	var after tea.Cmd
	switch msg.Type {
	case tea.KeyEsc:
		m.editing = false
		m.editBuf = ""
		m.status = "已取消编辑"
	case tea.KeyEnter:
		val := strings.TrimSpace(m.editBuf)
		prev := m.values[f.key]
		switch f.key {
		case "api_key", "base_url", "model":
			m.values[f.key] = val
			m.dirty = true
		}
		m.editing = false
		m.editBuf = ""
		m.status = "已更新 · 按 s 保存"
		// Re-fetch models when credentials / endpoint change.
		if (f.key == "api_key" || f.key == "base_url") && val != prev {
			after = m.refreshModels()
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.editBuf) > 0 {
			r := []rune(m.editBuf)
			m.editBuf = string(r[:len(r)-1])
		}
	case tea.KeyRunes:
		m.editBuf += string(msg.Runes)
	case tea.KeySpace:
		m.editBuf += " "
	}
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit
	}
	return m, after
}

func (m *configModel) cycle(f fieldDef, dir int) {
	switch f.kind {
	case fieldEnum:
		opts := f.options
		cur := m.values[f.key]
		idx := indexOf(opts, cur)
		if idx < 0 {
			idx = 0
		}
		idx = (idx + dir + len(opts)) % len(opts)
		m.values[f.key] = opts[idx]
		m.dirty = true
		m.status = "已切换 · 按 s 保存"
	case fieldModel:
		opts := m.modelOpts
		if len(opts) == 0 {
			if m.modelsLoading {
				m.status = "模型列表加载中…"
			} else if m.modelsErr != "" {
				m.status = "无可用列表 · Enter 手动输入 · r 重试"
			} else {
				m.status = "无模型列表 · Enter 手动输入 · r 从 Base URL 拉取"
			}
			return
		}
		cur := m.values[f.key]
		idx := indexOf(opts, cur)
		if idx < 0 {
			// Custom value not in list: step into list from start/end.
			if dir > 0 {
				idx = -1
			} else {
				idx = 0
			}
		}
		idx = (idx + dir + len(opts)) % len(opts)
		m.values[f.key] = opts[idx]
		m.dirty = true
		m.status = fmt.Sprintf("已选择 %d/%d · 按 s 保存", idx+1, len(opts))
	case fieldInt:
		v, _ := strconv.Atoi(m.values[f.key])
		v += dir
		if v < f.min {
			v = f.min
		}
		if v > f.max {
			v = f.max
		}
		m.values[f.key] = strconv.Itoa(v)
		m.dirty = true
		m.status = "已调整 · 按 s 保存"
	case fieldText:
		m.status = "按 Enter 编辑此项"
	}
}

func (m configModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render("wikify config")
	path := lipgloss.NewStyle().Faint(true).Render(ConfigPath())

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("  ")
	b.WriteString(path)
	b.WriteString("\n\n")

	labelW := 12
	for i, f := range configFields {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = "❯ "
			style = style.Bold(true).Foreground(lipgloss.Color("14"))
		}

		val := m.displayValue(f)
		if m.editing && i == m.cursor {
			val = m.editBuf + "▏"
			style = style.Foreground(lipgloss.Color("11"))
		}

		hint := ""
		if i == m.cursor && !m.editing {
			switch f.kind {
			case fieldEnum, fieldInt:
				hint = lipgloss.NewStyle().Faint(true).Render("  ←/→")
			case fieldModel:
				if m.modelsLoading {
					hint = lipgloss.NewStyle().Faint(true).Render("  加载中…")
				} else if len(m.modelOpts) > 0 {
					hint = lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("  ←/→ (%d)  Enter", len(m.modelOpts)))
				} else {
					hint = lipgloss.NewStyle().Faint(true).Render("  Enter 输入  r 拉取")
				}
			case fieldText:
				hint = lipgloss.NewStyle().Faint(true).Render("  Enter")
			}
		}

		line := fmt.Sprintf("%s%-*s  %s%s", cursor, labelW, f.label, val, hint)
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	help := "↑/↓ 选择  ·  ←/→ 切换  ·  Enter 输入  ·  r 刷新模型  ·  s 保存  ·  q 退出"
	if m.dirty {
		help += "  ·  *未保存"
	}
	b.WriteString(lipgloss.NewStyle().Faint(true).Render(help))
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(m.status))
	}
	if m.modelsErr != "" && !m.modelsLoading {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("模型列表: " + m.modelsErr))
	}
	b.WriteString("\n")
	return b.String()
}

func (m configModel) displayValue(f fieldDef) string {
	v := m.values[f.key]
	if f.key == "api_key" {
		return maskKey(v)
	}
	if f.kind == fieldModel {
		if m.modelsLoading && v == "" {
			return lipgloss.NewStyle().Faint(true).Render("(加载中…)")
		}
		if v == "" {
			return lipgloss.NewStyle().Faint(true).Render("(空 · Enter 输入)")
		}
		// Mark if current value is from remote list.
		if len(m.modelOpts) > 0 && !enumContains(m.modelOpts, v) {
			return v + lipgloss.NewStyle().Faint(true).Render(" (手动)")
		}
		return v
	}
	if v == "" {
		return lipgloss.NewStyle().Faint(true).Render("(空)")
	}
	return v
}

func maskKey(k string) string {
	if k == "" {
		return lipgloss.NewStyle().Faint(true).Render("(未设置)")
	}
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

func indexOf(opts []string, v string) int {
	for i, o := range opts {
		if o == v {
			return i
		}
	}
	return -1
}

func enumContains(opts []string, v string) bool {
	return indexOf(opts, v) >= 0
}
