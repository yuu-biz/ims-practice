package ue_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
// The rejected call fails on both legs with the status of the callee and
// succeeds on neither. The answered one succeeds on both, under the 180
// (Ringing) that the callee sends before it picks up and not under the
// 200 (OK) after it (4.1.2.2), and is among the answered sessions from
// that 200 (OK) to its BYE.
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
	}, "ims_session_answers_total{", "ims_session_successes_total{")

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
	settles(t, reg, []string{`ims_answered_sessions{role="pcscf"} 2`})
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
		`ims_session_successes_total{role="pcscf",direction="originating",response="180"} 1`,
		`ims_session_successes_total{role="pcscf",direction="terminating",response="180"} 1`,
		`ims_session_answers_total{role="pcscf",direction="originating"} 1`,
		`ims_session_answers_total{role="pcscf",direction="terminating"} 1`,
		`ims_answered_sessions{role="pcscf"} 0`,
	}, `ims_session_successes_total{role="pcscf",direction="originating",response="200"}`)
}

// settles waits up to 3 seconds for every line of want to be in the
// series of reg, and for no line to start with any of forbidden.
func settles(t *testing.T, reg *metrics.Registry, want []string, forbidden ...string) {
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
		there := 0
		for _, prefix := range forbidden {
			if strings.Contains(text.String(), "\n"+prefix) {
				there++
			}
		}
		if missing == 0 && there == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want the lines %q, and none that starts with one of %q, in:\n%s", want, forbidden, text.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TS 32.409 4.1.2.2 and 4.1.2.12 count a successful session
// establishment on the first response that tells of one, which is the
// 180 (Ringing) of a callee that alerts its user before it answers. One
// call between two users of this network passes the S-CSCF twice, as
// the originating and as the terminating establishment, and the I-CSCF
// once, when it enters the home network. The call that the callee
// refuses outright never rings, so it succeeds nowhere.
func TestTheCSCFsCountTheSuccessfulSessionEstablishments(t *testing.T) {
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
		`ims_session_attempts_total{role="icscf",direction="terminating"} 1`,
		`ims_session_attempts_total{role="scscf",direction="originating"} 1`,
		`ims_session_attempts_total{role="scscf",direction="terminating"} 1`,
	}, "ims_session_successes_total{")

	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		answered <- callee.ue.Answer(ctx, ic, callee.media, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	if err := caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(100*time.Millisecond), ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	settles(t, reg, []string{
		`ims_session_successes_total{role="icscf",direction="terminating",response="180"} 1`,
		`ims_session_successes_total{role="scscf",direction="originating",response="180"} 1`,
		`ims_session_successes_total{role="scscf",direction="terminating",response="180"} 1`,
		`ims_session_successes_total{role="pcscf",direction="originating",response="180"} 1`,
		`ims_session_successes_total{role="pcscf",direction="terminating",response="180"} 1`,
	}, `ims_session_successes_total{role="icscf",direction="terminating",response="200"}`)
}

// TS 32.409 has the I-CSCF count the Cx requests of the registration
// status query (4.1.1.5) and of the user location query (4.1.3), and
// the S-CSCF those of the authentication (4.1.6) and of the
// registration notification (4.1.1.6). A registration and a call bring
// out all four, each under the role that sent the request, and the
// handler that the maintenance listener of a node mounts at /metrics
// serves them in the format that Prometheus scrapes.
func TestTheCSCFsCountTheirCxRequestsAndAnswers(t *testing.T) {
	reg := metrics.NewRegistry()
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, metrics: reg})
	caller := newParty(t, n.pcscf, impi, impu, password)
	callee := newParty(t, n.pcscf, impi2, impu2, password2)
	ctx := t.Context()

	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		answered <- callee.ue.Answer(ctx, ic, callee.media, ue.ToneSpeech(time.Second), ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	if err := caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(100*time.Millisecond), ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := <-answered; err != nil {
		t.Fatal(err)
	}

	// Each of the two UEs registers once, and the 401 of its first
	// REGISTER brings a second REGISTER that the I-CSCF asks the HSS
	// about again: two registration status queries per UE. The first
	// answer is DIAMETER_FIRST_REGISTRATION, and the second
	// DIAMETER_SUBSEQUENT_REGISTRATION, because the MAR in between has
	// left the name of the S-CSCF at the HSS, which sends the user back
	// to the S-CSCF that is already serving the procedure. The S-CSCF
	// asks for each UE's authentication vector once and tells the HSS
	// once that it serves the user. Of the three location queries, one
	// per registration is for the SUBSCRIBE with which the P-CSCF
	// subscribes to the reg event package of the user (TS 24.229 5.2.3),
	// which enters the home network through the I-CSCF, and one is for
	// the INVITE of the call.
	served(t, reg,
		`ims_diameter_requests_total{role="icscf",direction="sent",command="UAR"} 4`,
		`ims_diameter_answers_total{role="icscf",direction="received",command="UAR",result="DIAMETER_FIRST_REGISTRATION"} 2`,
		`ims_diameter_answers_total{role="icscf",direction="received",command="UAR",result="DIAMETER_SUBSEQUENT_REGISTRATION"} 2`,
		`ims_diameter_requests_total{role="icscf",direction="sent",command="LIR"} 3`,
		`ims_diameter_answers_total{role="icscf",direction="received",command="LIR",result="DIAMETER_SUCCESS"} 3`,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 2`,
		`ims_diameter_answers_total{role="scscf",direction="received",command="MAR",result="DIAMETER_SUCCESS"} 2`,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="SAR"} 2`,
		`ims_diameter_answers_total{role="scscf",direction="received",command="SAR",result="DIAMETER_SUCCESS"} 2`)
}

// served waits up to 3 seconds for every line of want to be in what the
// /metrics of a node answers, which is what Prometheus reads.
func served(t *testing.T, reg *metrics.Registry, want ...string) {
	t.Helper()
	server := httptest.NewServer(metrics.NewHandler(reg))
	defer server.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != metrics.ContentType {
			t.Fatalf("GET /metrics = %s with Content-Type %q, want 200 with %q",
				resp.Status, resp.Header.Get("Content-Type"), metrics.ContentType)
		}
		missing := 0
		for _, line := range want {
			if !strings.Contains(string(body), "\n"+line+"\n") {
				missing++
			}
		}
		if missing == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want the lines %q in what /metrics answers:\n%s", want, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TS 29.214 at the P-CSCF, which is the AF of Rx: the P-CSCF sends its
// policy control through a peer group of the one PCRF, as it does the
// Cx of the CSCFs through the group of the HSS, so those requests are
// counted under the role pcscf as well.
//
// Each registration opens the Rx session of the UE's signalling path
// with one AAR (TS 29.214 4.4.5), which the two UEs make two of. Each
// leg of the call is then authorised twice: the P-CSCF sends no service
// information for an offer alone, so an AAR follows each answer, the
// one in the 183 and the one in the 200 to the UPDATE in which the
// caller reports its reservation. The BYE ends the Rx session of each
// leg with an STR (4.4.4). The sessions of the two signalling paths
// stay, because both UEs stay registered.
func TestThePCSCFCountsItsRxRequestsAndAnswers(t *testing.T) {
	reg := metrics.NewRegistry()
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, epc: true, metrics: reg})
	caller := newEPCParty(t, n, impi, impu, password)
	callee := newEPCParty(t, n, impi2, impu2, password2)
	ctx := t.Context()

	speech := ue.ToneSpeech(300 * time.Millisecond)
	answered := answerIn(t, callee, speech, ue.AnswerOptions{PickUp: time.After(0)})
	if err := caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, speech, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}

	served(t, reg,
		`ims_diameter_requests_total{role="pcscf",direction="sent",command="AAR"} 6`,
		`ims_diameter_answers_total{role="pcscf",direction="received",command="AAR",result="DIAMETER_SUCCESS"} 6`,
		`ims_diameter_requests_total{role="pcscf",direction="sent",command="STR"} 2`,
		`ims_diameter_answers_total{role="pcscf",direction="received",command="STR",result="DIAMETER_SUCCESS"} 2`)
}

// TS 29.328 at the MMTel AS: it reads and writes the user's data in the
// HSS over Sh through a peer group of the instances of the HSS, which
// counts those requests under the role mmtel.
//
// The shorter no reply timer takes three requests of the UE over Ut:
// the client reads the document, writes the changed node back, and
// reads the document again for the settings it returns. Each
// authenticated request costs the AS a UDR for the Ut credential it
// authenticates against (TS 24.623 5.3.2.3) and a UDR for the document
// itself, and the challenge that the first request of the client is
// answered with costs the credential alone: seven UDRs. The write goes
// to the HSS in the one PUR.
func TestTheMMTelASCountsItsShRequestsAndAnswers(t *testing.T) {
	_, services, reg := utNetwork(t)
	if _, err := services.SetNoReplyTimer(t.Context(), 5); err != nil {
		t.Fatal(err)
	}
	served(t, reg,
		`ims_diameter_requests_total{role="mmtel",direction="sent",command="UDR"} 7`,
		`ims_diameter_answers_total{role="mmtel",direction="received",command="UDR",result="DIAMETER_SUCCESS"} 7`,
		`ims_diameter_requests_total{role="mmtel",direction="sent",command="PUR"} 1`,
		`ims_diameter_answers_total{role="mmtel",direction="received",command="PUR",result="DIAMETER_SUCCESS"} 1`)
}
