package maintenance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"
)

// ErrStaleGeneration is the answer to a configuration change whose
// generation is not above the node's: the OAM server sent a change
// that an earlier one has overtaken.
var ErrStaleGeneration = errors.New("configuration generation is not above the present one")

// SettingsRefusedError is the answer to a configuration change in which
// some settings were refused. Nothing was changed.
type SettingsRefusedError struct {
	// Settings names each refused setting with the reason.
	Settings map[string]string
}

func (e *SettingsRefusedError) Error() string {
	names := slices.Sorted(maps.Keys(e.Settings))
	return fmt.Sprintf("%d of the settings refused, nothing changed: %s", len(names), strings.Join(names, ", "))
}

// Setting is one part of the configuration that the node can change
// while it runs. The role makes it with NewSetting.
type Setting struct {
	name, description string
	current           func() (json.RawMessage, error)
	// prepare checks a new value and returns it as current would give
	// it once in force, with the function that sets it, which cannot
	// fail. prepare changes nothing.
	prepare func(json.RawMessage) (value json.RawMessage, apply func(), err error)
	// group, when not nil, is the settings that a change puts in force
	// together (Together).
	group *settingGroup
}

// settingGroup is how the settings of one group are put in force at
// once: begin before the first of them is set, commit after the last.
type settingGroup struct{ begin, commit func() }

// Together makes settings one group. A change that names several of
// them puts them in force at once: the API calls begin, then the set of
// each setting the change names, then commit, so the role can gather
// the values that set gives it and publish them in commit. A request of
// the role that reads its settings then sees them all from before the
// change or all from after it, never a part of each. begin and commit
// cannot fail; the API calls them once per change that names a setting
// of the group, and never for one that names none.
func Together(begin, commit func(), settings ...Setting) []Setting {
	g := &settingGroup{begin: begin, commit: commit}
	grouped := make([]Setting, len(settings))
	for i, s := range settings {
		s.group = g
		grouped[i] = s
	}
	return grouped
}

// NewSetting declares a setting that the maintenance API may change. Its
// name is the path of the setting in the configuration file, such as
// hss.admission; get gives the present value; validate refuses a value
// that the role cannot take, or is nil to take every value that decodes
// as a T; set puts a validated value in force and cannot fail. A value
// is JSON, decoded into T with unknown fields refused.
//
// The API changes several settings at once by validating all of them
// first and setting them only when all are valid, so validate must
// change nothing. A setting is never a secret: secrets are provisioned,
// and New refuses a setting whose name marks one (CheckRedacted).
func NewSetting[T any](name, description string, get func() T, validate func(T) error, set func(T)) Setting {
	return Setting{
		name:        name,
		description: description,
		current:     func() (json.RawMessage, error) { return json.Marshal(get()) },
		prepare: func(raw json.RawMessage) (json.RawMessage, func(), error) {
			var v T
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&v); err != nil {
				return nil, nil, err
			}
			if _, err := dec.Token(); !errors.Is(err, io.EOF) {
				return nil, nil, errors.New("more than one JSON value")
			}
			if validate != nil {
				if err := validate(v); err != nil {
					return nil, nil, err
				}
			}
			value, err := json.Marshal(v)
			if err != nil {
				return nil, nil, err
			}
			return value, func() { set(v) }, nil
		},
	}
}

// Configuration is the answer to GET /maintenance/v1/config.
type Configuration struct {
	Generation uint64 `json:"generation"`
	// Configuration is the node's effective configuration as the node's
	// view shows it, with every secret redacted.
	Configuration json.RawMessage `json:"configuration"`
	// Changeable lists the settings that a change may name, by name.
	// Every other part of the configuration changes only with the file
	// and a restart.
	Changeable []SettingValue `json:"changeable"`
}

// SettingValue is a changeable setting with its present value.
type SettingValue struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Value       json.RawMessage `json:"value"`
}

// ConfigurationChangeRequest is the body of PATCH
// /maintenance/v1/config.
type ConfigurationChangeRequest struct {
	// Settings are the new values by setting name. At least one.
	Settings map[string]json.RawMessage `json:"settings"`
	// Generation is the number that the OAM server gives this
	// configuration, so that it can record each change and go back to an
	// earlier one; it must be above the node's, or the node's own for a
	// change that the node has in force already. 0 lets the node count
	// one up.
	Generation uint64 `json:"generation,omitempty"`
	// Operator and Comment are who asks and why, for the record.
	Operator string `json:"operator,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// ConfigurationChange is the answer to PATCH /maintenance/v1/config:
// the record of one change, in the shape that the OAM server keeps in
// its history.
type ConfigurationChange struct {
	Node       string    `json:"node"`
	Instance   string    `json:"instance,omitempty"`
	Generation uint64    `json:"generation"`
	Time       time.Time `json:"time"`
	Operator   string    `json:"operator,omitempty"`
	Comment    string    `json:"comment,omitempty"`
	// Changes are the settings whose value changed, by name; a setting
	// that was sent with its present value is not among them.
	Changes []SettingChange `json:"changes"`
}

// SettingChange is one setting in a ConfigurationChange, with the value
// before and after. Previous is what a later rollback sends back.
type SettingChange struct {
	Name     string          `json:"name"`
	Previous json.RawMessage `json:"previous"`
	Value    json.RawMessage `json:"value"`
}

// isSettingName says whether s is a dotted name of lowercase words
// with digits and underscores, as the keys of the configuration file
// are: hss.admission, log.level.
func isSettingName(s string) bool {
	for part := range strings.SplitSeq(s, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				return false
			}
		}
	}
	return true
}

// Configuration is the node's effective configuration and its
// changeable settings. It fails when the view does not marshal to JSON
// or holds a secret that is not redacted.
func (n *Node) Configuration() (Configuration, error) {
	n.changeMu.Lock()
	defer n.changeMu.Unlock()
	view, err := json.Marshal(n.view())
	if err != nil {
		return Configuration{}, fmt.Errorf("configuration view: %w", err)
	}
	if err := checkRedactedJSON(view); err != nil {
		return Configuration{}, fmt.Errorf("configuration view: %w", err)
	}
	c := Configuration{Generation: n.generation.Load(), Configuration: view, Changeable: []SettingValue{}}
	for _, s := range n.settings {
		v, err := s.current()
		if err != nil {
			return Configuration{}, fmt.Errorf("setting %s: %w", s.name, err)
		}
		c.Changeable = append(c.Changeable, SettingValue{Name: s.name, Description: s.description, Value: v})
	}
	return c, nil
}

// ChangeConfiguration changes the settings of the request, all or none.
// It validates every value first; when one is refused it changes
// nothing and returns a SettingsRefusedError. Changes run one at a
// time, and a read of the configuration waits for one to finish.
//
// A request with the node's own generation is the change that gave the
// node that generation, sent again: an OAM server that lost the answer
// sends it so, and so does one whose change reached the node by another
// way first. When every value of the request is the one in force, it is
// answered with the record of that generation, which changes nothing;
// otherwise it is a change that another one overtook.
func (n *Node) ChangeConfiguration(req ConfigurationChangeRequest) (ConfigurationChange, error) {
	if len(req.Settings) == 0 {
		return ConfigurationChange{}, fmt.Errorf("%w: no settings", ErrInvalidRequest)
	}
	n.changeMu.Lock()
	defer n.changeMu.Unlock()
	generation := n.generation.Load()
	stale := fmt.Errorf("%w: %d is not above %d", ErrStaleGeneration, req.Generation, generation)
	if req.Generation != 0 && req.Generation < generation {
		return ConfigurationChange{}, stale
	}
	type prepared struct {
		setting         Setting
		previous, value json.RawMessage
		apply           func()
	}
	var changes []prepared
	refused := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(req.Settings)) {
		s, ok := n.settingsByName[name]
		if !ok {
			refused[name] = "not a setting that changes while the node runs; change the configuration file and restart the node"
			continue
		}
		previous, err := s.current()
		if err != nil {
			refused[name] = "present value: " + err.Error()
			continue
		}
		value, apply, err := s.prepare(req.Settings[name])
		if err != nil {
			refused[name] = err.Error()
			continue
		}
		changes = append(changes, prepared{s, previous, value, apply})
	}
	if len(refused) > 0 {
		return ConfigurationChange{}, &SettingsRefusedError{Settings: refused}
	}

	record := ConfigurationChange{
		Node:       n.node,
		Instance:   n.instance,
		Generation: req.Generation,
		Time:       n.clock.Now(),
		Operator:   req.Operator,
		Comment:    req.Comment,
		Changes:    []SettingChange{},
	}
	if req.Generation != 0 && req.Generation == generation {
		for _, c := range changes {
			if !bytes.Equal(c.value, c.previous) {
				return ConfigurationChange{}, stale
			}
		}
		n.log.Info("configuration change in force already", "generation", record.Generation,
			"operator", record.Operator, "comment", record.Comment)
		return record, nil
	}
	if req.Generation == 0 {
		record.Generation = generation + 1
	}
	// The settings of a group go in force together (Together): its begin
	// comes before the first of them, and its commit after the last.
	var groups []*settingGroup
	for _, c := range changes {
		if g := c.setting.group; g != nil && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	for _, g := range groups {
		g.begin()
	}
	for _, c := range changes {
		c.apply()
	}
	for _, g := range groups {
		g.commit()
	}
	for _, c := range changes {
		// The value in force is read back, so the record has it as the
		// configuration shows it, not as the request spelled it.
		value, err := c.setting.current()
		if err != nil {
			value = req.Settings[c.setting.name]
		}
		if !bytes.Equal(value, c.previous) {
			record.Changes = append(record.Changes, SettingChange{Name: c.setting.name, Previous: c.previous, Value: value})
		}
	}
	n.generation.Store(record.Generation)
	n.log.Info("configuration changed", "generation", record.Generation, "operator", record.Operator,
		"comment", record.Comment, "changes", record.Changes)
	return record, nil
}
