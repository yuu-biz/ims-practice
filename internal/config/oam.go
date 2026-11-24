package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// OAM is the configuration of the oam role: the OAM server, which
// operates every node of the network through the maintenance API of
// each node, provisions the subscribers through the provisioning API of
// the HSS, and serves the OAM screen.
type OAM struct {
	// HTTPListen is where the OAM API and the OAM screen are served.
	// The API changes the configuration of the nodes and hands out
	// backups that hold credentials, and asks for no password, so this
	// listener belongs on the network of the nodes alone.
	HTTPListen string
	// Nodes is the inventory: every node that the OAM operates. At
	// least one.
	Nodes []OAMNode
	// HSSProvisioning is the URL of the HSS provisioning API, such as
	// http://hss:8081. Empty leaves the subscriber endpoints
	// unavailable.
	HSSProvisioning string
	// DataDir is where the OAM keeps what it holds itself: the history
	// of the configuration changes it made, and the backups. Empty
	// leaves the history and the backups unavailable. Several OAM
	// instances that share one directory see the same history and the
	// same backups.
	DataDir string
	// SubNetwork names the SubNetwork whose alarm list the OAM offers to
	// an OSS through the 3GPP management services, such as ims: the
	// network seen as one subnetwork of the operator. Empty, with
	// MnSRoot, leaves those services out.
	SubNetwork string
	// MnSRoot is where an OSS reaches those services (TS 32.158 4.4.2):
	// the URL of the OAM listener as the OSS calls it, then the root
	// path, such as http://oam:8080/3GPPManagement.
	MnSRoot string
}

// OAMNode is one node of the inventory. Several nodes may have the same
// role, as a role can run as several instances, and one node may have
// several roles, as one process can run several roles.
type OAMNode struct {
	// Name tells the node apart in the OAM API, such as pcscf-1. It is
	// a path segment: lowercase letters, digits and hyphens. It need
	// not be the node name of the node's own configuration file, which
	// two instances of a role share.
	Name string
	// Roles are the roles of the node, as its own configuration file
	// names them, such as pcscf.
	Roles []string
	// Maintenance is the base URL of the node's maintenance listener,
	// such as http://pcscf:8085.
	Maintenance string
}

type rawOAM struct {
	HTTPListen      string `yaml:"http_listen"`
	HSSProvisioning string `yaml:"hss_provisioning"`
	DataDir         string `yaml:"data_dir"`
	SubNetwork      string `yaml:"subnetwork"`
	MnSRoot         string `yaml:"mns_root"`
	Nodes           []struct {
		Name        string   `yaml:"name"`
		Roles       []string `yaml:"roles"`
		Maintenance string   `yaml:"maintenance"`
	} `yaml:"nodes"`
}

func (raw *rawOAM) validate() (OAM, error) {
	if err := checkHostPort(raw.HTTPListen); err != nil {
		return OAM{}, fmt.Errorf("http_listen: %w", err)
	}
	if raw.HSSProvisioning != "" {
		if err := checkHTTPURL(raw.HSSProvisioning); err != nil {
			return OAM{}, fmt.Errorf("hss_provisioning: %w", err)
		}
	}
	if len(raw.Nodes) == 0 {
		return OAM{}, errors.New("nodes: at least one node is required")
	}
	c := OAM{HTTPListen: raw.HTTPListen, HSSProvisioning: raw.HSSProvisioning, DataDir: raw.DataDir, SubNetwork: raw.SubNetwork, MnSRoot: raw.MnSRoot}
	if err := checkMnS(raw.SubNetwork, raw.MnSRoot); err != nil {
		return OAM{}, err
	}
	names := map[string]bool{}
	for i, n := range raw.Nodes {
		if !maintenance.IsName(n.Name) {
			return OAM{}, fmt.Errorf("nodes[%d].name: %q is not a name of lowercase letters, digits and hyphens", i, n.Name)
		}
		if names[n.Name] {
			return OAM{}, fmt.Errorf("nodes[%d].name: %q is used twice", i, n.Name)
		}
		names[n.Name] = true
		if len(n.Roles) == 0 {
			return OAM{}, fmt.Errorf("nodes[%d].roles: at least one role is required", i)
		}
		for j, r := range n.Roles {
			// Any name the maintenance API accepts, not only the roles
			// of this binary: the OAM reaches nodes of other products
			// too.
			if !maintenance.IsName(r) {
				return OAM{}, fmt.Errorf("nodes[%d].roles[%d]: %q is not a name of lowercase letters, digits and hyphens", i, j, r)
			}
		}
		if err := checkHTTPURL(n.Maintenance); err != nil {
			return OAM{}, fmt.Errorf("nodes[%d].maintenance: %w", i, err)
		}
		c.Nodes = append(c.Nodes, OAMNode{Name: n.Name, Roles: slices.Clone(n.Roles), Maintenance: n.Maintenance})
	}
	return c, nil
}

// checkMnS checks the names of the 3GPP management services: both or
// neither, a SubNetwork name that a DN can hold, and an MnSRoot that
// is an http URL.
func checkMnS(subNetwork, root string) error {
	switch {
	case subNetwork == "" && root == "":
		return nil
	case subNetwork == "":
		return errors.New("subnetwork: required with mns_root")
	case root == "":
		return errors.New("mns_root: required with subnetwork")
	}
	if err := mns.SubNetworkDN(subNetwork).Check(); err != nil {
		return fmt.Errorf("subnetwork: %w", err)
	}
	if _, err := mns.ParseMnSRoot(root); err != nil {
		return fmt.Errorf("mns_root: %w", err)
	}
	if err := checkHTTPURL(root); err != nil {
		return fmt.Errorf("mns_root: %w", err)
	}
	return nil
}

// checkHTTPURL checks the base URL of an API that the OAM calls: plain
// HTTP to a host, with nothing after the host and port but an optional
// path, so that a mistake shows when the file is read rather than at the
// first request.
func checkHTTPURL(s string) error {
	u, err := url.Parse(s)
	switch {
	case err != nil:
		return fmt.Errorf("%q: %w", s, err)
	case u.Scheme != "http":
		return fmt.Errorf("%q is not an http:// URL", s)
	case u.Hostname() == "":
		return fmt.Errorf("%q names no host", s)
	case u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return fmt.Errorf("%q has more than a host, a port and a path", s)
	}
	return nil
}
