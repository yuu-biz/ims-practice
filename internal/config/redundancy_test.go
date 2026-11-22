package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// The files of deploy/redundancy, the deployment whose roles keep the
// state of their users in the shared store.

// composeOverlay is the file of deploy/redundancy that
// deploy/redundancy/redundancy.yaml itself is: a Compose file, not a
// node's configuration.
const composeOverlay = "redundancy.yaml"

func redundancyNodes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("../../deploy/redundancy/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []string
	for _, f := range files {
		if filepath.Base(f) != composeOverlay {
			nodes = append(nodes, f)
		}
	}
	if len(nodes) == 0 {
		t.Fatal("no node files in deploy/redundancy")
	}
	return nodes
}

// TestRedundancyConfigsFollowCompose: each node of deploy/redundancy is
// the node of the same name in deploy/compose with a store section
// added, and nothing else changed. The two files are kept side by side,
// so that the deployment without a shared store stays exactly as the
// guides describe it.
func TestRedundancyConfigsFollowCompose(t *testing.T) {
	for _, f := range redundancyNodes(t) {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			compose, err := Load(filepath.Join("../../deploy/compose", name))
			if err != nil {
				t.Fatal(err)
			}
			redundant, err := Load(f)
			if err != nil {
				t.Fatal(err)
			}
			if redundant.Store == nil {
				t.Fatal("no store section")
			}
			want := Store{Kind: StoreValkey, Address: "172.28.0.5:6379", Prefix: "ims:", Timeout: time.Second}
			if *redundant.Store != want {
				t.Errorf("store = %+v, want %+v", *redundant.Store, want)
			}
			redundant.Store = nil
			if !reflect.DeepEqual(redundant, compose) {
				t.Errorf("%s differs from deploy/compose/%s in more than the store section:\n%+v\n%+v",
					name, name, redundant, compose)
			}
		})
	}
}

// TestRedundancyGivesEveryStoreOfARoleToTheSharedStore: no node that
// holds the state of users is left out of deploy/redundancy. Such a
// node would keep that state in its own process, where another instance
// of its role does not find it, while the rest of the deployment shares
// it.
func TestRedundancyGivesEveryStoreOfARoleToTheSharedStore(t *testing.T) {
	files, err := filepath.Glob("../../deploy/compose/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		cfg, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.readsStore() {
			continue
		}
		if _, err := os.Stat(filepath.Join("../../deploy/redundancy", name)); err != nil {
			t.Errorf("deploy/compose/%s holds the state of users, but deploy/redundancy has no %s: %v", name, name, err)
		}
	}
}

// TestRedundancyConfigsAreFullyRead: every section of these files
// reaches a role. A store section that no role reads, such as one on
// the I-CSCF or on a stub, would make the node look as though it shared
// its state when it has none to share.
func TestRedundancyConfigsAreFullyRead(t *testing.T) {
	for _, f := range redundancyNodes(t) {
		t.Run(filepath.Base(f), func(t *testing.T) {
			cfg, err := Load(f)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Unread(); len(got) != 0 {
				t.Errorf("Unread() = %v, want none", got)
			}
		})
	}
}

// TestRedundancyMountsEveryNodeFile: the Compose override gives each
// node of deploy/redundancy its file in place of the one of
// deploy/compose, and starts it once the store answers. A node file
// that the override does not mount would leave that node on the memory
// of its process, while every test of the file itself still passes.
func TestRedundancyMountsEveryNodeFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("../../deploy/redundancy", composeOverlay))
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Services map[string]struct {
			Volumes   []string `yaml:"volumes"`
			DependsOn map[string]struct {
				Condition string `yaml:"condition"`
			} `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &overlay); err != nil {
		t.Fatal(err)
	}
	for _, f := range redundancyNodes(t) {
		name := filepath.Base(f)
		service := strings.TrimSuffix(name, ".yaml")
		t.Run(service, func(t *testing.T) {
			s, ok := overlay.Services[service]
			if !ok {
				t.Fatalf("%s has no service %s", composeOverlay, service)
			}
			mount := "./deploy/redundancy/" + name + ":/etc/ims/node.yaml:ro"
			if !slices.Contains(s.Volumes, mount) {
				t.Errorf("volumes = %v, want %s", s.Volumes, mount)
			}
			if got := s.DependsOn["valkey"].Condition; got != "service_healthy" {
				t.Errorf("depends_on valkey condition = %q, want service_healthy", got)
			}
		})
	}
}
