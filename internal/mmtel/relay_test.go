package mmtel_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the B2BUA of the AS on its own: a caller's UA sends
// the INVITE straight to the AS, with the Route and the P-Served-User
// that the S-CSCF would give it, and the AS's outgoing leg goes along
// the rest of the Route straight to the callee's UA.

// quiet is the logger of the nodes of these tests. Its handler writes
// nothing and takes no lock: the lock of a handler that writes orders
// the goroutines that log one after another, which would hide from the
// race detector a race between two of them.
func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// ipLocator finds a host that is an IP address, which is all these
// tests use.
type ipLocator struct{}

func (ipLocator) Locate(_ context.Context, host string, port int, _ sip.Transport) ([]sip.Destination, error) {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	if port == 0 {
		port = 5060
	}
	return []sip.Destination{{Transport: sip.UDP, Addr: netip.AddrPortFrom(addr, uint16(port))}}, nil
}

// noServices is an HSS that has no repository data for anybody.
type noServices struct{}

func (noServices) RepositoryData(context.Context, string, string) (sh.TransparentData, bool, error) {
	return sh.TransparentData{}, false, nil
}

// testAS is an AS on loopback, the address it listens on, and the
// store of its calls and conferences.
type testAS struct {
	*mmtel.AS
	addr  netip.AddrPort
	store stores
}

// stores is a Store and a ConferenceStore in one, as both
// mmtel.MemoryStore and mmtelstore.Store are.
type stores interface {
	mmtel.Store
	mmtel.ConferenceStore
}

func startAS(t *testing.T, hss mmtel.HSS, cfg mmtel.Config) testAS {
	t.Helper()
	return startASOn(t, hss, cfg, mmtel.NewMemoryStore(), quiet())
}

// startASOn is startAS with store in place of a MemoryStore, and log in
// place of quiet.
func startASOn(t *testing.T, hss mmtel.HSS, cfg mmtel.Config, store stores, log *slog.Logger) testAS {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Host, cfg.Port = "127.0.0.1", int(tr.LocalAddr().Port())
	if cfg.HomeDomain == "" {
		cfg.HomeDomain = domain
	}
	if cfg.MRFC != "" && cfg.ConferenceDomain == "" {
		cfg.ConferenceDomain = conferenceDomain
	}
	as := mmtel.New(cfg, hss, store, store, ipLocator{}, log)
	if err := as.Listen(tr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(as.Close)
	return testAS{AS: as, addr: tr.LocalAddr(), store: store}
}

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

// conferenceDomain is the subdomain of the conference URIs of the AS
// that startAS starts, as in deploy/compose/mmtel.yaml.
const conferenceDomain = "conf." + domain

var (
	offer  = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 1 IN IP4 192.0.2.1\r\n")}
	answer = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 1 IN IP4 192.0.2.2\r\n")}
	hold   = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 2 IN IP4 192.0.2.1\r\na=sendonly\r\n")}
	held   = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 2 IN IP4 192.0.2.2\r\na=recvonly\r\n")}
)

// newUA starts a UA on loopback for user; onCall answers its calls.
func newUA(t *testing.T, user string, onCall func(*sip.IncomingCall)) *sip.UA {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy: tr.LocalAddr().String(), Contact: "sip:" + user + "@" + tr.LocalAddr().String(),
		Supported: []string{sip.OptionPrecondition}, OnCall: onCall,
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua
}

// newParticipant is newUA for a participant of the conferences of as:
// its requests to a conference URI go to as, as DNS would send them.
func newParticipant(t *testing.T, as testAS, user string, onCall func(*sip.IncomingCall)) *sip.UA {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy: tr.LocalAddr().String(), Contact: "sip:" + user + "@" + tr.LocalAddr().String(),
		Supported: []string{sip.OptionPrecondition}, OnCall: onCall,
		Locate: func(u sip.URI) ([]sip.Destination, error) {
			if u.Host == conferenceDomain {
				return []sip.Destination{{Transport: sip.UDP, Addr: as.addr}}, nil
			}
			addr, err := netip.ParseAddr(u.Host)
			if err != nil {
				return nil, err
			}
			return []sip.Destination{{Transport: sip.UDP, Addr: netip.AddrPortFrom(addr, uint16(u.Port))}}, nil
		},
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua
}

// hop is a node of a test with a stack of its own: a UA, or a stack
// that answers for several UASs.
type hop interface{ Stack() *sip.Stack }

// contactOf is the address of h's Contact.
func contactOf(t *testing.T, h hop) string {
	t.Helper()
	return h.Stack().LocalAddr().String()
}

// viaAS is the INVITE of the S-CSCF for served in the session case
// sescase: the AS on top of the Route, then, where the S-CSCF would be,
// the callee itself.
func viaAS(t *testing.T, as testAS, callee hop, sescase string, body sip.Body) sip.Invitation {
	t.Helper()
	served := sip.PServedUser{URI: "sip:alice@" + domain, SessionCase: sip.SessionCase(sescase), RegState: sip.RegStateRegistered}
	return sip.Invitation{
		RequestURI: "sip:bob@" + domain, From: "sip:alice@" + domain, To: "sip:bob@" + domain,
		Route: []string{"<sip:" + as.addr.String() + ";lr>", "<sip:" + contactOf(t, callee) + ";lr;odi=x1>"},
		Headers: []sip.HeaderField{
			{Name: "P-Served-User", Value: served.String()},
			{Name: "P-Asserted-Identity", Value: "<sip:alice@" + domain + ">"},
			{Name: "P-Charging-Vector", Value: "icid-value=icid1;orig-ioi=" + domain},
			{Name: "P-Early-Media", Value: "supported"},
		},
		Body: body,
	}
}

func readAll(t *testing.T, out *sip.OutgoingCall) []*sip.Message {
	t.Helper()
	var got []*sip.Message
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-out.Responses():
			if !ok {
				return got
			}
			got = append(got, m)
		case <-timeout:
			t.Fatalf("no final response; got %v so far", statuses(got))
		}
	}
}

func statuses(ms []*sip.Message) []int {
	var codes []int
	for _, m := range ms {
		codes = append(codes, m.StatusCode)
	}
	return codes
}

func receive[T any](t *testing.T, c <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("no %s", what)
		var zero T
		return zero
	}
}

func TestCallThroughTheAS(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	calls := make(chan *sip.IncomingCall, 1)
	holds := make(chan *sip.Message, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) {
			holds <- r.Request
			r.Accept(held)
		})
		calls <- c
		c.Progress(183, answer, sip.HeaderField{Name: "Require", Value: sip.OptionPrecondition},
			sip.HeaderField{Name: "P-Early-Media", Value: "sendrecv"})
		c.Progress(180, sip.Body{})
		c.Accept(sip.Body{})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out)
	if !reflect.DeepEqual(statuses(got), []int{183, 180, 200}) {
		t.Fatalf("caller got %v, want 183, 180 and 200", statuses(got))
	}
	if string(got[0].Body) != string(answer.Content) || !got[0].Requires(sip.Option100rel) || !got[0].Requires(sip.OptionPrecondition) ||
		got[0].Get("P-Early-Media") != "sendrecv" {
		t.Errorf("183 to the caller is\n%s\nwant the callee's answer, reliable, with its precondition and P-Early-Media", got[0])
	}
	if pcv := got[2].Get("P-Charging-Vector"); pcv != "icid-value=icid1;orig-ioi="+domain+";term-ioi="+domain {
		t.Errorf("200 has P-Charging-Vector %q, want the icid and the orig-ioi of the INVITE with the term-ioi of the AS", pcv)
	}

	in := receive(t, calls, "call at the callee")
	req := in.Request
	if req.Get("Call-ID") == out.Request.Get("Call-ID") || req.RequestURI != "sip:bob@"+domain {
		t.Errorf("callee's INVITE has Call-ID %s and Request-URI %s; want a Call-ID of its own and the Request-URI as it was",
			req.Get("Call-ID"), req.RequestURI)
	}
	if req.Get("P-Asserted-Identity") != "<sip:alice@"+domain+">" || req.Get("P-Early-Media") != "supported" ||
		req.Get("P-Served-User") != "" || req.Get("P-Charging-Vector") != "icid-value=icid1" || req.Get("Max-Forwards") != "69" {
		t.Errorf("callee's INVITE is\n%s\nwant P-Asserted-Identity and P-Early-Media copied, no P-Served-User, no orig-ioi and one hop less", req)
	}
	if got := req.GetAll("Route"); !reflect.DeepEqual(got, []string{"<sip:" + contactOf(t, callee) + ";lr;odi=x1>"}) {
		t.Errorf("callee's INVITE has Route %q, want the S-CSCF's with its original dialog identifier", got)
	}
	if string(req.Body) != string(offer.Content) {
		t.Errorf("callee got offer %q, want the caller's", req.Body)
	}
	if !req.Supports(sip.OptionPrecondition) {
		t.Errorf("callee's INVITE has Supported %q, want the caller's with precondition", req.GetAll("Supported"))
	}

	// While the call is up, its record is in the Store under both
	// dialogs.
	uac, _ := out.Dialog()
	uas, _ := in.Dialog()
	incoming := sip.DialogID{CallID: uac.ID.CallID, LocalTag: uac.ID.RemoteTag, RemoteTag: uac.ID.LocalTag}
	outgoing := sip.DialogID{CallID: uas.ID.CallID, LocalTag: uas.ID.RemoteTag, RemoteTag: uas.ID.LocalTag}
	waitFor(t, func() bool { _, ok := callOf(t, as, incoming); return ok }, "call record")
	record, _ := callOf(t, as, outgoing)
	if record.Incoming.ID != incoming || record.ServedUser != "sip:alice@"+domain || record.SessionCase != sip.SessionCaseOrig {
		t.Errorf("record is %+v, want both dialogs and the served user", record)
	}

	// HOLD: the re-INVITE goes on to the callee, and its answer back.
	resp, err := out.ReInvite(hold)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(held.Content) {
		t.Fatalf("re-INVITE got %v, %v; want 200 with the callee's answer", resp, err)
	}
	if r := receive(t, holds, "re-INVITE at the callee"); string(r.Body) != string(hold.Content) {
		t.Errorf("callee got re-INVITE body %q, want the caller's sendonly offer", r.Body)
	}

	why := sip.NewReason("RELEASE_CAUSE", 1, "User ends call")
	if err := out.Bye(why.Header()); err != nil {
		t.Fatal(err)
	}
	receive(t, in.Done(), "end of the callee's call")
	if bye := in.EndedBy(); bye == nil || !reflect.DeepEqual(sip.Reasons(bye), []sip.Reason{why}) {
		t.Errorf("callee's call ended by %v, want a BYE with the caller's Reason", bye)
	}
	waitFor(t, func() bool { _, ok := callOf(t, as, incoming); return !ok }, "the record to go")
}

// callOf is the record in the store of as of the call that the dialog
// id belongs to. The record is what another instance of the AS would
// find; no message on the wire shows it.
func callOf(t *testing.T, as testAS, id sip.DialogID) (mmtel.CallRecord, bool) {
	t.Helper()
	r, ok, err := as.store.CallOf(context.Background(), id)
	if err != nil {
		t.Fatalf("CallOf(%v): %v", id, err)
	}
	return r, ok
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The record of a call follows its dialogs after each request within
// them, for another instance that ends the call: the callee moves to
// another contact with a re-INVITE, a target refresh request (RFC 3261
// 12.2), and the caller's HOLD has the AS send the callee a request
// with a higher CSeq, below which the callee would refuse a BYE
// (12.2.2).
func TestRecordFollowsTheDialogs(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	calls := make(chan *sip.IncomingCall, 1)
	holds := make(chan *sip.Message, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) {
			holds <- r.Request
			r.Accept(held)
		})
		calls <- c
		c.Accept(answer)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "term", offer)
	inv.OnRequest = func(r *sip.InDialogRequest) { r.Accept(hold) }
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("caller got %v, want 200", got)
	}
	defer out.Bye()
	in := receive(t, calls, "call at the callee")
	uas, _ := in.Dialog()
	outgoing := sip.DialogID{CallID: uas.ID.CallID, LocalTag: uas.ID.RemoteTag, RemoteTag: uas.ID.LocalTag}
	waitFor(t, func() bool { _, ok := callOf(t, as, outgoing); return ok }, "call record")

	moved := "sip:moved@" + contactOf(t, callee)
	in.SetContact(sip.NameAddr{URI: moved}, false)
	if resp, err := in.ReInvite(held); err != nil || resp.StatusCode != 200 {
		t.Fatalf("re-INVITE from the new contact got %v, %v; want 200", resp, err)
	}
	waitFor(t, func() bool { r, _ := callOf(t, as, outgoing); return r.Outgoing.RemoteTarget == moved },
		"the record to have the callee's new contact "+moved)

	if resp, err := out.ReInvite(hold); err != nil || resp.StatusCode != 200 {
		t.Fatalf("re-INVITE of the caller got %v, %v; want 200", resp, err)
	}
	cseq, err := sip.ParseCSeq(receive(t, holds, "re-INVITE at the callee").Get("CSeq"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { r, _ := callOf(t, as, outgoing); return r.Outgoing.LocalSeq == cseq.Number },
		fmt.Sprintf("the record to have the CSeq %d of the AS's re-INVITE to the callee", cseq.Number))
}

// watchedStore is a store that tells each call record written into it.
type watchedStore struct {
	stores
	put chan mmtel.CallRecord
}

func (s watchedStore) PutCall(ctx context.Context, r mmtel.CallRecord) error {
	err := s.stores.PutCall(ctx, r)
	select {
	case s.put <- r:
	default:
	}
	return err
}

// A request within the dialogs that the AS finishes after the call has
// ended does not write the record of the call back, where another
// instance would find a call that is over: the callee hangs up while
// the caller's re-INVITE waits for its answer, which comes only after
// the AS has deleted the record.
func TestNoRecordAfterTheCall(t *testing.T) {
	store := watchedStore{stores: mmtel.NewMemoryStore(), put: make(chan mmtel.CallRecord, 16)}
	as := startASOn(t, noServices{}, mmtel.Config{}, store, quiet())
	calls := make(chan *sip.IncomingCall, 1)
	reinvites := make(chan *sip.InDialogRequest, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) { reinvites <- r })
		calls <- c
		c.Accept(answer)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("caller got %v, want 200", got)
	}
	in := receive(t, calls, "call at the callee")
	uas, _ := in.Dialog()
	outgoing := sip.DialogID{CallID: uas.ID.CallID, LocalTag: uas.ID.RemoteTag, RemoteTag: uas.ID.LocalTag}
	waitFor(t, func() bool { _, ok := callOf(t, as, outgoing); return ok }, "call record")

	go out.ReInvite(hold)
	r := receive(t, reinvites, "re-INVITE at the callee")
	in.Bye()
	waitFor(t, func() bool { _, ok := callOf(t, as, outgoing); return !ok }, "the record of the ended call to go")
	for len(store.put) > 0 {
		<-store.put
	}
	r.Reject(487)
	select {
	case <-store.put:
		t.Error("the answer to the re-INVITE wrote the record of the ended call again")
	case <-time.After(500 * time.Millisecond):
	}
	if _, ok := callOf(t, as, outgoing); ok {
		t.Error("the store has the record of the ended call")
	}
}

func TestCancelThroughTheAS(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Progress(180, sip.Body{})
		calls <- c
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	if r := receive(t, out.Responses(), "180"); r.StatusCode != 180 {
		t.Fatalf("got %d, want 180", r.StatusCode)
	}
	why := sip.NewReason(sip.ReasonSIP, 487, "Caller gave up")
	if err := out.Cancel(why.Header()); err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{487}) {
		t.Errorf("caller got %v after the CANCEL, want 487", got)
	}
	in := receive(t, calls, "call at the callee")
	receive(t, in.Done(), "end of the callee's call")
	if cancel := in.EndedBy(); cancel == nil || cancel.Method != sip.CANCEL || !reflect.DeepEqual(sip.Reasons(cancel), []sip.Reason{why}) {
		t.Errorf("callee's call ended by %v, want a CANCEL with the caller's Reason", cancel)
	}
}

// TestReasonOfTheEndGoesOnAsItCame: the Reason of the request that ends
// one leg goes into the request that ends the other as it came, values
// that the AS cannot read among them (RFC 3326 2). A dropped value
// would leave the other side without the cause of the end.
func TestReasonOfTheEndGoesOnAsItCame(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	up := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		if _, err := c.Accept(answer); err != nil {
			return
		}
		up <- c
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("the caller got %v, want 200", got)
	}
	// The second value has a cause that is no number, which the AS cannot
	// read; RFC 3326 2 leaves the values of other protocols open.
	why := []sip.HeaderField{
		{Name: "Reason", Value: `Q.850;cause=16;text="Normal call clearing"`},
		{Name: "Reason", Value: `RELEASE_CAUSE;cause=normal;text="Hung up"`},
	}
	if err := receive(t, up, "answered call at the callee").Bye(why...); err != nil {
		t.Fatal(err)
	}
	receive(t, out.Done(), "end of the caller's call")
	if got := sip.ReasonFields(out.EndedBy()); !reflect.DeepEqual(got, why) {
		t.Errorf("the caller's BYE has Reason %v, want %v, as the callee sent them", got, why)
	}
}

// TestRequestWithoutALeg: the AS gave the caller an early dialog of its
// own to play it an announcement, and sends no INVITE toward the callee.
// A request of the caller within that dialog has no leg to go on in, and
// gets an answer of the AS instead of waiting at the caller for its
// transaction to time out: a PRACK or an UPDATE with an offer 488, since
// the AS keeps the session of the announcement, an UPDATE without one
// 200, and any other request 481 (Call/Transaction Does Not Exist).
func TestRequestWithoutALeg(t *testing.T) {
	var log events
	uris := make(chan string, 1)
	byed := make(chan struct{})
	mrfc := fakeMRFC(t, &log, uris, byed)
	hss := &documents{docs: map[string]string{"sip:alice@" + domain: mmtelServices(barring(rule("baic", "", false), ""))}}
	as := startAS(t, hss, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc), Announcement: "notice"})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(486) })
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "term", pcmuOffer(t, netip.MustParseAddrPort("192.0.2.1:4000"), pcmuOnly))
	inv.ManualPRACK = true
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	announcement := receive(t, out.Responses(), "183 of the announcement")
	if announcement.StatusCode != 183 || !announcement.Requires(sip.Option100rel) {
		t.Fatalf("the caller got %d (Require %q), want the reliable 183 that carries the announcement", announcement.StatusCode, announcement.Get("Require"))
	}
	// RFC 3262 5: after the answer in the 183, the PRACK may carry a new
	// offer.
	resp, err := out.PRACK(announcement, pcmuOffer(t, netip.MustParseAddrPort("192.0.2.1:4002"), pcmuOnly))
	if err != nil || resp.StatusCode != 488 {
		t.Errorf("the PRACK with an offer got %v, %v; want 488", resp, err)
	}
	resp, err = out.Update(pcmuOffer(t, netip.MustParseAddrPort("192.0.2.1:4002"), pcmuOnly))
	if err != nil || resp.StatusCode != 488 {
		t.Errorf("the UPDATE with an offer got %v, %v; want 488", resp, err)
	}
	resp, err = out.Update(sip.Body{})
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("the UPDATE without an offer got %v, %v; want 200", resp, err)
	}
	resp, err = out.Send("INFO", sip.Body{})
	if err != nil || resp.StatusCode != 481 {
		t.Errorf("the INFO got %v, %v; want 481, since the call has no leg to send it on", resp, err)
	}
	// The announcement waits for the end of the dialog with the MRFC,
	// which the fake MRFC leaves to the caller's CANCEL.
	out.Cancel()
}

// TestCallerEndsItsEarlyDialogBeforeTheAnswer: a caller may end an
// early dialog with a BYE instead of cancelling its INVITE (RFC 3261
// 15), and its UA answers the INVITE with 487 itself (15.1.2). The AS
// cancels the leg to the callee all the same (RFC 3261 9.1): the callee
// would otherwise go on alerting a user whose caller is gone.
func TestCallerEndsItsEarlyDialogBeforeTheAnswer(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Progress(180, sip.Body{})
		calls <- c
		// The callee alerts its user until the AS cancels the INVITE.
		<-c.Done()
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	if r := receive(t, out.Responses(), "180"); r.StatusCode != 180 {
		t.Fatalf("got %d, want 180", r.StatusCode)
	}
	early, ok := out.Dialog()
	if !ok {
		t.Fatal("the caller has no early dialog after the 180")
	}
	why := sip.NewReason(sip.ReasonSIP, 487, "Caller hung up")
	bye := early.NewRequest(sip.BYE)
	bye.Add("Reason", why.String())
	if _, err := caller.Stack().SendRequest(bye, sip.Destination{Transport: sip.UDP, Addr: as.addr}); err != nil {
		t.Fatal(err)
	}
	in := receive(t, calls, "call at the callee")
	receive(t, in.Done(), "end of the leg to the callee")
	if cancel := in.EndedBy(); cancel == nil || cancel.Method != sip.CANCEL || !reflect.DeepEqual(sip.Reasons(cancel), []sip.Reason{why}) {
		t.Errorf("leg to the callee ended by %v, want a CANCEL with the Reason of the caller's BYE", cancel)
	}
}

func TestRejectionThroughTheAS(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Reject(486, sip.HeaderField{Name: "Warning", Value: `399 bob "In a call"`})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out)
	if !reflect.DeepEqual(statuses(got), []int{486}) || got[0].Get("Warning") != `399 bob "In a call"` {
		t.Errorf("caller got %v with Warning %q, want the callee's 486 and its Warning", statuses(got), got[len(got)-1].Get("Warning"))
	}
}

// TestOfferInTheReliableResponse: after an INVITE without an offer,
// the callee offers in its reliable 183, and the caller's answer goes
// on in the PRACK (RFC 3262 5).
func TestOfferInTheReliableResponse(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	pracks := make(chan *sip.Message, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) {
			pracks <- r.Request
			r.Accept(sip.Body{})
		})
		c.Progress(183, answer)
		c.Accept(sip.Body{})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", sip.Body{}))
	if err != nil {
		t.Fatal(err)
	}
	offered := receive(t, out.Responses(), "183")
	if offered.StatusCode != 183 || string(offered.Body) != string(answer.Content) {
		t.Fatalf("caller got\n%s\nwant the callee's 183 with its offer", offered)
	}
	if resp, err := out.PRACK(offered, offer); err != nil || resp.StatusCode != 200 {
		t.Fatalf("PRACK got %v, %v; want 200", resp, err)
	}
	if p := receive(t, pracks, "PRACK at the callee"); p.Method != sip.PRACK || string(p.Body) != string(offer.Content) {
		t.Errorf("callee got\n%s\nwant the PRACK with the caller's answer", p)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Errorf("caller got %v after the PRACK, want 200", got)
	}
	out.Bye()
}

// After an INVITE without an offer, the first reliable response with a
// body in each early dialog has the offer of its UAS (RFC 3262 5). Once
// the caller has answered the offer of one, that of the next early
// dialog does not reach it, even after a 199 has ended the first: the
// AS would send a second offer in its responses to the INVITE (RFC 3261
// 13.2.1).
func TestOfferOfTheNextEarlyDialog(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Progress(183, answer)
		c.EarlyDialogTerminated(480)
		c.Progress(183, answer2)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", sip.Body{}))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Cancel()
	offered := receive(t, out.Responses(), "183")
	if _, err := out.PRACK(offered, offer); err != nil {
		t.Fatal(err)
	}
	if next := receive(t, out.Responses(), "the 183 of the next early dialog"); next.StatusCode != 183 || len(next.Body) != 0 {
		t.Errorf("caller got\n%s\nwant the 183 of the next early dialog without its offer", next)
	}
}

// TestUpdateInTheEarlyDialog: an UPDATE of the caller before the 2xx,
// such as the one that tells the callee that the caller's resources are
// reserved (RFC 3312, RFC 3311), reaches the callee in its early
// dialog, and the answer comes back.
func TestUpdateInTheEarlyDialog(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	updates := make(chan *sip.Message, 1)
	confirmed := make(chan struct{})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) {
			updates <- r.Request
			r.Accept(held, sip.HeaderField{Name: "Require", Value: sip.OptionPrecondition})
			close(confirmed)
		})
		c.Progress(183, answer, sip.HeaderField{Name: "Require", Value: sip.OptionPrecondition})
		<-confirmed
		c.Progress(180, sip.Body{})
		c.Accept(sip.Body{})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	if r := receive(t, out.Responses(), "183"); r.StatusCode != 183 {
		t.Fatalf("got %d, want 183", r.StatusCode)
	}
	resp, err := out.Update(hold, sip.HeaderField{Name: "Require", Value: sip.OptionPrecondition})
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(held.Content) || !resp.Requires(sip.OptionPrecondition) {
		t.Fatalf("UPDATE got %v, %v; want the callee's 200 with its answer and precondition", resp, err)
	}
	if u := receive(t, updates, "UPDATE at the callee"); string(u.Body) != string(hold.Content) || !u.Requires(sip.OptionPrecondition) {
		t.Errorf("callee got\n%s\nwant the caller's UPDATE", u)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{180, 200}) {
		t.Errorf("caller got %v after the UPDATE, want 180 and 200", got)
	}
	out.Bye()
}

func TestINVITEWithoutAServedUser(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Accept(answer) })
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", offer)
	inv.Headers = inv.Headers[1:]
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{403}) {
		t.Errorf("caller got %v, want 403", got)
	}
}
