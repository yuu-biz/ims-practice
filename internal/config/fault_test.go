package config

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const faultYAML = `
node: fault-agent
roles: [fault]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
fault:
  http_listen: 0.0.0.0:8080
  docker_socket: /var/run/docker.sock
  compose_project: ims-practice
  tools_image: ims-practice/fault-tools:dev
`

func TestParseFault(t *testing.T) {
	cfg, err := Parse(strings.NewReader(faultYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := Fault{
		HTTPListen:     "0.0.0.0:8080",
		DockerSocket:   "/var/run/docker.sock",
		ComposeProject: "ims-practice",
		ToolsImage:     "ims-practice/fault-tools:dev",
	}
	if cfg.Fault == nil || !reflect.DeepEqual(*cfg.Fault, want) {
		t.Errorf("fault = %+v, want %+v", cfg.Fault, want)
	}
	// The agent speaks neither SIP nor Diameter.
	if cfg.Diameter != nil || !slices.Equal(cfg.Roles, []Role{RoleFault}) {
		t.Errorf("roles %v, diameter %+v; want the fault role alone and no Diameter", cfg.Roles, cfg.Diameter)
	}
}

func TestParseRejectsFault(t *testing.T) {
	for _, tt := range []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"listen without a port", "http_listen: 0.0.0.0:8080", "http_listen: 0.0.0.0", "http_listen"},
		{"socket that is not a path", "/var/run/docker.sock", "docker.sock", "docker_socket"},
		{"project with capitals", "compose_project: ims-practice", "compose_project: IMS-Practice", "compose_project"},
		{"no tools image", "  tools_image: ims-practice/fault-tools:dev\n", "", "tools_image"},
		{"a Diameter peer", "fault:\n", "diameter:\n  origin_host: a\n  origin_realm: b\nfault:\n", "diameter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(faultYAML, tt.old, tt.new, 1)
			if yaml == faultYAML {
				t.Fatalf("the document has no %q to replace", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse = %v, want an error about %s", err, tt.wantErr)
			}
		})
	}

	// The section belongs to the role, both ways round.
	roleOnly, _, _ := strings.Cut(faultYAML, "fault:\n")
	if _, err := Parse(strings.NewReader(roleOnly)); err == nil || !strings.Contains(err.Error(), "fault: required for the fault role") {
		t.Errorf("the fault role without its section: %v", err)
	}
	sectionOnly := dashboardYAML + "fault:\n" + strings.SplitN(faultYAML, "fault:\n", 2)[1]
	if _, err := Parse(strings.NewReader(sectionOnly)); err == nil || !strings.Contains(err.Error(), "only the fault role") {
		t.Errorf("the fault section on a node without the role: %v", err)
	}
}

// TestFaultAgentConfigFollowsCompose: the configuration of the agent
// (deploy/chaos/fault-agent.yaml) loads, and the project it may reach
// is the one that compose.yaml names. With another project name the
// agent would find no container of the stack.
func TestFaultAgentConfigFollowsCompose(t *testing.T) {
	cfg, err := Load("../../deploy/chaos/fault-agent.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Roles, []Role{RoleFault}) {
		t.Errorf("roles = %v, want the fault role alone", cfg.Roles)
	}
	b, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(b, &compose); err != nil {
		t.Fatal(err)
	}
	if compose.Name == "" {
		t.Fatal("compose.yaml names no project")
	}
	if cfg.Fault.ComposeProject != compose.Name {
		t.Errorf("compose_project = %q, want the project of compose.yaml, %q", cfg.Fault.ComposeProject, compose.Name)
	}
}

// The agent keeps its faults in its own memory: a lab holds a handful
// of them, so a node of this role alone runs as it would without the
// store section.
func TestUnreadNamesTheStoreOfTheFaultAgent(t *testing.T) {
	cfg := Config{Store: &Store{Kind: StoreValkey, Address: "valkey:6379"}, Fault: &Fault{}}
	want := []string{"store"}
	if got := cfg.Unread(); !slices.Equal(got, want) {
		t.Errorf("Unread() = %v, want %v", got, want)
	}
}
