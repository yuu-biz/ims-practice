package oam

import (
	"cmp"
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
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
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
	// Name names the OAM as the system through which an operator
	// acknowledges an alarm: the OAM passes it to the node as the
	// ackSystemId of each acknowledgement of its API (TS 28.111 7.4.1).
	// The oam role gives the node name of its configuration file, as a
	// node names itself when it acknowledges an alarm of its own.
	// Required.
	Name string
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

	// SubNetwork names the SubNetwork whose AlarmList the OAM offers to
	// an OSS through the 3GPP management services (MnSHandler), such as
	// ims. Empty, with MnSRoot empty too, for an OAM without them.
	SubNetwork string
	// MnSRoot is the {MnSRoot} of TS 32.158 4.4.2 at which an OSS
	// reaches those services, such as http://oam:8080/3GPPManagement:
	// the address of the OAM listener and the {root} path. Its scheme
	// and host are also those of the canonical URIs that name the
	// alarmed objects.
	MnSRoot string
	// NtfSubscriptions keeps the subscriptions of the OSSs to the alarm
	// notifications; nil for an OAM that takes none.
	NtfSubscriptions NtfSubscriptionStore
	// MaxNtfSubscriptions bounds the number of subscriptions; 0 for
	// DefaultMaxNtfSubscriptions.
	MaxNtfSubscriptions int
	// AlarmWatches keeps which instance watches each node for the alarm
	// notifications, and how far it has sent them (RunNotifications);
	// nil for an OAM that sends none.
	AlarmWatches AlarmWatchStore
	// Instance names this OAM instance to the watch store; empty for a
	// random name, chosen when the server is made.
	Instance string
	// AlarmEventsWait is how long a request for the alarm events of a
	// node waits for one, in whole seconds; 0 for
	// DefaultAlarmEventsWait. It is also the wait before a node that
	// does not answer is asked again.
	AlarmEventsWait time.Duration
	// WatchRenewal is how often an instance renews its watches and
	// tries to take the others; 0 for DefaultWatchRenewal.
	WatchRenewal time.Duration
	// NotificationTimeout bounds the POST of one notification; 0 for
	// DefaultNotificationTimeout.
	NotificationTimeout time.Duration
	// NotificationConcurrency bounds how many recipients a notification
	// goes to at once; 0 for DefaultNotificationConcurrency.
	NotificationConcurrency int
	// MissedRetryInterval is how often a recipient that missed a
	// notification is tried again; 0 for DefaultMissedRetryInterval.
	MissedRetryInterval time.Duration
	// Metrics is where the OAM counts the notifications it sends; nil
	// for a registry of its own, which nothing serves.
	Metrics *metrics.Registry
}

// Server is the OAM server. It keeps no state of its own about
// the nodes or the subscribers: every answer comes from the nodes and
// the HSS when it is asked for. It is safe for concurrent use.
type Server struct {
	name            string
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

	// mns is the 3GPP management services; nil without them.
	mns                 *northbound
	ntfSubscriptions    NtfSubscriptionStore
	maxNtfSubscriptions int

	watches                 AlarmWatchStore
	instance                string
	alarmEventsWait         time.Duration
	watchRenewal            time.Duration
	notificationTimeout     time.Duration
	notificationConcurrency int
	missedRetryInterval     time.Duration
	notificationMetrics     notificationMetrics
	// owed is the tries of the notifications that misses owe, which
	// RunNotifications waits for before it returns.
	owed sync.WaitGroup
	// started is when the server was made: the lastModification of an
	// alarm list that no node answered for.
	started time.Time
}

// New checks c and makes the OAM server.
func New(c Config) (*Server, error) {
	if len(c.Nodes) == 0 {
		return nil, errors.New("oam: no nodes in the inventory")
	}
	if c.Name == "" {
		return nil, errors.New("oam: no name, which each acknowledgement names as its system")
	}
	s := &Server{
		name:            c.Name,
		byName:          map[string]Endpoint{},
		httpClient:      c.HTTPClient,
		nodeTimeout:     c.NodeTimeout,
		nodeConcurrency: c.NodeConcurrency,
		now:             c.Now,
		log:             c.Log,

		provisioningConcurrency: c.ProvisioningConcurrency,
		ntfSubscriptions:        c.NtfSubscriptions,
		maxNtfSubscriptions:     c.MaxNtfSubscriptions,
		watches:                 c.AlarmWatches,
		instance:                cmp.Or(c.Instance, randomHex(8)),
		alarmEventsWait:         cmp.Or(c.AlarmEventsWait, DefaultAlarmEventsWait),
		watchRenewal:            cmp.Or(c.WatchRenewal, DefaultWatchRenewal),
		notificationTimeout:     cmp.Or(c.NotificationTimeout, DefaultNotificationTimeout),
		notificationConcurrency: cmp.Or(c.NotificationConcurrency, DefaultNotificationConcurrency),
		missedRetryInterval:     cmp.Or(c.MissedRetryInterval, DefaultMissedRetryInterval),
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
	if s.maxNtfSubscriptions == 0 {
		s.maxNtfSubscriptions = DefaultMaxNtfSubscriptions
	}
	if s.nodeTimeout < 0 || s.nodeConcurrency < 0 || s.provisioningConcurrency < 0 || s.maxNtfSubscriptions < 0 ||
		s.watchRenewal <= 0 || s.notificationTimeout < 0 || s.notificationConcurrency < 0 || s.missedRetryInterval < 0 {
		return nil, errors.New("oam: negative timeout or concurrency")
	}
	if s.alarmEventsWait < time.Second || s.alarmEventsWait%time.Second != 0 || s.alarmEventsWait > maintenance.MaxAlarmEventsWait {
		return nil, fmt.Errorf("oam: the wait for alarm events, %v, is no whole number of seconds up to %v", s.alarmEventsWait, maintenance.MaxAlarmEventsWait)
	}
	// A store whose watches run out unless renewed (internal/kvstore/
	// oamstore) needs them renewed well within the lease: two renewals
	// in a row may fail, and the watch still holds.
	if l, ok := c.AlarmWatches.(interface{ WatchLease() time.Duration }); ok && 2*s.watchRenewal >= l.WatchLease() {
		return nil, fmt.Errorf("oam: the watches are renewed every %v, which is not less than half their lease, %v", s.watchRenewal, l.WatchLease())
	}
	reg := c.Metrics
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	var err error
	if s.notificationMetrics, err = newNotificationMetrics(reg); err != nil {
		return nil, fmt.Errorf("oam: %w", err)
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
	if c.SubNetwork != "" || c.MnSRoot != "" {
		nb, err := newNorthbound(c.SubNetwork, c.MnSRoot, s.nodes)
		if err != nil {
			return nil, err
		}
		s.mns = nb
	}
	s.started = s.now()
	return s, nil
}

func checkEndpoint(e Endpoint) error {
	if !maintenance.IsName(e.Name) {
		return fmt.Errorf("oam: node name %q is not a name of lowercase letters, digits and hyphens", e.Name)
	}
	if len(e.Roles) == 0 {
		return fmt.Errorf("oam: node %s has no roles", e.Name)
	}
	for _, r := range e.Roles {
		if !maintenance.IsName(r) {
			return fmt.Errorf("oam: node %s: role %q is not a name of lowercase letters, digits and hyphens", e.Name, r)
		}
	}
	u, err := url.Parse(e.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("oam: node %s: %q is not an http or https URL", e.Name, e.URL)
	}
	return nil
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
