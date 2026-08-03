package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichGraphGoImportsAndEntries(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(dir, "cmd", "app", "main.go"), `package main

import (
	"fmt"
	"example.com/demo/internal/svc"
)

func main() {
	fmt.Println(svc.Name)
}
`)
	mustWrite(t, filepath.Join(dir, "internal", "svc", "svc.go"), `package svc

const Name = "x"
`)
	mustWrite(t, filepath.Join(dir, "internal", "svc", "util.go"), `package svc

func Helper() {}
`)

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ImportEdges) == 0 {
		t.Fatalf("expected import edges, got none; files=%d", len(m.Files))
	}
	// main should import something under internal/svc
	found := false
	for _, e := range m.ImportEdges {
		if strings.Contains(e.From, "main.go") && strings.Contains(e.To, "internal/svc") && e.Kind == "import" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("main→svc import not found: %+v", m.ImportEdges)
	}
	// same_package between svc files
	same := false
	for _, e := range m.ImportEdges {
		if e.Kind == "same_package" && strings.Contains(e.From, "svc") && strings.Contains(e.To, "svc") {
			same = true
			break
		}
	}
	if !same {
		t.Fatalf("expected same_package edge in svc: %+v", m.ImportEdges)
	}
	// entry: main.go
	hasMain := false
	for _, e := range m.EntryPoints {
		if strings.Contains(e.Path, "main.go") && e.Kind == "main" {
			hasMain = true
		}
	}
	if !hasMain {
		t.Fatalf("expected main entry: %+v", m.EntryPoints)
	}
	// neighbors API
	nb := m.ImportNeighbors("cmd/app/main.go", 8)
	if len(nb) == 0 {
		for _, f := range m.Files {
			if strings.HasSuffix(f.RelativePath, "main.go") {
				nb = m.ImportNeighbors(f.RelativePath, 8)
				break
			}
		}
	}
	if len(nb) == 0 {
		t.Fatalf("ImportNeighbors empty for main")
	}
}

func TestEnrichGraphJavaAndTS(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src/main/java/com/demo/web/OrderController.java"),
		"package com.demo.web;\nimport com.demo.service.OrderService;\n@RestController\npublic class OrderController {}\n")
	mustWrite(t, filepath.Join(dir, "src/main/java/com/demo/service/OrderService.java"),
		"package com.demo.service;\npublic class OrderService {}\n")
	mustWrite(t, filepath.Join(dir, "web/src/api/client.ts"),
		"import { helper } from './helper';\nexport const x = helper;\n")
	mustWrite(t, filepath.Join(dir, "web/src/api/helper.ts"),
		"export const helper = 1;\n")

	m, err := Scan(dir, "zh", Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Java import edge
	javaHit := false
	for _, e := range m.ImportEdges {
		if strings.Contains(e.From, "OrderController") && strings.Contains(e.To, "OrderService") {
			javaHit = true
		}
	}
	if !javaHit {
		t.Fatalf("java import edge missing: %+v", m.ImportEdges)
	}
	// TS relative
	tsHit := false
	for _, e := range m.ImportEdges {
		if strings.Contains(e.From, "client.ts") && strings.Contains(e.To, "helper.ts") {
			tsHit = true
		}
	}
	if !tsHit {
		t.Fatalf("ts import edge missing: %+v", m.ImportEdges)
	}
	// API entry
	api := false
	for _, e := range m.EntryPoints {
		if strings.Contains(e.Path, "OrderController") {
			api = true
		}
	}
	if !api {
		t.Fatalf("controller entry missing: %+v", m.EntryPoints)
	}
}

func TestApplyGraphFileOverlay(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
	mustWrite(t, filepath.Join(dir, "b.go"), "package b\n")
	_ = os.MkdirAll(filepath.Join(dir, ".wikify"), 0o755)
	mustWrite(t, filepath.Join(dir, ".wikify", "graph.json"), `{
  "import_edges": [{"from":"a.go","to":"b.go","kind":"import"}],
  "entry_points": [{"path":"a.go","kind":"main"}]
}`)

	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, e := range m.ImportEdges {
		if e.From == "a.go" && e.To == "b.go" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("overlay edge missing: %+v", m.ImportEdges)
	}
}

func TestImportNeighborsPrefersImport(t *testing.T) {
	m := &Model{
		ImportEdges: []ImportEdge{
			{From: "a/Ctrl.java", To: "a/Util.java", Kind: "same_package"},
			{From: "a/Ctrl.java", To: "b/Svc.java", Kind: "import"},
			{From: "a/Ctrl.java", To: "a/Dto.java", Kind: "same_package"},
		},
	}
	nb := m.ImportNeighbors("a/Ctrl.java", 3)
	if len(nb) == 0 {
		t.Fatal("empty neighbors")
	}
	if nb[0] != "b/Svc.java" {
		t.Fatalf("expected import neighbor first, got %v", nb)
	}
	joined := strings.Join(nb, ",")
	if !strings.Contains(joined, "Util.java") && !strings.Contains(joined, "Dto.java") {
		t.Fatalf("expected same_package still included when room: %v", nb)
	}
}

func TestScanGraphFileOption(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
	mustWrite(t, filepath.Join(dir, "b.go"), "package b\n")
	overlay := filepath.Join(dir, "custom-graph.json")
	mustWrite(t, overlay, `{
  "import_edges": [{"from":"a.go","to":"b.go","kind":"import"}]
}`)
	m, err := Scan(dir, "en", Options{GraphFile: overlay})
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, e := range m.ImportEdges {
		if e.From == "a.go" && e.To == "b.go" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("GraphFile option not applied: %+v", m.ImportEdges)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}


func TestWriteGraphFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
	mustWrite(t, filepath.Join(dir, "b.go"), "package b\n")
	m, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Ensure there is something to write even if EnrichGraph found no edges.
	if len(m.ImportEdges) == 0 {
		m.ImportEdges = []ImportEdge{{From: "a.go", To: "b.go", Kind: "import"}}
		m.EntryPoints = []EntryPoint{{Path: "a.go", Kind: "main"}}
		m.rebuildAdjacency()
	}
	path := DefaultGraphPath(dir)
	if err := WriteGraphFile(path, m); err != nil {
		t.Fatal(err)
	}
	g, err := LoadGraphFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.ImportEdges) == 0 {
		t.Fatal("expected edges in written graph.json")
	}
	// Fresh scan without explicit GraphFile should auto-load .wikify/graph.json
	m2, err := Scan(dir, "en", Options{})
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, e := range m2.ImportEdges {
		if e.From == "a.go" && e.To == "b.go" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("auto-loaded graph missing edge: %+v", m2.ImportEdges)
	}
}

func TestWriteGraphFileNoopEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "graph.json")
	if err := WriteGraphFile(path, &Model{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("empty model must not write graph.json")
	}
}
