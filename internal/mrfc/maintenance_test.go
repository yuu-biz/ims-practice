package mrfc_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the MRFC while the operator has it out of service
// through the maintenance API of its node.

// mrfcRole is the name of the MRFC in the configuration file, which is
// the path segment of its maintenance API.
const mrfcRole = string(config.RoleMRFC)

// mrfcRetryAfter is the Retry-After that the MRFC answers with its 503,
// in seconds.
const mrfcRetryAfter = "30"

// manage gives m the maintenance API of a node, as the node of the MRFC
// does, and returns the node.
func manage(t *testing.T, m testMRFC) *maintenance.Node {
	t.Helper()
	node, err := maintenance.New(maintenance.Config{
		Node:          mrfcRole,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{m.Function()},
		Configuration: func() any { return map[string]string{"node": mrfcRole} },
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Manage(node.ManagedFunction(mrfcRole))
	return node
}

// mrfcState is the state of the MRFC in the maintenance API of node.
func mrfcState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	return node.State().Functions[0]
}

// setState makes the event that takes the MRFC to want, and returns its
// state after it.
func setState(t *testing.T, node *maintenance.Node, want maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := node.SetAdministrativeState(mrfcRole, maintenance.AdministrativeStateRequest{AdministrativeState: want, Operator: "test"})
	if err != nil {
		t.Fatalf("%s: %v", want, err)
	}
	return f
}

// users is how many users the MRFC counts, which must be a count that
// the role gives.
func users(t *testing.T, node *maintenance.Node) int {
	t.Helper()
	f := mrfcState(t, node)
	if f.Users == nil {
		t.Fatalf("the MRFC is %+v, want a count of its users", f)
	}
	return *f.Users
}

// waitFor waits up to ten seconds for cond, which the end of a dialog
// in another goroutine brings about.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// refusal checks that resp is the 503 of an MRFC out of service: with
// Retry-After, the Warning that says why, and the P-Asserted-Identity
// of the service that was asked for (TS 24.229 5.8.2.1.1.1).
func refusal(t *testing.T, resp *sip.Message, requestURI string) {
	t.Helper()
	if resp.StatusCode != 503 || resp.Get("Retry-After") != mrfcRetryAfter {
		t.Errorf("got %d with Retry-After %q, want 503 with %s", resp.StatusCode, resp.Get("Retry-After"), mrfcRetryAfter)
	}
	if w := resp.Get("Warning"); !strings.Contains(w, "Out of service") {
		t.Errorf("the 503 has Warning %q, want one that says the MRFC is out of service", w)
	}
	u, err := sip.ParseURI(requestURI)
	if err != nil {
		t.Fatal(err)
	}
	want := "<" + (sip.URI{Scheme: u.Scheme, User: u.User, Host: u.Host, Port: u.Port}).String() + ">"
	if pai := resp.Get("P-Asserted-Identity"); pai != want {
		t.Errorf("the 503 has P-Asserted-Identity %q, want %s", pai, want)
	}
}

// A shut down MRFC refuses the INVITE of an announcement with 503 and
// Retry-After, and has the MRFP play nothing. With no dialog of its own
// it is LOCKED at once, as a shut down leaves a function with no user
// (X.731 7.1.3.3), and after the unlock the announcement plays again.
func TestAShutDownMRFCRefusesANewAnnouncement(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	node := manage(t, m)
	as := newUA(t, "mmtel", nil)
	uri := "sip:annc@" + m.host + ";play=/provisioned/notice"

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v, want LOCKED with no dialog of its own", f)
	}
	refusal(t, final(t, invite(t, as, uri, offerFrom(t, addrOf(party(t)), "sendrecv"))), uri)
	if cs := media.relay.Contexts(); len(cs) != 0 {
		t.Errorf("the MRFP holds %v after the refusal, want no termination of a refused INVITE", cs)
	}
	if n := users(t, node); n != 0 {
		t.Errorf("the MRFC counts %d users after the refusal, want none of a refused INVITE", n)
	}

	setState(t, node, maintenance.Unlocked)
	caller := party(t)
	out := invite(t, as, uri, offerFrom(t, addrOf(caller), "sendrecv"))
	if ok := final(t, out); ok.StatusCode != 200 {
		t.Fatalf("the announcement after the unlock got %d, want 200", ok.StatusCode)
	}
	waitDone(t, out.Done(), "the dialog of the announcement after the unlock")
}

// The users of the MRFC are the dialogs it holds. A shut down waits for
// them: an announcement that loops, a customized alerting tone, keeps
// the MRFC SHUTTINGDOWN and goes on playing, the MRFC refuses a new
// INVITE meanwhile, and it becomes LOCKED once the AS has ended the
// dialog with the BYE that stops the play (TS 24.229 5.8.3.1.1).
func TestAShutDownWaitsForTheAnnouncementsOfTheMRFC(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	node := manage(t, m)
	as := newUA(t, "mmtel", nil)
	tone := "sip:annc@" + m.host + ";play=/provisioned/cat1;repeat=forever"
	out := invite(t, as, tone, offerFrom(t, addrOf(party(t)), "sendrecv"))
	if ok := final(t, out); ok.StatusCode != 200 {
		t.Fatalf("the tone got %d, want 200", ok.StatusCode)
	}

	waitFor(t, func() bool { return users(t, node) == 1 }, "the MRFC to count the dialog of the tone as its user")
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a tone is playing", f)
	}
	refused := "sip:annc@" + m.host + ";play=/provisioned/notice"
	refusal(t, final(t, invite(t, as, refused, offerFrom(t, addrOf(party(t)), "sendrecv"))), refused)
	// The tone plays on: its termination is the one context of the MRFP,
	// and the dialog is still up.
	if cs := media.relay.Contexts(); len(cs) != 1 {
		t.Errorf("the MRFP holds %v, want the one context of the tone that is playing", cs)
	}
	if f := mrfcState(t, node); f.AdministrativeState != maintenance.ShuttingDown || f.Users == nil || *f.Users != 1 {
		t.Errorf("the MRFC is %+v after the refusal, want SHUTTINGDOWN with the one user of the tone", f)
	}

	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return mrfcState(t, node).AdministrativeState == maintenance.Locked },
		"the MRFC to be LOCKED once the tone has stopped")
	waitNoContexts(t, media.relay)
}

// A shut down MRFC goes on with the parties of a conference that it
// holds: a re-INVITE within such a dialog changes the mode of the
// party's termination as ever (TS 24.229 5.8.4.1.1.1), the mix stays on
// the MRFP, and the MRFC is LOCKED only once the last of its parties
// has left. A party that would be new to this instance gets 503, even
// for the conference that it holds a party of.
func TestAShutDownWaitsForThePartiesOfTheConferencesOfTheMRFC(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	node := manage(t, m)
	as := newUA(t, "mmtel", nil)
	room := "sip:conf=room@" + m.host
	p := party(t)
	out := invite(t, as, room, offerFrom(t, addrOf(p), "sendrecv"))
	if ok := final(t, out); ok.StatusCode != 200 {
		t.Fatalf("the first party got %d, want 200", ok.StatusCode)
	}

	waitFor(t, func() bool { return users(t, node) == 1 }, "the MRFC to count the party of the conference as its user")
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a party is in a mix", f)
	}
	// Another party of the same conference is a dialog this instance
	// does not have, so it is new work and gets the 503. The AS then
	// takes it to another instance, which joins it to the same mix
	// (TestAnotherInstanceTakesThePartyThatAShutDownMRFCRefuses).
	refusal(t, final(t, invite(t, as, room, offerFrom(t, addrOf(party(t)), "sendrecv"))), room)
	// The party that is in the mix stays, and a request within its
	// dialog is served.
	resp, err := out.ReInvite(offerFrom(t, addrOf(p), "sendonly"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("the re-INVITE of the party got %d while the MRFC is shut down, want 200", resp.StatusCode)
	}
	if cs := media.relay.Contexts(); len(cs) != 1 || len(cs[0].Terminations) != 1 {
		t.Errorf("the MRFP holds %v, want the one termination of the party that is in the mix", cs)
	}
	if f := mrfcState(t, node); f.AdministrativeState != maintenance.ShuttingDown || f.Users == nil || *f.Users != 1 {
		t.Errorf("the MRFC is %+v, want SHUTTINGDOWN with the one user of the party in the mix", f)
	}

	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return mrfcState(t, node).AdministrativeState == maintenance.Locked },
		"the MRFC to be LOCKED once its last party has left")
	waitNoContexts(t, media.relay)
}

// A conference of the MRFC is tied to no instance of it: the Store
// names the MRFP and the context that hold the mix. So the party that a
// shut down instance refuses loses nothing. The AS sends the INVITE on
// to the next instance that DNS gave it for the name of the MRFC (RFC
// 3263 4.3), and that instance adds a termination to the same context,
// beside the party of the instance that is out of service.
func TestAnotherInstanceTakesThePartyThatAShutDownMRFCRefuses(t *testing.T) {
	media := startMRFP(t)
	store := mrfc.NewMemoryStore()
	first := startMRFC(t, store, media.processor("mrfp1"))
	second := startMRFC(t, store, media.processor("mrfp1"))
	node := manage(t, first)
	as := newUA(t, "mmtel", nil)
	out := invite(t, as, "sip:conf=room@"+first.host, offerFrom(t, addrOf(party(t)), "sendrecv"))
	if ok := final(t, out); ok.StatusCode != 200 {
		t.Fatalf("the first party got %d, want 200", ok.StatusCode)
	}
	defer out.Bye()
	held, ok, err := store.Conference(context.Background(), "room")
	if err != nil || !ok {
		t.Fatalf("the Store has %v, %v for the conference, want the MRFP and the context of its mix", held, err)
	}

	setState(t, node, maintenance.ShuttingDown)
	joined := invite(t, as, "sip:conf=room@"+second.host, offerFrom(t, addrOf(party(t)), "sendrecv"))
	if resp := final(t, joined); resp.StatusCode != 200 {
		t.Fatalf("the party of the second instance got %d, want 200", resp.StatusCode)
	}
	defer joined.Bye()
	cs := media.relay.Contexts()
	if len(cs) != 1 || cs[0].ID != held.Context || len(cs[0].Terminations) != 2 {
		t.Errorf("the MRFP holds %v, want the two parties in the context %s of the conference", cs, held.Context)
	}
	if n := users(t, node); n != 1 {
		t.Errorf("the instance that is shut down counts %d users, want only the party it holds", n)
	}
}

// The MRFC reports itself ENABLED while the node runs: it keeps no
// association with an MRFP whose state it could report, and an MRFP
// that does not answer an operation of Mp shows in the refusal of the
// INVITE that needed it.
func TestTheMRFCIsEnabledWhileTheNodeRuns(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	node := manage(t, m)
	if f := mrfcState(t, node); f.OperationalState != maintenance.Enabled {
		t.Errorf("the MRFC is %s, want ENABLED while the node runs", f.OperationalState)
	}
}
