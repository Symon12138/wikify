package scan

import (
	"regexp"
	"strings"
)

// Symbol is a top-level declaration extracted from a source file by
// lightweight per-language regexes. Line is the 1-based line number of the
// declaration, suitable for #Lstart cite anchors.
type Symbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // func | method | type | class | interface | enum | const | var
	Line int    `json:"line"`
}

const maxSymbolsPerFile = 20

var (
	// Go
	reSymGoMethod = regexp.MustCompile(`^func\s+\([^)]+\)\s+([A-Za-z_]\w*)\s*[\[(]`)
	reSymGoFunc   = regexp.MustCompile(`^func\s+([A-Za-z_]\w*)\s*[\[(]`)
	reSymGoType   = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`)
	// Java / Kotlin family
	reSymJavaType   = regexp.MustCompile(`^\s*(?:(?:public|protected|private|abstract|final|static|sealed|data|open|internal)\s+)*(class|interface|enum|record|object)\s+([A-Za-z_]\w*)`)
	reSymJavaMethod = regexp.MustCompile(`^\s*(?:public|protected)\s+(?:[\w<>\[\],.?]+\s+)*([A-Za-z_]\w*)\s*\(`)
	reSymKotlinFun  = regexp.MustCompile(`^\s*(?:(?:public|protected|internal|open|override|suspend|private)\s+)*fun\s+([A-Za-z_]\w*)`)
	// TS / JS family
	reSymJSExport = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:abstract\s+)?(?:async\s+)?(function|class|const|interface|type|enum|let|var)\s+([A-Za-z_$][\w$]*)`)
	// Python
	reSymPy = regexp.MustCompile(`^(?:async\s+)?(def|class)\s+([A-Za-z_]\w*)`)
)

// extractSymbols returns capped top-level declarations for the given file
// extension. Line numbers are exact for whatever the regexes match; multi-line
// signatures and decorated declarations may be missed (advisory data only).
func extractSymbols(ext, text string) []Symbol {
	var out []Symbol
	add := func(name, kind string, line int) bool {
		if name != "" {
			out = append(out, Symbol{Name: name, Kind: kind, Line: line})
		}
		return len(out) < maxSymbolsPerFile
	}
	lines := strings.Split(text, "\n")
	switch ext {
	case ".go":
		for i, ln := range lines {
			if m := reSymGoMethod.FindStringSubmatch(ln); m != nil {
				if !add(m[1], "method", i+1) {
					return out
				}
				continue
			}
			if m := reSymGoFunc.FindStringSubmatch(ln); m != nil {
				if !add(m[1], "func", i+1) {
					return out
				}
				continue
			}
			if m := reSymGoType.FindStringSubmatch(ln); m != nil {
				if !add(m[1], "type", i+1) {
					return out
				}
			}
		}
	case ".java", ".kt", ".kts", ".scala", ".groovy":
		kotlin := ext == ".kt" || ext == ".kts"
		for i, ln := range lines {
			if m := reSymJavaType.FindStringSubmatch(ln); m != nil {
				kind := m[1]
				if kind == "record" || kind == "object" {
					kind = "class"
				}
				if !add(m[2], kind, i+1) {
					return out
				}
				continue
			}
			if kotlin {
				if m := reSymKotlinFun.FindStringSubmatch(ln); m != nil {
					if !add(m[1], "method", i+1) {
						return out
					}
				}
				continue
			}
			if m := reSymJavaMethod.FindStringSubmatch(ln); m != nil {
				if isDeclKeyword(m[1]) {
					continue
				}
				if !add(m[1], "method", i+1) {
					return out
				}
			}
		}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".svelte":
		for i, ln := range lines {
			if m := reSymJSExport.FindStringSubmatch(ln); m != nil {
				kind := m[1]
				switch kind {
				case "function":
					kind = "func"
				case "let", "var":
					kind = "var"
				}
				if !add(m[2], kind, i+1) {
					return out
				}
			}
		}
	case ".py":
		for i, ln := range lines {
			if m := reSymPy.FindStringSubmatch(ln); m != nil {
				kind := "func"
				if m[1] == "class" {
					kind = "class"
				}
				if !add(m[2], kind, i+1) {
					return out
				}
			}
		}
	}
	return out
}

// isDeclKeyword filters keywords the loose Java method regex can capture on
// declaration lines that are not methods.
func isDeclKeyword(name string) bool {
	switch name {
	case "class", "interface", "enum", "record", "if", "for", "while", "switch", "return", "new", "throw", "catch":
		return true
	}
	return false
}

// entrySymbol picks the best declared symbol name for an entry point: a real
// main function first, then the primary class-like declaration, falling back
// to the provided file stem when extraction found nothing usable.
func entrySymbol(syms []Symbol, stem string) string {
	for _, s := range syms {
		if (s.Kind == "func" || s.Kind == "method") && s.Name == "main" {
			return "main"
		}
	}
	for _, s := range syms {
		switch s.Kind {
		case "class", "interface", "enum":
			return s.Name
		}
	}
	return stem
}
