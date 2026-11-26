package hss

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// This file is the settings that an operator changes while the HSS runs
// (HSS.Settings): the admission policy and the two service profile
// names. Each test drives the HSS through the maintenance API of its
// node, as the OAM server does, and then through Cx, where the change
// shows.

// The application servers of the two service profiles, which tell apart
// the profile that a SAA carried.
const (
	settingsDefaultAS = "sip:mmtel." + realm + ":5060"
	settingsTrialAS   = "sip:trial." + realm + ":5060"
)

// settingsHSS is an HSS behind the maintenance API of its node, wired as
// cmd/ims wires the two. It holds the ordinary subscriber impi and the
// test user testIMPI, and two service profiles whose criteria name
// different application servers, so a SAA says which profile a
// subscription got.
func settingsHSS(t *testing.T) (*HSS, *maintenance.Node) {
	t.Helper()
	return settingsInstance(t, settingsStore(t), nil)
}

// settingsStore is the store of settingsHSS: the ordinary subscriber
// and the test user.
func settingsStore(t *testing.T) *MemoryStore {
	t.Helper()
	store, err := NewMemoryStore(
		Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"},
		Subscription{PrivateIdentity: testIMPI, PublicIdentities: []string{testIMPU}, Password: "test-password-9", TestUser: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// settingsInstance is one instance of the HSS of settingsHSS on store,
// which other instances may share, with the lock per subscriber of
// locks; nil gives it a lock of its own process. It reads the settings
// that the instances share every few milliseconds, so that a test sees
// in moments what the network sees within a second, and stops with the
// test.
func settingsInstance(t *testing.T, store Store, locks ims.UserLocker) (*HSS, *maintenance.Node) {
	t.Helper()
	return newSettingsInstance(t, store, locks, nil)
}

// settingsInstanceReading is settingsInstance reading the store every
// interval.
func settingsInstanceReading(t *testing.T, store Store, locks ims.UserLocker, interval time.Duration) (*HSS, *maintenance.Node) {
	t.Helper()
	return newSettingsInstance(t, store, locks, func(c *Config) { c.SweepInterval = interval })
}

// newSettingsInstance is settingsInstance with what configure changes of
// its configuration, which may be nil.
func newSettingsInstance(t *testing.T, store Store, locks ims.UserLocker, configure func(*Config)) (*HSS, *maintenance.Node) {
	t.Helper()
	return newSettingsInstanceLogging(t, store, locks, configure, quiet())
}

// newSettingsInstanceLogging is newSettingsInstance logging to log.
func newSettingsInstanceLogging(t *testing.T, store Store, locks ims.UserLocker, configure func(*Config), log *slog.Logger) (*HSS, *maintenance.Node) {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	cfg := Config{
		UserLocks:          locks,
		SweepInterval:      5 * time.Millisecond,
		Realm:              realm,
		VisitedNetworks:    []string{realm},
		ServerCapabilities: cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}},
		Admission:          AdmitAll,
		ServiceProfiles: map[string]ServiceProfile{
			"default": {Criteria: []ifc.InitialFilterCriteria{profileCriterion(settingsDefaultAS)}, Features: []string{"mmtel"}},
			"trial":   {Criteria: []ifc.InitialFilterCriteria{profileCriterion(settingsTrialAS)}, Features: []string{"mmtel", "trial"}},
		},
		DefaultServiceProfile:  "default",
		TestUserServiceProfile: "default",
	}
	if configure != nil {
		configure(&cfg)
	}
	h, err := New(cfg, store, node, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	node.Handle(h.Handle)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	api, err := maintenance.New(maintenance.Config{
		Node:          role,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{h.Function(server)},
		Configuration: func() any { return struct{}{} },
		Settings:      h.Settings(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Manage(api.ManagedFunction(role))
	return h, api
}

// change sends one configuration change and returns its record.
func change(t *testing.T, api *maintenance.Node, name, value string) maintenance.ConfigurationChange {
	t.Helper()
	record, err := api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{
		Settings: map[string]json.RawMessage{name: json.RawMessage(`"` + value + `"`)},
		Operator: "tester",
	})
	if err != nil {
		t.Fatalf("setting %s to %q: %v", name, value, err)
	}
	return record
}

// settingValue is the value of one changeable setting as the
// configuration answer shows it.
func settingValue(t *testing.T, api *maintenance.Node, name string) string {
	t.Helper()
	conf, err := api.Configuration()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range conf.Changeable {
		if s.Name == name {
			return strings.Trim(string(s.Value), `"`)
		}
	}
	t.Fatalf("the configuration has no changeable setting %s: %+v", name, conf.Changeable)
	return ""
}

// registers sends a UAR for a first registration and reports whether
// the HSS admitted the subscriber.
func registers(t *testing.T, h *HSS, private, public string) bool {
	t.Helper()
	a := uar(t, h, private, public, cx.Registration)
	if a.Result == diameter.Base(diameter.DiameterAuthorizationRejected) {
		return false
	}
	if a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
		t.Fatalf("UAA for %s = %+v, want FIRST_REGISTRATION or AUTHORIZATION_REJECTED", private, a.Result)
	}
	return true
}

// TestMaintenanceAPIChangesTheAdmissionPolicy: an operator narrows the
// network to its test users, and widens it again, without restarting
// the HSS. The policy decides the UAR of TS 29.228 6.1.1.1 step 5 from
// the moment it is in force.
func TestMaintenanceAPIChangesTheAdmissionPolicy(t *testing.T) {
	h, api := settingsHSS(t)
	if !registers(t, h, impi, impu) {
		t.Fatal("the ordinary subscriber was refused on the policy of the configuration file, all")
	}

	record := change(t, api, "hss.admission", string(AdmitTestUsers))
	if len(record.Changes) != 1 || string(record.Changes[0].Previous) != `"all"` || string(record.Changes[0].Value) != `"test_users"` {
		t.Errorf("the change record is %+v, want hss.admission from all to test_users", record.Changes)
	}
	if registers(t, h, impi, impu) {
		t.Error("the ordinary subscriber still registers on the policy test_users")
	}
	if !registers(t, h, testIMPI, testIMPU) {
		t.Error("the test user was refused on the policy test_users")
	}
	if got := settingValue(t, api, "hss.admission"); got != string(AdmitTestUsers) {
		t.Errorf("the configuration answers %q for hss.admission", got)
	}

	// What a rollback from the history of the OAM sends: the previous
	// value that the record of the change holds.
	var previous Admission
	if err := json.Unmarshal(record.Changes[0].Previous, &previous); err != nil {
		t.Fatal(err)
	}
	change(t, api, "hss.admission", string(previous))
	if !registers(t, h, impi, impu) {
		t.Error("the ordinary subscriber is still refused after the policy went back to all")
	}
}

// TestMaintenanceAPIChangesTheServiceProfileOfATestUser: naming another
// profile for the test users gives them the criteria of that profile
// in the next SAA, and leaves every other subscription on the profile
// it had. That is how an operator turns a feature on for the test users
// alone.
func TestMaintenanceAPIChangesTheServiceProfileOfATestUser(t *testing.T) {
	h, api := settingsHSS(t)
	assigned := func(private, public string) string {
		t.Helper()
		saa, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
			UserName: private, PublicIdentities: []string{public}, ServerName: scscf,
			ServerAssignmentType: cx.AssignRegistration}.Message()))
		if err != nil || !saa.Result.Success() {
			t.Fatalf("SAA for %s: %+v, %v", private, saa, err)
		}
		profile, err := cx.ParseUserProfile(saa.UserData)
		if err != nil {
			t.Fatal(err)
		}
		if len(profile.InitialFilterCriteria) != 1 {
			t.Fatalf("the user profile of %s has %d criteria", private, len(profile.InitialFilterCriteria))
		}
		return profile.InitialFilterCriteria[0].ApplicationServer.ServerName
	}
	if got := assigned(testIMPI, testIMPU); got != settingsDefaultAS {
		t.Fatalf("the test user starts on the criterion of %q, want %q", got, settingsDefaultAS)
	}

	change(t, api, "hss.test_user_service_profile", "trial")
	if got := assigned(testIMPI, testIMPU); got != settingsTrialAS {
		t.Errorf("the test user got the criterion of %q, want that of the trial profile, %q", got, settingsTrialAS)
	}
	if got := assigned(impi, impu); got != settingsDefaultAS {
		t.Errorf("the ordinary subscriber got the criterion of %q, want the default profile's %q", got, settingsDefaultAS)
	}

	// Giving every subscription the trial profile reaches the ordinary
	// subscriber too.
	change(t, api, "hss.default_service_profile", "trial")
	if got := assigned(impi, impu); got != settingsTrialAS {
		t.Errorf("after hss.default_service_profile, the ordinary subscriber got %q, want %q", got, settingsTrialAS)
	}
}

// TestMaintenanceAPIRefusesSettingsTheHSSCannotTake: a policy that is
// neither of the two, and a profile that service_profiles does not
// define, are refused, and a request that holds one of them changes
// none of its settings. An operator who mistypes a profile name does
// not leave the subscriptions of that name with no criteria at all.
func TestMaintenanceAPIRefusesSettingsTheHSSCannotTake(t *testing.T) {
	_, api := settingsHSS(t)
	for _, tt := range []struct {
		name     string
		settings map[string]json.RawMessage
		refused  string
	}{
		{"a policy that is neither", map[string]json.RawMessage{"hss.admission": json.RawMessage(`"everyone"`)}, "hss.admission"},
		{"an empty policy", map[string]json.RawMessage{"hss.admission": json.RawMessage(`""`)}, "hss.admission"},
		{"a profile that is not defined", map[string]json.RawMessage{"hss.default_service_profile": json.RawMessage(`"video"`)}, "hss.default_service_profile"},
		{"no profile at all", map[string]json.RawMessage{"hss.test_user_service_profile": json.RawMessage(`""`)}, "hss.test_user_service_profile"},
		{"a good policy beside a bad profile", map[string]json.RawMessage{
			"hss.admission":                 json.RawMessage(`"test_users"`),
			"hss.test_user_service_profile": json.RawMessage(`"video"`),
		}, "hss.test_user_service_profile"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{Settings: tt.settings, Operator: "tester"})
			var refused *maintenance.SettingsRefusedError
			if !errors.As(err, &refused) {
				t.Fatalf("the change gave %v, want the settings refused", err)
			}
			if _, named := refused.Settings[tt.refused]; !named {
				t.Errorf("the refusal names %v, want %s among them", refused.Settings, tt.refused)
			}
			if got := settingValue(t, api, "hss.admission"); got != string(AdmitAll) {
				t.Errorf("hss.admission is now %q, want the policy of the configuration file", got)
			}
			if got := settingValue(t, api, "hss.test_user_service_profile"); got != "default" {
				t.Errorf("hss.test_user_service_profile is now %q, want the profile of the configuration file", got)
			}
		})
	}
}

// TestSettingsChangeWhileRequestsRun: the settings are read on the
// request path, and changed from the maintenance API, so a change while
// Cx requests run must neither be lost nor leave a request with half of
// one. Run with -race.
func TestSettingsChangeWhileRequestsRun(t *testing.T) {
	h, api := settingsHSS(t)
	const rounds = 40
	var wg sync.WaitGroup
	// The requests of both subscribers: a UAR, which reads the admission
	// policy, and a SAR, which reads the profile names as well.
	for _, user := range [][2]string{{impi, impu}, {testIMPI, testIMPU}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				uar(t, h, user[0], user[1], cx.Registration)
				saa, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
					UserName: user[0], PublicIdentities: []string{user[1]}, ServerName: scscf,
					ServerAssignmentType: cx.AssignRegistration}.Message()))
				if err != nil {
					t.Error(err)
					return
				}
				// The policy refuses the UAR of a subscriber it does not
				// admit, and this SAR of a registration goes through whatever
				// the policy, so the answer always carries a user profile.
				if !saa.Result.Success() {
					t.Errorf("SAA for %s = %+v, want DIAMETER_SUCCESS whatever the policy", user[0], saa.Result)
					return
				}
				profile, err := cx.ParseUserProfile(saa.UserData)
				if err != nil {
					t.Error(err)
					return
				}
				// Whichever profile the settings named, the SAA carries that
				// one profile whole and not a mixture of the two.
				if len(profile.InitialFilterCriteria) != 1 {
					t.Errorf("the user profile of %s has %d criteria, want the one of a single profile",
						user[0], len(profile.InitialFilterCriteria))
					return
				}
				if as := profile.InitialFilterCriteria[0].ApplicationServer.ServerName; as != settingsDefaultAS && as != settingsTrialAS {
					t.Errorf("the criterion of %s names %q, neither profile's server", user[0], as)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range rounds {
			policy, profile := AdmitAll, "default"
			if i%2 == 0 {
				policy, profile = AdmitTestUsers, "trial"
			}
			change(t, api, "hss.admission", string(policy))
			change(t, api, "hss.default_service_profile", profile)
			change(t, api, "hss.test_user_service_profile", profile)
		}
	}()
	wg.Wait()

	// The last round set them, so these are the values the requests left
	// alone.
	if h.Admission() != AdmitAll || h.DefaultServiceProfile() != "default" || h.TestUserServiceProfile() != "default" {
		t.Errorf("the settings in force are %q, %q and %q after the last change set all and default",
			h.Admission(), h.DefaultServiceProfile(), h.TestUserServiceProfile())
	}
}

// TestSettingsOfOneChangeGoInForceTogether: the settings that one
// maintenance request names are published at its commit, so a request
// of the HSS never sees the policy of the change beside the profile
// name from before it. From test_users with the trial
// profile to all with the default one, a SAR in between would admit an
// ordinary subscriber and give it the trial criteria.
func TestSettingsOfOneChangeGoInForceTogether(t *testing.T) {
	h, api := settingsHSS(t)
	change(t, api, "hss.admission", string(AdmitTestUsers))
	change(t, api, "hss.default_service_profile", "trial")

	h.beginSettings()
	h.setting(func(s *settings) { s.admission = AdmitAll })
	if got := h.Admission(); got != AdmitTestUsers {
		t.Errorf("before the commit the policy in force is %q, want %q", got, AdmitTestUsers)
	}
	h.setting(func(s *settings) { s.defaultServiceProfile = "default" })
	if got := h.DefaultServiceProfile(); got != "trial" {
		t.Errorf("before the commit the default profile in force is %q, want trial", got)
	}
	if err := h.commitSettings(3); err != nil {
		t.Fatal(err)
	}
	if h.Admission() != AdmitAll || h.DefaultServiceProfile() != "default" {
		t.Errorf("after the commit: %q and %q, want all and default", h.Admission(), h.DefaultServiceProfile())
	}

	record, err := api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{Settings: map[string]json.RawMessage{
		"hss.admission":               json.RawMessage(`"test_users"`),
		"hss.default_service_profile": json.RawMessage(`"trial"`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Changes) != 2 || h.Admission() != AdmitTestUsers || h.DefaultServiceProfile() != "trial" {
		t.Errorf("one change of both: record %+v, in force %q and %q", record.Changes, h.Admission(), h.DefaultServiceProfile())
	}
}
