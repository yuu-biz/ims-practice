package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Fault is the configuration of the fault role: the fault agent, which
// serves the HTTP API of internal/fault and applies each fault to the
// containers of one Compose project. It is not an IMS node and sends no
// signalling.
type Fault struct {
	// HTTPListen is where the API is served, such as 0.0.0.0:8080.
	HTTPListen string
	// DockerSocket is the path of the Docker Engine's socket in the
	// container, such as /var/run/docker.sock. Whoever can use it has
	// the power of root on the host, so only the agent's own override
	// file mounts it (deploy/chaos/agent.yaml).
	DockerSocket string
	// ComposeProject is the project whose containers a fault may reach,
	// such as ims-practice. A container of another project, and the
	// host, stay as they are.
	ComposeProject string
	// ToolsImage holds the nft, tc and ip that a network fault needs
	// (deploy/chaos/tools). The agent runs a throw-away container of it
	// in the target's network namespace; the IMS image needs none of
	// them.
	ToolsImage string
}

type rawFault struct {
	HTTPListen     string `yaml:"http_listen"`
	DockerSocket   string `yaml:"docker_socket"`
	ComposeProject string `yaml:"compose_project"`
	ToolsImage     string `yaml:"tools_image"`
}

// composeProject is what Compose takes as the name of a project.
var composeProject = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (raw *rawFault) validate() (Fault, error) {
	if err := checkHostPort(raw.HTTPListen); err != nil {
		return Fault{}, fmt.Errorf("http_listen: %w", err)
	}
	if !strings.HasPrefix(raw.DockerSocket, "/") {
		return Fault{}, fmt.Errorf("docker_socket: %q is not the path of the Engine's socket, such as /var/run/docker.sock", raw.DockerSocket)
	}
	if !composeProject.MatchString(raw.ComposeProject) {
		return Fault{}, fmt.Errorf("compose_project: %q is not the name of a Compose project", raw.ComposeProject)
	}
	if raw.ToolsImage == "" {
		return Fault{}, errors.New("tools_image: required")
	}
	return Fault{
		HTTPListen:     raw.HTTPListen,
		DockerSocket:   raw.DockerSocket,
		ComposeProject: raw.ComposeProject,
		ToolsImage:     raw.ToolsImage,
	}, nil
}
