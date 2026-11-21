package k8s

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// checkedKustomizations runs check-overlays.sh --list, which needs no
// kubectl.
func checkedKustomizations(t *testing.T) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("check-overlays.sh needs bash, which is not installed")
	}
	out, err := exec.Command(bash, "check-overlays.sh", "--list").Output()
	if err != nil {
		t.Fatalf("check-overlays.sh --list: %v", err)
	}
	return strings.Fields(string(out))
}

// TestCheckOverlaysListsEveryKustomization: the script builds what a
// person applies, and every kustomization and component of deploy/k8s
// is built by one of them, so that a new overlay, or a component that
// only a comment names, cannot be left out of the check.
func TestCheckOverlaysListsEveryKustomization(t *testing.T) {
	listed := checkedKustomizations(t)
	var applied []string
	built := map[string]bool{}
	for _, name := range listed {
		// A variant is named base+component+component.
		base, components, _ := strings.Cut(name, "+")
		if !slices.Contains(applied, base) && components == "" {
			applied = append(applied, base)
		}
		reach(t, base, built)
		if components != "" {
			for _, component := range strings.Split(components, "+") {
				reach(t, "components/"+component, built)
			}
		}
	}
	if want := appliedKustomizations(t); !slices.Equal(applied, want) {
		t.Errorf("check-overlays.sh builds %v, want the applied kustomizations %v", applied, want)
	}

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// The work directories of a check-overlays.sh that is running.
		if d.IsDir() && strings.HasPrefix(d.Name(), ".check-overlays.") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "kustomization.yaml" {
			return nil
		}
		if dir := filepath.ToSlash(filepath.Dir(path)); !built[dir] {
			t.Errorf("deploy/k8s/%s is built by none of %v; add it to check-overlays.sh, or take it into a kustomization", dir, listed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// reach marks dir and every directory that its resources and components
// name.
func reach(t *testing.T, dir string, built map[string]bool) {
	t.Helper()
	if built[dir] {
		return
	}
	built[dir] = true
	k := readKustomization(t, dir)
	for _, next := range slices.Concat(k.Resources, k.Components) {
		path := filepath.ToSlash(filepath.Clean(filepath.Join(dir, next)))
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			reach(t, path, built)
		}
	}
}
