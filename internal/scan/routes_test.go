package scan

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRoutesPerFramework(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		text string
		want []Route // File filled by caller convention; checked via Method+Path+Hint
	}{
		{
			name: "spring",
			ext:  ".java",
			text: "@RestController\n@RequestMapping(value = \"/api/orders\")\npublic class C {\n  @GetMapping(\"/list\")\n  public void list() {}\n  @PostMapping(\"create\")\n  public void create() {}\n}\n",
			want: []Route{
				{Method: "ANY", Path: "/api/orders", Hint: "spring"},
				{Method: "GET", Path: "/list", Hint: "spring"},
				// "create" has no "/" → dropped
			},
		},
		{
			name: "jaxrs",
			ext:  ".java",
			text: "@Path(\"/users\")\npublic class UserResource {}\n",
			want: []Route{{Method: "ANY", Path: "/users", Hint: "jaxrs"}},
		},
		{
			name: "go",
			ext:  ".go",
			text: "func main() {\n\thttp.HandleFunc(\"/healthz\", h)\n\tr.GET(\"/api/items\", list)\n\tr.Post(\"/api/items\", create)\n\tmux.Handle(\"/static/\", fs)\n}\n",
			want: []Route{
				{Method: "ANY", Path: "/healthz", Hint: "go-http"},
				{Method: "GET", Path: "/api/items", Hint: "go-router"},
				{Method: "POST", Path: "/api/items", Hint: "go-router"},
				{Method: "ANY", Path: "/static/", Hint: "go-router"},
			},
		},
		{
			name: "express",
			ext:  ".ts",
			text: "app.get('/api/users', handler);\nrouter.post(\"/api/users\", create);\naxios.get('/should/not/match/axios');\n",
			want: []Route{
				{Method: "GET", Path: "/api/users", Hint: "express"},
				{Method: "POST", Path: "/api/users", Hint: "express"},
			},
		},
		{
			name: "flask",
			ext:  ".py",
			text: "@app.route('/api/ping')\ndef ping():\n    pass\n\n@router.get(\"/items/{id}\")\ndef item():\n    pass\n",
			want: []Route{
				{Method: "ANY", Path: "/api/ping", Hint: "flask"},
				{Method: "GET", Path: "/items/{id}", Hint: "flask"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRoutes(tc.ext, "x"+tc.ext, tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d routes, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Method != w.Method || g.Path != w.Path || g.Hint != w.Hint {
					t.Fatalf("route %d = %+v, want %+v", i, g, w)
				}
				if g.File != "x"+tc.ext {
					t.Fatalf("route file not stamped: %+v", g)
				}
			}
		})
	}
}

func TestExtractRoutesPerFileCap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxRoutesPerFile+10; i++ {
		sb.WriteString("r.GET(\"/api/x")
		for j := 0; j <= i; j++ {
			sb.WriteByte('a')
		}
		sb.WriteString("\", h)\n")
	}
	got := extractRoutes(".go", "r.go", sb.String())
	if len(got) != maxRoutesPerFile {
		t.Fatalf("per-file cap not applied: got %d, want %d", len(got), maxRoutesPerFile)
	}
}

func TestScanFillsRoutes(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src/main/java/com/demo/web/OrderController.java"),
		"package com.demo.web;\n@RestController\npublic class OrderController {\n  @GetMapping(\"/api/orders\")\n  public void list() {}\n}\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, r := range m.Routes {
		if r.Method == "GET" && r.Path == "/api/orders" && strings.Contains(r.File, "OrderController.java") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected GET /api/orders route: %+v", m.Routes)
	}
}
