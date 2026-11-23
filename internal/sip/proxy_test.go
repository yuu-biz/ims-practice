package sip

import (
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoveFirst(t *testing.T) {
	m := &Message{Method: REGISTER, RequestURI: "sip:" + imsDomain}
	m.Add("Via", "SIP/2.0/UDP a;branch=z9hG4bK1, SIP/2.0/UDP b;branch=z9hG4bK2")
	m.Add("Via", "SIP/2.0/UDP c;branch=z9hG4bK3")
	m.RemoveFirst("v")
	if got := m.GetAll("Via"); len(got) != 2 || got[0] != "SIP/2.0/UDP b;branch=z9hG4bK2" {
		t.Errorf("after one RemoveFirst: %q", got)
	}
	m.RemoveFirst("Via")
	m.RemoveFirst("Via")
	if got := m.GetAll("Via"); len(got) != 0 {
		t.Errorf("after three RemoveFirst: %q", got)
	}
	m.RemoveFirst("Via") // nothing left; must not panic
}

// TestProxyRelaysTheFinalResponse runs UAC -> proxy -> UAS on loopback.
// The UAC must see the UAS's response with only its own Via left.
func TestProxyRelaysTheFinalResponse(t *testing.T) {
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		if n := len(st.Request.GetAll("Via")); n != 2 {
			t.Errorf("UAS got %d Vias, want the UAC's and the proxy's", n)
		}
		st.Respond(NewResponse(st.Request, 200, NewTag()))
	})
	proxy := newStack(t, testTimers, func(st *ServerTransaction) {
		final, err := st.Proxy(st.Request.Clone(), udpTo(uas.LocalAddr()))
		if err != nil {
			t.Error(err)
			return
		}
		st.Respond(final)
	})
	uac := newStack(t, testTimers, ignoreRequests)

	ct, err := uac.SendRequest(registerRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	var final *Message
	for resp := range ct.Responses() {
		final = resp
	}
	if final == nil || final.StatusCode != 200 {
		t.Fatalf("final response = %v, err = %v", final, ct.Err())
	}
	if n := len(final.GetAll("Via")); n != 1 {
		t.Errorf("UAC got %d Vias, want 1", n)
	}
}

func TestProxyReportsATimeoutWhenTheNextHopIsSilent(t *testing.T) {
	silent := newStack(t, testTimers, func(*ServerTransaction) {})
	got := make(chan error, 1)
	proxy := newStack(t, testTimers, func(st *ServerTransaction) {
		_, err := st.Proxy(st.Request.Clone(), udpTo(silent.LocalAddr()))
		got <- err
		st.Respond(NewResponse(st.Request, 504, NewTag()))
	})
	uac := newStack(t, Timers{T1: 200 * time.Millisecond, T2: 4 * time.Second, T4: time.Second}, ignoreRequests)
	if _, err := uac.SendRequest(registerRequest(), udpTo(proxy.LocalAddr())); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if !errors.Is(err, ErrTimeout) {
			t.Errorf("err = %v, want ErrTimeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy never gave up")
	}
}

type proxyResult struct {
	final *Message
	err   error
}

// newInviteProxy runs a proxy stack that forwards every request and
// every ACK for a 2xx to next, and reports what each Proxy call
// returned.
func newInviteProxy(t *testing.T, cfg Config, next netip.AddrPort) (*Stack, <-chan proxyResult) {
	t.Helper()
	results := make(chan proxyResult, 4)
	var proxy atomic.Pointer[Stack]
	cfg.OnRequest = func(st *ServerTransaction) {
		final, err := st.Proxy(st.Request.Clone(), udpTo(next))
		if final != nil {
			st.Respond(final)
		}
		results <- proxyResult{final, err}
	}
	cfg.OnACK = func(ack *Message) {
		if err := proxy.Load().SendACK(ack, udpTo(next)); err != nil {
			t.Error(err)
		}
	}
	s := newStackWith(t, cfg)
	proxy.Store(s)
	return s, results
}

// ackWatch is how long the UAS of a test watches for an ACK through the
// proxy: TestProxyInviteEndToEnd sees the ACK for a 2xx come within it,
// and TestProxyReturnsAFailureForTheTUToSend sees none for a 486.
const ackWatch = 500 * time.Millisecond

func TestProxyInviteEndToEnd(t *testing.T) {
	acks := make(chan *Message, 1)
	uas := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			st.Respond(NewResponse(st.Request, 180, "callee"))
			st.Respond(NewResponse(st.Request, 200, "callee"))
		},
		OnACK: func(ack *Message) { acks <- ack },
	})
	proxy, results := newInviteProxy(t, Config{Timers: testTimers}, uas.LocalAddr())
	uac := newStack(t, testTimers, ignoreRequests)

	ct, err := uac.SendRequest(inviteRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	ringing, ok := next(t, ct), next(t, ct)
	if ringing.StatusCode != 180 || ok.StatusCode != 200 {
		t.Fatalf("UAC got %s and %s, want 180 and 200", startLine(ringing), startLine(ok))
	}
	for _, resp := range []*Message{ringing, ok} {
		if n := len(resp.GetAll("Via")); n != 1 {
			t.Errorf("%d at the UAC has %d Vias, want 1", resp.StatusCode, n)
		}
	}
	if err := uac.SendACK(ackFor2xx(ct.Request, ok), udpTo(proxy.LocalAddr())); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-acks:
		if n := len(ack.GetAll("Via")); n != 2 || ack.Get("CSeq") != "1 ACK" {
			t.Errorf("UAS got an ACK with %d Vias and CSeq %q, want 2 and 1 ACK", n, ack.Get("CSeq"))
		}
	case <-time.After(ackWatch):
		t.Fatalf("the ACK did not reach the UAS within %v", ackWatch)
	}
	// After the 2xx, Proxy waits out Timer M and leaves nothing to send.
	select {
	case r := <-results:
		if r.final != nil || r.err != nil {
			t.Errorf("Proxy returned %v, %v; want nil, nil after a 2xx", r.final, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Proxy never returned")
	}
}

func TestProxyReturnsAFailureForTheTUToSend(t *testing.T) {
	var acks atomic.Int32
	uas := newStackWith(t, Config{
		Timers:    testTimers,
		OnRequest: func(st *ServerTransaction) { st.Respond(NewResponse(st.Request, 486, "callee")) },
		OnACK:     func(*Message) { acks.Add(1) },
	})
	proxy, results := newInviteProxy(t, Config{Timers: testTimers}, uas.LocalAddr())
	uac := newStack(t, testTimers, ignoreRequests)

	ct, err := uac.SendRequest(inviteRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if busy := next(t, ct); busy.StatusCode != 486 || len(busy.GetAll("Via")) != 1 {
		t.Errorf("UAC got %s with Vias %q, want 486 with its own Via", startLine(busy), busy.GetAll("Via"))
	}
	if r := <-results; r.final == nil || r.final.StatusCode != 486 || r.err != nil {
		t.Errorf("Proxy returned %v, %v; want the 486", r.final, r.err)
	}
	// Each hop's client transaction acknowledges the 486, and each
	// server transaction absorbs that ACK. An ACK that went on to the
	// UAS would reach it within ackWatch, as the ACK for a 2xx does.
	time.Sleep(ackWatch)
	if n := acks.Load(); n != 0 {
		t.Errorf("OnACK called %d times at the UAS, want 0", n)
	}
}

func TestProxyPassesOnA2xxRetransmission(t *testing.T) {
	acked := make(chan struct{})
	var once sync.Once
	var sent atomic.Int32
	uas := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			// RFC 6026 8.1: the UAS core retransmits its 2xx until the ACK.
			ok := NewResponse(st.Request, 200, "callee")
			interval := testTimers.T1
			for deadline := time.Now().Add(64 * testTimers.T1); time.Now().Before(deadline); interval = min(2*interval, testTimers.T2) {
				// Counted before it goes out: the ACK for this 200 may
				// come back before this goroutine runs again.
				sent.Add(1)
				st.Respond(ok)
				select {
				case <-acked:
					return
				case <-time.After(interval):
				}
			}
		},
		OnACK: func(*Message) { once.Do(func() { close(acked) }) },
	})
	proxy, _ := newInviteProxy(t, Config{Timers: testTimers}, uas.LocalAddr())
	// The first 200 that the proxy sends back is lost.
	r := newRelay(t, proxy.LocalAddr(), 0, 1)
	uac := newStack(t, testTimers, ignoreRequests)

	start := time.Now()
	ct, err := uac.SendRequest(inviteRequest(), udpTo(r.addr()))
	if err != nil {
		t.Fatal(err)
	}
	ok := next(t, ct)
	if ok.StatusCode != 200 {
		t.Fatalf("UAC got %s, want 200", startLine(ok))
	}
	if elapsed := time.Since(start); elapsed < testTimers.T1 {
		t.Errorf("200 arrived after %v, before the UAS could retransmit it at T1", elapsed)
	}
	if err := uac.SendACK(ackFor2xx(ct.Request, ok), udpTo(r.addr())); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acked:
	case <-time.After(2 * time.Second):
		t.Fatal("the ACK never reached the UAS")
	}
	if n := sent.Load(); n < 2 {
		t.Errorf("UAS sent the 200 %d times, want a retransmission", n)
	}
	if _, responses := r.counts(); responses < 2 {
		t.Errorf("relay saw %d responses, want the lost 200 and its retransmission", responses)
	}
}

func TestProxyCancelsItsBranch(t *testing.T) {
	uasCancelled := make(chan bool, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 180, "callee"))
		select {
		case <-st.Cancelled():
			uasCancelled <- true
			st.Respond(NewResponse(st.Request, 487, "callee"))
		case <-time.After(2 * time.Second):
			uasCancelled <- false
		}
	})
	proxy, results := newInviteProxy(t, Config{Timers: testTimers}, uas.LocalAddr())
	peer := rawSocket(t)
	invite := rawInvite(peer)
	send(t, peer, invite, proxy)
	for {
		m := readDatagram(t, peer)
		if m.StatusCode == 180 {
			break
		}
		if m.StatusCode != 100 {
			t.Fatalf("got %s, want 180", startLine(m))
		}
	}

	send(t, peer, newMatchingRequest(invite, CANCEL, invite.Get("To")), proxy)
	byCSeq := map[string]*Message{}
	for len(byCSeq) < 2 {
		m := readDatagram(t, peer)
		byCSeq[m.Get("CSeq")] = m
	}
	if ok := byCSeq["1 CANCEL"]; ok == nil || ok.StatusCode != 200 {
		t.Errorf("CANCEL got %v, want 200 from the proxy", ok)
	}
	terminated := byCSeq["1 INVITE"]
	if terminated == nil || terminated.StatusCode != 487 {
		t.Fatalf("INVITE got %v, want the UAS's 487", terminated)
	}
	if !<-uasCancelled {
		t.Error("the proxy never cancelled its branch")
	}
	if res := <-results; res.final == nil || res.final.StatusCode != 487 {
		t.Errorf("Proxy returned %v, %v; want the 487", res.final, res.err)
	}
	send(t, peer, newMatchingRequest(invite, ACK, terminated.Get("To")), proxy)
}

// TestProxyCancelsAPendingNonInviteBranch is RFC 3261 16.10 for a
// request other than INVITE: the proxy answers the CANCEL with 200 and
// cancels its branch when that branch is pending, with a provisional
// response and no final one (16.7 step 10), and not when it has had no
// provisional response. Either way the request gets its final response
// as it would without the CANCEL (9.2).
func TestProxyCancelsAPendingNonInviteBranch(t *testing.T) {
	for _, c := range []struct {
		name        string
		provisional bool
	}{{"with a provisional response", true}, {"without one", false}} {
		t.Run(c.name, func(t *testing.T) {
			uasCancelled := make(chan bool, 1)
			uas := newStack(t, testTimers, func(st *ServerTransaction) {
				if c.provisional {
					st.Respond(NewResponse(st.Request, 100, ""))
				}
				select {
				case <-st.Cancelled():
					uasCancelled <- true
				case <-time.After(300 * time.Millisecond):
					uasCancelled <- false
				}
				st.Respond(NewResponse(st.Request, 200, "server"))
			})
			proxy := newStack(t, testTimers, func(st *ServerTransaction) {
				final, err := st.Proxy(st.Request.Clone(), udpTo(uas.LocalAddr()))
				if err != nil {
					t.Error(err)
					return
				}
				st.Respond(final)
			})
			peer := rawSocket(t)
			options := rawInvite(peer)
			options.Method = OPTIONS
			options.Set("CSeq", "1 OPTIONS")
			send(t, peer, options, proxy)
			// Give the branch time to reach the UAS, and its 100 time to
			// come back.
			time.Sleep(50 * time.Millisecond)

			send(t, peer, newMatchingRequest(options, CANCEL, options.Get("To")), proxy)
			byCSeq := map[string]*Message{}
			for len(byCSeq) < 2 {
				m := readDatagram(t, peer)
				if m.StatusCode >= 200 {
					byCSeq[m.Get("CSeq")] = m
				}
			}
			if ok := byCSeq["1 CANCEL"]; ok.StatusCode != 200 {
				t.Errorf("CANCEL got %s, want 200 from the proxy", startLine(ok))
			}
			if final := byCSeq["1 OPTIONS"]; final.StatusCode != 200 {
				t.Errorf("OPTIONS got %s, want the UAS's 200", startLine(final))
			}
			if got := <-uasCancelled; got != c.provisional {
				t.Errorf("the UAS saw a CANCEL: %v, want %v", got, c.provisional)
			}
		})
	}
}

// TestProxyCopiesTheReasonOfTheCANCEL: RFC 3326 2 has a proxy copy the
// Reason of the CANCEL it got into the CANCEL it sends, so that the
// callee learns why the caller gave up, such as the end of a no reply
// timer.
func TestProxyCopiesTheReasonOfTheCANCEL(t *testing.T) {
	reasons := make(chan []string, 1)
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 180, "callee"))
		<-st.Cancelled()
		reasons <- st.CancelRequest().GetAll("Reason")
		st.Respond(NewResponse(st.Request, 487, "callee"))
	})
	proxy, results := newInviteProxy(t, Config{Timers: testTimers}, uas.LocalAddr())
	uac := newStack(t, testTimers, ignoreRequests)
	ct, err := uac.SendRequest(inviteRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if ringing := next(t, ct); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}
	sent := []string{`SIP;cause=408;text="Request Timeout"`, `Q.850;cause=19;text="No answer"`}
	if err := ct.Cancel(HeaderField{Name: "Reason", Value: sent[0]}, HeaderField{Name: "Reason", Value: sent[1]}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-reasons:
		if !slices.Equal(got, sent) {
			t.Errorf("the CANCEL from the proxy has Reason %q, want %q", got, sent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the proxy never cancelled its branch")
	}
	if res := <-results; res.final == nil || res.final.StatusCode != 487 {
		t.Errorf("Proxy returned %v, %v; want the 487", res.final, res.err)
	}
}

func TestProxyTimerCCancelsABranchThatNeverAnswers(t *testing.T) {
	uas := newStack(t, testTimers, func(st *ServerTransaction) {
		st.Respond(NewResponse(st.Request, 180, "callee"))
		<-st.Cancelled()
		st.Respond(NewResponse(st.Request, 487, "callee"))
	})
	timerC := 300 * time.Millisecond
	proxy, _ := newInviteProxy(t, Config{Timers: testTimers, TimerC: timerC}, uas.LocalAddr())
	uac := newStack(t, testTimers, ignoreRequests)

	start := time.Now()
	ct, err := uac.SendRequest(inviteRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	if ringing := next(t, ct); ringing.StatusCode != 180 {
		t.Fatalf("got %s, want 180", startLine(ringing))
	}
	// RFC 3261 16.8: with a provisional response in, Timer C makes the
	// proxy send CANCEL. The 180 may come again before the 487: when the
	// UAC's INVITE goes again before the 180 has reached it, the proxy
	// answers the repeated INVITE with its latest provisional response
	// (17.2.1), which the UAC passes up as well (17.1.1.2).
	terminated := next(t, ct)
	for terminated.StatusCode == 180 {
		terminated = next(t, ct)
	}
	if terminated.StatusCode != 487 {
		t.Fatalf("got %s, want 487", startLine(terminated))
	}
	if elapsed := time.Since(start); elapsed < timerC {
		t.Errorf("487 after %v, before Timer C (%v)", elapsed, timerC)
	}
}

func TestProxyGivesUpABranchThatIgnoresTheCANCEL(t *testing.T) {
	timers := Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}
	timerC := 200 * time.Millisecond
	hop := rawSocket(t)
	proxy, results := newInviteProxy(t, Config{Timers: timers, TimerC: timerC}, addrOf(hop))
	uac := rawSocket(t)

	start := time.Now()
	send(t, uac, rawInvite(uac), proxy)
	req := readDatagram(t, hop)
	if _, err := hop.WriteToUDPAddrPort(NewResponse(req, 180, "callee").Bytes(), proxy.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	// Timer C makes the proxy cancel the ringing branch, which then stays
	// silent.
	for {
		m := readDatagram(t, hop)
		if m.Method == CANCEL {
			break
		}
		if m.Method != INVITE {
			t.Fatalf("next hop got %s, want CANCEL", startLine(m))
		}
	}
	select {
	case r := <-results:
		if r.final != nil || !errors.Is(r.err, ErrTimeout) {
			t.Errorf("Proxy returned %v, %v; want ErrTimeout", r.final, r.err)
		}
		// RFC 3261 9.1: 64*T1 after the CANCEL.
		if elapsed, want := time.Since(start), timerC+64*timers.T1; elapsed < want {
			t.Errorf("Proxy gave up after %v, before %v", elapsed, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Proxy never gave up on the branch")
	}
}

// TestProxyWithRewritesRelayedResponses checks that a TU can change the
// 1xx and the 2xx that the proxy relays by itself, as a P-CSCF does with
// the SDP of an answer.
func TestProxyWithRewritesRelayedResponses(t *testing.T) {
	uas := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			st.Respond(NewResponse(st.Request, 183, "callee"))
			st.Respond(NewResponse(st.Request, 200, "callee"))
		},
		OnACK: func(*Message) {},
	})
	proxy := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			st.ProxyWith(st.Request.Clone(), func(resp *Message) {
				resp.Set("X-Rewritten", strconv.Itoa(resp.StatusCode))
			}, udpTo(uas.LocalAddr()))
		},
		OnACK: func(*Message) {},
	})
	uac := newStack(t, testTimers, ignoreRequests)
	ct, err := uac.SendRequest(inviteRequest(), udpTo(proxy.LocalAddr()))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"183", "200"} {
		if resp := next(t, ct); resp.Get("X-Rewritten") != want {
			t.Errorf("%s reached the UAC without the rewrite", startLine(resp))
		}
	}
}
