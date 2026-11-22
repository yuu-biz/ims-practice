package interconnect

import (
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/yamledit"
)

// TestCopiesAreGenerated fails when deploy/compose or changes.go has
// changed and the copies have not been made again.
func TestCopiesAreGenerated(t *testing.T) {
	want, err := Copies(ComposeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("changes.go makes no copy")
	}
	for _, path := range slices.Sorted(maps.Keys(want)) {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("deploy/interconnect/%s is missing: run %q from the root of the repository and commit the result", path, GenerateCommand)
			continue
		}
		if line, g, w, differ := yamledit.FirstDifference(got, want[path]); differ {
			t.Errorf("deploy/interconnect/%s line %d is\n\t%q\nwant\n\t%q\nrun %q from the root of the repository and commit the result", path, line, g, w, GenerateCommand)
		}
	}
}

// A change with no file would apply to every file in the other
// generator; here it is a mistake.
func TestChangeNeedsAFile(t *testing.T) {
	saved := changes
	defer func() { changes = saved }()
	changes = []yamledit.Change{{Key: "scscf.ibcf", Add: true, Value: ibcf}}
	if _, err := Copies(ComposeDir); err == nil {
		t.Error("no error for a change without a file")
	}
}
