package mmtel_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// cdiv is a CDIV service with the given rules, which the operator
// allows up to limit diversions.
func cdiv(limit, rules string) string {
	return `<complete-communication-diversion>
  <communication-diversion><NoReplyTimer>5</NoReplyTimer><cp:ruleset>` + rules + `</cp:ruleset></communication-diversion>
  <operator-communication-diversion authorized="true">
    <communication-retention-on-invocation>clear-communication-on-invocation-of-diversion</communication-retention-on-invocation>
    <retention-when-diverting-rejected-at-diverted-to-user>no-action-at-diverting-user</retention-when-diverting-rejected-at-diverted-to-user>
    <total-number-of-diversions-for-each-communication>` + limit + `</total-number-of-diversions-for-each-communication>
    <cdiv-indication-timer>0</cdiv-indication-timer>
    <communication-forwarding-on-no-reply-timer>20</communication-forwarding-on-no-reply-timer>
  </operator-communication-diversion>
</complete-communication-diversion>`
}

// forwardRule is a diversion rule to target under conditions; extra
// goes into its forward-to.
func forwardRule(id, conditions, target, extra string) string {
	return `<cp:rule id="` + id + `"><cp:conditions>` + conditions + `</cp:conditions><cp:actions><forward-to><target>` +
		target + `</target>` + extra + `</forward-to></cp:actions></cp:rule>`
}

const (
	carolTel = "tel:+12025550103"
	carolSIP = "sip:+12025550103@" + domain + ";user=phone"
	aliceURI = "sip:alice@" + domain
)

// toServedUser is the INVITE of the S-CSCF to the AS for a call to the
// served user alice.
func toServedUser(t *testing.T, as testAS, next *sip.UA, regstate string, headers ...sip.HeaderField) sip.Invitation {
	t.Helper()
	inv := viaAS(t, as, next, "term", offer)
	inv.RequestURI, inv.To = aliceURI, aliceURI
	served := sip.PServedUser{URI: aliceURI, SessionCase: sip.SessionCaseTerm, RegState: regstate}
	inv.Headers = append([]sip.HeaderField{{Name: "P-Served-User", Value: served.String()}}, inv.Headers[1:]...)
	inv.Headers = append(inv.Headers, headers...)
	return inv
}

// divertedAtSetup sends a call to alice, whose services are doc, and
// returns what the caller got and the INVITE that went on.
func divertedAtSetup(t *testing.T, doc, regstate string, headers ...sip.HeaderField) ([]*sip.Message, *sip.Message) {
	t.Helper()
	hss := &documents{docs: map[string]string{aliceURI: mmtelServices(doc)}}
	as := startAS(t, hss, mmtel.Config{})
	got := make(chan *sip.Message, 1)
	next := newUA(t, "next", func(c *sip.IncomingCall) {
		got <- c.Request
		c.Progress(180, sip.Body{})
		c.Reject(486)
	})
	caller := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(toServedUser(t, as, next, regstate, headers...))
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, out)
	select {
	case req := <-got:
		return resps, req
	default:
		return resps, nil
	}
}

func historyOf(t *testing.T, m *sip.Message) []string {
	t.Helper()
	entries, err := sip.ParseHistoryInfo(m.GetAll("History-Info"))
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, e := range entries {
		s = append(s, e.String())
	}
	return s
}

func TestCommunicationForwardingUnconditional(t *testing.T) {
	doc := cdiv("5", forwardRule("cfu", "", carolTel, ""))
	resps, req := divertedAtSetup(t, doc, "reg")
	if req == nil {
		t.Fatal("no INVITE went on")
	}
	// TS 24.604 4.5.2.6.2.2: the Request-URI with the cause-param of
	// CFU, and History-Info with the served user and the target.
	if req.RequestURI != carolSIP+";cause=302" {
		t.Errorf("Request-URI = %q, want the target with cause 302", req.RequestURI)
	}
	wantHistory := []string{"<" + aliceURI + ">;index=1", "<" + carolSIP + ";cause=302>;index=1.1;mp=1"}
	if got := historyOf(t, req); !reflect.DeepEqual(got, wantHistory) {
		t.Errorf("History-Info = %q, want %q", got, wantHistory)
	}
	to, _ := sip.ParseNameAddr(req.Get("To"))
	if to.URI != aliceURI {
		t.Errorf("To = %q, want the served user's: alice does not hide herself", req.Get("To"))
	}
	// 4.5.2.6.4: the 181 first, with the diverting user and the
	// diverted-to entry kept private.
	if !reflect.DeepEqual(statuses(resps), []int{181, 180, 486}) {
		t.Fatalf("caller got %v, want 181, then the diverted-to side's 180 and 486", statuses(resps))
	}
	notified := resps[0]
	wantNotified := []string{"<" + aliceURI + ">;index=1", "<" + carolSIP + ";cause=302?Privacy=history>;index=1.1;mp=1"}
	if notified.Get("P-Asserted-Identity") != "<"+aliceURI+">" || !reflect.DeepEqual(historyOf(t, notified), wantNotified) {
		t.Errorf("181 is\n%s\nwant P-Asserted-Identity of alice and History-Info %q", notified, wantNotified)
	}
}

func TestCommunicationForwardingWithoutNotification(t *testing.T) {
	doc := cdiv("5", forwardRule("cfu", "", carolTel, "<notify-caller>false</notify-caller>"))
	resps, req := divertedAtSetup(t, doc, "reg")
	if req == nil || req.RequestURI != carolSIP+";cause=302" {
		t.Fatalf("INVITE that went on is %v, want one to carol", req)
	}
	if !reflect.DeepEqual(statuses(resps), []int{180, 486}) {
		t.Errorf("caller got %v, want no 181", statuses(resps))
	}
}

func TestCommunicationForwardingNotLoggedIn(t *testing.T) {
	doc := cdiv("5", forwardRule("cfnl", "<not-registered/>", carolTel, ""))
	if _, req := divertedAtSetup(t, doc, "reg"); req == nil || req.RequestURI != "sip:alice@"+domain {
		t.Errorf("registered user: INVITE that went on is %v, want one to alice herself", req)
	}
	if _, req := divertedAtSetup(t, doc, "unreg"); req == nil || req.RequestURI != carolSIP+";cause=404" {
		t.Errorf("unregistered user: INVITE that went on is %v, want one to carol with cause 404", req)
	}
}

// TestDivertedAgain: alice gets a call that bob diverted to her; her
// own diversion adds one entry below hers (TS 24.604 4.5.2.6.2.3).
func TestDivertedAgain(t *testing.T) {
	doc := cdiv("5", forwardRule("cfu", "", carolTel, ""))
	history := []sip.HeaderField{
		{Name: "History-Info", Value: "<sip:bob@" + domain + ">;index=1"},
		{Name: "History-Info", Value: "<sip:+12025550101@" + domain + ";user=phone;cause=302>;index=1.1;mp=1"},
	}
	_, req := divertedAtSetup(t, doc, "reg", history...)
	want := []string{
		"<sip:bob@" + domain + ">;index=1",
		"<sip:+12025550101@" + domain + ";user=phone;cause=302>;index=1.1;mp=1",
		"<" + carolSIP + ";cause=302>;index=1.1.1;mp=1.1",
	}
	if req == nil || !reflect.DeepEqual(historyOf(t, req), want) {
		t.Errorf("History-Info = %q, want %q", historyOf(t, req), want)
	}
}

// TestTooManyDiversions is TS 24.604 4.5.2.6.1 with the operator's
// limit of two.
func TestTooManyDiversions(t *testing.T) {
	doc := cdiv("2", forwardRule("cfu", "", carolTel, ""))
	history := []sip.HeaderField{
		{Name: "History-Info", Value: "<sip:bob@" + domain + ">;index=1"},
		{Name: "History-Info", Value: "<sip:+12025550101@" + domain + ";user=phone;cause=302>;index=1.1;mp=1"},
		{Name: "History-Info", Value: "<sip:+12025550102@" + domain + ";user=phone;cause=302>;index=1.1.1;mp=1.1"},
	}
	resps, req := divertedAtSetup(t, doc, "reg", history...)
	if req != nil {
		t.Errorf("an INVITE went on to %s", req.RequestURI)
	}
	if !reflect.DeepEqual(statuses(resps), []int{480}) || resps[0].Get("Warning") == "" {
		t.Errorf("caller got %v with Warning %q, want 480 with a Warning", statuses(resps), resps[len(resps)-1].Get("Warning"))
	}
}

// TestDiversionOfAUserWhoWithholdsItsIdentity: the diverted-to user
// does not see alice, who has OIR (TS 24.604 4.5.2.6.2.2 b and c).
func TestDiversionOfAUserWhoWithholdsItsIdentity(t *testing.T) {
	doc := cdiv("5", forwardRule("cfu", "", carolTel, "")) + oir("permanent", "presentation-restricted")
	_, req := divertedAtSetup(t, doc, "reg")
	to, _ := sip.ParseNameAddr(req.Get("To"))
	want := []string{"<" + aliceURI + "?Privacy=history>;index=1", "<" + carolSIP + ";cause=302>;index=1.1;mp=1"}
	if to.URI != carolSIP+";cause=302" || !reflect.DeepEqual(historyOf(t, req), want) {
		t.Errorf("To = %q and History-Info = %q; want the target in To and %q", req.Get("To"), historyOf(t, req), want)
	}
}

// TestDiversionRuleWithoutTarget: a rule that the operator provisioned
// but the user has not set up diverts nothing (TS 24.604 4.9.1.4).
func TestDiversionRuleWithoutTarget(t *testing.T) {
	doc := cdiv("5", forwardRule("cfu", "", "", ""))
	if _, req := divertedAtSetup(t, doc, "reg"); req == nil || req.RequestURI != aliceURI {
		t.Errorf("INVITE that went on is %v, want one to alice", req)
	}
}

var answer2 = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=carol 1 1 IN IP4 192.0.2.3\r\n")}

// twoParties is the next hop of the AS for a call to alice that may be
// diverted to carol: it plays alice's side, as served answers, and
// carol's, which answers with answer2. The INVITEs come on got.
func twoParties(t *testing.T, served func(*sip.IncomingCall)) (*sip.UA, chan *sip.Message) {
	t.Helper()
	got := make(chan *sip.Message, 2)
	next := newUA(t, "next", func(c *sip.IncomingCall) {
		got <- c.Request
		if c.Request.RequestURI == aliceURI {
			served(c)
			return
		}
		c.Progress(180, sip.Body{})
		c.Accept(answer2)
	})
	return next, got
}

// divertedLater sends a call from a caller with supported in its
// Supported to alice, whose side answers as served, and returns the
// caller's call, its responses up to the final one, and the INVITEs
// that went on.
func divertedLater(t *testing.T, doc, supported string, served func(*sip.IncomingCall)) (*sip.OutgoingCall, []*sip.Message, chan *sip.Message) {
	t.Helper()
	hss := &documents{docs: map[string]string{aliceURI: mmtelServices(doc)}}
	as := startAS(t, hss, mmtel.Config{})
	next, got := twoParties(t, served)
	caller := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(toServedUser(t, as, next, "reg", sip.HeaderField{Name: "Supported", Value: supported}))
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, out)
	t.Cleanup(func() { out.Bye() })
	return out, resps, got
}

func TestCommunicationForwardingBusy(t *testing.T) {
	doc := cdiv("5", forwardRule("cfb", "<busy/>", carolTel, ""))
	out, resps, got := divertedLater(t, doc, "100rel, 199", func(c *sip.IncomingCall) {
		c.Progress(183, answer)
		c.Progress(180, sip.Body{})
		c.Reject(486)
	})
	// RFC 6228: the early dialog with alice's answer ends with a 199, and
	// carol's responses come in a new one.
	if !reflect.DeepEqual(statuses(resps), []int{183, 180, 199, 181, 180, 200}) {
		t.Fatalf("caller got %v, want alice's 183 and 180, 199, 181, carol's 180 and 200", statuses(resps))
	}
	if string(resps[0].Body) != string(answer.Content) || string(resps[5].Body) != string(answer2.Content) {
		t.Errorf("caller got answers %q and %q, want alice's in the 183 and carol's in the 200", resps[0].Body, resps[5].Body)
	}
	if r := sip.Reasons(resps[2]); len(r) != 1 || r[0].Cause != 486 {
		t.Errorf("199 has Reason %v, want SIP cause 486", r)
	}
	if tag := func(m *sip.Message) string {
		to, _ := sip.ParseNameAddr(m.Get("To"))
		v, _ := to.Params.Get("tag")
		return v
	}; tag(resps[1]) == tag(resps[5]) {
		t.Error("carol's 200 came in alice's early dialog")
	}
	if dialogs := out.Dialogs(); len(dialogs) != 2 {
		t.Errorf("caller has %d dialogs, want alice's ended one and carol's", len(dialogs))
	}
	receive(t, got, "INVITE to alice")
	req := receive(t, got, "INVITE to carol")
	want := []string{"<" + aliceURI + "?Reason=SIP%3Bcause%3D486%3Btext%3D%22Busy%20Here%22>;index=1", "<" + carolSIP + ";cause=486>;index=1.1;mp=1"}
	if req.RequestURI != carolSIP+";cause=486" || !reflect.DeepEqual(historyOf(t, req), want) {
		t.Errorf("INVITE to carol has Request-URI %q and History-Info %q; want cause 486 and %q", req.RequestURI, historyOf(t, req), want)
	}
}

// TestDiversionWithoutA199: a caller that does not take 199 never sees
// alice's answer, so carol's is the one it gets, in the 200.
func TestDiversionWithoutA199(t *testing.T) {
	doc := cdiv("5", forwardRule("cfb", "<busy/>", carolTel, ""))
	_, resps, _ := divertedLater(t, doc, "100rel", func(c *sip.IncomingCall) {
		c.Progress(183, answer, sip.HeaderField{Name: "P-Early-Media", Value: "sendrecv"})
		c.Reject(486)
	})
	if !reflect.DeepEqual(statuses(resps), []int{183, 181, 180, 200}) {
		t.Fatalf("caller got %v, want 183, 181, 180 and 200", statuses(resps))
	}
	if len(resps[0].Body) != 0 || resps[0].Get("P-Early-Media") != "" || string(resps[3].Body) != string(answer2.Content) {
		t.Errorf("caller got\n%s\nand\n%s\nwant a 183 without a session and carol's answer in the 200", resps[0], resps[3])
	}
}

func TestCommunicationForwardingNotReachable(t *testing.T) {
	doc := cdiv("5", forwardRule("cfnrc", "<not-reachable/>", carolTel, ""))
	_, resps, got := divertedLater(t, doc, "100rel, 199", func(c *sip.IncomingCall) { c.Reject(503) })
	if !reflect.DeepEqual(statuses(resps), []int{181, 180, 200}) {
		t.Fatalf("caller got %v, want 181, carol's 180 and 200", statuses(resps))
	}
	receive(t, got, "INVITE to alice")
	if req := receive(t, got, "INVITE to carol"); req.RequestURI != carolSIP+";cause=503" {
		t.Errorf("INVITE to carol has Request-URI %q, want cause 503", req.RequestURI)
	}
}

// TestFailureAfterAlertingIsNotNotReachable: a 500 after a 180 tells
// nothing about reachability (TS 24.604 4.5.2.6.6).
func TestFailureAfterAlertingIsNotNotReachable(t *testing.T) {
	doc := cdiv("5", forwardRule("cfnrc", "<not-reachable/>", carolTel, ""))
	_, resps, _ := divertedLater(t, doc, "100rel, 199", func(c *sip.IncomingCall) {
		c.Progress(180, sip.Body{})
		c.Reject(500)
	})
	if !reflect.DeepEqual(statuses(resps), []int{180, 500}) {
		t.Errorf("caller got %v, want alice's 180 and 500", statuses(resps))
	}
}

// handTimer is a no reply timer that the test fires.
type handTimer struct {
	set  chan time.Duration
	fire chan func()
}

func newHandTimer() *handTimer {
	return &handTimer{set: make(chan time.Duration, 1), fire: make(chan func(), 1)}
}

func (h *handTimer) afterFunc(d time.Duration, f func()) func() bool {
	h.set <- d
	h.fire <- f
	return func() bool { return true }
}

func TestCommunicationForwardingNoReply(t *testing.T) {
	timer := newHandTimer()
	hss := &documents{docs: map[string]string{aliceURI: mmtelServices(cdiv("5", forwardRule("cfnr", "<no-answer/>", carolTel, "")))}}
	as := startAS(t, hss, mmtel.Config{AfterFunc: timer.afterFunc})
	ringing := make(chan *sip.IncomingCall, 1)
	next, got := twoParties(t, func(c *sip.IncomingCall) {
		c.Progress(180, sip.Body{})
		ringing <- c
	})
	caller := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(toServedUser(t, as, next, "reg", sip.HeaderField{Name: "Supported", Value: "100rel, 199"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Bye() })
	if r := receive(t, out.Responses(), "alice's 180"); r.StatusCode != 180 {
		t.Fatalf("caller got %d, want alice's 180", r.StatusCode)
	}
	// TS 24.604 4.5.2.6.3 item 2: the first 180 starts the timer, which
	// runs for the user's NoReplyTimer.
	if d := receive(t, timer.set, "no reply timer"); d != 5*time.Second {
		t.Errorf("no reply timer runs for %v, want the user's 5 s", d)
	}
	receive(t, timer.fire, "no reply timer")()
	alice := receive(t, ringing, "call at alice")
	receive(t, alice.Done(), "end of alice's call")
	if cancel := alice.EndedBy(); cancel == nil || cancel.Method != sip.CANCEL || !reflect.DeepEqual(sip.Reasons(cancel), []sip.Reason{sip.NewReason(sip.ReasonSIP, 408, "Request Timeout")}) {
		t.Errorf("alice's call ended by %v, want a CANCEL with Reason SIP cause 408", cancel)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{199, 181, 180, 200}) {
		t.Fatalf("caller got %v after the timer, want 199, 181, carol's 180 and 200", got)
	}
	receive(t, got, "INVITE to alice")
	req := receive(t, got, "INVITE to carol")
	want := []string{"<" + aliceURI + ">;index=1", "<" + carolSIP + ";cause=408>;index=1.1;mp=1"}
	if req.RequestURI != carolSIP+";cause=408" || !reflect.DeepEqual(historyOf(t, req), want) {
		t.Errorf("INVITE to carol has Request-URI %q and History-Info %q; want cause 408 and %q", req.RequestURI, historyOf(t, req), want)
	}
}

// TestNoReplyTimerOnlyWithARule: without a rule for no answer, no timer
// runs.
func TestNoReplyTimerOnlyWithARule(t *testing.T) {
	timer := newHandTimer()
	hss := &documents{docs: map[string]string{aliceURI: mmtelServices(cdiv("5", forwardRule("cfb", "<busy/>", carolTel, "")))}}
	as := startAS(t, hss, mmtel.Config{AfterFunc: timer.afterFunc})
	next, _ := twoParties(t, func(c *sip.IncomingCall) {
		c.Progress(180, sip.Body{})
		c.Accept(answer)
	})
	caller := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(toServedUser(t, as, next, "reg"))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{180, 200}) {
		t.Errorf("caller got %v, want alice's 180 and 200", got)
	}
	out.Bye()
	if len(timer.set) != 0 {
		t.Error("a no reply timer was started")
	}
}
