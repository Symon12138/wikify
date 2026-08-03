package browse

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// Offline static assets for browse (Mermaid diagrams).
//
//go:embed static/*
var staticFS embed.FS

func writeStaticAssets(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"mermaid.min.js"} {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			return fmt.Errorf("读取内嵌 %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return fmt.Errorf("写出 %s: %w", name, err)
		}
	}
	return nil
}
