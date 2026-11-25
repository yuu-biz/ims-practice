package hss

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	state, err := api.State()
	if err != nil {
		t.Fatal(err)
	}
	return state.ConfigurationGeneration
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

// TestTheChangeOfARoleReachesEveryInstanceOnce: the OAM server sends
// one change, with one generation, to every instance of the role. The
// instance it reaches second finds the change in force in the store,
// takes it on and answers it as done. A change of another OAM server
// with the same generation and another value is one that the first
// overtook, and changes nothing.
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
	if len(record.Changes) != 1 || string(record.Changes[0].Value) != `"test_users"` {
		t.Errorf("the second instance recorded %+v, want hss.admission from all to test_users", record.Changes)
	}
	if other.Admission() != AdmitTestUsers || generationOf(t, second) != 1 {
		t.Errorf("the second instance has %q at generation %d", other.Admission(), generationOf(t, second))
	}
}
