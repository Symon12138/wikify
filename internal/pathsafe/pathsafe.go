// Package pathsafe guards against path traversal from untrusted page metadata
// (LLM-generated titles/sections/content_path). Policy: relative, slash-form,
// no ".." segments anywhere — fail loudly instead of clamping silently.
package pathsafe

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafe is returned when a path escapes its root.
var ErrUnsafe = errors.New(`unsafe path: absolute or contains ".." segments`)

// Rel validates rel as a safe slash-relative subpath and returns it cleaned.
// Rejects: empty, absolute paths (Windows drive/UNC included via IsAbs), any
// ".." segment. Forward slashes only; callers convert with filepath.FromSlash.
func Rel(rel string) (string, error) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" {
		return "", ErrUnsafe
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", ErrUnsafe
	}
	// Windows drive letters like "C:" also slip past IsAbs on unix builds; reject
	// any single-letter-drive prefix defensively.
	if len(rel) >= 2 && rel[1] == ':' {
		return "", ErrUnsafe
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", ErrUnsafe
		}
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == "" {
		return "", ErrUnsafe
	}
	return cleaned, nil
}
