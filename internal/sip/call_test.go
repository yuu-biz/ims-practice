package sip

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// forwarded is what a routing proxy noted about a request it forwarded.
type forwarded struct {
	method Method
	cseq   string
	rack   string
	// popped tells that the top Route named the proxy, so that the
	// request came by the route set (RFC 3261 16.4).
	popped bool
}

type forwardLog struct {
	mu   sync.Mutex
	list []forwarded
}

func (l *forwardLog) add(f forwarded) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, f)
}

func (l *forwardLog) get() []forwarded {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]forwarded(nil), l.list...)
}

// newRoutingProxy runs a stateful proxy that routes the way a CSCF does
// (RFC 3261 16.4, 16.6): it drops its own Route, sends to the top Route
// or else the Request-URI, and record-routes each initial INVITE. ACKs
// for a 2xx go the same way.
func newRoutingProxy(t *testing.T) (*Stack, URI, *forwardLog) {
	t.Helper()
	log := &forwardLog{}
	var proxy atomic.Pointer[Stack]
	own := func() URI {
		return URI{Scheme: "sip", Host: "127.0.0.1", Port: int(proxy.Load().LocalAddr().Port())}
	}
	route := func(req *Message) Destination {
		_, popped := PopOwnRoute(req, own())
		log.add(forwarded{method: req.Method, cseq: req.Get("CSeq"), rack: req.Get("RAck"), popped: popped})
		if err := DecrementMaxForwards(req); err != nil {
			t.Error(err)
		}
		hop, err := NextHop(req)
		if err != nil {
			t.Error(err)
		}
		addr, err := locateIPAddress(hop)
		if err != nil {
			t.Error(err)
		}
		return addr
	}
	s := newStackWith(t, Config{
		Timers: testTimers,
		OnRequest: func(st *ServerTransaction) {
			req := st.Request.Clone()
			next := route(req)
			if req.Method == INVITE && tagOf(req.Get("To")) == "" {
				AddRecordRoute(req, own())
			}
			final, err := st.Proxy(req, next)
			if err != nil {
				t.Error(err)
			}
			if final != nil {
				st.Respond(final)
			}
		},
		OnACK: func(ack *Message) {
			ack = ack.Clone()
			// A caller sends its ACK again for each 200 the callee sends
			// again (RFC 3261 13.2.2.4), and the last one can come in
			// while the test closes the proxy.
			if err := proxy.Load().SendACK(ack, route(ack)); err != nil && !errors.Is(err, ErrStackClosed) {
				t.Error(err)
			}
		},
	})
	proxy.Store(s)
	return s, own(), log
}

// TestCallThroughAProxy is the call of TS 24.229 in small: a reliable
// 183 with the SDP answer, its PRACK, the 200 and its ACK, and the BYE,
// with a proxy in the middle that record-routes. Every request after
// the INVITE has to come through the proxy by the route set.
func TestCallThroughAProxy(t *testing.T) {
	_, proxyURI, fwd := newRoutingProxy(t)
	progress := make(chan error, 1)
	acks := make(chan accepted, 1)
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		calls <- c
		progress <- c.Progress(183, answer)
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)

	inv := invitation(calleeUA)
	// The preloaded route, as an IMS UE has its P-CSCF in Route.
	inv.Route = []string{"<" + proxyURI.String() + ";lr>"}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{183, 200}) {
		t.Fatalf("caller got %v, want 183 and 200", codes(got))
	}
	if err := <-progress; err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if a := <-acks; a.err != nil {
		t.Fatalf("Accept: %v", a.err)
	}
	in := <-calls

	wantRoute := []string{proxyURI.String() + ";lr"}
	uac, _ := out.Dialog()
	uas, _ := in.Dialog()
	if !reflect.DeepEqual(uac.RouteSet, wantRoute) || !reflect.DeepEqual(uas.RouteSet, wantRoute) {
		t.Errorf("route sets are %q at the caller and %q at the callee, want %q at both", uac.RouteSet, uas.RouteSet, wantRoute)
	}

	if err := out.Bye(); err != nil {
		t.Fatalf("Bye: %v", err)
	}
	waitDone(t, in.Done(), "callee")

	rseq := got[0].Get("RSeq")
	want := []forwarded{
		{method: INVITE, cseq: "1 INVITE", popped: true},
		{method: PRACK, cseq: "2 PRACK", rack: rseq + " 1 INVITE", popped: true},
		{method: ACK, cseq: "1 ACK", popped: true},
		{method: BYE, cseq: "3 BYE", popped: true},
	}
	if got := fwd.get(); !reflect.DeepEqual(got, want) {
		t.Errorf("proxy forwarded\n%+v\nwant\n%+v", got, want)
	}
}

func TestCalleeHangsUpThroughAProxy(t *testing.T) {
	_, proxyURI, fwd := newRoutingProxy(t)
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		if _, err := c.Accept(answer); err != nil {
			t.Error(err)
		}
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.Route = []string{"<" + proxyURI.String() + ";lr>"}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	responses(t, out)
	in := <-calls

	// RFC 3261 12.1.1: the callee's route set is the Record-Route in
	// order, and its BYE goes to the caller's Contact through the proxy.
	if err := in.Bye(); err != nil {
		t.Fatalf("Bye: %v", err)
	}
	waitDone(t, out.Done(), "caller")
	// The BYE need not be the last thing the proxy forwards: when the
	// callee sends its 200 again before the ACK has come (RFC 3261
	// 13.3.1.4), the caller's second ACK may pass the proxy after it.
	list := fwd.get()
	if !slices.Contains(list, forwarded{method: BYE, cseq: "1 BYE", popped: true}) {
		t.Errorf("proxy forwarded %+v, want the callee's BYE by the route set among them", list)
	}
}
