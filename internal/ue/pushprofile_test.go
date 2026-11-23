package ue_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// smsCriterionTo is moSMSCriterion with the server named, so that the
// service profile a user has is told by the server its short messages
// reach.
func smsCriterionTo(server string) ifc.InitialFilterCriteria {
	c := moSMSCriterion()
	c.ApplicationServer.ServerName = server
	return c
}

// messageServer is an application server that answers every MESSAGE with
// 202, as an IP-SM-GW whose SC takes the message does (TS 24.341
// 5.3.3.4), and hands each one it got to the test.
func messageServer(t *testing.T, n testNetwork, host string) <-chan *sip.Message {
	t.Helper()
	got := make(chan *sip.Message, 4)
	s := stack(t, func(st *sip.ServerTransaction) {
		got <- st.Request
		st.Respond(sip.NewResponse(st.Request, 202, sip.NewTag()))
	})
	n.dns.add(host+":5060", s.LocalAddr())
	return got
}

// TestAProfileChangeReachesTheSCSCFWithoutANewRegistration runs the
// whole IMS in one process and has the operator move a registered user
// to another service profile. The HSS pushes the new profile with a PPR
// (TS 29.228 6.6.1), so the user's next short message goes to the
// application server of the new profile. Without the push the S-CSCF
// would keep the criteria of the registration until the user registered
// again.
func TestAProfileChangeReachesTheSCSCFWithoutANewRegistration(t *testing.T) {
	const first, second = "as1." + domain, "as2." + domain
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour,
		criteria:        []ifc.InitialFilterCriteria{smsCriterionTo("sip:" + first + ":5060")},
		serviceProfiles: map[string]hss.ServiceProfile{"trial": {Criteria: []ifc.InitialFilterCriteria{smsCriterionTo("sip:" + second + ":5060")}}},
	})
	toFirst := messageServer(t, n, first)
	toSecond := messageServer(t, n, second)
	a, _ := newSMSUE(t, n.pcscf, impi, impu, password, ue.DefaultFeatures)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := a.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: "before the change"}); err != nil {
		t.Fatal(err)
	}
	receive(t, toFirst, "short message at the server of the first profile")

	patchSubscription(t, n, impi, `{"service_profile": "trial"}`)

	if _, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: "after the change"}); err != nil {
		t.Fatal(err)
	}
	m := receive(t, toSecond, "short message at the server of the new profile")
	if !strings.Contains(m.Get("Route"), second) {
		t.Errorf("Route = %q, want the server of the new profile", m.Get("Route"))
	}
	select {
	case m := <-toFirst:
		t.Errorf("the server of the old profile got %s %s as well", m.Method, m.RequestURI)
	default:
	}
}

// TestAChangedProfileSettingReachesTheSCSCF runs the whole IMS in one
// process and has the operator name another service profile for the
// test users, through the maintenance API of the HSS node rather than
// for one subscription. The HSS pushes the new profile to the S-CSCF of
// every test user whose profile it holds (TS 29.228 6.6.1), so the
// user's next short messages go to the application server of the new
// profile without the user registering again.
func TestAChangedProfileSettingReachesTheSCSCF(t *testing.T) {
	const first, second = "as1." + domain, "as2." + domain
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour,
		criteria:        []ifc.InitialFilterCriteria{smsCriterionTo("sip:" + first + ":5060")},
		serviceProfiles: map[string]hss.ServiceProfile{"trial": {Criteria: []ifc.InitialFilterCriteria{smsCriterionTo("sip:" + second + ":5060")}}},
		// The test user takes the profile that the setting names; the
		// other subscriber takes the default one, which stays as it is.
		subscriptions: []hss.Subscription{
			{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password, TestUser: true},
			{PrivateIdentity: impi2, PublicIdentities: []string{impu2}, Password: password2},
		},
	})
	toFirst := messageServer(t, n, first)
	toSecond := messageServer(t, n, second)
	a, _ := newSMSUE(t, n.pcscf, impi, impu, password, ue.DefaultFeatures)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := a.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: "before the change"}); err != nil {
		t.Fatal(err)
	}
	receive(t, toFirst, "short message at the server of the first profile")

	changeHSSSetting(t, n, "hss.test_user_service_profile", "trial")

	// The push follows the answer to the maintenance request, so the
	// short messages of the user reach the server of the old profile
	// until the S-CSCF has the new criteria.
	var m *sip.Message
	for deadline := time.Now().Add(10 * time.Second); m == nil; {
		if _, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: "after the change"}); err != nil {
			t.Fatal(err)
		}
		select {
		case m = <-toSecond:
		case <-toFirst:
			if time.Now().After(deadline) {
				t.Fatal("10 s after the setting changed, the short messages still reach the server of the old profile")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !strings.Contains(m.Get("Route"), second) {
		t.Errorf("Route = %q, want the server of the new profile", m.Get("Route"))
	}
}

// changeHSSSetting changes one setting of the HSS through the
// maintenance API of its node, as the OAM server does. The API is built
// here rather than by the test network, since only this test drives it.
func changeHSSSetting(t *testing.T, n testNetwork, name, value string) {
	t.Helper()
	api, err := maintenance.New(maintenance.Config{
		Node:          "hss",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{n.hssRole.Function(n.hss[0].server)},
		Configuration: func() any { return map[string]string{"node": "hss"} },
		Settings:      n.hssRole.Settings(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{
		Settings: map[string]json.RawMessage{name: json.RawMessage(`"` + value + `"`)},
		Operator: "tester",
	}); err != nil {
		t.Fatalf("setting %s to %q: %v", name, value, err)
	}
}

// patchSubscription changes the subscription of privateID through the
// provisioning API of the HSS, as an operator does.
func patchSubscription(t *testing.T, n testNetwork, privateID, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	n.provisioning.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/subscriptions/"+privateID, strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH the subscription: %d %s", w.Code, w.Body)
	}
}
