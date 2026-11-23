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
	settles(t, reg, want, "ims_registration_failures_total{")
}

// TS 32.409 4.1.2 at the P-CSCF: a call between two of its UEs passes it
// twice, as an originating and as a terminating session establishment.
// The rejected call fails on both legs with the status of the callee,
// and the answered one is among the answered sessions from its 200 (OK)
// to its BYE.
func TestThePCSCFCountsTheSessionEstablishments(t *testing.T) {
	reg := metrics.NewRegistry()
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, metrics: reg})
	caller := newParty(t, n.pcscf, impi, impu, password)
	callee := newParty(t, n.pcscf, impi2, impu2, password2)
	ctx := t.Context()

	go func() {
		ic := <-callee.calls
		callee.ue.Answer(ctx, ic, callee.media, ue.Speech{}, ue.AnswerOptions{Reject: 486})
	}()
	if err := caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.Speech{}, ue.CallOptions{}); err == nil {
		t.Fatal("the rejected call went through")
	}
	settles(t, reg, []string{
		`ims_session_attempts_total{role="pcscf",direction="originating"} 1`,
		`ims_session_attempts_total{role="pcscf",direction="terminating"} 1`,
		`ims_session_failures_total{role="pcscf",direction="originating",status="486"} 1`,
		`ims_session_failures_total{role="pcscf",direction="terminating",status="486"} 1`,
	}, "ims_session_answers_total{")

	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		answered <- callee.ue.Answer(ctx, ic, callee.media, ue.Speech{PCM: ue.Tone(880, time.Second)}, ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.Speech{PCM: ue.Tone(440, time.Second)}, ue.CallOptions{})
	}()
	// Both dialogs of the call are at the P-CSCF while the call lasts.
	settles(t, reg, []string{`ims_answered_sessions{role="pcscf"} 2`}, "")
	for _, ended := range []chan error{called, answered} {
		select {
		case err := <-ended:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the call did not end")
		}
	}
	settles(t, reg, []string{
		`ims_session_attempts_total{role="pcscf",direction="originating"} 2`,
		`ims_session_attempts_total{role="pcscf",direction="terminating"} 2`,
		`ims_session_answers_total{role="pcscf",direction="originating"} 1`,
		`ims_session_answers_total{role="pcscf",direction="terminating"} 1`,
		`ims_answered_sessions{role="pcscf"} 0`,
	}, "")
}

// settles waits up to 3 seconds for every line of want to be in the
// series of reg, and for none to start with forbidden, unless that is
// empty.
func settles(t *testing.T, reg *metrics.Registry, want []string, forbidden string) {
	t.Helper()
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
		if missing == 0 && (forbidden == "" || !strings.Contains(text.String(), "\n"+forbidden)) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want the lines %q, and none that starts with %q, in:\n%s", want, forbidden, text.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
