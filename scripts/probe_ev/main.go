package main

import (
	"fmt"
	"os"

	"github.com/Symon12138/wikify/internal/evidence"
	"github.com/Symon12138/wikify/internal/scan"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: probe_ev <dir> <title>...")
		os.Exit(2)
	}
	dir := os.Args[1]
	m, err := scan.Scan(dir, "zh", scan.Options{})
	if err != nil {
		fmt.Println("scan err", err)
		return
	}
	fmt.Println("files", len(m.Files))
	for _, t := range os.Args[2:] {
		deps := evidence.PickDependentFiles(m, t, t, 8)
		fmt.Printf("%q -> %d %v\n", t, len(deps), deps)
	}
}
