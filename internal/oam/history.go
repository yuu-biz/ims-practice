package oam

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// ChangeRecord is one configuration change of one node, as the history
// keeps it, so that a change can be traced and rolled back: who changed
// what and when, with the value of each
// setting before and after, as the node answered.
type ChangeRecord struct {
	ID string `json:"id"`
	// ChangeSet is the same for the records of one operation of the
	// OAM, such as one change of every node of a role.
	ChangeSet string `json:"change_set"`
	// Node is the node's name in the inventory.
	Node       string    `json:"node"`
	RecordedAt time.Time `json:"recorded_at"`
	// RollbackOf is the record that this change rolled back.
	RollbackOf string `json:"rollback_of,omitempty"`
	// Forced says that the rollback went ahead over the settings that a
	// later change had touched.
	Forced bool                            `json:"forced,omitempty"`
	Change maintenance.ConfigurationChange `json:"change"`
}

// ChangeStore keeps the history: records are only ever added. Several
// OAM instances that share one store share the history.
type ChangeStore interface {
	Append(ChangeRecord) error
	// Scan calls f with each record in the order they were added, until
	// f returns false.
	Scan(f func(ChangeRecord) bool) error
}

// FileChangeStore keeps the history in one file, a JSON object per line,
// only ever appended to. The file is 0600; it holds no secret, as no
// changeable setting is one (maintenance.NewSetting).
//
// Each record is one write with O_APPEND, so several OAM instances can
// share the file on one local volume. A line that a crash cut short at
// the end is skipped.
type FileChangeStore struct {
	path string
	// mu keeps the appends of this process apart; it is not held while
	// a node is called.
	mu sync.Mutex
}

// NewFileChangeStore uses the file at path, which it makes if need be.
func NewFileChangeStore(path string) (*FileChangeStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &FileChangeStore{path: path}, f.Close()
}

func (f *FileChangeStore) Append(r ChangeRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := os.OpenFile(f.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(b, '\n'))
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

// historyLine bounds a line of the history file.
const historyLine = 1 << 20

func (f *FileChangeStore) Scan(fn func(ChangeRecord) bool) error {
	file, err := os.Open(f.path)
	if err != nil {
		return err
	}
	defer file.Close()
	r := bufio.NewReaderSize(file, historyLine)
	for n := 1; ; n++ {
		line, err := readLine(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("history line %d: %v", n, err)
		}
		var rec ChangeRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			if _, next := readLine(r); errors.Is(next, io.EOF) {
				return nil
			}
			return fmt.Errorf("history line %d is not a record", n)
		}
		if !fn(rec) {
			return nil
		}
	}
}

// ChangeConfiguration changes the settings of req on each target, and
// records what each node changed. Every target gets the same new
// generation, one above the highest that the targets have, so the nodes
// of a role keep one generation. A node that
// another change has overtaken meanwhile refuses with 409, and only
// that node fails. The OAM numbers the generations: req must have none.
func (s *Server) ChangeConfiguration(ctx context.Context, targets []Endpoint, req maintenance.ConfigurationChangeRequest) (FanOut[ChangeRecord], error) {
	if s.history == nil {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: no configuration history", ErrUnavailable)
	}
	if req.Generation != 0 {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: generation: the OAM numbers the generations", ErrInvalidRequest)
	}
	if len(req.Settings) == 0 {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: settings: at least one", ErrInvalidRequest)
	}
	states := s.State(ctx, targets)
	var answered []Endpoint
	for i, n := range states.Nodes {
		if n.Result != nil {
			req.Generation = max(req.Generation, n.Result.ConfigurationGeneration)
			answered = append(answered, targets[i])
		}
	}
	req.Generation++
	changeSet := randomHex(8)
	s.log.Info("configuration change requested", "nodes", names(targets), "generation", req.Generation,
		"change_set", changeSet, "operator", req.Operator, "comment", req.Comment)
	changed := fanOut(ctx, s, answered, func(ctx context.Context, c *maintenance.Client, e Endpoint) (ChangeRecord, error) {
		change, err := c.ChangeConfiguration(ctx, req)
		return ChangeRecord{ID: randomHex(8), ChangeSet: changeSet, Node: e.Name, RecordedAt: s.now().UTC(), Change: change}, err
	})

	results := make([]NodeResult[ChangeRecord], len(targets))
	j := 0
	for i, n := range states.Nodes {
		if n.Result == nil {
			results[i] = NodeResult[ChangeRecord]{Node: n.Node, URL: n.URL, Error: n.Error}
			continue
		}
		results[i] = changed.Nodes[j]
		j++
		if results[i].Result != nil {
			if err := s.record(*results[i].Result); err != nil {
				results[i].Error = &NodeError{Error: "the node changed, but the history could not record it: " + err.Error()}
			}
		}
	}
	return tally(results), nil
}

func (s *Server) record(r ChangeRecord) error {
	if err := maintenance.CheckRedacted(r); err != nil {
		return err
	}
	return s.history.Append(r)
}

// History is the last limit records of a node, the newest first. It
// reads the whole history, which grows with the operators' changes, not
// with the subscribers.
func (s *Server) History(node string, limit int) ([]ChangeRecord, error) {
	if s.history == nil {
		return nil, fmt.Errorf("%w: no configuration history", ErrUnavailable)
	}
	var last []ChangeRecord
	err := s.history.Scan(func(r ChangeRecord) bool {
		if r.Node == node {
			last = append(last, r)
			if len(last) > limit {
				last = last[1:]
			}
		}
		return true
	})
	slices.Reverse(last)
	if last == nil {
		last = []ChangeRecord{}
	}
	return last, err
}

// changeRecord finds a record by its ID, with the records of the same
// node after it that changed one of its settings.
func (s *Server) changeRecord(id string) (ChangeRecord, []ChangeRecord, error) {
	var found *ChangeRecord
	var later []ChangeRecord
	err := s.history.Scan(func(r ChangeRecord) bool {
		switch {
		case found == nil && r.ID == id:
			found = &r
		case found != nil && r.Node == found.Node && sharesSettings(r, *found):
			later = append(later, r)
		}
		return true
	})
	if err != nil {
		return ChangeRecord{}, nil, err
	}
	if found == nil {
		return ChangeRecord{}, nil, fmt.Errorf("%w: no change %q in the history", ErrNotFound, id)
	}
	return *found, later, nil
}

func sharesSettings(a, b ChangeRecord) bool {
	for _, x := range a.Change.Changes {
		for _, y := range b.Change.Changes {
			if x.Name == y.Name {
				return true
			}
		}
	}
	return false
}

// RollbackRequest is the body of a rollback; it may be empty.
type RollbackRequest struct {
	Operator string `json:"operator,omitempty"`
	Comment  string `json:"comment,omitempty"`
	// Force rolls back even the settings that a later change, or a
	// change that the OAM did not make, has touched since.
	Force bool `json:"force,omitempty"`
}

// Rollback puts back the previous value of each setting that the
// change id changed, as a new change with a new generation: the history
// keeps both (TS 32.600 7.1, the fallback of Bulk CM). It refuses, with
// a SettingsConflictError that names each setting, when a later change
// recorded for the node touched one of the settings, or when the node
// no longer has the value that the change set, unless req.Force.
//
// A setting that the node has at its previous value already is rolled
// back, and no conflict: the instances of a role that share their
// settings, as those of the HSS do, take on the rollback that reached
// another of them. A node that has every previous value already answers
// the rollback with a record of no change, at its own generation.
func (s *Server) Rollback(ctx context.Context, id string, req RollbackRequest) (ChangeRecord, error) {
	if s.history == nil {
		return ChangeRecord{}, fmt.Errorf("%w: no configuration history", ErrUnavailable)
	}
	target, later, err := s.changeRecord(id)
	if err != nil {
		return ChangeRecord{}, err
	}
	if len(target.Change.Changes) == 0 {
		return ChangeRecord{}, fmt.Errorf("%w: change %s changed no setting", ErrConflict, id)
	}
	e, err := s.node(target.Node)
	if err != nil {
		return ChangeRecord{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
	defer cancel()
	client := maintenance.NewClient(e.URL, s.httpClient)
	conf, err := client.Configuration(ctx)
	if err != nil {
		return ChangeRecord{}, s.rollbackNodeError(e, err)
	}

	conflicts := map[string]string{}
	for _, r := range later {
		for _, c := range r.Change.Changes {
			if _, ok := conflicts[c.Name]; !ok && slices.ContainsFunc(target.Change.Changes, func(t maintenance.SettingChange) bool { return t.Name == c.Name }) {
				conflicts[c.Name] = fmt.Sprintf("changed again by %s (generation %d)", r.ID, r.Change.Generation)
			}
		}
	}
	settings := map[string]json.RawMessage{}
	// rolledBack: the node has the previous value of every setting
	// already.
	rolledBack := true
	for _, c := range target.Change.Changes {
		settings[c.Name] = c.Previous
		if _, ok := conflicts[c.Name]; ok {
			rolledBack = false
			continue
		}
		now := changeableValue(conf, c.Name)
		switch {
		case now == nil:
			conflicts[c.Name] = "the node no longer has this setting"
		case sameJSON(now, c.Previous):
			continue
		case !sameJSON(now, c.Value):
			conflicts[c.Name] = fmt.Sprintf("the node has %s, not the %s that the change set", now, c.Value)
		}
		rolledBack = false
	}
	if len(conflicts) > 0 && !req.Force {
		return ChangeRecord{}, &SettingsConflictError{
			Message:  fmt.Sprintf("settings of change %s changed since; send force to roll back anyway", id),
			Settings: conflicts,
		}
	}
	if req.Comment == "" {
		req.Comment = "rollback of " + id
	}
	s.log.Info("rollback requested", "change", id, "node", e.Name, "operator", req.Operator, "force", req.Force)
	// A node that has every previous value already is sent them with
	// its own generation, which it answers as the change it has.
	generation := conf.Generation + 1
	if rolledBack {
		generation = conf.Generation
	}
	change, err := client.ChangeConfiguration(ctx, maintenance.ConfigurationChangeRequest{
		Settings: settings, Generation: generation, Operator: req.Operator, Comment: req.Comment,
	})
	if err != nil {
		return ChangeRecord{}, s.rollbackNodeError(e, err)
	}
	r := ChangeRecord{ID: randomHex(8), ChangeSet: randomHex(8), Node: e.Name, RecordedAt: s.now().UTC(),
		RollbackOf: id, Forced: len(conflicts) > 0, Change: change}
	if err := s.record(r); err != nil {
		return r, fmt.Errorf("the node rolled back, but the history could not record it: %w", err)
	}
	return r, nil
}

// changeableValue is the value of a changeable setting of conf, or nil.
func changeableValue(conf maintenance.Configuration, name string) json.RawMessage {
	for _, v := range conf.Changeable {
		if v.Name == name {
			return v.Value
		}
	}
	return nil
}

// sameJSON says whether two JSON values are the same apart from space.
func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// rollbackNodeError is the error of a rollback for what the node said:
// a refusal or a stale generation is a conflict, anything else an
// upstream failure.
func (s *Server) rollbackNodeError(e Endpoint, err error) error {
	n := s.nodeError(err)
	msg := "node " + e.Name + ": " + n.Error
	switch n.StatusCode {
	case http.StatusBadRequest, http.StatusConflict:
		return &SettingsConflictError{Message: msg, Settings: n.Settings}
	}
	return fmt.Errorf("%w: %s", ErrUpstream, msg)
}

// RollbackChangeSet rolls back each record of a change set, such as a
// change of every node of a role, each with the checks of Rollback.
func (s *Server) RollbackChangeSet(ctx context.Context, changeSet string, req RollbackRequest) (FanOut[ChangeRecord], error) {
	if s.history == nil {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: no configuration history", ErrUnavailable)
	}
	var records []ChangeRecord
	if err := s.history.Scan(func(r ChangeRecord) bool {
		if r.ChangeSet == changeSet {
			records = append(records, r)
		}
		return true
	}); err != nil {
		return FanOut[ChangeRecord]{}, err
	}
	if len(records) == 0 {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: no change set %q in the history", ErrNotFound, changeSet)
	}
	results := make([]NodeResult[ChangeRecord], len(records))
	for i, r := range records {
		results[i].Node = r.Node
		if e, err := s.node(r.Node); err == nil {
			results[i].URL = e.URL
		}
		rolled, err := s.Rollback(ctx, r.ID, req)
		if rolled.ID != "" {
			results[i].Result = &rolled
		}
		if err != nil {
			results[i].Error = rollbackError(err)
		}
	}
	return tally(results), nil
}

func rollbackError(err error) *NodeError {
	n := &NodeError{Error: err.Error()}
	var conflict *SettingsConflictError
	if errors.As(err, &conflict) {
		n.StatusCode, n.Settings = http.StatusConflict, conflict.Settings
	}
	return n
}

// maxHistory bounds the limit of a history listing.
const maxHistory = 1000

func (s *Server) historyRoutes(rt *routes) {
	change := func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
		var req maintenance.ConfigurationChangeRequest
		if err := readJSON(w, r, &req, false); err != nil {
			writeError(w, err)
			return
		}
		f, err := s.ChangeConfiguration(r.Context(), ts, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	}
	rt.handle(http.MethodPatch, PathPrefix+"nodes/{node}/config", s.withTargets(change))
	rt.handle(http.MethodPatch, PathPrefix+"roles/{role}/config", s.withTargets(change))
	rt.handle(http.MethodGet, PathPrefix+"nodes/{node}/config/history", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > maxHistory {
				writeError(w, fmt.Errorf("%w: limit: from 1 to %d", ErrInvalidRequest, maxHistory))
				return
			}
			limit = n
		}
		// A node that has left the inventory keeps its history.
		h, err := s.History(r.PathValue("node"), limit)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, History{Changes: h})
	})
	rt.handle(http.MethodGet, PathPrefix+"config/history/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s.history == nil {
			writeError(w, fmt.Errorf("%w: no configuration history", ErrUnavailable))
			return
		}
		rec, _, err := s.changeRecord(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rec)
	})
	rt.handle(http.MethodPost, PathPrefix+"config/history/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		var req RollbackRequest
		if err := readJSON(w, r, &req, true); err != nil {
			writeError(w, err)
			return
		}
		rec, err := s.Rollback(r.Context(), r.PathValue("id"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rec)
	})
	rt.handle(http.MethodPost, PathPrefix+"config/change-sets/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		var req RollbackRequest
		if err := readJSON(w, r, &req, true); err != nil {
			writeError(w, err)
			return
		}
		f, err := s.RollbackChangeSet(r.Context(), r.PathValue("id"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
}

// History is the answer to GET /oam/v1/nodes/{node}/config/history.
type History struct {
	Changes []ChangeRecord `json:"changes"`
}
