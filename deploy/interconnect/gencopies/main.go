// Command gencopies writes the copies of deploy/compose that the two
// joined networks use (deploy/interconnect/changes.go). go generate
// runs it in deploy/interconnect:
//
//	go generate ./deploy/interconnect/...
package main

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/yuu-biz/ims-practice/deploy/interconnect"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gencopies:", err)
		os.Exit(1)
	}
}

func run() error {
	copies, err := interconnect.Copies(interconnect.ComposeDir)
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
		fmt.Println("wrote deploy/interconnect/" + path)
	}
	return nil
}
