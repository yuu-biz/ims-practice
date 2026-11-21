// Command gencopies writes the copies of deploy/compose that the
// Kubernetes manifests read (deploy/k8s/changes.go). go generate runs it
// in deploy/k8s:
//
//	go generate ./deploy/k8s/...
package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/yuu-biz/ims-practice/deploy/k8s"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gencopies:", err)
		os.Exit(1)
	}
}

func run() error {
	copies, err := k8s.Copies(k8s.ComposeDir)
	if err != nil {
		return err
	}
	for _, path := range slices.Sorted(maps.Keys(copies)) {
		content := copies[path]
		old, err := os.ReadFile(path)
		if err == nil && bytes.Equal(old, content) {
			continue
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
		fmt.Println("wrote deploy/k8s/" + path)
	}
	// A copy whose original is gone goes too. Only copies match these
	// patterns.
	for _, pattern := range []string{k8s.ConfigDir + "/*.yaml", k8s.ZoneDir + "/*.zone"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if _, ok := copies[path]; ok {
				continue
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			fmt.Println("removed deploy/k8s/" + path)
		}
	}
	return nil
}
