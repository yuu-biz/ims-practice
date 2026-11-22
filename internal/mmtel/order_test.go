package mmtel_test

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file shows that the requests of the caller within the dialog
// reach the callee in the order they came, the PRACK of a reliable
// provisional response among them: the callee holds the PRACK of its
// reliable 183 while it watches for the UPDATE that the caller sent
// right after it, and the UPDATE comes only once the PRACK has its
// answer.
//
// The AS has no step of its own where a test can hold a request back,
// so what the order of the two requests at the callee shows here is
// that the AS relays both and keeps their order. That a request waits
// for the one before it however long the B2BUA takes to prepare it is
// shown in internal/b2bua.

// overtake is how long the callee holds a request while it watches for
// the one after it.
const overtake = 500 * time.Millisecond

// came tells whether cond held within d.
func came(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// After an INVITE without an offer, the callee offers in its reliable
// 183 and the caller answers in the PRACK (RFC 3262 5); the caller
// sends an UPDATE of its own right after it, without waiting for the
// answer to the PRACK. The callee gets the two in that order: a UA
// takes the requests within a dialog in the order of their CSeq numbers
// (RFC 3261 12.2.2), and an UPDATE that overtook the PRACK would carry
// an offer that crosses the answer in it (RFC 3264).
func TestRequestsOfTheCallerReachTheCalleeInTheOrderTheyCame(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	var mu sync.Mutex
	var got []sip.Method
	has := func(m sip.Method) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(got, m)
	}
	var overtaken atomic.Bool
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) {
			mu.Lock()
			got = append(got, r.Request.Method)
			mu.Unlock()
			if r.Request.Method != sip.PRACK {
				r.Accept(held)
				return
			}
			if came(overtake, func() bool { return has(sip.UPDATE) }) {
				overtaken.Store(true)
			}
			r.Accept(sip.Body{})
		})
		c.Progress(183, answer)
		c.Accept(sip.Body{})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", sip.Body{}))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Bye()
	first := receive(t, out.Responses(), "the 183 with the callee's offer")
	if first.StatusCode != 183 || string(first.Body) != string(answer.Content) {
		t.Fatalf("the caller got\n%s\nwant the 183 with the callee's offer", first)
	}
	pracked := make(chan *sip.Message, 1)
	go func() {
		resp, err := out.PRACK(first, offer)
		if err != nil {
			t.Errorf("the PRACK with the answer: %v", err)
		}
		pracked <- resp
	}()
	// The UPDATE goes once the AS has sent the PRACK on, so that the two
	// are at the AS together.
	if !came(10*time.Second, func() bool { return has(sip.PRACK) }) {
		t.Fatal("the PRACK did not reach the callee")
	}
	resp, err := out.Update(hold)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(held.Content) {
		t.Fatalf("the UPDATE got %v, %v; want the callee's 200 with its answer", resp, err)
	}
	if resp := receive(t, pracked, "the answer to the PRACK"); resp == nil || resp.StatusCode != 200 {
		t.Errorf("the PRACK got %v, want 200", resp)
	}
	if overtaken.Load() {
		t.Error("the UPDATE reached the callee while the PRACK before it had no answer")
	}
	mu.Lock()
	received := slices.Clone(got)
	mu.Unlock()
	if want := []sip.Method{sip.PRACK, sip.UPDATE}; !slices.Equal(received, want) {
		t.Errorf("the callee received %v, want %v", received, want)
	}
}
