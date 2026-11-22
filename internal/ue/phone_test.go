package ue_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// statusJSON is the Status as a controller reads it.
type statusJSON struct {
	IMPU       string `json:"impu"`
	Registered bool   `json:"registered"`
	Call       *struct {
		CallID    string            `json:"call_id"`
		Direction string            `json:"direction"`
		Peer      string            `json:"peer"`
		State     string            `json:"state"`
		Started   time.Time         `json:"started"`
		Session   *ue.SessionStatus `json:"session"`
	} `json:"call"`
	Calls []struct {
		CallID  string            `json:"call_id"`
		State   string            `json:"state"`
		Waiting bool              `json:"waiting"`
		Session *ue.SessionStatus `json:"session"`
	} `json:"calls"`
	AnswerPolicy struct {
		Mode         string   `json:"mode"`
		RingSeconds  *float64 `json:"ring_seconds"`
		RejectStatus int      `json:"reject_status"`
	} `json:"answer_policy"`
	SMS ue.SMSHistory `json:"sms"`
}

func (s statusJSON) callState() string {
	if s.Call == nil {
		return "none"
	}
	return s.Call.Direction + " " + s.Call.State
}

// phone is "ue serve" for one subscriber: a Phone registered through
// the IMS at pcscfAddr, with its control API on url.
type phone struct {
	t   *testing.T
	url string
}

func newPhone(t *testing.T, pcscfAddr netip.AddrPort, private, public, pass string, policy ue.AnswerPolicy) *phone {
	t.Helper()
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	contact := ue.NewContact(addr)
	line := ue.NewLine(true)
	var receiver atomic.Pointer[ue.UE]
	ua, err := sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy:    addr.String(),
		Contact:   contact.URI(),
		OnCall:    line.Ring,
		OnMessage: func(st *sip.ServerTransaction) { receiver.Load().ReceiveSMS(st) },
		Locate: func(sip.URI) (sip.Destination, error) {
			return sip.Destination{Transport: sip.UDP, Addr: pcscfAddr}, nil
		},
	}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	u := ue.New(ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscfAddr, PCSCFURI: "sip:pcscf." + domain + ":5060", Features: ue.DefaultFeatures, SCAddress: scAddress},
		ua.Stack(), contact, quiet())
	receiver.Store(u)
	p := ue.NewPhone(u, ua, line, ue.PhoneConfig{MediaListen: "127.0.0.1:0", Speech: ue.Speech{PCM: ue.Tone(440, 200*time.Millisecond)}, Policy: policy}, quiet())
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- p.Serve(ctx) }()
	srv := httptest.NewServer(p.Handler())
	t.Cleanup(func() {
		stop()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after its context ended")
		}
		srv.Close()
	})
	ph := &phone{t: t, url: srv.URL}
	ph.waitFor("registered", func(s statusJSON) bool { return s.Registered })
	return ph
}

var autoAnswer = ue.AnswerPolicy{Mode: ue.AnswerAuto, Ring: 200 * time.Millisecond}

func (p *phone) status() statusJSON {
	p.t.Helper()
	resp, err := http.Get(p.url + "/status")
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	var s statusJSON
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		p.t.Fatal(err)
	}
	return s
}

// do sends a request with a JSON body and returns the status code and
// the body.
func (p *phone) do(method, path, body string) (int, string) {
	p.t.Helper()
	req, err := http.NewRequest(method, p.url+path, strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	if _, err := bufio.NewReader(resp.Body).WriteTo(&b); err != nil {
		p.t.Fatal(err)
	}
	return resp.StatusCode, b.String()
}

// must sends a request that is to succeed.
func (p *phone) must(method, path, body string) statusJSON {
	p.t.Helper()
	code, text := p.do(method, path, body)
	if code != http.StatusOK {
		p.t.Fatalf("%s %s: %d %s", method, path, code, text)
	}
	var s statusJSON
	if path != "/answer-policy" {
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			p.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return s
}

func (p *phone) wantCode(method, path, body string, want int) {
	p.t.Helper()
	if code, text := p.do(method, path, body); code != want {
		p.t.Errorf("%s %s %s: %d %s, want %d", method, path, body, code, text, want)
	}
}

func (p *phone) waitFor(what string, ok func(statusJSON) bool) statusJSON {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := p.status()
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("never %s; last status %+v, call %s", what, s, s.callState())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *phone) waitCall(want string) statusJSON {
	p.t.Helper()
	return p.waitFor("call "+want, func(s statusJSON) bool { return s.callState() == want })
}

// events reads GET /events and delivers each status it sends.
func (p *phone) events() <-chan statusJSON {
	p.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p.t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		p.t.Fatalf("/events is %s, want text/event-stream", ct)
	}
	out := make(chan statusJSON, 64)
	go func() {
		defer resp.Body.Close()
		lines := bufio.NewScanner(resp.Body)
		for lines.Scan() {
			data, ok := strings.CutPrefix(lines.Text(), "data: ")
			if !ok {
				continue
			}
			var s statusJSON
			if json.Unmarshal([]byte(data), &s) == nil {
				out <- s
			}
		}
	}()
	return out
}

// nextEvent waits for an event that ok accepts, skipping the others.
func nextEvent(t *testing.T, events <-chan statusJSON, what string, ok func(statusJSON) bool) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case s := <-events:
			if ok(s) {
				return
			}
		case <-timeout:
			t.Fatalf("no event with %s", what)
		}
	}
}

func TestServeRegistersAndDeregistersOnRequest(t *testing.T) {
	addr := network(t, time.Second)
	p := newPhone(t, addr, impi, impu, password, autoAnswer)
	if s := p.status(); s.IMPU != impu || s.Call != nil || s.AnswerPolicy.Mode != "auto" {
		t.Errorf("status %+v", s)
	}
	if s := p.must(http.MethodPost, "/deregister", ""); s.Registered {
		t.Error("still registered after POST /deregister")
	}
	p.wantCode(http.MethodPost, "/call", `{"target": "`+impu2+`"}`, http.StatusConflict)
	// Deregistering twice changes nothing.
	p.must(http.MethodPost, "/deregister", "")
	if s := p.must(http.MethodPost, "/register", ""); !s.Registered {
		t.Error("not registered after POST /register")
	}
	// The NOTIFY that ends the old subscription comes after the new
	// registration, and must leave it alone.
	time.Sleep(500 * time.Millisecond)
	if !p.status().Registered {
		t.Error("the end of the old reg event subscription took the new registration away")
	}
}

// waitForUESubscription waits until the S-CSCF has the reg event
// subscription of UE 1 itself, besides that of the P-CSCF. The phone
// reports that it is registered once it has the 200 to its REGISTER,
// and subscribes only after that (TS 24.229 5.1.1.3). A deregistration
// by the network before the S-CSCF has the subscription reaches the UE
// in no NOTIFY: the UE learns of it only at its next refresh.
func waitForUESubscription(t *testing.T, n testNetwork) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, sub := range n.scscfStore.SubscriptionsOf(impi) {
			if sub.Dialog.RemoteURI == impu {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the S-CSCF has no reg event subscription of the UE")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestServeRegistersAgainWhenDeactivated is TS 24.229 5.1.1.7.
func TestServeRegistersAgainWhenDeactivated(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	p := newPhone(t, n.pcscf, impi, impu, password, autoAnswer)
	events := p.events()
	waitForUESubscription(t, n)
	if err := n.scscf.Deregister(impi, reginfo.Deactivated); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, events, "registered false", func(s statusJSON) bool { return !s.Registered })
	nextEvent(t, events, "registered true", func(s statusJSON) bool { return s.Registered })
}

func TestServeStaysDeregisteredWhenRejected(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	p := newPhone(t, n.pcscf, impi, impu, password, autoAnswer)
	waitForUESubscription(t, n)
	if err := n.scscf.Deregister(impi, reginfo.Rejected); err != nil {
		t.Fatal(err)
	}
	p.waitFor("deregistered", func(s statusJSON) bool { return !s.Registered })
	time.Sleep(300 * time.Millisecond)
	if p.status().Registered {
		t.Error("registered again after the network rejected the registration")
	}
}

// TestServeCallsAnotherServe: one "ue serve" calls another, which
// answers by itself; the second call gets 409; the caller hangs up.
func TestServeCallsAnotherServe(t *testing.T) {
	addr := network(t, time.Second)
	caller := newPhone(t, addr, impi, impu, password, autoAnswer)
	callee := newPhone(t, addr, impi2, impu2, password2, autoAnswer)
	calleeEvents := callee.events()

	s := caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)
	if s.Call == nil || s.Call.Direction != "outgoing" || s.Call.Peer != impu2 || s.Call.CallID == "" {
		t.Fatalf("POST /call answered %+v", s.Call)
	}
	callID := s.Call.CallID
	caller.wantCode(http.MethodPost, "/call", `{"target": "`+impu2+`"}`, http.StatusConflict)

	nextEvent(t, calleeEvents, "an incoming call that rings", func(s statusJSON) bool { return s.callState() == "incoming ringing" })
	got := callee.waitCall("incoming active")
	if got.Call.CallID != callID || got.Call.Peer != impu {
		t.Errorf("callee has call %+v, want Call-ID %s from %s", got.Call, callID, impu)
	}
	nextEvent(t, calleeEvents, "the answered call", func(s statusJSON) bool { return s.callState() == "incoming active" })
	caller.waitCall("outgoing active")
	// The call stays up after the audio.
	time.Sleep(500 * time.Millisecond)
	if s := caller.status(); s.callState() != "outgoing active" {
		t.Fatalf("call %s after the audio, want it still active", s.callState())
	}
	caller.wantCode(http.MethodPost, "/call", `{"target": "`+impu2+`"}`, http.StatusConflict)

	caller.must(http.MethodPost, "/hangup", "")
	caller.waitCall("none")
	callee.waitCall("none")
	nextEvent(t, calleeEvents, "the end of the call", func(s statusJSON) bool { return s.Call == nil })
	// The line is free for the next call.
	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)
	callee.waitCall("incoming active")
}

func TestServeAnswersByHandInManualMode(t *testing.T) {
	addr := network(t, time.Second)
	caller := newPhone(t, addr, impi, impu, password, autoAnswer)
	callee := newPhone(t, addr, impi2, impu2, password2, autoAnswer)
	callee.must(http.MethodPut, "/answer-policy", `{"mode": "manual"}`)
	if s := callee.status(); s.AnswerPolicy.Mode != "manual" {
		t.Fatalf("answer policy %+v after PUT", s.AnswerPolicy)
	}
	callee.wantCode(http.MethodPost, "/answer", "", http.StatusConflict)

	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)
	callee.waitCall("incoming ringing")
	caller.waitCall("outgoing ringing")
	time.Sleep(500 * time.Millisecond)
	if s := callee.status(); s.callState() != "incoming ringing" {
		t.Fatalf("call %s without POST /answer, want it ringing", s.callState())
	}
	// Only the callee has a call to answer.
	caller.wantCode(http.MethodPost, "/answer", "", http.StatusConflict)
	callee.must(http.MethodPost, "/answer", "")
	callee.waitCall("incoming active")
	caller.waitCall("outgoing active")
	callee.wantCode(http.MethodPost, "/deregister", "", http.StatusConflict)

	// The callee hangs up with BYE.
	callee.must(http.MethodPost, "/hangup", "")
	callee.waitCall("none")
	caller.waitCall("none")
	callee.wantCode(http.MethodPost, "/hangup", "", http.StatusConflict)
}

// TestServeCallerHangsUpWhileRinging is a CANCEL (RFC 3261 9.1).
func TestServeCallerHangsUpWhileRinging(t *testing.T) {
	addr := network(t, time.Second)
	caller := newPhone(t, addr, impi, impu, password, autoAnswer)
	callee := newPhone(t, addr, impi2, impu2, password2, ue.AnswerPolicy{Mode: ue.AnswerManual})
	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)
	callee.waitCall("incoming ringing")
	caller.must(http.MethodPost, "/hangup", "")
	caller.waitCall("none")
	callee.waitCall("none")
}

// callAndFail calls impu2 from a UE that uses ue.Call, and delivers the
// status code of the final response that the call fails with.
func callAndFail(t *testing.T, addr netip.AddrPort) <-chan int {
	t.Helper()
	p := newParty(t, addr, impi, impu, password)
	status := make(chan int, 1)
	go func() {
		err := p.ue.Call(context.Background(), p.ua, p.reg, impu2, p.media, ue.Speech{}, ue.CallOptions{})
		var failed *ue.CallFailed
		if !errors.As(err, &failed) {
			t.Errorf("call ended with %v, want a failure", err)
			status <- 0
			return
		}
		status <- failed.StatusCode
	}()
	return status
}

func wantFinal(t *testing.T, status <-chan int, want int) {
	t.Helper()
	select {
	case got := <-status:
		if got != want {
			t.Errorf("call failed with %d, want %d", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("no final response, want %d", want)
	}
}

func TestServeDeclinesARingingCall(t *testing.T) {
	addr := network(t, time.Second)
	callee := newPhone(t, addr, impi2, impu2, password2, ue.AnswerPolicy{Mode: ue.AnswerManual})
	status := callAndFail(t, addr)
	callee.waitCall("incoming ringing")
	callee.must(http.MethodPost, "/hangup", "")
	wantFinal(t, status, 603)
	callee.waitCall("none")
}

func TestServeRejectPolicy(t *testing.T) {
	addr := network(t, time.Second)
	callee := newPhone(t, addr, impi2, impu2, password2, autoAnswer)
	callee.must(http.MethodPut, "/answer-policy", `{"mode": "reject", "reject_status": 600}`)
	wantFinal(t, callAndFail(t, addr), 600)
	callee.waitCall("none")
}

func TestServeRefusesBadRequests(t *testing.T) {
	addr := network(t, time.Second)
	p := newPhone(t, addr, impi, impu, password, autoAnswer)
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/call", `{"target": "nobody"}`},
		{http.MethodPost, "/call", `{"target": "` + impu2 + `", "give_up_after_seconds": -1}`},
		{http.MethodPost, "/call", `{"to": "` + impu2 + `"}`},
		{http.MethodPost, "/hangup", `{"reject_status": 200}`},
		{http.MethodPut, "/answer-policy", `{"mode": "sometimes"}`},
		{http.MethodPut, "/answer-policy", `{"mode": "reject", "reject_status": 302}`},
		{http.MethodPut, "/answer-policy", `{"mode": "auto", "ring_seconds": -1}`},
	} {
		p.wantCode(r.method, r.path, r.body, http.StatusBadRequest)
	}
	if s := p.status(); s.Call != nil || s.AnswerPolicy.Mode != "auto" {
		t.Errorf("a bad request changed the status to %+v", s)
	}
}

// TestServeHoldsResumesAndSendsDTMF drives the session of a call over
// the control API: the codec it offers, DTMF, hold and resume.
func TestServeHoldsResumesAndSendsDTMF(t *testing.T) {
	addr := network(t, time.Second)
	caller := newPhone(t, addr, impi, impu, password, autoAnswer)
	callee := newPhone(t, addr, impi2, impu2, password2, autoAnswer)
	caller.wantCode(http.MethodPost, "/call", `{"target": "`+impu2+`", "codecs": ["G729"]}`, http.StatusBadRequest)
	caller.wantCode(http.MethodPost, "/hold", "", http.StatusConflict)
	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`", "codecs": ["PCMU"]}`)
	callee.waitCall("incoming active")
	s := caller.waitCall("outgoing active")
	if c := s.Call.Session.Codec; c == nil || c.Name != "PCMU" || c.TelephoneEvent != 101 {
		t.Fatalf("session %+v, want PCMU with telephone-event 101", s.Call.Session)
	}
	caller.wantCode(http.MethodPost, "/dtmf", `{"digits": "1x"}`, http.StatusBadRequest)
	caller.must(http.MethodPost, "/dtmf", `{"digits": "12"}`)
	callee.waitFor("the digits", func(s statusJSON) bool { return s.Call != nil && s.Call.Session.DTMFReceived == "12" })
	if s := caller.must(http.MethodPost, "/hold", ""); !s.Call.Session.Held || s.Call.Session.Direction != "sendonly" {
		t.Errorf("after POST /hold: %+v", s.Call.Session)
	}
	callee.waitFor("held", func(s statusJSON) bool { return s.Call != nil && s.Call.Session.Direction == "recvonly" })
	if s := caller.must(http.MethodPost, "/resume", ""); s.Call.Session.Held || s.Call.Session.Direction != "sendrecv" {
		t.Errorf("after POST /resume: %+v", s.Call.Session)
	}
	caller.must(http.MethodPost, "/hangup", "")
	caller.waitCall("none")
}

// held tells which calls of s are on hold, in their order.
func (s statusJSON) held() []bool {
	var held []bool
	for _, c := range s.Calls {
		held = append(held, c.Session != nil && c.Session.Held)
	}
	return held
}

// TestServeTwoCalls: a second call goes out once the first is on hold,
// and swap goes between them (TS 24.610). At the callee the second call
// waits behind the first (TS 24.615 4.5.5.3.2), rings without the
// automatic answer, and taking it holds the first (4.5.5.3.3 case A).
// Without a conference there is nothing to merge or to list.
func TestServeTwoCalls(t *testing.T) {
	addr := network(t, time.Second)
	caller := newPhone(t, addr, impi, impu, password, autoAnswer)
	callee := newPhone(t, addr, impi2, impu2, password2, autoAnswer)

	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)
	caller.waitCall("outgoing active")
	callee.waitCall("incoming active")
	caller.wantCode(http.MethodPost, "/merge", "", http.StatusConflict)
	caller.wantCode(http.MethodPost, "/call", `{"target": "`+impu2+`"}`, http.StatusConflict)
	caller.must(http.MethodPost, "/hold", "")
	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)

	callee.waitFor("a waiting call", func(s statusJSON) bool {
		return len(s.Calls) == 2 && s.Calls[1].Waiting && s.Calls[1].State == "ringing"
	})
	time.Sleep(200 * time.Millisecond) // longer than the ring of the automatic answer
	if s := callee.status(); s.Calls[1].State != "ringing" {
		t.Fatalf("the waiting call is %s, want it still ringing", s.Calls[1].State)
	}
	callee.must(http.MethodPost, "/answer", "")
	callee.waitFor("the first call held and the second answered", func(s statusJSON) bool {
		return len(s.Calls) == 2 && s.Calls[1].State == "active" && s.held()[0] && !s.held()[1]
	})
	caller.waitFor("two calls, the first held", func(s statusJSON) bool {
		return len(s.Calls) == 2 && s.Calls[1].State == "active" && s.held()[0] && !s.held()[1]
	})
	caller.must(http.MethodPost, "/swap", "")
	caller.waitFor("the calls swapped", func(s statusJSON) bool { return len(s.Calls) == 2 && !s.held()[0] && s.held()[1] })
	caller.wantCode(http.MethodGet, "/conference", "", http.StatusNotFound)

	first := caller.status().Calls[0].CallID
	caller.must(http.MethodPost, "/hangup", `{"call_id": "`+first+`"}`)
	caller.waitFor("one call left", func(s statusJSON) bool { return len(s.Calls) == 1 && s.Calls[0].CallID != first })
}
