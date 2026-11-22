package sip

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
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

// admitQuestion is a request that a test's Config.Admit holds until the
// test answers.
type admitQuestion struct {
	req    *Message
	answer chan bool
}

// heldAdmit is a Config.Admit that hands each request of the methods
// in held to the test on questions and waits for its answer. It lets
// any other request in at once, among them the OPTIONS of
// udpPeer.sync, and refuses what is still held when the test ends.
func heldAdmit(t *testing.T, questions chan<- admitQuestion, held ...Method) func(*Message, Destination) bool {
	return func(req *Message, _ Destination) bool {
		if !slices.Contains(held, req.Method) {
			return true
		}
		q := admitQuestion{req: req, answer: make(chan bool)}
		select {
		case questions <- q:
		case <-t.Context().Done():
			return false
		}
		select {
		case ok := <-q.answer:
			return ok
		case <-t.Context().Done():
			return false
		}
	}
}

func nextQuestion(t *testing.T, questions <-chan admitQuestion) admitQuestion {
	t.Helper()
	select {
	case q := <-questions:
		return q
	case <-time.After(5 * time.Second):
		t.Fatal("Admit was asked about nothing more")
		return admitQuestion{}
	}
}

func nextTransaction(t *testing.T, transactions <-chan *ServerTransaction) *ServerTransaction {
	t.Helper()
	select {
	case st := <-transactions:
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("the request let in did not reach the TU")
		return nil
	}
}

// Config.Admit decides off the loop: while it holds an INVITE, the
// stack answers an OPTIONS from another source. An INVITE it refuses
// leaves nothing behind, so the next copy is asked about again. A copy
// let in starts the transaction, and a copy let in after it is a
// retransmission of that transaction.
func TestAdmitDecidesWhileTheStackGoesOn(t *testing.T) {
	questions := make(chan admitQuestion)
	transactions := make(chan *ServerTransaction, 2)
	stack := newStackWith(t, Config{
		Timers:    testTimers,
		Admit:     heldAdmit(t, questions, INVITE),
		OnRequest: answerOptions(func(st *ServerTransaction) { transactions <- st }),
	})
	peer := newUDPPeer(t, stack.LocalAddr())
	other := newUDPPeer(t, stack.LocalAddr())
	invite := peer.request(INVITE)

	peer.send(invite)
	q := nextQuestion(t, questions)
	if got := other.sync(); len(got) != 0 {
		t.Errorf("stack sent %s to the other source, want only the 200 to its OPTIONS", startLine(got[0]))
	}
	expectSilence(t, peer.conn, 50*time.Millisecond, "the stack sends nothing while Admit holds the INVITE")
	q.answer <- false
	peer.send(invite)
	nextQuestion(t, questions).answer <- false

	peer.send(invite)
	peer.send(invite)
	// Both copies wait for Admit once the stack has answered this
	// OPTIONS. Otherwise the second could arrive after the first had
	// started the transaction, and match it without Admit.
	other.sync()
	nextQuestion(t, questions).answer <- true
	st := nextTransaction(t, transactions)
	st.Respond(NewResponse(st.Request, 180, NewTag()))
	if got := peer.read(); got.StatusCode != 180 {
		t.Fatalf("got %s, want the 180 of the TU", startLine(got))
	}
	nextQuestion(t, questions).answer <- true
	// RFC 3261 17.2.1: a retransmission gets the last provisional
	// response again.
	if got := peer.read(); got.StatusCode != 180 {
		t.Fatalf("got %s, want the 180 again for the copy let in later", startLine(got))
	}
	st.Respond(NewResponse(st.Request, 486, NewTag()))
	if got := peer.read(); got.StatusCode != 486 {
		t.Errorf("got %s, want the 486 of the TU", startLine(got))
	}
	if len(transactions) != 0 {
		t.Errorf("the TU got %d more transactions for the INVITE, want none", len(transactions))
	}
}

// RFC 3261 9.2: a CANCEL cancels the INVITE server transaction that it
// matches. Admit decides on the CANCEL only after the INVITE that came
// before it from the same source, so the CANCEL finds the transaction
// even when Admit takes longer over the INVITE: the TU sees Cancelled,
// and the caller gets 200 for the CANCEL and then the 487 of the TU.
func TestAdmitLetsACANCELInAfterItsINVITE(t *testing.T) {
	questions := make(chan admitQuestion)
	transactions := make(chan *ServerTransaction, 1)
	stack := newStackWith(t, Config{
		Timers:    testTimers,
		Admit:     heldAdmit(t, questions, INVITE),
		OnRequest: answerOptions(func(st *ServerTransaction) { transactions <- st }),
	})
	peer := newUDPPeer(t, stack.LocalAddr())
	invite := peer.request(INVITE)

	peer.send(invite)
	q := nextQuestion(t, questions)
	peer.send(newMatchingRequest(invite, CANCEL, invite.Get("To")))
	// The stack reads one socket in order, so it has the CANCEL in hand
	// once it answers this OPTIONS.
	newUDPPeer(t, stack.LocalAddr()).sync()
	q.answer <- true
	st := nextTransaction(t, transactions)

	answer := peer.read()
	for answer.StatusCode == 100 {
		answer = peer.read()
	}
	if answer.StatusCode != 200 || answer.Get("CSeq") != "1 CANCEL" {
		t.Fatalf("got %s for %q, want the 200 for the CANCEL", startLine(answer), answer.Get("CSeq"))
	}
	select {
	case <-st.Cancelled():
	case <-time.After(5 * time.Second):
		t.Fatal("the TU of the INVITE never saw Cancelled")
	}
	st.Respond(NewResponse(st.Request, 487, NewTag()))
	terminated := peer.read()
	for terminated.StatusCode == 100 {
		terminated = peer.read()
	}
	if terminated.StatusCode != 487 || terminated.Get("CSeq") != "1 INVITE" {
		t.Fatalf("got %s for %q, want the 487 for the INVITE", startLine(terminated), terminated.Get("CSeq"))
	}
	peer.send(newMatchingRequest(invite, ACK, terminated.Get("To")))
}

// Admit decides on the requests from one source one at a time, in the
// order they arrived. A request it refuses holds up none after it.
func TestAdmitTakesTheRequestsOfOneSourceInTurn(t *testing.T) {
	questions := make(chan admitQuestion)
	held := heldAdmit(t, questions, INVITE, CANCEL, MESSAGE, ACK)
	// most is the most requests from peer that Admit has decided on at
	// once.
	var mu sync.Mutex
	var deciding, most int
	stack := newStackWith(t, Config{
		Timers: testTimers,
		Admit: func(req *Message, source Destination) bool {
			if req.Method == OPTIONS {
				return true
			}
			mu.Lock()
			deciding++
			most = max(most, deciding)
			mu.Unlock()
			defer func() {
				mu.Lock()
				defer mu.Unlock()
				deciding--
			}()
			return held(req, source)
		},
		OnRequest: answerOptions(func(st *ServerTransaction) { t.Errorf("TU got %s", st.Request.Method) }),
	})
	peer := newUDPPeer(t, stack.LocalAddr())
	other := newUDPPeer(t, stack.LocalAddr())
	invite := peer.request(INVITE)
	sent := []*Message{invite, newMatchingRequest(invite, CANCEL, invite.Get("To")), peer.request(MESSAGE), peer.request(ACK)}
	for _, m := range sent {
		peer.send(m)
	}

	var asked []Method
	for range sent {
		// The stack has read every request from peer by the time it
		// answers this OPTIONS, so all of them could be asked about.
		other.sync()
		q := nextQuestion(t, questions)
		asked = append(asked, q.req.Method)
		q.answer <- false
	}
	if want := []Method{INVITE, CANCEL, MESSAGE, ACK}; !reflect.DeepEqual(asked, want) {
		t.Errorf("Admit was asked about %v, want %v, the order they arrived in", asked, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 1 {
		t.Errorf("Admit decided on %d requests from the source at once, want one at a time", most)
	}
	if got := peer.sync(); len(got) != 0 {
		t.Errorf("stack sent %d messages for refused requests, want none; first: %s", len(got), startLine(got[0]))
	}
}

// Admit is asked about every request that Screen lets in, CANCEL and
// ACK included, and only about those. What it refuses is dropped as
// what Screen refuses: nothing goes back and nothing reaches the TU.
func TestAdmitDropsARequestWithoutAWord(t *testing.T) {
	var log screenLog
	decided := make(chan struct{}, 16)
	stack := newStackWith(t, Config{
		Timers: testTimers,
		Screen: func(req *Message, _ Destination) bool { return !strings.HasPrefix(req.Get("Call-ID"), "screened-") },
		Admit: func(req *Message, source Destination) bool {
			if req.Method == OPTIONS {
				return true
			}
			log.ask(req)
			defer func() { decided <- struct{}{} }()
			return refusedCallID(req, source)
		},
		OnRequest: answerOptions(func(st *ServerTransaction) {
			log.reachTU(st.Request)
			st.Respond(NewResponse(st.Request, 200, NewTag()))
		}),
		OnACK: log.reachTU,
	})
	peer := newUDPPeer(t, stack.LocalAddr())

	var refused []*Message
	for _, method := range []Method{INVITE, CANCEL, MESSAGE, ACK} {
		m := peer.request(method)
		m.SetFirst("Call-ID", "refused-"+NewCallID())
		refused = append(refused, m)
	}
	screened := peer.request(MESSAGE)
	screened.SetFirst("Call-ID", "screened-"+NewCallID())
	for range 2 {
		for _, m := range refused {
			peer.send(m)
		}
		peer.send(screened)
	}
	for range 2 * len(refused) {
		select {
		case <-decided:
		case <-time.After(5 * time.Second):
			t.Fatal("Admit was not asked about every copy of the requests that Screen let in")
		}
	}
	if got := peer.sync(); len(got) != 0 {
		t.Errorf("stack sent %d messages for refused requests, want none; first: %s", len(got), startLine(got[0]))
	}

	asked, tu := log.methods()
	slices.Sort(asked)
	want := []Method{ACK, ACK, CANCEL, CANCEL, INVITE, INVITE, MESSAGE, MESSAGE}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("Admit was asked about %v, want each copy of the requests that Screen let in: %v", asked, want)
	}
	if len(tu) != 0 {
		t.Errorf("TU got %v, want nothing", tu)
	}
}

// Close returns only once the call of Admit under way has returned, so
// that nothing reads a store that the node closes after the stack.
// Admit is asked about nothing after the stack has stopped: the request
// that waited behind the one under way is dropped with the stack.
func TestCloseWaitsForAdmit(t *testing.T) {
	questions := make(chan admitQuestion)
	held := heldAdmit(t, questions, INVITE, MESSAGE)
	var mu sync.Mutex
	var asked []Method
	stack := newStackWith(t, Config{
		Timers: testTimers,
		Admit: func(req *Message, source Destination) bool {
			if req.Method != OPTIONS {
				mu.Lock()
				asked = append(asked, req.Method)
				mu.Unlock()
			}
			return held(req, source)
		},
		OnRequest: answerOptions(func(st *ServerTransaction) { t.Errorf("TU got %s", st.Request.Method) }),
	})
	peer := newUDPPeer(t, stack.LocalAddr())
	peer.send(peer.request(INVITE))
	q := nextQuestion(t, questions)
	peer.send(peer.request(MESSAGE))
	// The stack reads one socket in order, so it has the MESSAGE in hand
	// once it answers this OPTIONS.
	newUDPPeer(t, stack.LocalAddr()).sync()

	closed := make(chan struct{})
	go func() {
		stack.Close()
		close(closed)
	}()
	// Close stops the loop first, so that the answer below cannot get
	// the stack to ask Admit about the MESSAGE.
	select {
	case <-stack.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop of the stack did not stop")
	}
	select {
	case <-closed:
		t.Error("Close returned while Admit was deciding on the INVITE")
	case <-time.After(100 * time.Millisecond):
	}
	q.answer <- true
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after Admit did")
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []Method{INVITE}; !reflect.DeepEqual(asked, want) {
		t.Errorf("Admit was asked about %v, want %v, the request it held when the stack stopped", asked, want)
	}
}

// At most maxAdmitQueue requests from one source wait for Admit, the
// one it is deciding on included, and the stack drops any more without
// a word. Once Admit has decided on one of them, there is room for the
// next request from the source.
func TestAdmitBoundsTheRequestsThatWaitFromOneSource(t *testing.T) {
	questions := make(chan admitQuestion)
	stack := newStackWith(t, Config{
		Timers:    testTimers,
		Admit:     heldAdmit(t, questions, MESSAGE),
		OnRequest: answerOptions(func(st *ServerTransaction) { t.Errorf("TU got %s", st.Request.Method) }),
	})
	peer := newUDPPeer(t, stack.LocalAddr())
	other := newUDPPeer(t, stack.LocalAddr())
	var sent []*Message
	for range maxAdmitQueue + 2 {
		m := peer.request(MESSAGE)
		sent = append(sent, m)
		peer.send(m)
	}
	// The stack has read every MESSAGE from peer once it answers this
	// OPTIONS.
	other.sync()
	nextQuestion(t, questions).answer <- false
	// Admit is asked about the next MESSAGE only after the stack has
	// taken up the answer about the first, which frees a place.
	q := nextQuestion(t, questions)
	late := peer.request(MESSAGE)
	peer.send(late)
	other.sync()

	index := map[string]int{late.Get("Call-ID"): len(sent)}
	for i, m := range sent {
		index[m.Get("Call-ID")] = i
	}
	var asked []int
	for range maxAdmitQueue {
		asked = append(asked, index[q.req.Get("Call-ID")])
		q.answer <- false
		if q.req.Get("Call-ID") == late.Get("Call-ID") {
			break
		}
		q = nextQuestion(t, questions)
	}
	var want []int
	for i := 1; i < maxAdmitQueue; i++ {
		want = append(want, i)
	}
	want = append(want, len(sent))
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("after the first MESSAGE, Admit was asked about the requests %v, want %v: those that waited, then the one sent later", asked, want)
	}
	if got := peer.sync(); len(got) != 0 {
		t.Errorf("stack sent %d messages for refused and dropped requests, want none; first: %s", len(got), startLine(got[0]))
	}
}

// The stack logs a request that Screen refuses, and leaves one that
// Admit refuses to Admit, which knows why, so that each discard is
// logged once.
func TestAdmitLogsItsOwnRefusals(t *testing.T) {
	var mu sync.Mutex
	var logs bytes.Buffer
	tr := listenLayer(t, testLogger(t))
	stack, err := NewStack(tr, Config{
		SentBy:    tr.LocalAddr().String(),
		Timers:    testTimers,
		Screen:    func(req *Message, _ Destination) bool { return !strings.HasPrefix(req.Get("Call-ID"), "screened-") },
		Admit:     refusedCallID,
		OnRequest: answerOptions(func(st *ServerTransaction) { t.Errorf("TU got %s", st.Request.Method) }),
	}, slog.New(slog.NewJSONHandler(lockedWriter{&mu, &logs}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	peer := newUDPPeer(t, stack.LocalAddr())
	screened := peer.request(MESSAGE)
	screened.SetFirst("Call-ID", "screened-"+NewCallID())
	refused := peer.request(INVITE)
	refused.SetFirst("Call-ID", "refused-"+NewCallID())
	peer.send(screened)
	peer.send(refused)
	// Admit lets this OPTIONS in only after it has refused the INVITE.
	peer.sync()

	mu.Lock()
	defer mu.Unlock()
	var dropped []string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry struct{ Msg, Method string }
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if strings.HasPrefix(entry.Msg, "dropped request") {
			dropped = append(dropped, entry.Method+": "+entry.Msg)
		}
	}
	if want := []string{"MESSAGE: dropped request that the screen refused"}; !reflect.DeepEqual(dropped, want) {
		t.Errorf("the stack logged %q, want %q: nothing for the INVITE that Admit refused", dropped, want)
	}
}
