// Package interconnect holds the files of the two joined networks
// (compose.yaml). The S-CSCF and the IP-SM-GW of network A, the home
// network, are copies of those in deploy/compose, made by go generate,
// that name the IBCF through which a request for network B leaves; the
// Lab is a copy that knows the address of that IBCF on the link. A
// copy differs from its original only by the table in changes.go.
package interconnect

//go:generate go run ./gencopies

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/yamledit"
)

// The directories of the originals and the copies, relative to
// deploy/interconnect, where go generate runs.
const (
	ComposeDir  = "../compose"
	networkADir = "a"
)

// GenerateCommand is what a person runs, from the root of the
// repository, after changing deploy/compose or changes.go.
const GenerateCommand = "go generate ./deploy/interconnect/..."

// Copies returns every copy, by its path relative to
// deploy/interconnect, made from the role files in composeDir.
func Copies(composeDir string) (map[string][]byte, error) {
	byFile := map[string][]yamledit.Change{}
	for _, c := range changes {
		if c.File == "" {
			return nil, fmt.Errorf("changes.go: the change of %s names no file of deploy/compose", c.Key)
		}
		byFile[c.File] = append(byFile[c.File], c)
	}
	copies := map[string][]byte{}
	for _, name := range slices.Sorted(maps.Keys(byFile)) {
		src, err := os.ReadFile(filepath.Join(composeDir, name))
		if err != nil {
			return nil, err
		}
		out, err := yamledit.Apply(src, byFile[name])
		if err != nil {
			return nil, fmt.Errorf("deploy/compose/%s: %w", name, err)
		}
		header := yamledit.Header("#", GenerateCommand, "deploy/compose/"+name, "deploy/interconnect/changes.go")
		copies[networkADir+"/"+name] = append(header, out...)
	}
	return copies, nil
}
