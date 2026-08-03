// Package tools provides the three read-only repository exploration tools.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
	openai "github.com/sashabaranov/go-openai"
)

// Directories always excluded from tree traversal.
var excludedDirs = map[string]bool{
	"node_modules": true, "vendor": true, ".git": true, "__pycache__": true,
	".venv": true, "venv": true, "dist": true, "build": true, "target": true,
	".gradle": true, ".idea": true, ".vscode": true, "coverage": true,
	".nyc_output": true, "tmp": true, ".tmp": true, ".pytest_cache": true,
	".mypy_cache": true, ".ruff_cache": true, "htmlcov": true, ".cache": true,
	".eggs": true, "site-packages": true, ".tox": true, "bower_components": true,
	".yarn": true, ".pnpm": true,
}

// Substrings that indicate a blocked (write/destructive) shell command.
var blockedTokens = []string{
	"del ", "erase ", " rd ", "rmdir ", " move ", " copy ", " ren ",
	"attrib ", "icacls ", "taskkill ", "format ", " reg ", " sc ",
	" net ", "mkdir ", " md ", "xcopy ", "robocopy ",
	"rm ", " mv ", " cp ", "chmod ", "chown ", "touch ",
	"wget ", "curl ", "pip install", "pip uninstall",
	"npm install", "npm uninstall", "yarn add", "apt ", "brew install",
	"| tee ",
}

// unixNullRedirects matches harmless POSIX redirect forms — stderr/stdout to
// the null device, or fd duplication (2>&1) — that read-only commands
// legitimately use under sh.
var unixNullRedirects = regexp.MustCompile(`(?:[0-9]?>>?|&>)\s*/dev/null|[0-9]>&[0-9]`)

// psCmdlets are common PowerShell cmdlets models emit on Windows. The shell
// there is cmd.exe, which does not know them; the failed invocation combined
// with PowerShell-style redirects (2>$null) used to litter the scanned
// repository with junk files.
var psCmdlets = []string{
	"get-childitem", "get-content", "get-item", "get-location",
	"select-string", "select-object", "sort-object", "measure-object",
	"where-object", "foreach-object", "format-table", "format-list",
	"out-null", "out-file", "out-string", "test-path", "resolve-path",
}

// commandPolicyError returns a non-empty error string when command violates
// the read-only shell policy; windows selects the cmd.exe rules.
func commandPolicyError(command string, windows bool) string {
	cmdLower := strings.ToLower(command)
	for _, token := range blockedTokens {
		if strings.Contains(cmdLower, token) {
			return fmt.Sprintf("Error: blocked operation '%s' detected in command", strings.TrimSpace(token))
		}
	}
	if windows {
		for _, cmdlet := range psCmdlets {
			if strings.Contains(cmdLower, cmdlet) {
				return fmt.Sprintf("Error: PowerShell cmdlet '%s' is not available — commands run under cmd.exe. Use dir /s /b, type, findstr, or git instead.", cmdlet)
			}
		}
	}
	// Output is captured and returned, so redirection is never needed. Under
	// cmd.exe a redirect writes a literal file into the scanned repository
	// (e.g. `2>$null` creates a file named $null), so block every `>` that
	// remains after stripping the harmless POSIX null-device forms.
	rest := cmdLower
	if !windows {
		rest = unixNullRedirects.ReplaceAllString(rest, "")
	}
	if strings.Contains(rest, ">") {
		return "Error: output redirection ('>') is blocked — command output is captured and returned automatically"
	}
	return ""
}

// ToolSet holds all tools bound to a specific repository root.
type ToolSet struct {
	Root   string
	spec   *gitignore.GitIgnore
	Tools  []openai.Tool
	Handle map[string]func(args map[string]any) string
}

// New creates a ToolSet bound to workDir.
func New(workDir string) *ToolSet {
	root, _ := filepath.Abs(workDir)

	var spec *gitignore.GitIgnore
	giPath := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(giPath); err == nil {
		if gi, err2 := gitignore.CompileIgnoreFile(giPath); err2 == nil {
			spec = gi
		}
	}

	ts := &ToolSet{Root: root, spec: spec}
	ts.Tools = ts.buildTools()
	ts.Handle = ts.buildHandlers()
	return ts
}

// ---------------------------------------------------------------------------
// Tool definitions (OpenAI function schema)
// ---------------------------------------------------------------------------

func (ts *ToolSet) buildTools() []openai.Tool {
	return []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "get_dir_structure",
				Description: "Get the directory structure of the repository as an ASCII tree. Filters .gitignore entries and common dependency directories. Use dir_path='.' for repository root. max_depth controls recursion depth (default 3).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"dir_path":  map[string]any{"type": "string", "description": "Relative path from repo root, or '.' for root"},
						"max_depth": map[string]any{"type": "integer", "description": "Maximum recursion depth (default 3)", "default": 3},
					},
					"required": []string{"dir_path"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "view_file_in_detail",
				Description: "View the content of a file in the repository. Reads lines start_line..end_line (1-indexed, inclusive). If end_line is 0 reads up to 200 lines. Set show_line_numbers=true to prefix each line with its number.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_path":         map[string]any{"type": "string", "description": "Relative file path from repo root"},
						"start_line":        map[string]any{"type": "integer", "description": "First line to read (1-indexed, default 1)", "default": 1},
						"end_line":          map[string]any{"type": "integer", "description": "Last line to read (0 = start_line+199)", "default": 0},
						"show_line_numbers": map[string]any{"type": "boolean", "description": "Prefix each line with its number", "default": false},
					},
					"required": []string{"file_path"},
				},
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "run_bash",
				Description: shellToolDescription(runtime.GOOS == "windows"),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{"type": "string", "description": "Shell command to execute"},
					},
					"required": []string{"command"},
				},
			},
		},
	}
}

// shellToolDescription documents the actual shell the command runs under so
// models stop emitting PowerShell/POSIX syntax on the wrong platform.
func shellToolDescription(windows bool) string {
	if windows {
		return "Run a read-only shell command in the repository directory. Commands run under Windows cmd.exe — use cmd syntax (dir /s /b, type, findstr, git log, git show). PowerShell cmdlets (Get-ChildItem, Select-String, ...) and single-quote quoting are NOT available. Commands that write, delete, or modify files are blocked; output redirection ('>') is blocked — output is captured and returned automatically. Timeout: 30 seconds."
	}
	return "Run a read-only shell command in the repository directory (POSIX sh). Only informational commands are allowed (ls, cat, find, grep, git log, git show, etc.). Commands that write, delete, or modify files are blocked; output redirection ('>') is blocked except to /dev/null — output is captured and returned automatically. Timeout: 30 seconds."
}

// ---------------------------------------------------------------------------
// Tool handler implementations
// ---------------------------------------------------------------------------

func (ts *ToolSet) buildHandlers() map[string]func(args map[string]any) string {
	return map[string]func(args map[string]any) string{
		"get_dir_structure":   ts.getDirStructure,
		"view_file_in_detail": ts.viewFileInDetail,
		"run_bash":            ts.runBash,
	}
}

func (ts *ToolSet) getDirStructure(args map[string]any) string {
	dirPath, _ := args["dir_path"].(string)
	maxDepth := 3
	if v, ok := args["max_depth"]; ok {
		switch d := v.(type) {
		case float64:
			maxDepth = int(d)
		case int:
			maxDepth = d
		}
	}
	// Model-supplied depth: unclamped values (models pass 99) explode the
	// listing on large repos and bloat every later prompt.
	if maxDepth < 1 {
		maxDepth = 1
	}
	if maxDepth > 6 {
		maxDepth = 6
	}

	var target string
	if dirPath == "" || dirPath == "." {
		target = ts.Root
	} else {
		target = filepath.Join(ts.Root, dirPath)
	}
	target = filepath.Clean(target)

	if !strings.HasPrefix(target, ts.Root) {
		return "Error: path is outside the repository root"
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Sprintf("Error: path '%s' does not exist", dirPath)
	}
	if !info.IsDir() {
		return fmt.Sprintf("Error: '%s' is not a directory", dirPath)
	}

	header := dirPath
	if header == "" || header == "." {
		header = "."
	}
	lines := []string{header + "/"}
	lines = append(lines, buildTree(ts.Root, target, ts.spec, maxDepth, "", 0)...)
	return strings.Join(lines, "\n")
}

func buildTree(root, current string, spec *gitignore.GitIgnore, maxDepth int, prefix string, depth int) []string {
	if depth > maxDepth {
		return nil
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return []string{prefix + "(permission denied)"}
	}

	// Sort: dirs first, then files, both alphabetically
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var filtered []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if excludedDirs[name] {
			continue
		}
		if strings.HasSuffix(name, ".egg-info") || strings.HasSuffix(name, ".dist-info") {
			continue
		}
		if spec != nil {
			rel, _ := filepath.Rel(root, filepath.Join(current, name))
			rel = filepath.ToSlash(rel)
			if e.IsDir() {
				rel += "/"
			}
			if spec.MatchesPath(rel) {
				continue
			}
		}
		filtered = append(filtered, e)
	}

	var lines []string
	for i, e := range filtered {
		isLast := i == len(filtered)-1
		connector := "├── "
		ext := "│   "
		if isLast {
			connector = "└── "
			ext = "    "
		}
		lines = append(lines, prefix+connector+e.Name())
		if e.IsDir() && depth < maxDepth {
			lines = append(lines, buildTree(root, filepath.Join(current, e.Name()), spec, maxDepth, prefix+ext, depth+1)...)
		}
	}
	return lines
}

func (ts *ToolSet) viewFileInDetail(args map[string]any) string {
	filePath, _ := args["file_path"].(string)
	startLine := 1
	endLine := 0
	showNums := false

	if v, ok := args["start_line"]; ok {
		if d, ok2 := v.(float64); ok2 {
			startLine = int(d)
		}
	}
	if v, ok := args["end_line"]; ok {
		if d, ok2 := v.(float64); ok2 {
			endLine = int(d)
		}
	}
	if v, ok := args["show_line_numbers"]; ok {
		if b, ok2 := v.(bool); ok2 {
			showNums = b
		}
	}

	target := filepath.Clean(filepath.Join(ts.Root, filePath))
	if !strings.HasPrefix(target, ts.Root) {
		return "Error: file is outside the repository root"
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Sprintf("Error: file '%s' does not exist", filePath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Sprintf("Error: '%s' is not a regular file", filePath)
	}
	if info.Size() > 5*1024*1024 {
		return "Error: file is too large (> 5 MB). Use line range parameters."
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}

	allLines := strings.SplitAfter(string(data), "\n")
	// Drop a trailing empty segment produced by a final newline so "total"
	// matches the human line count when possible.
	if n := len(allLines); n > 0 && allLines[n-1] == "" {
		allLines = allLines[:n-1]
	}
	total := len(allLines)
	if total == 0 {
		return fmt.Sprintf("File: %s (empty, 0 lines)", filePath)
	}

	// Clamp 1-based line args. Models often pass inverted ranges
	// (start_line > end_line) or start past EOF — never panic.
	if startLine < 1 {
		startLine = 1
	}
	if startLine > total {
		startLine = total
	}
	startIdx := startLine - 1

	var endIdx int
	if endLine <= 0 {
		endIdx = startIdx + 200
	} else {
		// end_line is 1-based inclusive → slice end exclusive at endLine.
		endIdx = endLine
	}
	if endIdx <= startIdx {
		// Inverted or empty range: default to a 200-line window from start.
		endIdx = startIdx + 200
	}
	// Cap the window: models ask for 1..99999 on big files, and every byte
	// returned here is resent in all subsequent turns of the page agent.
	const maxViewLines = 400
	if endIdx-startIdx > maxViewLines {
		endIdx = startIdx + maxViewLines
	}
	if endIdx > total {
		endIdx = total
	}
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx > endIdx {
		startIdx = endIdx
	}
	if startIdx >= total {
		startIdx = total - 1
		endIdx = total
	}

	selected := allLines[startIdx:endIdx]
	var body strings.Builder
	for i, ln := range selected {
		if showNums {
			body.WriteString(fmt.Sprintf("%4d | %s", startIdx+i+1, ln))
		} else {
			body.WriteString(ln)
		}
	}

	dispEnd := endIdx
	if dispEnd < startIdx+1 {
		dispEnd = startIdx + 1
	}
	return fmt.Sprintf("File: %s (lines %d-%d of %d)\n%s", filePath, startIdx+1, dispEnd, total, body.String())
}

func (ts *ToolSet) runBash(args map[string]any) string {
	command, _ := args["command"].(string)
	if msg := commandPolicyError(command, runtime.GOOS == "windows"); msg != "" {
		return msg
	}
	return runShell(command, ts.Root)
}
