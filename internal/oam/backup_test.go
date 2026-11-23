package oam

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// streamed is what a backup or a restore streamed back.
type streamed struct {
	steps    []Step
	outcomes []Outcome
	summary  *Summary
	manifest *Manifest
	errors   []string
}

func (ts *testServer) streamCall(t *testing.T, path string) (int, streamed) {
	t.Helper()
	resp, err := ts.server.Client().Post(ts.server.URL+path, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got streamed
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var l StreamLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", sc.Bytes(), err)
		}
		switch {
		case l.Step != nil:
			got.steps = append(got.steps, *l.Step)
		case l.Outcome != nil:
			got.outcomes = append(got.outcomes, *l.Outcome)
		case l.Summary != nil:
			got.summary = l.Summary
		case l.Manifest != nil:
			got.manifest = l.Manifest
		case l.Error != "":
			got.errors = append(got.errors, l.Error)
		}
	}
	return resp.StatusCode, got
}

// stepStates is the steps in the form name:state, the section after a
// slash.
func stepStates(steps []Step) string {
	var s []string
	for _, st := range steps {
		name := st.Name
		if st.Section != "" {
			name += "/" + st.Section
		}
		s = append(s, name+":"+string(st.State))
	}
	return strings.Join(s, " ")
}

const mmtelDocument = `<?xml version="1.0" encoding="UTF-8"?><simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"/>`

func provisionForBackup(t *testing.T, ts *testServer) []string {
	t.Helper()
	lines := []string{
		digestLine(1, "test-password-1"),
		digestLine(2, "test-password-2"),
		akaLine(10),
		fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:00101%010d@%s"], "password": "test-password-3", "ut_password": "test-password-3", "mmtel_services": %q, "test_user": true}`,
			impiOf(3), 3, testRealm, mmtelDocument),
		digestLine(4, "test-password-4"),
	}
	_, summary := ts.bulk(t, "", strings.Join(lines, "\n"))
	if summary.Created != len(lines) {
		t.Fatalf("provisioning: %+v", summary)
	}
	return lines
}

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	store, err := NewFileBackupStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := newFakeHSS(t)
	ts := newProvisioningServer(t, before, Config{Backups: store})
	ts.oam.exportPageSize = 2 // three pages for five subscriptions
	provisionForBackup(t, ts)
	// Vectors were used after the provisioning: SQN_HE moved on.
	before.mu.Lock()
	s := before.subs[strings.ToLower(impiOf(10))]
	aka := *s.AKA
	aka.SQN = "000000000020"
	s.AKA = &aka
	before.subs[strings.ToLower(impiOf(10))] = s
	before.mu.Unlock()

	status, backup := ts.streamCall(t, "/oam/v1/backups")
	if status != http.StatusOK || len(backup.errors) > 0 || backup.manifest == nil {
		t.Fatalf("backup: %d %+v", status, backup)
	}
	m := *backup.manifest
	if m.State != JobCompleted || m.Records["hss.subscriptions"] != 5 || m.Bytes == 0 || !strings.HasPrefix(m.ID, "20261120T090000Z-") {
		t.Errorf("manifest: %+v", m)
	}
	if got := stepStates(backup.steps); got != "export/hss.subscriptions:running export/hss.subscriptions:completed store:running store:completed" {
		t.Errorf("steps: %s", got)
	}

	// The archive is readable by its owner only, and holds the
	// credentials, as a restore needs them.
	archive := filepath.Join(dir, m.ID+".jsonl")
	for path, want := range map[string]os.FileMode{dir: 0o700, archive: 0o600, filepath.Join(dir, m.ID+".json"): 0o600} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %v", path, info.Mode().Perm(), err, want)
		}
	}
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 7 || !strings.Contains(lines[0], `"format":"ims-practice-oam-backup"`) || !strings.Contains(lines[6], `"end":{"records":{"hss.subscriptions":5}}`) {
		t.Errorf("archive:\n%s", b)
	}
	if !strings.Contains(string(b), testK) || !strings.Contains(string(b), "test-password-1") {
		t.Error("the archive lacks the credentials")
	}
	if strings.Contains(ts.log.String(), testK) || strings.Contains(ts.log.String(), "test-password-1") {
		t.Errorf("the log shows a credential: %s", ts.log)
	}

	// The HSS lost its data; a new one starts empty.
	after := newFakeHSS(t)
	ts2 := newProvisioningServer(t, after, Config{Backups: store})

	status, diff := ts2.streamCall(t, "/oam/v1/backups/"+m.ID+"/restore?dry_run=true")
	if status != http.StatusOK || len(diff.errors) > 0 || diff.summary == nil || diff.summary.Created != 5 || !diff.summary.DryRun {
		t.Fatalf("dry run: %d %+v", status, diff)
	}
	if got := stepStates(diff.steps); got != "verify:running verify:completed diff:running diff:completed" {
		t.Errorf("dry run steps: %s", got)
	}
	if after.count() != 0 {
		t.Fatal("the dry run restored")
	}
	if got := results(diff.outcomes); got != "2:created 3:created 4:created 5:created 6:created" {
		t.Errorf("dry run outcomes: %s", got)
	}

	_, apply := ts2.streamCall(t, "/oam/v1/backups/"+m.ID+"/restore")
	if apply.summary == nil || apply.summary.Created != 5 || after.count() != 5 {
		t.Fatalf("restore: %+v, %d in the HSS", apply, after.count())
	}
	for k, want := range before.subs {
		got, _ := after.stored(k)
		if string(mustJSON(got)) != string(mustJSON(want)) {
			t.Errorf("%s restored as %s, want %s", k, mustJSON(got), mustJSON(want))
		}
	}
	_, again := ts2.streamCall(t, "/oam/v1/backups/"+m.ID+"/restore")
	if again.summary == nil || again.summary.Unchanged != 5 {
		t.Errorf("restore again: %+v", again)
	}

	status, body := ts2.call(t, "GET", "/oam/v1/backups/"+m.ID, "")
	got := decodeAs[Manifest](t, body)
	if status != http.StatusOK || len(got.Restores) != 3 || !got.Restores[0].DryRun || got.Restores[2].Summary.Unchanged != 5 || got.Restores[1].State != JobCompleted {
		t.Errorf("manifest after the restores: %d %s", status, body)
	}
	_, body = ts2.call(t, "GET", "/oam/v1/backups", "")
	if list := decodeAs[Backups](t, body); len(list.Backups) != 1 || list.Backups[0].ID != m.ID {
		t.Errorf("list: %s", body)
	}
}

// TestAnExportEndsAtTheLinkHeaderAlone: the end of the export is the
// page that names no page after it. A page of exactly the limit always
// names one, so the page after a whole number of full pages holds no
// record and still ends the export; and a page can hold fewer records
// than places, as the HSS leaves out a subscription that went while the
// page was read, without the export ending there.
func TestAnExportEndsAtTheLinkHeaderAlone(t *testing.T) {
	store, err := NewFileBackupStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{Backups: store})
	ts.oam.exportPageSize = 2
	var lines []string
	for n := 1; n <= 4; n++ {
		lines = append(lines, digestLine(n, fmt.Sprintf("test-password-%d", n)))
	}
	if _, summary := ts.bulk(t, "", strings.Join(lines, "\n")); summary.Created != 4 {
		t.Fatalf("provisioning: %+v", summary)
	}
	// The first subscription goes while the first page is read, so that
	// page holds one record of its two places.
	hss.mu.Lock()
	hss.hidden[strings.ToLower(impiOf(1))] = true
	hss.mu.Unlock()

	status, backup := ts.streamCall(t, "/oam/v1/backups")
	if status != http.StatusOK || len(backup.errors) > 0 || backup.manifest == nil {
		t.Fatalf("backup: %d %+v", status, backup)
	}
	// Four places in three pages: two records, one record, and the empty
	// page that ends it.
	if m := *backup.manifest; m.State != JobCompleted || m.Records["hss.subscriptions"] != 3 {
		t.Errorf("manifest: %+v", m)
	}
}

func TestRestoreRefusesAnArchiveThatIsCutShort(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileBackupStore(dir)
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{Backups: store})
	provisionForBackup(t, ts)
	_, backup := ts.streamCall(t, "/oam/v1/backups")
	m := backup.manifest

	archive := filepath.Join(dir, m.ID+".jsonl")
	b, _ := os.ReadFile(archive)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if err := os.WriteFile(archive, []byte(strings.Join(lines[:len(lines)-2], "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := newFakeHSS(t)
	ts2 := newProvisioningServer(t, empty, Config{Backups: store})
	_, restore := ts2.streamCall(t, "/oam/v1/backups/"+m.ID+"/restore")
	if len(restore.errors) != 1 || !strings.Contains(restore.errors[0], "cut short") || len(restore.outcomes) != 0 || empty.count() != 0 {
		t.Errorf("restore of a cut archive: %+v", restore)
	}
	if got := stepStates(restore.steps); got != "verify:running verify:failed" {
		t.Errorf("steps: %s", got)
	}

	// A record of a section that this OAM does not restore.
	extra := strings.Join(lines[:len(lines)-1], "\n") + "\n" + `{"section": "sc.messages", "record": {}}` + "\n" + lines[len(lines)-1] + "\n"
	os.WriteFile(archive, []byte(extra), 0o600)
	_, restore = ts2.streamCall(t, "/oam/v1/backups/"+m.ID+"/restore")
	if len(restore.errors) != 1 || !strings.Contains(restore.errors[0], "sc.messages") || empty.count() != 0 {
		t.Errorf("restore with an unknown section: %+v", restore)
	}
}

func TestABackupThatFailsLeavesNoArchive(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileBackupStore(dir)
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{Backups: store})
	// The HSS is down before the export reads its first page.
	hss.server.Close()

	_, backup := ts.streamCall(t, "/oam/v1/backups")
	if backup.manifest == nil || backup.manifest.State != JobFailed || len(backup.errors) != 1 || !strings.Contains(backup.errors[0], "upstream") {
		t.Fatalf("backup with the HSS down: %+v", backup)
	}
	if got := stepStates(backup.steps); got != "export/hss.subscriptions:running export/hss.subscriptions:failed" {
		t.Errorf("steps: %s", got)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{backup.manifest.ID + ".json"}) {
		t.Errorf("the directory holds %v", names)
	}
	status, body := ts.call(t, "POST", "/oam/v1/backups/"+backup.manifest.ID+"/restore", "")
	if status != http.StatusConflict {
		t.Errorf("restore of a failed backup: %d %s", status, body)
	}
}

func TestBackupErrors(t *testing.T) {
	store, _ := NewFileBackupStore(t.TempDir())
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{Backups: store})
	for _, tt := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/oam/v1/backups/20261120T090000Z-000000", "", http.StatusNotFound},
		{"GET", "/oam/v1/backups/..%2Fetc", "", http.StatusNotFound},
		{"POST", "/oam/v1/backups/20261120T090000Z-000000/restore", "", http.StatusNotFound},
		{"POST", "/oam/v1/backups", `{"operator": "x", "when": "now"}`, http.StatusBadRequest},
		{"POST", "/oam/v1/backups/x/restore?dry_run=1x", "", http.StatusBadRequest},
	} {
		if status, body := ts.call(t, tt.method, tt.path, tt.body); status != tt.want {
			t.Errorf("%s %s: %d %s, want %d", tt.method, tt.path, status, body, tt.want)
		}
	}
	none := newTestServer(t, Config{}, newFakeNode(t, "hss-1", []string{"hss"}))
	if status, body := none.call(t, "POST", "/oam/v1/backups", ""); status != http.StatusServiceUnavailable {
		t.Errorf("backup without a store: %d %s", status, body)
	}
}
