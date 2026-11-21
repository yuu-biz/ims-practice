package oam

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// PathPrefix is where the OAM API lives. A role mounts Handler under
// it.
const PathPrefix = "/oam/v1/"

// The defaults of Config.
const (
	DefaultNodeTimeout     = 5 * time.Second
	DefaultNodeConcurrency = 16
)

// Endpoint is one node of the inventory: a process that serves the
// maintenance API. Several endpoints may have the same role, as a role
// can run as several instances, and one endpoint may have several
// roles, as one process can run several roles.
type Endpoint struct {
	// Name tells the endpoint apart in the OAM API, such as pcscf-1. It
	// is a path segment: lowercase letters, digits and hyphens. It need
	// not be the node name of the node's configuration file, which two
	// instances of the same role share.
	Name string `json:"name"`
	// Roles are the roles of the node, as its configuration file names
	// them, such as pcscf.
	Roles []string `json:"roles"`
	// URL is the base URL of the node's maintenance listener, such as
	// http://pcscf-1:8085.
	URL string `json:"url"`
}

// Config is what the OAM server is given.
type Config struct {
	// Nodes is the inventory: every node that the OAM operates. At
	// least one.
	Nodes []Endpoint
	// HSSProvisioning is the base URL of the HSS provisioning API, such
	// as http://hss:8081; empty for an OAM without subscriber
	// operations. Several HSS instances that share their store are
	// behind one name.
	HSSProvisioning string
	// ProvisioningConcurrency bounds how many records of one bulk
	// provisioning go to the HSS at once; 0 for
	// DefaultProvisioningConcurrency.
	ProvisioningConcurrency int
	// History keeps the record of each configuration change that the
	// OAM makes; nil for an OAM that changes no configuration.
	History ChangeStore
	// Backups keeps the backups; nil for an OAM without them.
	Backups BackupStore
	// Sections are the persistent data of other roles that a backup
	// holds after the HSS subscriptions, such as the SC's short messages
	// that wait for delivery, for a role that serves its data through
	// its maintenance API. What lives only
	// while a node runs (registrations, dialogs, media contexts) is not
	// backed up: the UEs and the restoration procedures rebuild it.
	Sections []Section
	// HTTPClient calls the nodes and the HSS; nil for
	// http.DefaultClient. Its own
	// Timeout, if any, applies on top of NodeTimeout.
	HTTPClient *http.Client
	// NodeTimeout bounds each call to one node or to the HSS; 0 for
	// DefaultNodeTimeout.
	NodeTimeout time.Duration
	// NodeConcurrency bounds how many nodes one operation calls at
	// once; 0 for DefaultNodeConcurrency.
	NodeConcurrency int
	// Now is the clock; nil for time.Now.
	Now func() time.Time
	// Log is where the server logs the operations; nil for none.
	Log *slog.Logger
}

// Server is the OAM server. It keeps no state of its own about
// the nodes or the subscribers: every answer comes from the nodes and
// the HSS when it is asked for. It is safe for concurrent use.
type Server struct {
	nodes           []Endpoint
	byName          map[string]Endpoint
	httpClient      *http.Client
	nodeTimeout     time.Duration
	nodeConcurrency int
	now             func() time.Time
	log             *slog.Logger

	hss                     *hssClient
	provisioningConcurrency int

	backups        BackupStore
	sections       []Section
	exportPageSize int

	history ChangeStore
}

// New checks c and makes the OAM server.
func New(c Config) (*Server, error) {
	if len(c.Nodes) == 0 {
		return nil, errors.New("oam: no nodes in the inventory")
	}
	s := &Server{
		byName:          map[string]Endpoint{},
		httpClient:      c.HTTPClient,
		nodeTimeout:     c.NodeTimeout,
		nodeConcurrency: c.NodeConcurrency,
		now:             c.Now,
		log:             c.Log,

		provisioningConcurrency: c.ProvisioningConcurrency,
		backups:                 c.Backups,
		history:                 c.History,
		exportPageSize:          defaultExportPageSize,
	}
	if s.httpClient == nil {
		s.httpClient = http.DefaultClient
	}
	if s.nodeTimeout == 0 {
		s.nodeTimeout = DefaultNodeTimeout
	}
	if s.nodeConcurrency == 0 {
		s.nodeConcurrency = DefaultNodeConcurrency
	}
	if s.provisioningConcurrency == 0 {
		s.provisioningConcurrency = DefaultProvisioningConcurrency
	}
	if s.nodeTimeout < 0 || s.nodeConcurrency < 0 || s.provisioningConcurrency < 0 {
		return nil, errors.New("oam: negative timeout or concurrency")
	}
	if c.HSSProvisioning != "" {
		u, err := url.Parse(c.HSSProvisioning)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("oam: HSS provisioning %q is not an http or https URL", c.HSSProvisioning)
		}
		s.hss = &hssClient{base: strings.TrimSuffix(c.HSSProvisioning, "/"), http: s.httpClient}
		s.sections = append(s.sections, hssSubscriptions{s})
	}
	for _, sec := range c.Sections {
		if !isDottedName(sec.Name()) {
			return nil, fmt.Errorf("oam: section %q is not a dotted name of lowercase words", sec.Name())
		}
		if s.section(sec.Name()) != nil {
			return nil, fmt.Errorf("oam: section %q twice", sec.Name())
		}
		s.sections = append(s.sections, sec)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	for _, e := range c.Nodes {
		if err := checkEndpoint(e); err != nil {
			return nil, err
		}
		if _, dup := s.byName[e.Name]; dup {
			return nil, fmt.Errorf("oam: node %q twice in the inventory", e.Name)
		}
		e.Roles = slices.Clone(e.Roles)
		s.nodes = append(s.nodes, e)
		s.byName[e.Name] = e
	}
	return s, nil
}

func checkEndpoint(e Endpoint) error {
	if !isName(e.Name) {
		return fmt.Errorf("oam: node name %q is not a name of lowercase letters, digits and hyphens", e.Name)
	}
	if len(e.Roles) == 0 {
		return fmt.Errorf("oam: node %s has no roles", e.Name)
	}
	for _, r := range e.Roles {
		if !isName(r) {
			return fmt.Errorf("oam: node %s: role %q is not a name of lowercase letters, digits and hyphens", e.Name, r)
		}
	}
	u, err := url.Parse(e.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("oam: node %s: %q is not an http or https URL", e.Name, e.URL)
	}
	return nil
}

// isName says whether s is a name of lowercase letters, digits and
// hyphens, as the roles of the configuration file are.
func isName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// isDottedName says whether s is a dotted name of lowercase words with
// digits and underscores, as the keys of the configuration file are.
func isDottedName(s string) bool {
	for part := range strings.SplitSeq(s, ".") {
		if part == "" || strings.Trim(part, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
			return false
		}
	}
	return true
}

// randomHex is n random octets in hexadecimal, for the names that the
// OAM makes.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Inventory is the answer to GET /oam/v1/inventory.
type Inventory struct {
	Nodes []Endpoint `json:"nodes"`
}

// node is the endpoint of the inventory named name.
func (s *Server) node(name string) (Endpoint, error) {
	e, ok := s.byName[name]
	if !ok {
		return Endpoint{}, fmt.Errorf("%w: no node %q in the inventory", ErrNotFound, name)
	}
	return e, nil
}

// role is every endpoint of the inventory that has role, in the order
// of the inventory.
func (s *Server) role(role string) ([]Endpoint, error) {
	var es []Endpoint
	for _, e := range s.nodes {
		if slices.Contains(e.Roles, role) {
			es = append(es, e)
		}
	}
	if len(es) == 0 {
		return nil, fmt.Errorf("%w: no node of role %q in the inventory", ErrNotFound, role)
	}
	return es, nil
}
