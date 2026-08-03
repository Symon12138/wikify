package scan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Config-key inventory: deterministic top-level key extraction from common
// runtime configuration files. Light line parsing only — no YAML/TOML libs.

const (
	maxConfigKeyFiles    = 40
	maxConfigKeysPerFile = 40
	maxConfigFileBytes   = 64_000
)

var (
	reConfigFileName = regexp.MustCompile(`(?i)^((application|bootstrap)[-.\w]*\.(ya?ml|properties)|\.env(\.example)?|config\.(json|toml|ini))$`)
	reYamlKeyLine    = regexp.MustCompile(`^([ \t]*)([A-Za-z_][\w.\-/]*)\s*:`)
	reTomlSection    = regexp.MustCompile(`^\s*\[\[?([^\]\s]+?)\]?\]\s*$`)
	reTomlAssign     = regexp.MustCompile(`^([A-Za-z_][\w.\-]*)\s*=`)
)

// IsConfigInventoryFile reports whether rel's basename looks like a runtime
// configuration file worth key extraction.
func IsConfigInventoryFile(rel string) bool {
	return reConfigFileName.MatchString(filepath.Base(filepath.ToSlash(rel)))
}

// enrichConfigKeys fills model.ConfigKeys by reading matching config files
// (bounded count and size). Called from EnrichGraph because the main read
// loop only visits code files.
func enrichConfigKeys(model *Model) {
	keys := map[string][]string{}
	scanned := 0
	for _, f := range model.Files {
		if scanned >= maxConfigKeyFiles {
			break
		}
		rel := filepath.ToSlash(f.RelativePath)
		if IsNoisePath(rel) || !IsConfigInventoryFile(rel) {
			continue
		}
		if f.Size <= 0 || f.Size > maxConfigFileBytes {
			continue
		}
		abs := rel
		if model.Root != "" {
			abs = filepath.Join(model.Root, filepath.FromSlash(rel))
		}
		data, err := os.ReadFile(abs)
		if err != nil || len(data) == 0 {
			continue
		}
		if len(data) > maxConfigFileBytes {
			data = data[:maxConfigFileBytes]
		}
		scanned++
		if ks := extractConfigKeys(rel, string(data)); len(ks) > 0 {
			keys[rel] = ks
		}
	}
	if len(keys) > 0 {
		model.ConfigKeys = keys
	} else {
		model.ConfigKeys = nil
	}
}

// extractConfigKeys dispatches on the file name to a format-specific key
// extractor. Returned keys are capped and deduped, order of appearance.
func extractConfigKeys(rel, text string) []string {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(rel)))
	switch {
	case strings.HasSuffix(base, ".yml"), strings.HasSuffix(base, ".yaml"):
		return yamlTopKeys(text)
	case strings.HasSuffix(base, ".properties"), strings.HasPrefix(base, ".env"):
		return propertiesKeys(text)
	case strings.HasSuffix(base, ".json"):
		return jsonTopKeys(text)
	case strings.HasSuffix(base, ".toml"), strings.HasSuffix(base, ".ini"):
		return tomlTopKeys(text)
	}
	return nil
}

// propertiesKeys handles java-properties and dotenv files: `key=value` lines.
// Dotted keys are trimmed to their top two segments (spring-style).
func propertiesKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") || strings.HasPrefix(t, ";") {
			continue
		}
		t = strings.TrimPrefix(t, "export ")
		eq := strings.Index(t, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:eq])
		if key == "" || strings.ContainsAny(key, " \t\"'") {
			continue
		}
		if parts := strings.Split(key, "."); len(parts) > 2 {
			key = parts[0] + "." + parts[1]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
		if len(out) >= maxConfigKeysPerFile {
			break
		}
	}
	return out
}

// yamlTopKeys extracts depth 0–1 mapping keys by indentation (no YAML lib).
// Depth-1 keys are reported as "parent.child".
func yamlTopKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	push := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	current := ""
	childIndent := -1
	for _, ln := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(ln)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "- ") {
			continue
		}
		if strings.HasPrefix(ln, "---") {
			current = ""
			childIndent = -1
			continue
		}
		m := reYamlKeyLine.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		indent := len(m[1])
		key := m[2]
		if indent == 0 {
			current = key
			childIndent = -1
			push(key)
		} else if current != "" {
			if childIndent < 0 {
				childIndent = indent
			}
			if indent == childIndent {
				push(current + "." + key)
			}
		}
		if len(out) >= maxConfigKeysPerFile {
			break
		}
	}
	return out
}

// jsonTopKeys extracts depth-1 object keys with a tiny depth scanner so both
// pretty-printed and minified config.json work.
func jsonTopKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	depth := 0
	inStr, esc := false, false
	var cur strings.Builder
	lastStr := ""
	for _, r := range text {
		if inStr {
			if esc {
				esc = false
				cur.WriteRune(r)
				continue
			}
			switch r {
			case '\\':
				esc = true
			case '"':
				inStr = false
				lastStr = cur.String()
			default:
				cur.WriteRune(r)
			}
			continue
		}
		switch r {
		case '"':
			inStr = true
			cur.Reset()
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ':':
			if depth == 1 && lastStr != "" && !seen[lastStr] {
				seen[lastStr] = true
				out = append(out, lastStr)
				if len(out) >= maxConfigKeysPerFile {
					return out
				}
			}
			lastStr = ""
		}
	}
	return out
}

// tomlTopKeys handles TOML and INI: section headers become keys, plus bare
// top-level assignments before the first section.
func tomlTopKeys(text string) []string {
	var out []string
	seen := map[string]bool{}
	push := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	inSection := false
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimRight(ln, "\r")
		trim := strings.TrimSpace(t)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			continue
		}
		if m := reTomlSection.FindStringSubmatch(t); m != nil {
			key := m[1]
			if parts := strings.Split(key, "."); len(parts) > 2 {
				key = parts[0] + "." + parts[1]
			}
			push(key)
			inSection = true
		} else if !inSection {
			if m := reTomlAssign.FindStringSubmatch(t); m != nil {
				push(m[1])
			}
		}
		if len(out) >= maxConfigKeysPerFile {
			break
		}
	}
	return out
}
