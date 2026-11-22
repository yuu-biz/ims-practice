package diameter

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// farmHost names one instance of the HSS server farm, as the
// configuration of a farm does: an Origin-Host per instance.
func farmHost(i int) string { return fmt.Sprintf("hss-%d.%s", i, realm) }

// openFarmPeer starts an HSS instance with handler and connects node to
// it, with the connection open by the time it returns.
func openFarmPeer(t *testing.T, node *Node, logs *logBuffer, host string, handler Handler) *Peer {
	t.Helper()
	hssNode, _ := newTestNode(host, testTimers)
	hssNode.Handle(handler)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	peer := node.Connect(PeerConfig{Address: server.Addr(), OriginHost: host})
	t.Cleanup(peer.Stop)
	waitForLog(t, logs, fields{"msg": "peer state", "peer": host, "to": "I-Open (OKAY)"})
	return peer
}

// deadFarmPeer is a peer of an instance that is not there: nothing
// listens on the address, so the connection never opens.
func deadFarmPeer(t *testing.T, node *Node, host string) *Peer {
	t.Helper()
	peer := node.Connect(PeerConfig{Address: "127.0.0.1:1", OriginHost: host})
	t.Cleanup(peer.Stop)
	return peer
}

// answering is the handler of the instance named host: it answers
// result and hands the test every request it saw.
func answering(host string, result uint32) (Handler, chan *diam.Message) {
	seen := make(chan *diam.Message, 4)
	return func(req *diam.Message) *diam.Message {
		seen <- req
		ans := req.Answer(result)
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(host))
		return ans
	}, seen
}

func requestSeen(t *testing.T, seen chan *diam.Message) *diam.Message {
	t.Helper()
	select {
	case m := <-seen:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("the instance saw no request")
		return nil
	}
}

func retransmitted(m *diam.Message) bool {
	return m.Header.CommandFlags&diam.RetransmittedFlag != 0
}

// An instance that is not there never had the request, so the next
// instance gets a first attempt and not a retransmission.
func TestPeerGroupSendsToTheFirstOpenPeer(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	handler, seen := answering(farmHost(2), DiameterSuccess)
	group := NewPeerGroup(
		deadFarmPeer(t, node, farmHost(1)),
		openFarmPeer(t, node, logs, farmHost(2), handler),
	)

	answer, err := group.Request(t.Context(), testRequest(node))
	if err != nil {
		t.Fatal(err)
	}
	if originHost(answer) != farmHost(2) {
		t.Errorf("the answer came from %q", originHost(answer))
	}
	if got := requestSeen(t, seen); retransmitted(got) {
		t.Error("T flag set on a request that no other instance had")
	}
}

// RFC 6733 5.5.4: a request that got no answer goes to another peer
// with the T flag set, keeping the End-to-End Identifier that tells the
// receiver it is a duplicate.
func TestPeerGroupFailsOverWhenThePeerDoesNotAnswer(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	silent, silentSeen := answering(farmHost(1), DiameterSuccess)
	release := make(chan struct{})
	defer close(release)
	blocking := func(req *diam.Message) *diam.Message {
		answer := silent(req)
		<-release
		return answer
	}
	handler, seen := answering(farmHost(2), DiameterSuccess)
	group := NewPeerGroup(
		openFarmPeer(t, node, logs, farmHost(1), blocking),
		openFarmPeer(t, node, logs, farmHost(2), handler),
	)

	if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
		t.Fatal(err)
	}
	first, second := requestSeen(t, silentSeen), requestSeen(t, seen)
	if retransmitted(first) {
		t.Error("T flag set on the first attempt")
	}
	if !retransmitted(second) {
		t.Error("T flag not set on the retransmission")
	}
	if first.Header.EndToEndID != second.Header.EndToEndID {
		t.Errorf("End-to-End Identifier 0x%08x then 0x%08x; a retransmission keeps it",
			first.Header.EndToEndID, second.Header.EndToEndID)
	}
	if first.Header.HopByHopID == second.Header.HopByHopID {
		t.Error("the two connections used one Hop-by-Hop Identifier")
	}
	waitForLog(t, logs, fields{"msg": "failover", "peer": farmHost(1), "next_peer": farmHost(2)})
}

// RFC 6733 7.1.3: DIAMETER_TOO_BUSY is an answer from an instance that
// is up and cannot serve the request, and tells its sender to try
// another.
func TestPeerGroupFailsOverOnTooBusy(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	busy, busySeen := answering(farmHost(1), DiameterTooBusy)
	handler, seen := answering(farmHost(2), DiameterSuccess)
	group := NewPeerGroup(
		openFarmPeer(t, node, logs, farmHost(1), busy),
		openFarmPeer(t, node, logs, farmHost(2), handler),
	)

	answer, err := group.Request(t.Context(), testRequest(node))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultCode(answer); got != DiameterSuccess {
		t.Errorf("Result-Code = %d, want %d", got, DiameterSuccess)
	}
	requestSeen(t, busySeen)
	if got := requestSeen(t, seen); !retransmitted(got) {
		t.Error("T flag not set on the retransmission")
	}
}

// With every instance busy the caller gets the DIAMETER_TOO_BUSY, not
// an error: the group reached the farm and the farm said no.
func TestPeerGroupReturnsTooBusyWhenEveryPeerIsBusy(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	first, _ := answering(farmHost(1), DiameterTooBusy)
	second, _ := answering(farmHost(2), DiameterTooBusy)
	group := NewPeerGroup(
		openFarmPeer(t, node, logs, farmHost(1), first),
		openFarmPeer(t, node, logs, farmHost(2), second),
	)

	answer, err := group.Request(t.Context(), testRequest(node))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultCode(answer); got != DiameterTooBusy {
		t.Errorf("Result-Code = %d, want %d", got, DiameterTooBusy)
	}
}

// DIAMETER_UNABLE_TO_DELIVER is an answer that arrived, so it is the
// group's answer: the instance is there and says the request got no
// further.
func TestPeerGroupKeepsAnAnswerThatIsNotTooBusy(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	const unableToDeliver uint32 = 3002
	refusing, _ := answering(farmHost(1), unableToDeliver)
	handler, seen := answering(farmHost(2), DiameterSuccess)
	group := NewPeerGroup(
		openFarmPeer(t, node, logs, farmHost(1), refusing),
		openFarmPeer(t, node, logs, farmHost(2), handler),
	)

	answer, err := group.Request(t.Context(), testRequest(node))
	if err != nil {
		t.Fatal(err)
	}
	if got := resultCode(answer); got != unableToDeliver {
		t.Errorf("Result-Code = %d, want %d", got, unableToDeliver)
	}
	select {
	case <-seen:
		t.Error("the second instance saw the request")
	default:
	}
}

// RFC 6733 5.5.4: a request whose Destination-Host names a peer has a
// fixed destination, so no other peer of the group can take it over.
// The Cx requests of a registration the HSS already knows name the
// instance that answered the MAR (TS 29.228 Table 6.1.2.1), and those
// are the requests that cannot fail over.
func TestPeerGroupKeepsToTheDestinationHost(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	first, firstSeen := answering(farmHost(1), DiameterSuccess)
	second, secondSeen := answering(farmHost(2), DiameterSuccess)
	group := NewPeerGroup(
		openFarmPeer(t, node, logs, farmHost(1), first),
		openFarmPeer(t, node, logs, farmHost(2), second),
	)

	// A request for the second instance goes there, not to the first,
	// which the group would otherwise have tried first.
	answer, err := group.Request(t.Context(), addressedTo(testRequest(node), farmHost(2)))
	if err != nil {
		t.Fatal(err)
	}
	if originHost(answer) != farmHost(2) {
		t.Errorf("the answer came from %q, want the instance the request named", originHost(answer))
	}
	requestSeen(t, secondSeen)
	select {
	case <-firstSeen:
		t.Error("the request reached an instance it did not name")
	default:
	}

	// No peer of the group is that host, so there is nowhere to send it.
	_, err = group.Request(t.Context(), addressedTo(testRequest(node), farmHost(3)))
	if !errors.Is(err, ErrPeerUnavailable) {
		t.Errorf("a request for a host the group does not hold: %v, want ErrPeerUnavailable", err)
	}
}

// addressedTo gives request a Destination-Host, as the S-CSCF does once
// it knows which instance of the HSS serves the user.
func addressedTo(request *diam.Message, host string) *diam.Message {
	request.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(host))
	return request
}

// The attempts share one Answer timer, so that a request costs the
// caller the same however many instances the group holds: two silent
// instances together take what one takes, not twice as long.
func TestPeerGroupSharesOneAnswerTimerAcrossTheAttempts(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	release := make(chan struct{})
	defer close(release)
	silent := func(host string) Handler {
		answer, _ := answering(host, DiameterSuccess)
		return func(req *diam.Message) *diam.Message {
			m := answer(req)
			<-release
			return m
		}
	}
	group := NewPeerGroup(
		openFarmPeer(t, node, logs, farmHost(1), silent(farmHost(1))),
		openFarmPeer(t, node, logs, farmHost(2), silent(farmHost(2))),
	)

	start := time.Now()
	if _, err := group.Request(t.Context(), testRequest(node)); err == nil {
		t.Fatal("two silent instances answered")
	}
	if took := time.Since(start); took > testTimers.Answer*3/2 {
		t.Errorf("two silent instances took %v; the attempts share one Answer timer of %v", took, testTimers.Answer)
	}
}

func TestPeerGroupWithoutAnOpenPeerFailsFast(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	group := NewPeerGroup(deadFarmPeer(t, node, farmHost(1)), deadFarmPeer(t, node, farmHost(2)))

	start := time.Now()
	_, err := group.Request(t.Context(), testRequest(node))
	if !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("err = %v, want ErrPeerUnavailable", err)
	}
	if time.Since(start) > testTimers.Answer/2 {
		t.Errorf("took %v; no attempt reached a connection", time.Since(start))
	}
}

// A group is available while one of its peers has an open connection,
// and no longer once that one is gone.
func TestPeerGroupIsAvailableWhileAPeerIsOpen(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	dead := NewPeerGroup(deadFarmPeer(t, node, farmHost(1)))
	if dead.Available() {
		t.Error("a group whose peer never connected is available")
	}
	handler, _ := answering(farmHost(2), DiameterSuccess)
	open := openFarmPeer(t, node, logs, farmHost(2), handler)
	group := NewPeerGroup(deadFarmPeer(t, node, farmHost(3)), open)
	if !group.Available() {
		t.Error("a group with an open peer is not available")
	}
	open.Stop()
	if group.Available() {
		t.Error("a group is available after its open peer stopped")
	}
}

func TestEmptyPeerGroupHasNowhereToSend(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	if _, err := NewPeerGroup().Request(t.Context(), testRequest(node)); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("err = %v, want ErrPeerUnavailable", err)
	}
}
