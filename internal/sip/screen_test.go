package sip

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// screenLog keeps what a Screen was asked about and what reached the
// TU, for tests that read it once the stack has handled everything.
type screenLog struct {
	mu    sync.Mutex
	asked []Method
	tu    []Method
}

func (l *screenLog) ask(req *Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, req.Method)
}

func (l *screenLog) reachTU(req *Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tu = append(l.tu, req.Method)
}

func (l *screenLog) methods() (asked, tu []Method) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.asked, l.tu
}

func refusedCallID(req *Message, _ Destination) bool {
	return !strings.HasPrefix(req.Get("Call-ID"), "refused-")
}

func TestScreenDropsARequestWithoutAWord(t *testing.T) {
	var log screenLog
	var mu sync.Mutex
	var captures []Capture
	tr, err := ListenObserved("127.0.0.1:0", testLogger(t), func(c Capture) {
		mu.Lock()
		defer mu.Unlock()
		captures = append(captures, c)
	})
	if err != nil {
		t.Fatal(err)
	}
	stack, err := NewStack(tr, Config{
		SentBy:  tr.LocalAddr().String(),
		Timers:  testTimers,
		Ordered: true,
		Screen: func(req *Message, source Destination) bool {
			log.ask(req)
			return refusedCallID(req, source)
		},
		OnRequest: func(st *ServerTransaction) {
			log.reachTU(st.Request)
			st.Respond(NewResponse(st.Request, 200, NewTag()))
		},
		OnACK: log.reachTU,
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	peer := newUDPPeer(t, stack.LocalAddr())

	// Unscreened, the INVITE would get a 200 from the TU, the CANCEL a
	// 481 from the stack, the MESSAGE a 200, and the ACK would reach
	// OnACK.
	var refused []*Message
	for _, method := range []Method{INVITE, CANCEL, MESSAGE, ACK} {
		m := peer.request(method)
		m.SetFirst("Call-ID", "refused-"+NewCallID())
		refused = append(refused, m)
	}
	for range 2 {
		for _, m := range refused {
			peer.send(m)
		}
	}
	if got := peer.sync(); len(got) != 0 {
		t.Errorf("stack sent %d messages for refused requests, want none; first: %s", len(got), startLine(got[0]))
	}
	accepted := peer.request(MESSAGE)
	peer.send(accepted)
	if got := peer.read(); got.StatusCode != 200 || got.Get("Call-ID") != accepted.Get("Call-ID") {
		t.Errorf("got %s, want the 200 to the MESSAGE that the screen let in", startLine(got))
	}
	peer.sync()

	asked, tu := log.methods()
	// Each copy of a refused request is asked about: no transaction kept
	// the first one.
	wantAsked := []Method{INVITE, CANCEL, MESSAGE, ACK, INVITE, CANCEL, MESSAGE, ACK, OPTIONS, MESSAGE, OPTIONS}
	if !reflect.DeepEqual(asked, wantAsked) {
		t.Errorf("Screen was asked about %v, want %v", asked, wantAsked)
	}
	if want := []Method{OPTIONS, MESSAGE, OPTIONS}; !reflect.DeepEqual(tu, want) {
		t.Errorf("TU got %v, want %v", tu, want)
	}

	// The capture shows the refused requests arriving and nothing going
	// back, as the wire does.
	mu.Lock()
	defer mu.Unlock()
	received := 0
	for _, c := range captures {
		if !strings.HasPrefix(c.Message.Get("Call-ID"), "refused-") {
			continue
		}
		if c.Sent {
			t.Errorf("capture shows %s sent for a refused request", startLine(c.Message))
		} else {
			received++
		}
	}
	if received != 2*len(refused) {
		t.Errorf("capture shows %d refused requests received, want %d", received, 2*len(refused))
	}
}

// RFC 3261 17.2.3: a CANCEL is a transaction of its own, so the screen
// is asked about it even when the INVITE it cancels was let in. The ACK
// for a 300-699 response is part of the INVITE's transaction, so it is
// not.
func TestScreenIsAskedAboutACANCELButNotAboutTheACKOfAnError(t *testing.T) {
	var log screenLog
	transactions := make(chan *ServerTransaction, 1)
	stack := newStackWith(t, Config{
		Timers:  testTimers,
		Ordered: true,
		Screen: func(req *Message, _ Destination) bool {
			log.ask(req)
			return req.Method != CANCEL && req.Method != ACK
		},
		OnRequest: answerOptions(func(st *ServerTransaction) { transactions <- st }),
	})
	peer := newUDPPeer(t, stack.LocalAddr())

	invite := peer.request(INVITE)
	peer.send(invite)
	var st *ServerTransaction
	select {
	case st = <-transactions:
	case <-time.After(5 * time.Second):
		t.Fatal("the INVITE never reached the TU")
	}
	peer.send(newMatchingRequest(invite, CANCEL, invite.Get("To")))
	for _, m := range peer.sync() {
		if cseq, _ := ParseCSeq(m.Get("CSeq")); cseq.Method == CANCEL {
			t.Errorf("stack answered the refused CANCEL with %s", startLine(m))
		}
	}
	select {
	case <-st.Cancelled():
		t.Error("the refused CANCEL cancelled the INVITE")
	default:
	}

	st.Respond(NewResponse(st.Request, 486, NewTag()))
	busy := peer.read()
	for busy.StatusCode == 100 {
		busy = peer.read()
	}
	if busy.StatusCode != 486 {
		t.Fatalf("got %s, want the 486", startLine(busy))
	}
	peer.send(newMatchingRequest(invite, ACK, busy.Get("To")))
	peer.sync()
	if asked, _ := log.methods(); !reflect.DeepEqual(asked, []Method{INVITE, CANCEL, OPTIONS, OPTIONS}) {
		t.Errorf("Screen was asked about %v, want the INVITE, the CANCEL and the OPTIONS, not the ACK", asked)
	}
}

func TestScreenOfAUAKeepsARequestFromIt(t *testing.T) {
	ua := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls, Screen: refusedCallID}, callee)
	peer := newUDPPeer(t, ua.Stack().LocalAddr())
	// A UA without OnMessage answers a MESSAGE with 405 at once.
	message := peer.request(MESSAGE)
	message.SetFirst("Call-ID", "refused-"+NewCallID())
	peer.send(message)
	if got := peer.sync(); len(got) != 0 {
		t.Errorf("UA answered the refused MESSAGE with %s, want nothing", startLine(got[0]))
	}
}

// Over TCP the screen drops the message and the connection stays: the
// next request on it is answered.
func TestScreenOverTCPKeepsTheConnection(t *testing.T) {
	stack := newStackWith(t, Config{
		Timers:    testTimers,
		Ordered:   true,
		Screen:    refusedCallID,
		OnRequest: func(st *ServerTransaction) { st.Respond(NewResponse(st.Request, 200, NewTag())) },
	})
	peer := dial(t, stack.LocalAddr())
	refused := tcpRequest(peer, REGISTER)
	refused.SetFirst("Call-ID", "refused-"+NewCallID())
	peer.write(t, refused)
	accepted := tcpRequest(peer, REGISTER)
	peer.write(t, accepted)
	got, err := peer.read(t, 2*time.Second)
	if err != nil {
		t.Fatalf("no response on the connection: %v", err)
	}
	if got.StatusCode != 200 || got.Get("Call-ID") != accepted.Get("Call-ID") {
		t.Errorf("first response on the connection is %s for %s, want the 200 to the second REGISTER",
			startLine(got), got.Get("Call-ID"))
	}
}
