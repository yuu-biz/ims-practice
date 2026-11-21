package ue_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// handTimers is a UAConfig.AfterFunc whose timers the test fires by
// hand, instead of waiting half an hour for a session refresh.
type handTimers struct {
	mu      sync.Mutex
	pending map[*func()]time.Duration
}

func (h *handTimers) afterFunc(d time.Duration, f func()) func() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil {
		h.pending = map[*func()]time.Duration{}
	}
	key := &f
	h.pending[key] = d
	return func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		_, ok := h.pending[key]
		delete(h.pending, key)
		return ok
	}
}

// fire runs the pending timer of d, once one is there.
func (h *handTimers) fire(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		for key, pd := range h.pending {
			if pd == d {
				delete(h.pending, key)
				h.mu.Unlock()
				(*key)()
				return
			}
		}
		h.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no timer of %v", d)
}

// TestSessionRefreshByUPDATE is IR.92 2.2.8 and RFC 4028: the caller
// asks for 1800 s, the callee makes the caller the refresher, and half
// an interval later the caller refreshes with an UPDATE without a body,
// which the callee accepts.
func TestSessionRefreshByUPDATE(t *testing.T) {
	addr := network(t, time.Second)
	timers := &handTimers{}
	caller := newPartyTweaked(t, addr, impi, impu, password, ue.CallConfig{}, func(c *sip.UAConfig) { c.AfterFunc = timers.afterFunc })
	callee := newParty(t, addr, impi2, impu2, password2)
	answered := answerIn(t, callee, ue.Speech{}, ue.AnswerOptions{PickUp: time.After(0)})
	ctx, hangUp := context.WithCancel(context.Background())
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.Speech{}, ue.CallOptions{StayUp: true})
	}()
	caller.wire.find(t, "> ACK")
	timers.fire(t, ue.SessionInterval/2)
	refresh := callee.wire.find(t, "< UPDATE")
	if len(refresh.Body) != 0 || refresh.Get("Session-Expires") != "1800;refresher=uac" || refresh.Get("User-Agent") != ue.UserAgent {
		t.Errorf("refresh with %d octets of body, Session-Expires %q, User-Agent %q", len(refresh.Body), refresh.Get("Session-Expires"), refresh.Get("User-Agent"))
	}
	if ok := caller.wire.find(t, "< 200 UPDATE"); ok.Get("Server") != ue.UserAgent {
		t.Errorf("200 to the refresh: Server %q", ok.Get("Server"))
	}
	hangUp()
	if err := wait(t, "the call", called); err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	wait(t, "the answer", answered)
	if slices.Contains(caller.wire.labels(), "< 488 UPDATE") {
		t.Error("the refresh was refused")
	}
}
