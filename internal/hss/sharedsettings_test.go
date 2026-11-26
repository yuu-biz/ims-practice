package hss

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// The tests of this file are the settings that an operator changes
// while the HSS runs, as the instances of the HSS on one store share
// them: a change that reaches one instance is in force on all of them,
// and an instance that starts in place of another answers with the
// settings in force from its first request, not with those of its
// configuration file.

// changeAt sends a change of one setting with the generation that the
// OAM server gives it, as it sends the same change to every instance of
// the role.
func changeAt(api *maintenance.Node, generation uint64, name, value string) (maintenance.ConfigurationChange, error) {
	return api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{
		Settings:   map[string]json.RawMessage{name: json.RawMessage(`"` + value + `"`)},
		Generation: generation,
		Operator:   "tester",
	})
}

// generationOf is the configuration generation that the maintenance API
// of an instance shows.
func generationOf(t *testing.T, api *maintenance.Node) uint64 {
	t.Helper()
	return api.State().ConfigurationGeneration
}

// TestARecreatedInstanceAnswersWithTheSettingsInForce: an instance that
// starts on the store of the role, from a configuration file whose
// policy is all, refuses the ordinary subscriber from its first UAR
// when the policy in force is test_users, and shows the generation of
// the change that set it. No operator sends it the settings again.
func TestARecreatedInstanceAnswersWithTheSettingsInForce(t *testing.T) {
	store := settingsStore(t)
	_, api := settingsInstance(t, store, nil)
	change(t, api, "hss.admission", string(AdmitTestUsers))
	change(t, api, "hss.test_user_service_profile", "trial")

	recreated, recreatedAPI := settingsInstance(t, store, nil)
	if registers(t, recreated, impi, impu) {
		t.Error("the recreated instance admits the ordinary subscriber on the policy of its file")
	}
	if !registers(t, recreated, testIMPI, testIMPU) {
		t.Error("the recreated instance refuses the test user")
	}
	if got := settingValue(t, recreatedAPI, "hss.test_user_service_profile"); got != "trial" {
		t.Errorf("the recreated instance names %q for the test users, want trial", got)
	}
	if got := generationOf(t, recreatedAPI); got != 2 {
		t.Errorf("the recreated instance is at generation %d, want 2, that of the last change", got)
	}
}

// TestAnInstanceTakesOnAChangeMadeThroughAnother: a change that reaches
// one instance alone, as when the OAM server reaches the role through
// one name, is in force on the others within their next reading of the
// store.
func TestAnInstanceTakesOnAChangeMadeThroughAnother(t *testing.T) {
	store := settingsStore(t)
	_, api := settingsInstance(t, store, nil)
	other, otherAPI := settingsInstance(t, store, nil)
	change(t, api, "hss.admission", string(AdmitTestUsers))

	for deadline := time.Now().Add(5 * time.Second); other.Admission() != AdmitTestUsers; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting 5 s for the other instance to take on the policy; it has %q", other.Admission())
		}
	}
	if registers(t, other, impi, impu) {
		t.Error("the other instance admits the ordinary subscriber")
	}
	if got := generationOf(t, otherAPI); got != 1 {
		t.Errorf("the other instance is at generation %d, want 1", got)
	}
}

// TestAChangeOnAnInstanceBehindTheStoreChangesWhatItNames: an instance
// that has not yet taken on the change of the admission policy that
// another instance made takes a change of the default profile alone.
// The policy stays as the store has it: a change sets the settings it
// names on those in force on the role, not on the copy of the instance
// that took it.
func TestAChangeOnAnInstanceBehindTheStoreChangesWhatItNames(t *testing.T) {
	store := settingsStore(t)
	_, first := settingsInstance(t, store, nil)
	behind, behindAPI := settingsInstanceReading(t, store, nil, time.Hour)
	if _, err := changeAt(first, 1, "hss.admission", string(AdmitTestUsers)); err != nil {
		t.Fatal(err)
	}
	if behind.Admission() != AdmitAll {
		t.Fatalf("the instance behind has taken on the policy already; the test needs it behind")
	}
	if _, err := changeAt(behindAPI, 2, "hss.default_service_profile", "trial"); err != nil {
		t.Fatal(err)
	}
	stored, _, err := store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := (StoredSettings{Generation: 2, Admission: AdmitTestUsers, DefaultServiceProfile: "trial",
		TestUserServiceProfile: "default", DefaultServiceProfileGeneration: 2}); stored != want {
		t.Errorf("the store has %+v, want %+v", stored, want)
	}
	if behind.Admission() != AdmitTestUsers || behind.DefaultServiceProfile() != "trial" {
		t.Errorf("the instance behind has %q and %q, want test_users and trial", behind.Admission(), behind.DefaultServiceProfile())
	}
	if registers(t, behind, impi, impu) {
		t.Error("the instance behind admits the ordinary subscriber again")
	}
}

// TestAnInstanceKeepsSettingsItCannotServe: the store names a service
// profile that this instance does not define, beside a policy that it
// can serve, as an instance with another configuration file put there.
// An instance that took that name on would give the subscriptions of the
// name a profile without criteria, so it keeps its last good name and
// takes the policy on, whether it reads the store as it starts, at a
// sweep, or as it takes a change of its own; and it sends no PPR of the
// name.
func TestAnInstanceKeepsSettingsItCannotServe(t *testing.T) {
	store := settingsStore(t)
	running, _ := settingsInstance(t, store, nil)
	serve(t, running, impi, impu, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	running.SetPeers(scscfs)
	stored, _, err := store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The change of an instance whose file defines video: the policy, and
	// a profile that the instances here do not define.
	unknown := stored
	unknown.Generation, unknown.Admission, unknown.DefaultServiceProfile = 1, AdmitTestUsers, "video"
	unknown.DefaultServiceProfileGeneration = 1
	if swapped, err := store.CompareAndSwapSettings(t.Context(), &stored, unknown); err != nil || !swapped {
		t.Fatalf("CompareAndSwapSettings: %v, %v", swapped, err)
	}

	started, startedAPI := settingsInstance(t, store, nil)
	if got := started.DefaultServiceProfile(); got != "default" {
		t.Errorf("the instance that started on the store names %q, want default, the last it could serve", got)
	}
	if got := started.Admission(); got != AdmitTestUsers {
		t.Errorf("the instance that started on the store admits %q, want test_users, the policy of the store", got)
	}
	if got := generationOf(t, startedAPI); got != 1 {
		t.Errorf("the instance that started on the store is at generation %d, want 1", got)
	}
	for deadline := time.Now().Add(5 * time.Second); running.Admission() != AdmitTestUsers; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting 5 s for the running instance to take on the policy; it has %q", running.Admission())
		}
	}
	if got := running.DefaultServiceProfile(); got != "default" {
		t.Errorf("the running instance took on %q", got)
	}

	// A change through an instance here of what it can serve keeps the
	// name that the store has, and does not put it in force here.
	if _, err := changeAt(startedAPI, 2, "hss.admission", string(AdmitAll)); err != nil {
		t.Fatal(err)
	}
	if started.Admission() != AdmitAll || started.DefaultServiceProfile() != "default" {
		t.Errorf("after the change the instance has %q and %q, want all and default", started.Admission(), started.DefaultServiceProfile())
	}
	if now, _, err := store.Settings(t.Context()); err != nil || now.DefaultServiceProfile != "video" || now.Admission != AdmitAll {
		t.Errorf("the store has %+v after the change, %v; want video kept and the policy all", now, err)
	}
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	if len(scscfs.pprs) != 0 {
		t.Errorf("%d PPRs went out for a profile that no instance here defines", len(scscfs.pprs))
	}
}

// lockedBuffer is a buffer that the goroutines of an HSS may log to at
// once.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// line is the first line logged with msg.
func (b *lockedBuffer) line(msg string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for line := range strings.Lines(b.buf.String()) {
		if strings.Contains(line, `msg="`+msg+`"`) {
			return line
		}
	}
	return ""
}

// TestTheLogOfATakeOnHasWhatWasTakenOn: an instance that starts on a
// store that names a service profile it does not define, beside a policy
// that it can serve, logs that it took on the policy and kept its own
// profile: the settings now in force on it, not those of the store.
func TestTheLogOfATakeOnHasWhatWasTakenOn(t *testing.T) {
	store := settingsStore(t)
	if swapped, err := store.CompareAndSwapSettings(t.Context(), nil, StoredSettings{Generation: 1, Admission: AdmitTestUsers,
		DefaultServiceProfile: "video", TestUserServiceProfile: "default", DefaultServiceProfileGeneration: 1}); err != nil || !swapped {
		t.Fatalf("CompareAndSwapSettings: %v, %v", swapped, err)
	}
	logged := &lockedBuffer{}
	newSettingsInstanceLogging(t, store, nil, nil, slog.New(slog.NewTextHandler(logged, nil)))
	line := logged.line("took on the settings in force on the instances of the role")
	for _, want := range []string{"generation=1", "admission=test_users", "default_service_profile=default", "test_user_service_profile=default"} {
		if !strings.Contains(line, want) {
			t.Errorf("the log of the take-on is %q, want %s in it", line, want)
		}
	}
	if line := logged.line("the settings in force on the instances of the role name what this instance does not have; it keeps its own for those"); !strings.Contains(line, "video") {
		t.Errorf("the log of what the instance kept is %q, want the name video in it", line)
	}
}

// TestTheChangeOfARoleReachesEveryInstanceOnce: the OAM server sends
// one change, with one generation, to every instance of the role. The
// instance it reaches second finds the change in force in the store,
// takes it on and answers it as done, with a record of no change: the
// change of the store was the first instance's, and the history has it
// there. A change of another OAM server with the same generation and
// another value is one that the first overtook, and changes nothing.
func TestTheChangeOfARoleReachesEveryInstanceOnce(t *testing.T) {
	store := settingsStore(t)
	_, first := settingsInstance(t, store, nil)
	// An instance that reads the store only as it starts and as it takes
	// a change, so that the change of the role reaches it before it has
	// taken that change on from the store.
	other, second := settingsInstanceReading(t, store, nil, time.Hour)
	if _, err := changeAt(first, 1, "hss.admission", string(AdmitTestUsers)); err != nil {
		t.Fatalf("the first instance: %v", err)
	}

	_, err := changeAt(second, 1, "hss.admission", string(AdmitAll))
	if !errors.Is(err, maintenance.ErrStaleGeneration) {
		t.Errorf("another value at the generation of the store: %v, want it overtaken", err)
	}
	if got := settingValue(t, first, "hss.admission"); got != string(AdmitTestUsers) {
		t.Errorf("the first instance has %q after the overtaken change", got)
	}

	record, err := changeAt(second, 1, "hss.admission", string(AdmitTestUsers))
	if err != nil {
		t.Fatalf("the second instance: %v", err)
	}
	if len(record.Changes) != 0 {
		t.Errorf("the second instance recorded %+v, want no change of its own", record.Changes)
	}
	if other.Admission() != AdmitTestUsers || generationOf(t, second) != 1 {
		t.Errorf("the second instance has %q at generation %d", other.Admission(), generationOf(t, second))
	}
}

// TestTheRecordOfAChangeHasWhatTheStoreHad: an instance that has not
// yet taken on the change of the admission policy that another instance
// made records, for a change of its own, the policy that its change
// replaced in the store, not the one of its copy. A rollback of the
// record puts that one back.
func TestTheRecordOfAChangeHasWhatTheStoreHad(t *testing.T) {
	store := settingsStore(t)
	_, first := settingsInstance(t, store, nil)
	behind, behindAPI := settingsInstanceReading(t, store, nil, time.Hour)
	if _, err := changeAt(first, 1, "hss.admission", string(AdmitTestUsers)); err != nil {
		t.Fatal(err)
	}
	if behind.Admission() != AdmitAll {
		t.Fatal("the instance behind has taken on the policy already; the test needs it behind")
	}
	record, err := changeAt(behindAPI, 2, "hss.admission", string(AdmitAll))
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Changes) != 1 || string(record.Changes[0].Previous) != `"test_users"` || string(record.Changes[0].Value) != `"all"` {
		t.Errorf("the record is %+v, want hss.admission from test_users to all", record.Changes)
	}
}
