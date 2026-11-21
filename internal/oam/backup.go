package oam

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"
)

// Section is one kind of persistent data that a backup holds, so that
// it can be put back after a failure: the subscriptions of the HSS, or
// the data of another role. The
// OAM takes the data through the API of the role that keeps it, never
// from the role's store directly, so a backup is a logical copy that a
// learner can read.
type Section interface {
	// Name names the section in the archive, such as hss.subscriptions:
	// dotted lowercase words.
	Name() string
	// Export calls emit with each record of the section, one JSON object
	// each, in an order that the role keeps from page to page. It takes
	// no lock over the whole data: each record is consistent, and the
	// whole may mix in changes made while it runs.
	Export(ctx context.Context, emit func(json.RawMessage) error) error
	// Restore puts one record back, or says what it would do on a dry
	// run. Restoring a record that is in place already is unchanged.
	Restore(ctx context.Context, record json.RawMessage, dryRun bool) Outcome
}

// hssSubscriptions is the section of the HSS subscriptions, through the
// export of the HSS provisioning API and the same PUT as a bulk
// provisioning. A restored subscription is Not Registered; the user
// registers again.
type hssSubscriptions struct{ s *Server }

func (h hssSubscriptions) Name() string { return "hss.subscriptions" }

func (h hssSubscriptions) Export(ctx context.Context, emit func(json.RawMessage) error) error {
	after := ""
	for {
		pageCtx, cancel := context.WithTimeout(ctx, h.s.nodeTimeout)
		n, last, err := h.s.hss.export(pageCtx, after, h.s.exportPageSize, emit)
		cancel()
		if err != nil {
			var refused *hssError
			if errors.As(err, &refused) {
				return fmt.Errorf("%w: %v", ErrUpstream, err)
			}
			return err
		}
		if n < h.s.exportPageSize {
			return nil
		}
		after = last
	}
}

func (h hssSubscriptions) Restore(ctx context.Context, record json.RawMessage, dryRun bool) Outcome {
	return h.s.provisionRaw(ctx, record, dryRun, Outcome{})
}

// defaultExportPageSize is how many subscriptions the OAM asks the HSS
// for at once.
const defaultExportPageSize = 500

// JobState is the state of a backup, of one of its steps, or of a
// restore.
type JobState string

const (
	JobRunning   JobState = "running"
	JobCompleted JobState = "completed"
	JobFailed    JobState = "failed"
)

// Step is one step of a backup or a restore, as it goes: export (one per
// section) and store for a backup; verify, then diff on a dry run or
// apply, for a restore.
type Step struct {
	Name     string     `json:"name"`
	Section  string     `json:"section,omitempty"`
	State    JobState   `json:"state"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	// Records counts the records that the step has handled so far.
	Records int    `json:"records,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Manifest is what the OAM records about one backup, next to its
// archive: who made it and when, how each step went, and each restore
// from it.
type Manifest struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	Operator string    `json:"operator,omitempty"`
	Comment  string    `json:"comment,omitempty"`
	// State is completed only when the archive is whole and stored.
	State    JobState `json:"state"`
	Sections []string `json:"sections"`
	Steps    []Step   `json:"steps"`
	// Records counts the records of each section in the archive.
	Records  map[string]int `json:"records,omitempty"`
	Bytes    int64          `json:"bytes,omitempty"`
	Restores []RestoreRun   `json:"restores,omitempty"`
}

// RestoreRun is one restore from a backup.
type RestoreRun struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	DryRun   bool      `json:"dry_run,omitempty"`
	Operator string    `json:"operator,omitempty"`
	State    JobState  `json:"state"`
	Steps    []Step    `json:"steps"`
	Summary  Summary   `json:"summary"`
}

// BackupRequest is the body of POST /oam/v1/backups; it may be empty.
type BackupRequest struct {
	Operator string `json:"operator,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// RestoreRequest is the body of POST /oam/v1/backups/{id}/restore; it
// may be empty.
type RestoreRequest struct {
	Operator string `json:"operator,omitempty"`
}

// The archive is a file of JSON objects, one per line: the header, one
// line for each record of each section in turn, and the end. A restore
// reads it twice, to check it whole and then to put the records back,
// and never holds more than a line.
//
//	{"format": "ims-practice-oam-backup", "version": 1, "id": "...", "created": "...", "sections": ["hss.subscriptions"]}
//	{"section": "hss.subscriptions", "record": {...}}
//	{"end": {"records": {"hss.subscriptions": 2}}}
const (
	archiveFormat  = "ims-practice-oam-backup"
	archiveVersion = 1
	// archiveLine bounds a line of the archive: a record of
	// maxRecordLine and its envelope.
	archiveLine = maxRecordLine + 4<<10
)

// archiveEntry is any line of the archive; which fields are set tells
// which line it is.
type archiveEntry struct {
	Format   string          `json:"format,omitempty"`
	Version  int             `json:"version,omitempty"`
	ID       string          `json:"id,omitempty"`
	Created  *time.Time      `json:"created,omitempty"`
	Sections []string        `json:"sections,omitempty"`
	Section  string          `json:"section,omitempty"`
	Record   json.RawMessage `json:"record,omitempty"`
	End      *archiveEnd     `json:"end,omitempty"`
}

type archiveEnd struct {
	Records map[string]int `json:"records"`
}

// exportProgress is how often, in records, an export reports how far it
// has come.
const exportProgress = 1000

// countingWriter counts what goes through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Backup exports every section into a new archive of the backup store
// and records each step in the manifest, which it also emits as each
// step goes. The archive is kept only when every section exported:
// otherwise the manifest is failed and there is no archive.
func (s *Server) Backup(ctx context.Context, req BackupRequest, emit func(StreamLine)) (Manifest, error) {
	if err := s.canBackUp(); err != nil {
		return Manifest{}, err
	}
	m := Manifest{ID: s.backupID(), Created: s.now().UTC(), Operator: req.Operator, Comment: req.Comment,
		State: JobRunning, Records: map[string]int{}}
	for _, sec := range s.sections {
		m.Sections = append(m.Sections, sec.Name())
	}
	s.log.Info("backup started", "backup", m.ID, "operator", req.Operator)
	archive, err := s.backups.Create(m.ID)
	if err != nil {
		return Manifest{}, fmt.Errorf("backup store: %w", err)
	}
	if err := s.backups.SaveManifest(m); err != nil {
		_ = archive.Abort()
		return Manifest{}, fmt.Errorf("backup store: %w", err)
	}
	w := &countingWriter{w: archive}
	enc := json.NewEncoder(w)
	err = enc.Encode(archiveEntry{Format: archiveFormat, Version: archiveVersion, ID: m.ID, Created: &m.Created, Sections: m.Sections})
	for _, sec := range s.sections {
		if err != nil {
			break
		}
		step := s.startStep(&m.Steps, "export", sec.Name(), emit)
		err = sec.Export(ctx, func(record json.RawMessage) error {
			if len(record) > maxRecordLine {
				return fmt.Errorf("a record of %d bytes is longer than %d", len(record), maxRecordLine)
			}
			if err := enc.Encode(archiveEntry{Section: sec.Name(), Record: record}); err != nil {
				return err
			}
			m.Steps[step].Records++
			if m.Steps[step].Records%exportProgress == 0 {
				emit(StreamLine{Step: new(m.Steps[step])})
			}
			return nil
		})
		m.Records[sec.Name()] = m.Steps[step].Records
		s.endStep(&m.Steps[step], err, emit)
	}
	if err == nil {
		step := s.startStep(&m.Steps, "store", "", emit)
		err = enc.Encode(archiveEntry{End: &archiveEnd{Records: m.Records}})
		if err == nil {
			err = archive.Commit()
		}
		m.Bytes = w.n
		s.endStep(&m.Steps[step], err, emit)
	}
	if err != nil {
		_ = archive.Abort()
	}
	m.State = JobCompleted
	if err != nil {
		m.State = JobFailed
	}
	if saveErr := s.backups.SaveManifest(m); saveErr != nil && err == nil {
		err = fmt.Errorf("backup store: %w", saveErr)
	}
	s.log.Info("backup done", "backup", m.ID, "state", string(m.State), "records", m.Records)
	return m, err
}

// startStep adds a running step to steps and emits it. It returns the
// index of the step, as steps may move when it grows.
func (s *Server) startStep(steps *[]Step, name, section string, emit func(StreamLine)) int {
	*steps = append(*steps, Step{Name: name, Section: section, State: JobRunning, Started: s.now().UTC()})
	i := len(*steps) - 1
	emit(StreamLine{Step: new((*steps)[i])})
	return i
}

func (s *Server) endStep(step *Step, err error, emit func(StreamLine)) {
	finished := s.now().UTC()
	step.Finished = &finished
	step.State = JobCompleted
	if err != nil {
		step.State, step.Error = JobFailed, err.Error()
	}
	emit(StreamLine{Step: new(*step)})
}

func (s *Server) canBackUp() error {
	switch {
	case s.backups == nil:
		return fmt.Errorf("%w: no backup store", ErrUnavailable)
	case len(s.sections) == 0:
		return fmt.Errorf("%w: no data to back up: no HSS provisioning API", ErrUnavailable)
	}
	return nil
}

// backupID names a backup by its time, so that the names sort as the
// backups were made, and a random part, so that two OAM instances that
// back up in the same second do not meet.
func (s *Server) backupID() string {
	return s.now().UTC().Format("20060102T150405Z") + "-" + randomHex(3)
}

// restorable is the manifest of a backup that a restore may read.
func (s *Server) restorable(id string) (Manifest, error) {
	if s.backups == nil {
		return Manifest{}, fmt.Errorf("%w: no backup store", ErrUnavailable)
	}
	m, err := s.backups.Manifest(id)
	if err != nil {
		return Manifest{}, err
	}
	if m.State != JobCompleted {
		return Manifest{}, fmt.Errorf("%w: backup %s is %s, not completed", ErrConflict, id, m.State)
	}
	return m, nil
}

// Restore puts the records of a backup back: it checks the archive
// whole first (verify), then, on a dry run, asks each section what each
// record would do (diff), or puts each back (apply). It emits each step
// and each outcome as it goes, and records the run in the manifest.
// Sections that the archive has and the OAM does not are refused before
// any record is restored.
func (s *Server) Restore(ctx context.Context, m Manifest, req RestoreRequest, dryRun bool, emit func(StreamLine)) (RestoreRun, error) {
	run := RestoreRun{Started: s.now().UTC(), DryRun: dryRun, Operator: req.Operator, State: JobRunning}
	run.Summary.DryRun = dryRun
	s.log.Info("restore started", "backup", m.ID, "dry_run", dryRun, "operator", req.Operator)

	step := s.startStep(&run.Steps, "verify", "", emit)
	records, err := s.verifyArchive(m)
	run.Steps[step].Records = records
	s.endStep(&run.Steps[step], err, emit)
	if err == nil {
		name := "apply"
		if dryRun {
			name = "diff"
		}
		step = s.startStep(&run.Steps, name, "", emit)
		err = s.restoreRecords(ctx, m.ID, dryRun, func(o Outcome) {
			run.Summary.add(o)
			run.Steps[step].Records++
			emit(StreamLine{Outcome: &o})
		})
		run.Summary.Lines = run.Steps[step].Records
		s.endStep(&run.Steps[step], err, emit)
	}
	run.Finished = s.now().UTC()
	run.State = JobCompleted
	if err != nil {
		run.State = JobFailed
	}
	// The manifest is read again, so that a restore that another
	// instance recorded meanwhile is kept unless both write at once.
	if latest, readErr := s.backups.Manifest(m.ID); readErr == nil {
		m = latest
	}
	m.Restores = append(m.Restores, run)
	if saveErr := s.backups.SaveManifest(m); saveErr != nil && err == nil {
		err = fmt.Errorf("backup store: %w", saveErr)
	}
	s.log.Info("restore done", "backup", m.ID, "dry_run", dryRun, "state", string(run.State), "summary", run.Summary)
	return run, err
}

// verifyArchive reads the archive of m whole: the header, sections that
// the OAM restores, and the end with counts that match the records.
// It returns how many records the archive has.
func (s *Server) verifyArchive(m Manifest) (int, error) {
	rc, err := s.backups.Open(m.ID)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	r := bufio.NewReaderSize(rc, archiveLine)
	counts := map[string]int{}
	total := 0
	for line := 1; ; line++ {
		raw, err := readLine(r)
		if errors.Is(err, io.EOF) {
			return total, fmt.Errorf("the archive ends at line %d without its end line: it is cut short", line-1)
		}
		if err != nil {
			return total, fmt.Errorf("line %d: %v", line, err)
		}
		var e archiveEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return total, fmt.Errorf("line %d: not a line of an archive", line)
		}
		switch {
		case line == 1:
			if e.Format != archiveFormat || e.Version != archiveVersion || e.ID != m.ID {
				return 0, fmt.Errorf("line 1: not the header of version %d of backup %s", archiveVersion, m.ID)
			}
		case e.End != nil:
			if !mapsEqual(e.End.Records, counts) || !mapsEqual(m.Records, counts) {
				return total, fmt.Errorf("the archive has %v records, its end line says %v and the manifest %v", counts, e.End.Records, m.Records)
			}
			if _, err := readLine(r); !errors.Is(err, io.EOF) {
				return total, fmt.Errorf("line %d: something after the end line", line+1)
			}
			return total, nil
		case e.Section != "" && e.Record != nil:
			if s.section(e.Section) == nil {
				return total, fmt.Errorf("line %d: section %s is not one that this OAM restores", line, e.Section)
			}
			counts[e.Section]++
			total++
		default:
			return total, fmt.Errorf("line %d: neither a record nor the end", line)
		}
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func (s *Server) section(name string) Section {
	for _, sec := range s.sections {
		if sec.Name() == name {
			return sec
		}
	}
	return nil
}

// restoreRecords reads the archive again and restores each record, at
// most provisioningConcurrency at once, emitting the outcomes in the
// order of the archive. An outcome's line is its line in the archive.
func (s *Server) restoreRecords(ctx context.Context, id string, dryRun bool, emit func(Outcome)) error {
	rc, err := s.backups.Open(id)
	if err != nil {
		return err
	}
	defer rc.Close()
	r := bufio.NewReaderSize(rc, archiveLine)
	line := 0
	next := func() (item, error) {
		for {
			raw, err := readLine(r)
			if err != nil {
				return item{}, err
			}
			line++
			var e archiveEntry
			if err := json.Unmarshal(raw, &e); err != nil {
				return item{}, fmt.Errorf("line %d: not a line of an archive", line)
			}
			if e.End != nil {
				return item{}, io.EOF
			}
			if e.Section != "" {
				return item{line: line, section: e.Section, raw: e.Record}, nil
			}
		}
	}
	return runOrdered(ctx, s.provisioningConcurrency, next,
		func(ctx context.Context, it item) Outcome {
			o := s.section(it.section).Restore(ctx, it.raw, dryRun)
			o.Line = it.line
			return o
		},
		func(o Outcome) error {
			emit(o)
			return nil
		})
}

func (s *Server) backupRoutes(rt *routes) {
	rt.handle(http.MethodPost, PathPrefix+"backups", func(w http.ResponseWriter, r *http.Request) {
		var req BackupRequest
		err := readJSON(w, r, &req, true)
		if err == nil {
			err = s.canBackUp()
		}
		if err != nil {
			writeError(w, err)
			return
		}
		out := startStream(w)
		m, err := s.Backup(r.Context(), req, func(l StreamLine) { _ = out.send(l) })
		if err != nil {
			_ = out.send(StreamLine{Error: err.Error()})
		}
		if m.ID != "" {
			_ = out.send(StreamLine{Manifest: &m})
		}
	})
	rt.handle(http.MethodGet, PathPrefix+"backups", func(w http.ResponseWriter, r *http.Request) {
		if s.backups == nil {
			writeError(w, fmt.Errorf("%w: no backup store", ErrUnavailable))
			return
		}
		ms, err := s.backups.Manifests()
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, Backups{Backups: ms})
	})
	rt.handle(http.MethodGet, PathPrefix+"backups/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s.backups == nil {
			writeError(w, fmt.Errorf("%w: no backup store", ErrUnavailable))
			return
		}
		m, err := s.backups.Manifest(r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})
	rt.handle(http.MethodPost, PathPrefix+"backups/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		dry, err := dryRun(r)
		var req RestoreRequest
		if err == nil {
			err = readJSON(w, r, &req, true)
		}
		var m Manifest
		if err == nil {
			m, err = s.restorable(r.PathValue("id"))
		}
		if err != nil {
			writeError(w, err)
			return
		}
		out := startStream(w)
		run, err := s.Restore(r.Context(), m, req, dry, func(l StreamLine) { _ = out.send(l) })
		if err != nil {
			_ = out.send(StreamLine{Error: err.Error()})
		}
		_ = out.send(StreamLine{Summary: &run.Summary})
	})
}

// Backups is the answer to GET /oam/v1/backups, the newest first.
type Backups struct {
	Backups []Manifest `json:"backups"`
}

// sortManifests puts the newest backup first; the IDs start with the
// time.
func sortManifests(ms []Manifest) {
	slices.SortFunc(ms, func(a, b Manifest) int { return cmp.Compare(b.ID, a.ID) })
}
