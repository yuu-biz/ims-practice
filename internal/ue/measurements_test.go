package ue_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// TS 32.409 4.1.1 has the P-CSCF and the S-CSCF count the same
// registration procedures. In one registry, which the roles of one node
// share, each counts every procedure once under its own role: the
// initial registration, whose 401s are steps and not ends, the
// re-registration and the deregistration. The UE with IMS AKA has a
// USIM ahead of the HSS, so its initial registration takes a second
// challenge after the re-synchronisation (TS 33.203 6.1.3), and is one
// procedure all the same.
func TestThePCSCFAndTheSCSCFCountTheSameRegistrations(t *testing.T) {
	reg := metrics.NewRegistry()
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, metrics: reg,
		subscriptions: []hss.Subscription{
			{PrivateIdentity: impiAKA, PublicIdentities: []string{impuAKA, sipAKA}, BarredIdentities: []string{impuAKA},
				AKA: &hss.AKACredentials{Secret: testSet1}},
			{PrivateIdentity: impi2, PublicIdentities: []string{impu2}, Password: password2},
		},
	})
	ctx := t.Context()
	for _, p := range []*party{
		newIMSParty(t, n.pcscf, impiAKA, impuAKA, "", &ue.AKA{Secret: testSet1, SQN: aka.NewSQN(5000, 1)}),
		newIMSParty(t, n.pcscf, impi2, impu2, password2, nil),
	} {
		for range 2 {
			if _, err := p.ue.Register(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.ue.Deregister(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var want []string
	for _, role := range []string{"pcscf", "scscf"} {
		for _, kind := range []string{"initial", "re", "de"} {
			want = append(want,
				fmt.Sprintf(`ims_registration_attempts_total{role=%q,type=%q} 2`, role, kind),
				fmt.Sprintf(`ims_registration_successes_total{role=%q,type=%q} 2`, role, kind))
		}
	}
	want = append(want, `ims_registration_setup_seconds_count{role="pcscf"} 2`)
	// The S-CSCF counts a response just after it has left, so the
	// series may still be settling when the UE has it.
	var text bytes.Buffer
	deadline := time.Now().Add(3 * time.Second)
	for {
		text.Reset()
		if err := reg.WriteText(&text); err != nil {
			t.Fatal(err)
		}
		missing := 0
		for _, line := range want {
			if !strings.Contains(text.String(), line+"\n") {
				missing++
			}
		}
		if missing == 0 && !strings.Contains(text.String(), "ims_registration_failures_total{") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want the lines %q and no failure in:\n%s", want, text.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
