package scan

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSymbolsPerLanguage(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		text string
		want []Symbol
	}{
		{
			name: "go",
			ext:  ".go",
			text: "package x\n\nfunc Foo() {}\n\nfunc (s *Svc) Bar() {}\n\ntype Baz struct{}\n",
			want: []Symbol{
				{Name: "Foo", Kind: "func", Line: 3},
				{Name: "Bar", Kind: "method", Line: 5},
				{Name: "Baz", Kind: "type", Line: 7},
			},
		},
		{
			name: "java",
			ext:  ".java",
			text: "package com.x;\npublic class OrderService {\n    public String createOrder(String id) {\n    }\n    protected void cancel() {}\n    private void hidden() {}\n}\n",
			want: []Symbol{
				{Name: "OrderService", Kind: "class", Line: 2},
				{Name: "createOrder", Kind: "method", Line: 3},
				{Name: "cancel", Kind: "method", Line: 5},
			},
		},
		{
			name: "kotlin",
			ext:  ".kt",
			text: "class Greeter {\n    fun greet() {}\n}\nfun main() {}\n",
			want: []Symbol{
				{Name: "Greeter", Kind: "class", Line: 1},
				{Name: "greet", Kind: "method", Line: 2},
				{Name: "main", Kind: "method", Line: 4},
			},
		},
		{
			name: "ts",
			ext:  ".ts",
			text: "import x from 'y';\nexport function doThing() {}\nexport default class Widget {}\nexport const limit = 5;\nexport interface Props {}\nexport type Id = string;\n",
			want: []Symbol{
				{Name: "doThing", Kind: "func", Line: 2},
				{Name: "Widget", Kind: "class", Line: 3},
				{Name: "limit", Kind: "const", Line: 4},
				{Name: "Props", Kind: "interface", Line: 5},
				{Name: "Id", Kind: "type", Line: 6},
			},
		},
		{
			name: "python",
			ext:  ".py",
			text: "import os\n\ndef handle(req):\n    pass\n\nclass Worker:\n    def run(self):\n        pass\n",
			want: []Symbol{
				{Name: "handle", Kind: "func", Line: 3},
				{Name: "Worker", Kind: "class", Line: 6},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSymbols(tc.ext, tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d symbols, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Fatalf("symbol %d = %+v, want %+v", i, got[i], w)
				}
			}
		})
	}
}

func TestExtractSymbolsCap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString("func Fn")
		sb.WriteByte(byte('A' + i%26))
		sb.WriteString(strings.Repeat("x", i/26))
		sb.WriteString("() {}\n")
	}
	got := extractSymbols(".go", sb.String())
	if len(got) != maxSymbolsPerFile {
		t.Fatalf("cap not applied: got %d, want %d", len(got), maxSymbolsPerFile)
	}
}

func TestEntrySymbol(t *testing.T) {
	if s := entrySymbol([]Symbol{{Name: "helper", Kind: "func"}, {Name: "main", Kind: "func"}}, "stem"); s != "main" {
		t.Fatalf("expected main, got %s", s)
	}
	if s := entrySymbol([]Symbol{{Name: "doIt", Kind: "func"}, {Name: "OrderApp", Kind: "class"}}, "stem"); s != "OrderApp" {
		t.Fatalf("expected class name, got %s", s)
	}
	if s := entrySymbol(nil, "stem"); s != "stem" {
		t.Fatalf("expected stem fallback, got %s", s)
	}
}

func TestScanFillsSymbolsAndEntrySymbol(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src/main/java/com/demo/DemoApp.java"),
		"package com.demo;\n@SpringBootApplication\npublic class DemoApplication {\n    public static void main(String[] args) {}\n}\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	syms := m.Symbols["src/main/java/com/demo/DemoApp.java"]
	if len(syms) == 0 {
		t.Fatalf("expected symbols for DemoApp.java: %+v", m.Symbols)
	}
	hasClass := false
	for _, s := range syms {
		if s.Name == "DemoApplication" && s.Kind == "class" {
			hasClass = true
		}
	}
	if !hasClass {
		t.Fatalf("class symbol missing: %+v", syms)
	}
	// Entry point should carry the real main symbol, not the file stem DemoApp.
	found := false
	for _, e := range m.EntryPoints {
		if strings.Contains(e.Path, "DemoApp.java") {
			found = true
			if e.Symbol != "main" && e.Symbol != "DemoApplication" {
				t.Fatalf("entry symbol should be a declared symbol, got %q", e.Symbol)
			}
			if e.Symbol == "DemoApp" {
				t.Fatalf("entry symbol still the file stem: %+v", e)
			}
		}
	}
	if !found {
		t.Fatalf("expected entry point for DemoApp.java: %+v", m.EntryPoints)
	}
}
