package fault

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The root of the repository, from this package, and the opt-in that
// alone may hand the Docker socket to a container.
const (
	repositoryRoot = "../.."
	agentOverride  = "deploy/chaos/agent.yaml"
	agentService   = "fault-agent"
)

// composeService is what the test reads of a service of a Compose file.
type composeService struct {
	Volumes []yaml.Node
	Ports   []yaml.Node
}

// composeFiles returns the Compose files of the repository, by their
// path from the root, with their services: compose.yaml and every
// override under deploy/.
func composeFiles(t *testing.T) map[string]map[string]composeService {
	t.Helper()
	files := map[string]map[string]composeService{}
	read := func(rel string) {
		f, err := os.Open(filepath.Join(repositoryRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		dec := yaml.NewDecoder(f)
		for {
			var doc struct {
				Services map[string]composeService
			}
			if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
				return
			} else if err != nil {
				// A file of another kind, such as a Kubernetes manifest
				// whose fields do not fit, is no Compose file.
				return
			}
			if doc.Services != nil {
				files[rel] = doc.Services
			}
		}
	}
	read("compose.yaml")
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "deploy"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (filepath.Ext(p) != ".yaml" && filepath.Ext(p) != ".yml") {
			return err
		}
		rel, err := filepath.Rel(repositoryRoot, p)
		if err != nil {
			return err
		}
		read(filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files["compose.yaml"] == nil || files[agentOverride] == nil {
		t.Fatalf("found the Compose files %v, want compose.yaml and %s among them", slices.Sorted(maps.Keys(files)), agentOverride)
	}
	return files
}

// volumeSource is the source of a volume, short ("src:dst:ro") or long
// ({source: src}).
func volumeSource(n yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		source, _, _ := strings.Cut(n.Value, ":")
		return source
	}
	var long struct{ Source string }
	n.Decode(&long)
	return long.Source
}

// TestOnlyTheAgentGetsTheDockerSocket: the Docker socket gives a
// container the power of root on the host, so no service of
// compose.yaml or of an override has it, but the fault agent of
// agent.yaml; and the agent's API is published on the host's loopback
// address only.
func TestOnlyTheAgentGetsTheDockerSocket(t *testing.T) {
	files := composeFiles(t)
	agentHasIt := false
	for file, services := range files {
		for name, s := range services {
			for _, v := range s.Volumes {
				if path.Base(volumeSource(v)) != dockerSocket {
					continue
				}
				if file == agentOverride && name == agentService {
					agentHasIt = true
					continue
				}
				t.Errorf("%s: the service %s mounts the Docker socket; only %s of %s may", file, name, agentService, agentOverride)
			}
		}
	}
	if !agentHasIt {
		t.Errorf("%s: %s does not mount the Docker socket", agentOverride, agentService)
	}

	agent := files[agentOverride][agentService]
	for _, p := range agent.Ports {
		if p.Kind != yaml.ScalarNode || !strings.HasPrefix(p.Value, "127.0.0.1:") {
			t.Errorf("%s: %s publishes %q; bind it to 127.0.0.1, so that only the host reaches the API", agentOverride, agentService, p.Value)
		}
	}
}
