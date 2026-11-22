package h248

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/h248/h248test"
)

// The addresses of the controller and the media gateway in the tests
// (RFC 5737), both on the port for the text encoding.
const (
	controllerAddr = "192.0.2.1:2944"
	gatewayAddr    = "192.0.2.2:2944"
)

// testbed is a controller and a media gateway on an h248test.Network
// with an h248test.Clock.
type testbed struct {
	clock      *h248test.Clock
	network    *h248test.Network
	controller *Endpoint
	gateway    *Endpoint
}

// newTestbed starts the two Endpoints. The configurations need only
// what a test changes: the MIds, the clock and the first transaction
// IDs are filled in.
func newTestbed(t *testing.T, controller, gateway EndpointConfig) *testbed {
	t.Helper()
	b := &testbed{clock: h248test.NewClock()}
	b.network = h248test.NewNetwork(b.clock)
	controller.MId, controller.Clock, controller.FirstTransactionID = mgc, b.clock, 1
	gateway.MId, gateway.Clock, gateway.FirstTransactionID = mg, b.clock, 5001
	b.controller = b.start(t, controllerAddr, controller)
	b.gateway = b.start(t, gatewayAddr, gateway)
	return b
}

func (b *testbed) start(t *testing.T, addr string, config EndpointConfig) *Endpoint {
	t.Helper()
	e, err := NewEndpoint(b.network.Listen(addr), config)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- e.Serve() }()
	t.Cleanup(func() {
		e.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve returned %v", err)
		}
	})
	return e
}

// request sends a request from the controller to the media gateway in
// a goroutine of its own.
func (b *testbed) request(ctx context.Context, actions []ActionRequest) <-chan outgoingResult {
	result := make(chan outgoingResult, 1)
	go func() {
		reply, err := b.controller.Request(ctx, h248test.Addr(gatewayAddr), actions)
		result <- outgoingResult{reply: reply, err: err}
	}()
	return result
}

// inject puts a datagram on the network as if from would have sent it.
func (b *testbed) inject(from, to, text string) {
	b.network.Deliver(h248test.Datagram{From: from, To: to, Payload: []byte(text)})
}

// reserve asks for a context with one termination on the access side
// (Reserve AGW Connection Point, TS 29.334 5.17.2.2).
func reserve() []ActionRequest {
	return []ActionRequest{{
		ContextID: ContextChoose,
		Commands: []CommandRequest{{
			Command:       Add,
			TerminationID: "ip/1/access/$",
			Media: &MediaDescriptor{OneStream: &StreamParms{
				Local: &LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\nb=AS:80\n"},
			}},
		}},
	}}
}

// reserved is the reply of the media gateway to reserve.
func reserved(context ContextID) TransactionReply {
	return TransactionReply{Actions: []ActionReply{{
		ContextID: context,
		Commands: []CommandReply{{
			Command:       Add,
			TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{OneStream: &StreamParms{
				Local: &LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 192.0.2.20\nm=audio 40000 RTP/AVP 0\nb=AS:80\n"},
			}},
		}},
	}}}
}

// countingHandler answers every request with reserved and counts how
// often it ran for each transaction ID.
type countingHandler struct {
	mu    sync.Mutex
	calls map[uint32]int
}

func (h *countingHandler) handle(ctx context.Context, r *IncomingRequest) TransactionReply {
	h.mu.Lock()
	if h.calls == nil {
		h.calls = map[uint32]int{}
	}
	h.calls[r.TransactionID]++
	h.mu.Unlock()
	return reserved(7)
}

func (h *countingHandler) count(id uint32) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[id]
}

func (h *countingHandler) total() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		n += c
	}
	return n
}

// wait returns the result of a request. Real time bounds the wait in
// case the Endpoint hangs; the timers of the Endpoint do not depend on
// it.
func wait(t *testing.T, result <-chan outgoingResult) outgoingResult {
	t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the request did not end")
		return outgoingResult{}
	}
}

// advanceUntil moves the clock in steps until the request ends.
func (b *testbed) advanceUntil(t *testing.T, result <-chan outgoingResult, step time.Duration) outgoingResult {
	t.Helper()
	for range 100000 {
		select {
		case r := <-result:
			return r
		default:
		}
		b.network.Settle(t)
		b.clock.Advance(step)
	}
	t.Fatal("the request did not end")
	return outgoingResult{}
}

// parsed reads a datagram back as a message.
func parsed(t *testing.T, d h248test.Datagram) Message {
	t.Helper()
	m, err := Parse(d.Payload)
	if err != nil {
		t.Fatalf("datagram from %s does not parse: %v\n%s", d.From, err, d.Payload)
	}
	return m
}

// kinds names the transactions of each datagram, as "Transaction=1" or
// "Reply=1", with "lost:" in front of the ones the network lost.
func kinds(t *testing.T, ds []h248test.Datagram) []string {
	t.Helper()
	var out []string
	for _, d := range ds {
		var names []string
		for _, tr := range parsed(t, d).Transactions {
			switch tr := tr.(type) {
			case TransactionRequest:
				names = append(names, fmt.Sprintf("Transaction=%d", tr.TransactionID))
			case TransactionReply:
				name := fmt.Sprintf("Reply=%d", tr.TransactionID)
				if tr.ImmAckRequired {
					name += "(ImmAckRequired)"
				}
				if tr.Error != nil {
					name += fmt.Sprintf("(Error=%d)", tr.Error.ErrorCode)
				}
				names = append(names, name)
			case TransactionPending:
				names = append(names, fmt.Sprintf("Pending=%d", tr.TransactionID))
			case TransactionResponseAck:
				names = append(names, fmt.Sprintf("TransactionResponseAck=%v", tr.Acks))
			}
		}
		name := strings.Join(names, ",")
		if d.Lost {
			name = "lost:" + name
		}
		out = append(out, name)
	}
	return out
}

func checkKinds(t *testing.T, ds []h248test.Datagram, want ...string) {
	t.Helper()
	got := kinds(t, ds)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("datagrams\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// loseFirst loses the first datagram that from sends.
func loseFirst(from string) func(d h248test.Datagram) bool {
	var lost atomic.Bool
	return func(d h248test.Datagram) bool {
		return d.From == from && lost.CompareAndSwap(false, true)
	}
}

func TestRequestAndReply(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})

	r := wait(t, b.request(context.Background(), reserve()))
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.reply.TransactionID != 1 || len(r.reply.Actions) != 1 || r.reply.Actions[0].ContextID != 7 {
		t.Errorf("reply %+v", r.reply)
	}
	ds := b.network.Datagrams()
	checkKinds(t, ds, "Transaction=1", "Reply=1")
	if ds[0].From != controllerAddr || ds[0].To != gatewayAddr || ds[1].From != gatewayAddr || ds[1].To != controllerAddr {
		t.Errorf("the reply does not go back to where the request came from: %+v", ds)
	}
	if !strings.HasPrefix(string(ds[0].Payload), "MEGACO/2 <pcscf1.ims.example.net>\n") ||
		!strings.HasPrefix(string(ds[1].Payload), "MEGACO/2 <agw1.ims.example.net>\n") {
		t.Errorf("headers:\n%s\n%s", ds[0].Payload, ds[1].Payload)
	}
	b.network.Settle(t)
	if n := b.controller.outgoingCount.Load(); n != 0 {
		t.Errorf("the controller still holds %d requests", n)
	}
}

func TestLostRequestIsRepeated(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	b.network.SetLose(loseFirst(controllerAddr))

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 1)
	b.clock.Advance(199 * time.Millisecond)
	if n := len(b.network.Datagrams()); n != 1 {
		t.Fatalf("the request was repeated before the timer expired: %d datagrams", n)
	}
	b.clock.Advance(time.Millisecond)
	r := wait(t, result)
	if r.err != nil {
		t.Fatal(r.err)
	}
	ds := b.network.Datagrams()
	checkKinds(t, ds, "lost:Transaction=1", "Transaction=1", "Reply=1")
	if string(ds[0].Payload) != string(ds[1].Payload) {
		t.Errorf("the repetition differs from the request:\n%s\n%s", ds[0].Payload, ds[1].Payload)
	}
	if got := ds[1].At.Sub(ds[0].At); got != 200*time.Millisecond {
		t.Errorf("repeated after %v, want 200ms", got)
	}
	if h.count(1) != 1 {
		t.Errorf("the handler ran %d times", h.count(1))
	}
}

func TestLostReplyIsSentAgainFromWhatIsKept(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	b.network.SetLose(loseFirst(gatewayAddr))

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 2)
	b.clock.Advance(200 * time.Millisecond)
	r := wait(t, result)
	if r.err != nil {
		t.Fatal(r.err)
	}
	ds := b.network.Datagrams()
	checkKinds(t, ds, "Transaction=1", "lost:Reply=1", "Transaction=1", "Reply=1")
	if string(ds[1].Payload) != string(ds[3].Payload) {
		t.Errorf("the second reply differs from the first:\n%s\n%s", ds[1].Payload, ds[3].Payload)
	}
	if h.count(1) != 1 {
		t.Errorf("the handler ran %d times, want once (at most once, RFC 3525 D.1.1)", h.count(1))
	}
}

func TestRetransmissionBacksOffAndGivesUp(t *testing.T) {
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{})
	b.network.SetLose(func(d h248test.Datagram) bool { return true })

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 1)
	r := b.advanceUntil(t, result, 50*time.Millisecond)
	if !errors.Is(r.err, ErrNoReply) {
		t.Fatalf("Request returned %v, want ErrNoReply", r.err)
	}
	ds := b.network.Datagrams()
	if len(ds) < 2 || len(ds) > 1+10 {
		t.Fatalf("%d transmissions, want 2 to 11", len(ds))
	}
	// Annex D.1.3: the first timer is the initial one, then AAD
	// doubles up to the bound, and each wait lies between 0.5 AAD
	// and AAD.
	aad := 200 * time.Millisecond
	for i := 1; i < len(ds); i++ {
		gap := ds[i].At.Sub(ds[i-1].At)
		low, high := aad/2, aad
		if i == 1 {
			low = aad
		}
		if gap < low || gap > high {
			t.Errorf("transmission %d came %v after the one before, want %v to %v", i, gap, low, high)
		}
		aad = min(2*aad, 4*time.Second)
	}
	if last := ds[len(ds)-1].At.Sub(ds[0].At); last > 25*time.Second {
		t.Errorf("the last repetition came %v after the request, after T-MAX", last)
	}
}

func TestGiveUpAfterMaxRetransmissions(t *testing.T) {
	b := newTestbed(t, EndpointConfig{Timers: Timers{MaxRetransmissions: 2}}, EndpointConfig{})
	b.network.SetLose(func(d h248test.Datagram) bool { return true })

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 1)
	b.clock.Advance(10 * time.Second)
	if r := wait(t, result); !errors.Is(r.err, ErrNoReply) {
		t.Fatalf("Request returned %v, want ErrNoReply", r.err)
	}
	checkKinds(t, b.network.Datagrams(), "lost:Transaction=1", "lost:Transaction=1", "lost:Transaction=1")
}

func TestGiveUpAfterTMax(t *testing.T) {
	b := newTestbed(t, EndpointConfig{Timers: Timers{TMax: time.Second, LongTimer: 2 * time.Second}}, EndpointConfig{})
	b.network.SetLose(func(d h248test.Datagram) bool { return true })

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 1)
	b.clock.Advance(10 * time.Second)
	if r := wait(t, result); !errors.Is(r.err, ErrNoReply) {
		t.Fatalf("Request returned %v, want ErrNoReply", r.err)
	}
	ds := b.network.Datagrams()
	for _, d := range ds {
		if d.At.Sub(ds[0].At) > time.Second {
			t.Errorf("a repetition went out %v after the request, after T-MAX", d.At.Sub(ds[0].At))
		}
	}
	if len(ds) < 3 {
		t.Errorf("only %d transmissions within T-MAX", len(ds))
	}
}

func TestReplyIsForgottenAfterLongTimer(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	if r := wait(t, b.request(context.Background(), reserve())); r.err != nil {
		t.Fatal(r.err)
	}
	request := string(b.network.Datagrams()[0].Payload)

	// Within LONG-TIMER a copy of the request gets the kept reply.
	b.clock.Advance(29 * time.Second)
	b.inject(controllerAddr, gatewayAddr, request)
	b.network.WaitDatagrams(t, 4)
	if h.count(1) != 1 {
		t.Fatalf("the handler ran %d times", h.count(1))
	}

	// After LONG-TIMER the gateway has forgotten the transaction, and
	// a copy is a new request (Annex D.1.2.2).
	b.clock.Advance(time.Second)
	b.network.Settle(t)
	if n := b.gateway.incomingCount.Load(); n != 0 {
		t.Fatalf("the gateway still holds %d transactions", n)
	}
	b.inject(controllerAddr, gatewayAddr, request)
	b.network.WaitDatagrams(t, 6)
	if h.count(1) != 2 {
		t.Errorf("the handler ran %d times", h.count(1))
	}
	checkKinds(t, b.network.Datagrams(), "Transaction=1", "Reply=1", "Transaction=1", "Reply=1", "Transaction=1", "Reply=1")
}

func TestSecondCopyOfAReplyIsIgnored(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	if r := wait(t, b.request(context.Background(), reserve())); r.err != nil {
		t.Fatal(r.err)
	}
	reply := string(b.network.Datagrams()[1].Payload)
	b.inject(gatewayAddr, controllerAddr, reply)
	b.network.Settle(t)

	r := wait(t, b.request(context.Background(), reserve()))
	if r.err != nil || r.reply.TransactionID != 2 {
		t.Fatalf("the next request got %+v, %v", r.reply, r.err)
	}
	checkKinds(t, b.network.Datagrams(), "Transaction=1", "Reply=1", "Reply=1", "Transaction=2", "Reply=2")
}

// blockingHandler holds every request until release is closed.
type blockingHandler struct {
	release   chan struct{}
	calls     atomic.Int32
	cancelled atomic.Int32
	// pendingAtOnce makes the handler call Pending as soon as it
	// starts.
	pendingAtOnce bool
}

func newBlockingHandler() *blockingHandler {
	return &blockingHandler{release: make(chan struct{})}
}

func (h *blockingHandler) handle(ctx context.Context, r *IncomingRequest) TransactionReply {
	h.calls.Add(1)
	if h.pendingAtOnce {
		r.Pending()
	}
	select {
	case <-h.release:
	case <-ctx.Done():
		h.cancelled.Add(1)
	}
	return reserved(7)
}

func (h *blockingHandler) waitStarted(t *testing.T, n int32) {
	t.Helper()
	h248test.Eventually(t, "the handler to start", func() bool { return h.calls.Load() == n })
}

func TestRepetitionWhileExecutingGetsPending(t *testing.T) {
	h := newBlockingHandler()
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{
		Handler: h.handle,
		// Long enough that only the repetition causes a Pending.
		Timers: Timers{ProvisionalResponseTimerValue: 10 * time.Second},
	})

	result := b.request(context.Background(), reserve())
	h.waitStarted(t, 1)
	b.clock.Advance(200 * time.Millisecond)
	b.network.WaitDatagrams(t, 3)
	b.network.Settle(t)
	close(h.release)
	if r := wait(t, result); r.err != nil {
		t.Fatal(r.err)
	}
	b.network.WaitDatagrams(t, 5)
	checkKinds(t, b.network.Datagrams(),
		"Transaction=1", "Transaction=1", "Pending=1", "Reply=1(ImmAckRequired)", "TransactionResponseAck=[{1 0}]")
	if h.calls.Load() != 1 {
		t.Errorf("the handler ran %d times", h.calls.Load())
	}
}

func TestPendingHoldsBackRepetition(t *testing.T) {
	h := newBlockingHandler()
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})

	result := b.request(context.Background(), reserve())
	h.waitStarted(t, 1)
	// The handler runs for 1 s, five times the initial retransmission
	// timer. The Pendings every 100 ms keep the controller from
	// repeating the request.
	for range 10 {
		b.network.Settle(t)
		b.clock.Advance(100 * time.Millisecond)
	}
	b.network.Settle(t)
	close(h.release)
	r := wait(t, result)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !r.reply.ImmAckRequired {
		t.Error("the reply after Pendings does not ask for an immediate confirmation")
	}
	b.network.WaitDatagrams(t, 13)
	want := []string{"Transaction=1"}
	for range 10 {
		want = append(want, "Pending=1")
	}
	want = append(want, "Reply=1(ImmAckRequired)", "TransactionResponseAck=[{1 0}]")
	checkKinds(t, b.network.Datagrams(), want...)

	// The gateway has dropped the confirmed reply: a late copy of the
	// request gets no answer and is not carried out (Annex D.1.2.2).
	b.network.Settle(t)
	b.inject(controllerAddr, gatewayAddr, string(b.network.Datagrams()[0].Payload))
	b.network.Settle(t)
	if n := len(b.network.Datagrams()); n != 14 {
		t.Errorf("%d datagrams, want no answer to the late copy", n)
	}
	if h.calls.Load() != 1 {
		t.Errorf("the handler ran %d times", h.calls.Load())
	}
}

func TestHandlerSendsPendingAtOnce(t *testing.T) {
	h := newBlockingHandler()
	h.pendingAtOnce = true
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 2)
	b.network.Settle(t)
	close(h.release)
	if r := wait(t, result); r.err != nil {
		t.Fatal(r.err)
	}
	b.network.WaitDatagrams(t, 4)
	checkKinds(t, b.network.Datagrams(),
		"Transaction=1", "Pending=1", "Reply=1(ImmAckRequired)", "TransactionResponseAck=[{1 0}]")
}

func TestRequestIsRepeatedWhenPendingsStop(t *testing.T) {
	h := newBlockingHandler()
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	var pendings atomic.Int32
	b.network.SetLose(func(d h248test.Datagram) bool {
		return d.From == gatewayAddr && strings.Contains(string(d.Payload), "Pending") && pendings.Add(1) > 1
	})

	result := b.request(context.Background(), reserve())
	h.waitStarted(t, 1)
	for range 4 {
		b.network.Settle(t)
		b.clock.Advance(100 * time.Millisecond)
	}
	b.network.Settle(t)
	ds := b.network.Datagrams()
	// The Pending at 100 ms sets the timer to the provisional timer of
	// the peer plus the initial retransmission timer, 300 ms.
	var repeated []time.Duration
	for _, d := range ds[1:] {
		if d.From == controllerAddr {
			repeated = append(repeated, d.At.Sub(ds[0].At))
		}
	}
	if len(repeated) != 1 || repeated[0] != 400*time.Millisecond {
		t.Errorf("repetitions at %v, want one at 400ms", repeated)
	}
	close(h.release)
	if r := wait(t, result); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestPendingLimitEndsTheTransaction(t *testing.T) {
	// The handler carries out the first of two Modifies, and is still
	// at the second when the Endpoint stops it.
	var cause atomic.Value
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Timers: Timers{PendingLimit: 2}, Handler: func(ctx context.Context, r *IncomingRequest) TransactionReply {
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		a := r.Actions[0]
		return TransactionReply{Actions: []ActionReply{{ContextID: a.ContextID, Commands: []CommandReply{
			{Command: Modify, TerminationID: a.Commands[0].TerminationID},
			{Command: Modify, TerminationID: a.Commands[1].TerminationID, Error: &ErrorDescriptor{ErrorCode: 506}},
		}}}}
	}})

	result := b.request(context.Background(), []ActionRequest{{ContextID: 7, Commands: []CommandRequest{
		{Command: Modify, TerminationID: "ip/1/access/7"},
		{Command: Modify, TerminationID: "ip/1/core/7"},
	}}})
	b.network.WaitDatagrams(t, 1)
	for range 3 {
		b.network.Settle(t)
		b.clock.Advance(100 * time.Millisecond)
	}
	r := wait(t, result)
	if r.err != nil {
		t.Fatal(r.err)
	}
	// H.248.1 (05/2002) 8.2.3: the reply holds the error for the
	// command that took too long, after the replies before it.
	if got := answered(Message{Transactions: []Transaction{r.reply}}); got != "Reply=1{Context=7{Modify=ip/1/access/7,Modify=ip/1/core/7}}" ||
		r.reply.Actions[0].Commands[1].Error == nil || r.reply.Actions[0].Commands[1].Error.ErrorCode != 506 {
		t.Fatalf("reply %+v, want error 506 for the second Modify", r.reply)
	}
	if got, _ := cause.Load().(error); !errors.Is(got, ErrPendingLimitReached) {
		t.Errorf("the handler was stopped with %v", got)
	}
	b.network.WaitDatagrams(t, 5)
	checkKinds(t, b.network.Datagrams(),
		"Transaction=1", "Pending=1", "Pending=1", "Reply=1(ImmAckRequired)", "TransactionResponseAck=[{1 0}]")
}

func TestPeerPendingLimit(t *testing.T) {
	h := newBlockingHandler()
	defer close(h.release)
	b := newTestbed(t, EndpointConfig{Timers: Timers{PeerPendingLimit: 2}}, EndpointConfig{Handler: h.handle})

	result := b.request(context.Background(), reserve())
	h.waitStarted(t, 1)
	for range 3 {
		b.network.Settle(t)
		b.clock.Advance(100 * time.Millisecond)
	}
	if r := wait(t, result); !errors.Is(r.err, ErrPendingLimitExceeded) {
		t.Fatalf("Request returned %v, want ErrPendingLimitExceeded", r.err)
	}
}

func TestResponseAckDropsKeptReplies(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	for range 3 {
		if r := wait(t, b.request(context.Background(), reserve())); r.err != nil {
			t.Fatal(r.err)
		}
	}
	var requests []string
	for _, d := range b.network.Datagrams() {
		if d.From == controllerAddr {
			requests = append(requests, string(d.Payload))
		}
	}
	// The MId is spelt in other case; it names the same controller.
	b.inject(controllerAddr, gatewayAddr, "MEGACO/2 <PCSCF1.ims.example.net>\nTransactionResponseAck { 1-2, 3 }\n")
	b.network.Settle(t)
	for _, request := range requests {
		b.inject(controllerAddr, gatewayAddr, request)
	}
	b.network.Settle(t)
	if n := len(b.network.Datagrams()); n != 6+1+3 {
		t.Errorf("%d datagrams, want no answers to the copies", n)
	}
	if h.total() != 3 {
		t.Errorf("the handler ran %d times", h.total())
	}
}

func TestConcurrentTransactions(t *testing.T) {
	var h countingHandler
	handler := func(ctx context.Context, r *IncomingRequest) TransactionReply {
		h.handle(ctx, r)
		// The reply names the context that the request named, so that
		// each requester can check it got its own reply.
		return reserved(r.Actions[0].ContextID)
	}
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: handler})
	var n atomic.Int64
	b.network.SetLose(func(d h248test.Datagram) bool { return n.Add(1)%3 == 0 })

	const count = 50
	results := make([]<-chan outgoingResult, count)
	for i := range count {
		actions := reserve()
		actions[0].ContextID = ContextID(100 + i)
		results[i] = b.request(context.Background(), actions)
	}
	b.network.WaitDatagrams(t, count)
	for i := range count {
		r := b.advanceUntil(t, results[i], 100*time.Millisecond)
		if r.err != nil {
			t.Fatalf("request %d: %v", i, r.err)
		}
		if got := r.reply.Actions[0].ContextID; got != ContextID(100+i) {
			t.Errorf("request %d got the reply for context %d", i, got)
		}
	}
	if h.total() != count {
		t.Errorf("the handler ran %d times for %d requests", h.total(), count)
	}
	for id := range uint32(count) {
		if c := h.count(1 + id); c != 1 {
			t.Errorf("transaction %d ran %d times", 1+id, c)
		}
	}
}

func TestTooManyTransactions(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	gateway := EndpointConfig{
		MaxTransactions: 1,
		Timers:          Timers{TMax: 400 * time.Millisecond, LongTimer: 500 * time.Millisecond},
		Handler: func(ctx context.Context, r *IncomingRequest) TransactionReply {
			calls.Add(1)
			<-release
			return reserved(7)
		},
	}
	b := newTestbed(t, EndpointConfig{MaxTransactions: 2}, gateway)

	first := b.request(context.Background(), reserve())
	h248test.Eventually(t, "the handler to start", func() bool { return calls.Load() == 1 })
	// The gateway has no room for a second request and drops it.
	second := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 2)
	b.network.Settle(t)
	if calls.Load() != 1 {
		t.Errorf("the handler ran for a request beyond MaxTransactions")
	}
	// The controller has no room for a third.
	if _, err := b.controller.Request(context.Background(), h248test.Addr(gatewayAddr), reserve()); !errors.Is(err, ErrTooManyTransactions) {
		t.Errorf("a third request returned %v, want ErrTooManyTransactions", err)
	}

	// The reply to the first takes the room until LONG-TIMER has
	// passed. Then a repetition of the second is carried out.
	close(release)
	if r := wait(t, first); r.err != nil {
		t.Fatal(r.err)
	}
	if r := b.advanceUntil(t, second, 100*time.Millisecond); r.err != nil {
		t.Fatal(r.err)
	}
	if calls.Load() != 2 {
		t.Errorf("the handler ran %d times", calls.Load())
	}
}

func TestMessageTooLong(t *testing.T) {
	long := strings.Repeat("a=label:x\n", 200)
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: func(ctx context.Context, r *IncomingRequest) TransactionReply {
		reply := reserved(7)
		reply.Actions[0].Commands[0].Media.OneStream.Local.SDP += long
		return reply
	}})

	// A request that does not fit is not sent.
	actions := reserve()
	actions[0].Commands[0].Media.OneStream.Local.SDP += long
	if _, err := b.controller.Request(context.Background(), h248test.Addr(gatewayAddr), actions); !errors.Is(err, ErrMessageTooLong) {
		t.Fatalf("Request returned %v, want ErrMessageTooLong", err)
	}
	if n := len(b.network.Datagrams()); n != 0 {
		t.Fatalf("%d datagrams went out", n)
	}

	// A reply that does not fit becomes error 533 in place of the
	// command reply that is too long (H.248.8 4.2.66).
	r := wait(t, b.request(context.Background(), reserve()))
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.reply.Error != nil || len(r.reply.Actions) != 1 || len(r.reply.Actions[0].Commands) != 0 ||
		r.reply.Actions[0].ContextID != 7 || r.reply.Actions[0].Error == nil || r.reply.Actions[0].Error.ErrorCode != 533 {
		t.Errorf("reply %+v, want error 533 for the action of context 7", r.reply)
	}
	for _, d := range b.network.Datagrams() {
		if len(d.Payload) > 1452 {
			t.Errorf("a datagram of %d octets went out", len(d.Payload))
		}
	}
	if n := b.controller.outgoingCount.Load(); n != 0 {
		t.Errorf("the controller still holds %d requests", n)
	}
}

func TestOverflowKeepsTheRepliesThatFit(t *testing.T) {
	long := strings.Repeat("a=label:x\n", 25)
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: func(ctx context.Context, r *IncomingRequest) TransactionReply {
		// Two contexts of two terminations each. The second
		// termination of the second context does not fit any more.
		reply := TransactionReply{}
		for _, context := range []ContextID{7, 8} {
			a := ActionReply{ContextID: context}
			for _, id := range []TerminationID{"ip/1/access/11", "ip/1/core/12"} {
				c := reserved(context).Actions[0].Commands[0]
				c.TerminationID = id
				c.Media.OneStream.Local.SDP += long
				a.Commands = append(a.Commands, c)
			}
			reply.Actions = append(reply.Actions, a)
		}
		return reply
	}})

	r := wait(t, b.request(context.Background(), reserve()))
	if r.err != nil {
		t.Fatal(r.err)
	}
	a := r.reply.Actions
	if r.reply.Error != nil || len(a) != 2 || len(a[0].Commands) != 2 || a[0].Error != nil ||
		len(a[1].Commands) != 1 || a[1].Commands[0].TerminationID != "ip/1/access/11" ||
		a[1].Error == nil || a[1].Error.ErrorCode != 533 {
		t.Errorf("reply %+v, want three command replies and error 533 in the second action", r.reply)
	}
	if d := b.network.Datagrams()[1]; len(d.Payload) > 1452 {
		t.Errorf("a datagram of %d octets went out", len(d.Payload))
	}
}

func TestRequestEndsWithItsContext(t *testing.T) {
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{})
	b.network.SetLose(func(d h248test.Datagram) bool { return true })

	ctx, cancel := context.WithCancel(context.Background())
	result := b.request(ctx, reserve())
	b.network.WaitDatagrams(t, 1)
	cancel()
	if r := wait(t, result); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("Request returned %v", r.err)
	}
	b.clock.Advance(time.Minute)
	if n := len(b.network.Datagrams()); n != 1 {
		t.Errorf("the request was repeated after its context ended: %d datagrams", n)
	}
}

func TestCloseEndsRequests(t *testing.T) {
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{})
	b.network.SetLose(func(d h248test.Datagram) bool { return true })

	result := b.request(context.Background(), reserve())
	b.network.WaitDatagrams(t, 1)
	b.controller.Close()
	if r := wait(t, result); !errors.Is(r.err, net.ErrClosed) {
		t.Fatalf("Request returned %v, want net.ErrClosed", r.err)
	}
	if _, err := b.controller.Request(context.Background(), h248test.Addr(gatewayAddr), reserve()); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Request after Close returned %v", err)
	}
}

// answered sums up the answer to a message: the error that takes the
// place of its transactions, or each reply with its action replies and
// the commands and errors in them.
func answered(m Message) string {
	if m.MessageError != nil {
		return fmt.Sprintf("Error=%d", m.MessageError.ErrorCode)
	}
	var out []string
	for _, tr := range m.Transactions {
		r, ok := tr.(TransactionReply)
		if !ok {
			out = append(out, fmt.Sprintf("%T", tr))
			continue
		}
		var parts []string
		if r.Error != nil {
			parts = append(parts, fmt.Sprintf("Error=%d", r.Error.ErrorCode))
		}
		for _, a := range r.Actions {
			var items []string
			for _, c := range a.Commands {
				items = append(items, fmt.Sprintf("%s=%s", c.Command, c.TerminationID))
			}
			if a.Error != nil {
				items = append(items, fmt.Sprintf("Error=%d", a.Error.ErrorCode))
			}
			parts = append(parts, fmt.Sprintf("Context=%s{%s}", a.ContextID, strings.Join(items, ",")))
		}
		out = append(out, fmt.Sprintf("Reply=%d{%s}", r.TransactionID, strings.Join(parts, ",")))
	}
	return strings.Join(out, " ")
}

// echoHandler answers every command it is given without an error.
func echoHandler(ctx context.Context, r *IncomingRequest) TransactionReply {
	var reply TransactionReply
	for _, a := range r.Actions {
		ar := ActionReply{ContextID: a.ContextID}
		for _, c := range a.Commands {
			ar.Commands = append(ar.Commands, CommandReply{Command: c.Command, TerminationID: c.TerminationID})
		}
		reply.Actions = append(reply.Actions, ar)
	}
	return reply
}

func TestMessageThatCannotBeReadInFull(t *testing.T) {
	const header = "MEGACO/2 <pcscf1.ims.example.net>\n"
	tests := []struct {
		name, message string
		// want is the answer, or "" for none.
		want string
	}{
		// H.248.8 (03/2013) 4.2.1, 4.2.5: errors at the message level.
		{"a broken header", "MEGACO/2 <pcscf1.ims", "Error=400"},
		{"no transaction", header + "Transactoin = 9 { }", "Error=400"},
		{"a version that is not supported", "MEGACO/3 <pcscf1.ims.example.net>\nTransaction = 9 { Context = - { AuditValue = ROOT { Audit { } } } }", "Error=406"},
		// H.248.1 (05/2002) 8.2.2 and 8.1.1: a transaction whose ID
		// cannot be read is answered with the null TransactionID.
		{"no transaction ID", header + "Transaction = x { }", "Reply=0{Error=403}"},
		{"no action", header + "Transaction = 9 {", "Reply=9{Error=403}"},
		{"a ContextID that cannot be read", header + "Transaction = 9 { Context = q }", "Reply=9{Error=422}"},
		{"no end of the transaction",
			header + "Transaction = 9 { Context = - { AuditValue = ROOT { Audit { } } }, Context = 7 { Modify = ip/1/access/7 }",
			"Reply=9{Context=-{AuditValue=ROOT},Context=7{Modify=ip/1/access/7,Error=403}}"},
		{"no end of an action",
			header + "Transaction = 9 { Context = 7 { Modify = ip/1/access/7, Bogus = 1 } }",
			"Reply=9{Context=7{Modify=ip/1/access/7,Error=422}}"},
		{"a TerminationID that cannot be read",
			header + "Transaction = 9 { Context = 7 { Modify = ip/1/access/7, Modify = { } } }",
			"Reply=9{Context=7{Modify=ip/1/access/7,Error=442}}"},
		{"no end of the first command",
			header + "Transaction = 9 { Context = 7 { Modify = ip/1/access/7 { Media { Bogus } } } }",
			"Reply=9{Context=7{Error=442}}"},
		{"a whole transaction before a broken one",
			header + "Transaction = 8 { Context = - { AuditValue = ROOT { Audit { } } } } Transaction = 9 {",
			"Reply=8{Context=-{AuditValue=ROOT}} Reply=9{Error=403}"},
		// Only requests are answered, and errors never.
		{"a broken reply", header + "Reply = 9 { Context = 7 { Bogus", ""},
		{"a broken error", header + "Error = 4x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: echoHandler})
			b.inject(controllerAddr, gatewayAddr, tt.message)
			// The Endpoint carries out a request, and answers it, in a
			// goroutine of its own, so the test waits for the answers.
			b.network.Settle(t)
			if tt.want != "" {
				b.network.WaitDatagrams(t, 1+len(strings.Fields(tt.want)))
			}
			var got []string
			for _, d := range b.network.Datagrams()[1:] {
				got = append(got, answered(parsed(t, d)))
			}
			// The transactions of a message are carried out
			// independently, so their replies may come in any order.
			slices.Sort(got)
			if strings.Join(got, " ") != tt.want {
				t.Errorf("answer %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPartOfARequestIsCarriedOutOnce(t *testing.T) {
	var calls atomic.Int32
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: func(ctx context.Context, r *IncomingRequest) TransactionReply {
		calls.Add(1)
		return echoHandler(ctx, r)
	}})
	broken := "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 9 { Context = 7 { Modify = ip/1/access/7, Bogus } }"
	b.inject(controllerAddr, gatewayAddr, broken)
	b.network.WaitDatagrams(t, 2)
	b.inject(controllerAddr, gatewayAddr, broken)
	ds := b.network.WaitDatagrams(t, 4)
	if n := calls.Load(); n != 1 || len(ds) != 4 || string(ds[1].Payload) != string(ds[3].Payload) {
		t.Errorf("the handler ran %d times; %d datagrams", n, len(ds))
	}
}

// notify reports that the bearer of a termination was released (IP
// Bearer Released, TS 29.334 5.17.2.7).
func notify() []ActionRequest {
	return []ActionRequest{{
		ContextID: 7,
		Commands: []CommandRequest{{
			Command:       Notify,
			TerminationID: "ip/1/access/11",
			ObservedEvents: &ObservedEventsDescriptor{RequestID: 1, ObservedEventList: []ObservedEvent{
				{EventName: "g/cause"},
			}},
		}},
	}}
}

func TestRepliesAreHandledBeforeRequests(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{Handler: h.handle}, EndpointConfig{})
	if _, err := b.gateway.Request(context.Background(), h248test.Addr(controllerAddr), notify()); err != nil {
		t.Fatal(err)
	}
	// The controller sends a request to a peer that never reads it.
	// Its reply comes in the same message as a repetition of the
	// gateway's request, after it.
	go b.controller.Request(context.Background(), h248test.Addr("192.0.2.3:2944"), reserve())
	ds := b.network.WaitDatagrams(t, 3)
	b.inject(gatewayAddr, controllerAddr, string(ds[0].Payload)+
		"Reply = 1 { ImmAckRequired, Context = 7 { Add = ip/1/access/11 } }\n")
	b.network.WaitDatagrams(t, 6)
	b.network.Settle(t)
	// H.248.1 version 2 Corrigendum 1, clause 8: the reply is handled
	// first, so its confirmation goes out before the kept reply to the
	// repetition.
	checkKinds(t, b.network.Datagrams()[3:],
		"Transaction=5001,Reply=1(ImmAckRequired)", "TransactionResponseAck=[{1 0}]", "Reply=5001")
	if h.count(5001) != 1 {
		t.Errorf("the handler ran %d times", h.count(5001))
	}
}

// rootServiceChange is a ServiceChange on ROOT from the media gateway.
func rootServiceChange(p ServiceChangeParm) []ActionRequest {
	return []ActionRequest{{
		ContextID: ContextNull,
		Commands: []CommandRequest{{
			Command:       ServiceChange,
			TerminationID: TerminationRoot,
			ServiceChange: &p,
		}},
	}}
}

func TestRegistrationIsAVersion1Message(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{Handler: h.handle}, EndpointConfig{})
	// IMS-AGW Register (TS 29.334 5.17.3.5): the ServiceChange offers
	// version 2.
	register := rootServiceChange(ServiceChangeParm{
		Method: Restart, Reason: "901 Cold Boot",
		Profile: &ServiceChangeProfile{ProfileName: "threegIq", Version: 7},
		Version: 2,
	})
	if _, err := b.gateway.Request(context.Background(), h248test.Addr(controllerAddr), register); err != nil {
		t.Fatal(err)
	}
	// Communication Recovery (TS 29.334 5.17.3.3) carries no version.
	disconnected := rootServiceChange(ServiceChangeParm{Method: Disconnected, Reason: "900 Service Restored"})
	if _, err := b.gateway.Request(context.Background(), h248test.Addr(controllerAddr), disconnected); err != nil {
		t.Fatal(err)
	}
	ds := b.network.Datagrams()
	checkKinds(t, ds, "Transaction=5001", "Reply=5001", "Transaction=5002", "Reply=5002")
	want := []string{
		"MEGACO/1 <agw1.ims.example.net>\n", "MEGACO/1 <pcscf1.ims.example.net>\n",
		"MEGACO/2 <agw1.ims.example.net>\n", "MEGACO/2 <pcscf1.ims.example.net>\n",
	}
	for i, d := range ds {
		if !strings.HasPrefix(string(d.Payload), want[i]) {
			t.Errorf("datagram %d starts\n%s\nwant %q", i, d.Payload, want[i])
		}
	}
}

func TestTooManyTransactionsInAMessage(t *testing.T) {
	var h countingHandler
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{Handler: h.handle})
	message := func(first, count int) string {
		text := "MEGACO/2 <pcscf1.ims.example.net>\n"
		for id := first; id < first+count; id++ {
			text += fmt.Sprintf("Transaction = %d { Context = 7 { Subtract = ip/1/access/11 } }\n", id)
		}
		// A Pending does not count towards the limit.
		return text + "Pending = 99 { }\n"
	}

	// Ten transactions are allowed (TS 29.334 5.10, Table 5.10.1).
	b.inject(controllerAddr, gatewayAddr, message(101, 10))
	b.network.WaitDatagrams(t, 11)
	b.network.Settle(t)
	if h.total() != 10 {
		t.Fatalf("the handler ran %d times for ten transactions", h.total())
	}

	// Eleven are refused as a whole with error 413 (H.248.8 4.2.9).
	b.inject(controllerAddr, gatewayAddr, message(201, 11))
	ds := b.network.WaitDatagrams(t, 13)
	b.network.Settle(t)
	if len(b.network.Datagrams()) != 13 || h.total() != 10 {
		t.Fatalf("%d datagrams and %d handler calls, want one answer and no call", len(b.network.Datagrams()), h.total())
	}
	m := parsed(t, ds[12])
	if ds[12].To != controllerAddr || m.MessageError == nil || m.MessageError.ErrorCode != 413 || len(m.Transactions) != 0 {
		t.Errorf("answer %+v, want a message with error 413", m)
	}
}

func TestNoHandlerAnswersNotImplemented(t *testing.T) {
	b := newTestbed(t, EndpointConfig{}, EndpointConfig{})
	r := wait(t, b.request(context.Background(), reserve()))
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.reply.Error == nil || r.reply.Error.ErrorCode != 501 {
		t.Errorf("reply %+v, want error 501", r.reply)
	}
}

func TestNewEndpointChecksItsConfiguration(t *testing.T) {
	n := h248test.NewNetwork(h248test.NewClock())
	if _, err := NewEndpoint(n.Listen(controllerAddr), EndpointConfig{}); err == nil {
		t.Error("an Endpoint without an MId was made")
	}
	if _, err := NewEndpoint(n.Listen(controllerAddr), EndpointConfig{MId: mgc, Timers: Timers{TMax: time.Minute}}); err == nil {
		t.Error("an Endpoint with LONG-TIMER shorter than T-MAX was made")
	}
}
