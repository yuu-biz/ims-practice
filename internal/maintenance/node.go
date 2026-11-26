package maintenance

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// ErrUnknownFunction is the answer for a role that the node does not
// run.
var ErrUnknownFunction = errors.New("no such role on this node")

// Config is what a node supplies to its maintenance API.
type Config struct {
	// Node is the node's name from its configuration file. Required.
	Node string
	// Instance tells apart the processes that run the same
	// configuration file, such as the host or pod name.
	Instance string
	Build    Build
	// LogLevel is the level of the node's log handler, which the API
	// reads and sets. Required.
	LogLevel *slog.LevelVar
	// Functions are the roles that the node's configuration gives it, in
	// the order
	// the state lists them. At least one.
	Functions []Function
	// Configuration is the node's effective configuration, as a value
	// that marshals to JSON with every secret a Secret (CheckRedacted).
	// The API checks it before it answers. Required.
	Configuration func() any
	// Settings are the parts of the configuration that the API may
	// change while the node runs, in the order it lists them. The rest
	// changes only with the file and a restart.
	Settings []Setting
	// MaxAlarmRecords bounds the alarm list (RaiseAlarm); 0 for
	// DefaultMaxAlarmRecords. Tests lower it, to fill the list with a
	// few alarms.
	MaxAlarmRecords int
	// MaxAlarmEvents bounds the journal of the changes of the alarm
	// list (AlarmEvents); 0 for DefaultMaxAlarmEvents. Tests lower it,
	// to run past the journal with a few events.
	MaxAlarmEvents int
	// Clock is the time for the start time, the shutdown deadlines, the
	// change records and the alarms; nil for the system clock.
	Clock Clock
	// Log is where the API logs the operations; nil for none.
	Log *slog.Logger
}

// Node is the maintenance API of one node. Apart from the
// administrative state of its roles, the configuration generation and
// the alarm list it keeps nothing: what it shows comes from the
// callbacks of Config. It is safe for concurrent use.
type Node struct {
	node, instance string
	build          Build
	start          time.Time
	clock          Clock
	logLevel       *slog.LevelVar
	log            *slog.Logger
	functions      []*ManagedFunction
	roles          map[string]*ManagedFunction
	view           func() any
	settings       []Setting
	settingsByName map[string]Setting
	// group is the group of settings that the node's settings belong
	// to, or nil when none of them is in a group (Together). A node has
	// one group at most: a change that named the settings of two could
	// put the one in force and then fail on the other.
	group *SettingsGroup

	// changeMu makes the configuration changes one at a time. It guards
	// the settings of this node only, not any user's state.
	changeMu   sync.Mutex
	generation atomic.Uint64

	alarms *alarmList
}

// New checks c and makes the maintenance API of the node. All its
// roles start UNLOCKED.
func New(c Config) (*Node, error) {
	if c.Node == "" {
		return nil, errors.New("maintenance: no node name")
	}
	if c.LogLevel == nil {
		return nil, errors.New("maintenance: no log level")
	}
	if len(c.Functions) == 0 {
		return nil, errors.New("maintenance: no roles")
	}
	if c.Configuration == nil {
		return nil, errors.New("maintenance: no configuration view")
	}
	if c.MaxAlarmRecords < 0 || c.MaxAlarmEvents < 0 {
		return nil, errors.New("maintenance: negative alarm list bound or journal bound")
	}
	maxAlarms := cmp.Or(c.MaxAlarmRecords, DefaultMaxAlarmRecords)
	maxEvents := cmp.Or(c.MaxAlarmEvents, DefaultMaxAlarmEvents)
	clock := c.Clock
	if clock == nil {
		clock = systemClock{}
	}
	log := c.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	n := &Node{
		node:           c.Node,
		instance:       c.Instance,
		build:          c.Build,
		start:          clock.Now(),
		clock:          clock,
		logLevel:       c.LogLevel,
		log:            log,
		roles:          map[string]*ManagedFunction{},
		view:           c.Configuration,
		settingsByName: map[string]Setting{},
	}
	n.alarms = newAlarmList(c.Node, maxAlarms, maxEvents, n.start, log)
	for _, f := range c.Functions {
		if !IsName(f.Role) {
			return nil, fmt.Errorf("maintenance: role %q is not a name of lowercase letters, digits and hyphens", f.Role)
		}
		if _, dup := n.roles[f.Role]; dup {
			return nil, fmt.Errorf("maintenance: role %q twice", f.Role)
		}
		m := newManagedFunction(f, clock, log)
		n.functions = append(n.functions, m)
		n.roles[f.Role] = m
	}
	for _, s := range c.Settings {
		switch {
		case s.current == nil:
			return nil, errors.New("maintenance: a setting not made by NewSetting")
		case !isSettingName(s.name):
			return nil, fmt.Errorf("maintenance: setting %q is not a dotted name of lowercase words", s.name)
		case isSecretName(s.name):
			return nil, fmt.Errorf("maintenance: setting %q would be a secret, which the API does not change", s.name)
		}
		if _, dup := n.settingsByName[s.name]; dup {
			return nil, fmt.Errorf("maintenance: setting %q twice", s.name)
		}
		if s.group != nil {
			if n.group != nil && n.group != s.group {
				return nil, fmt.Errorf("maintenance: setting %q is in another group than the settings before it, and a node takes no two groups", s.name)
			}
			n.group = s.group
		}
		n.settings = append(n.settings, s)
		n.settingsByName[s.name] = s
	}
	return n, nil
}

// IsName says whether s is a name of lowercase letters, digits and
// hyphens, as the roles of the configuration file are.
func IsName(s string) bool {
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

// ManagedFunction is the administrative state of a role, which the role
// reads, or nil for a role that the node does not run.
func (n *Node) ManagedFunction(role string) *ManagedFunction { return n.roles[role] }

// State is the state of the node and its roles. It calls the callbacks
// of each role.
func (n *Node) State() NodeState {
	s := NodeState{
		Node:                    n.node,
		Instance:                n.instance,
		Build:                   n.build,
		StartTime:               n.start,
		ConfigurationGeneration: n.configurationGeneration(),
		LogLevel:                n.logLevel.Level(),
		Functions:               []FunctionState{},
	}
	for _, m := range n.functions {
		s.Functions = append(s.Functions, n.functionState(m))
	}
	return s
}

func (n *Node) functionState(m *ManagedFunction) FunctionState {
	callbacks := m.callbacks
	s := FunctionState{
		Role:                m.role,
		AdministrativeState: m.AdministrativeState(),
		OperationalState:    Enabled,
	}
	if callbacks.OperationalState != nil {
		s.OperationalState = callbacks.OperationalState()
	}
	if users, counted := m.Users(); counted {
		s.Users = &users
	}
	if d := m.ShutdownDeadline(); !d.IsZero() {
		s.ShutdownDeadline = &d
	}
	return s
}

// MaxTimeoutSeconds bounds the timeout_seconds of a shut down: a day,
// far longer than any session or registration transaction of a role
// takes. Without a bound, the seconds could overflow the nanoseconds of
// a time.Duration and give a deadline that has already passed.
const MaxTimeoutSeconds = 24 * 60 * 60

// SetAdministrativeState makes the event of X.731 7.1.3 that the
// request asks for on a role, and returns the role's state after it.
func (n *Node) SetAdministrativeState(role string, req AdministrativeStateRequest) (FunctionState, error) {
	m := n.roles[role]
	if m == nil {
		return FunctionState{}, fmt.Errorf("%w: %q", ErrUnknownFunction, role)
	}
	if req.TimeoutSeconds != 0 && req.AdministrativeState != ShuttingDown {
		return FunctionState{}, fmt.Errorf("%w: timeout_seconds goes only with %s", ErrInvalidRequest, ShuttingDown)
	}
	if req.TimeoutSeconds > MaxTimeoutSeconds {
		return FunctionState{}, fmt.Errorf("%w: timeout_seconds %d is more than %d", ErrInvalidRequest, req.TimeoutSeconds, MaxTimeoutSeconds)
	}
	log := n.log.With("role", role, "operator", req.Operator)
	switch req.AdministrativeState {
	case Unlocked:
		log.Info("unlock requested")
		m.Unlock()
	case Locked:
		log.Info("lock requested")
		m.Lock()
	case ShuttingDown:
		timeout := time.Duration(req.TimeoutSeconds) * time.Second
		log.Info("shut down requested", "timeout", timeout.String())
		if err := m.ShutDown(timeout); err != nil {
			return FunctionState{}, err
		}
	default:
		return FunctionState{}, fmt.Errorf("%w: administrative state %q is none of %s, %s, %s",
			ErrInvalidRequest, req.AdministrativeState, Unlocked, ShuttingDown, Locked)
	}
	return n.functionState(m), nil
}

// SetLogLevel sets the level of the node's log.
func (n *Node) SetLogLevel(req LogLevelRequest) LogLevelChange {
	previous := n.logLevel.Level()
	n.logLevel.Set(req.Level)
	// Logged at WARN, so that the change shows at any level up to it.
	n.log.Warn("log level changed", "from", previous.String(), "to", req.Level.String(), "operator", req.Operator)
	return LogLevelChange{Level: req.Level, Previous: previous}
}

// ReadBuild is the Build of the running binary: the version of its main
// module, the VCS revision it was built from, and the Go version.
func ReadBuild() Build {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Build{}
	}
	b := Build{Version: info.Main.Version, GoVersion: info.GoVersion}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			b.Revision = s.Value
		}
	}
	return b
}
