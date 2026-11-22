package b2bua_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file runs a caller, a B2BUA on a b2bua.Call and a callee, each a
// UA of package sip on the loopback interface. The B2BUA sends each
// INVITE it takes on to the callee, as the smallest role on top of the
// Call does, and its Hooks mark what they relay, so that a test sees
// which way a message crossed.

var timers = sip.Timers{T1: 20 * time.Millisecond, T2: 160 * time.Millisecond, T4: 100 * time.Millisecond}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

var (
	offer  = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 1 IN IP4 127.0.0.1\r\n")}
	answer = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 1 IN IP4 127.0.0.1\r\n")}
)

// newUA runs a UA on loopback whose Contact names user at the address
// it listens on.
func newUA(t *testing.T, user string, onCall func(*sip.IncomingCall)) *sip.UA {
	t.Helper()
	return startUA(t, user, timers, nil, onCall)
}

// startUA is newUA with timers of its own, and with w, when it is not
// nil, keeping the requests that the UA receives.
func startUA(t *testing.T, user string, tm sip.Timers, w *wire, onCall func(*sip.IncomingCall)) *sip.UA {
	t.Helper()
	var observe sip.Observer
	if w != nil {
		observe = w.observe
	}
	tl, err := sip.ListenObserved("127.0.0.1:0", discard, observe)
	if err != nil {
		t.Fatal(err)
	}
	ua, err := sip.NewUA(tl, sip.UAConfig{
		SentBy:  tl.LocalAddr().String(),
		Timers:  tm,
		Contact: "sip:" + user + "@" + tl.LocalAddr().String(),
		Allow:   []sip.Method{"INFO"},
		OnCall:  onCall,
	}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua
}

// contactOf is the Contact of ua, which the INVITE of the B2BUA goes to.
func contactOf(t *testing.T, ua *sip.UA) string {
	t.Helper()
	return "sip:callee@" + ua.Stack().LocalAddr().String()
}

// wire keeps the requests that one node received, one for each
// transaction, in the order in which the first copy of each came: a
// client sends a request again over UDP while it waits for the response
// (RFC 3261 17.1.2.2).
type wire struct {
	mu   sync.Mutex
	seen map[string]bool
	got  []*sip.Message
}

func newWire() *wire { return &wire{seen: map[string]bool{}} }

func (w *wire) observe(c sip.Capture) {
	if c.Sent || !c.Message.IsRequest() {
		return
	}
	v, _ := sip.ParseVia(c.Message.Get("Via"))
	key := v.Branch() + " " + string(c.Message.Method)
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.seen[key] {
		w.seen[key] = true
		w.got = append(w.got, c.Message)
	}
}

// requests are the requests received, as "CSeq method", such as
// "2 PRACK", in the order they came; an ACK shows the number of its
// INVITE (RFC 3261 13.2.2.4).
func (w *wire) requests() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, m := range w.got {
		out = append(out, m.Get("CSeq"))
	}
	return out
}

// bodies are the bodies of the requests of method, in the order they
// came.
func (w *wire) bodies(method sip.Method) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, m := range w.got {
		if m.Method == method {
			out = append(out, string(m.Body))
		}
	}
	return out
}

// has reports whether a request of method came.
func (w *wire) has(method sip.Method) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range w.got {
		if m.Method == method {
			return true
		}
	}
	return false
}

// until waits for cond for at most d, and reports whether it held: a
// test that holds a request in the B2BUA lets it go once the next one
// has overtaken it, or after d if that does not happen.
func until(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// way is the Subject that the hooks put in what crosses in d.
func way(d b2bua.Direction) string {
	if d == b2bua.ToCallee {
		return "toward the callee"
	}
	return "toward the caller"
}

// hooks are those of the B2BUA of the tests: every header field but
// those of the dialog stays on its leg, and Subject tells which way the
// message crossed. request, when set, may refuse a request or hold it
// in the B2BUA. own, when set, is what the role does on the call of its
// own before it sends the INVITE on, such as sending the caller a 1xx
// (b2bua.Call.Progress). noLeg, when set, has the role send no INVITE
// toward the callee (b2bua.Call.NoLeg) and reject the caller's INVITE
// once that channel is closed.
type hooks struct {
	request  func(req *sip.Message, d b2bua.Direction) *b2bua.Refusal
	response func(resp *sip.Message, d b2bua.Direction)
	ack      func(ack *sip.Message, d b2bua.Direction)
	own      func(c *b2bua.Call)
	noLeg    chan struct{}

	mu    sync.Mutex
	kept  int
	ended []*sip.Message
}

func (h *hooks) Request(req *sip.Message, d b2bua.Direction) (b2bua.Message, *b2bua.Refusal) {
	if h.request != nil {
		if r := h.request(req, d); r != nil {
			return b2bua.Message{}, r
		}
	}
	return b2bua.Message{Headers: []sip.HeaderField{{Name: "Subject", Value: way(d)}}, Body: sip.BodyOf(req)}, nil
}

func (h *hooks) Response(resp, _ *sip.Message, d b2bua.Direction) b2bua.Message {
	if h.response != nil {
		h.response(resp, d)
	}
	return b2bua.Message{Headers: []sip.HeaderField{{Name: "Subject", Value: way(d)}}, Body: sip.BodyOf(resp)}
}

func (h *hooks) Answer(resp, _ *sip.Message) b2bua.Message {
	return h.Response(resp, nil, b2bua.ToCaller)
}

func (h *hooks) Ack(ack *sip.Message, d b2bua.Direction) sip.Body {
	if h.ack != nil {
		h.ack(ack, d)
	}
	return sip.BodyOf(ack)
}

func (h *hooks) Contact(*sip.Message) (sip.NameAddr, bool, bool) {
	return sip.NameAddr{}, false, false
}

func (h *hooks) Reasons(ended *sip.Message, _ b2bua.Direction) []sip.HeaderField {
	h.mu.Lock()
	h.ended = append(h.ended, ended)
	h.mu.Unlock()
	return sip.ReasonFields(ended)
}

func (h *hooks) KeepDialogs(in, out sip.Dialog) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kept++
}

func (h *hooks) keptCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.kept
}

// newB2BUA runs a B2BUA that sends each call it takes on to callee,
// through a Call with h.
func newB2BUA(t *testing.T, h *hooks, callee string) *sip.UA {
	t.Helper()
	return startB2BUA(t, h, callee, timers)
}

// startB2BUA is newB2BUA with timers of its own.
func startB2BUA(t *testing.T, h *hooks, callee string, tm sip.Timers) *sip.UA {
	t.Helper()
	var ua *sip.UA
	started := make(chan struct{})
	ua = startUA(t, "b2bua", tm, nil, func(in *sip.IncomingCall) {
		<-started
		c := b2bua.New(in, h, discard)
		if h.own != nil {
			h.own(c)
		}
		if h.noLeg != nil {
			c.NoLeg()
			<-h.noLeg
			in.Reject(480)
			return
		}
		from, _ := sip.ParseNameAddr(in.Request.Get("From"))
		out, err := c.Attempt(ua, sip.Invitation{RequestURI: callee, From: from.URI, To: callee, Body: sip.BodyOf(in.Request)})
		if err != nil {
			c.NoLeg()
			in.Reject(500)
			return
		}
		var final *sip.Message
		for resp := range out.Responses() {
			switch {
			case resp.StatusCode < 200:
				c.RelayProvisional(out, resp)
			case resp.StatusCode < 300:
				c.Answer(out, resp)
				return
			default:
				final = resp
			}
		}
		if final != nil {
			in.Reject(final.StatusCode)
		}
	})
	close(started)
	return ua
}

// invitation calls the B2BUA, which sends the call on.
func invitation(t *testing.T, b2b *sip.UA, onRequest func(*sip.InDialogRequest)) sip.Invitation {
	t.Helper()
	return sip.Invitation{RequestURI: "sip:b2bua@" + b2b.Stack().LocalAddr().String(), From: "sip:caller@127.0.0.1", Body: offer, OnRequest: onRequest}
}

// within waits for what ch delivers.
func within[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
	}
	var zero T
	return zero
}

// finalOf reads the responses of an outgoing call to its final one.
func finalOf(t *testing.T, c *sip.OutgoingCall) (provisional []*sip.Message, final *sip.Message) {
	t.Helper()
	for {
		select {
		case resp, ok := <-c.Responses():
			if !ok {
				t.Fatalf("no final response: %v", c.Err())
			}
			if resp.StatusCode >= 200 {
				return provisional, resp
			}
			provisional = append(provisional, resp)
		case <-time.After(5 * time.Second):
			t.Fatal("no final response in time")
		}
	}
}
