package sip

import (
	"errors"
	"reflect"
	"testing"
)

// finalByCSeq reads from the raw peer until it has the final response to
// each of the requests named by CSeq, and returns their status codes.
// Retransmissions and provisional responses are skipped.
func finalByCSeq(t *testing.T, p *rawCallee, cseqs ...string) map[string]int {
	t.Helper()
	codes := map[string]int{}
	for len(codes) < len(cseqs) {
		m := readDatagram(t, p.peer)
		if m.StatusCode < 200 {
			continue
		}
		for _, cseq := range cseqs {
			if m.Get("CSeq") == cseq {
				codes[cseq] = m.StatusCode
			}
		}
	}
	return codes
}

// ackNonOK is the ACK for a final response other than 2xx to a
// re-INVITE of the raw peer: it goes in the transaction of the
// re-INVITE, so it carries its topmost Via and its CSeq number (RFC
// 3261 17.1.1.3).
func ackNonOK(t *testing.T, p *rawCallee, reinvite *Message) {
	t.Helper()
	ack := newMatchingRequest(reinvite, ACK, reinvite.Get("To"))
	send(t, p.peer, ack, p.ua.Stack())
}

// RFC 3261 15.1.2: a UAS that takes a BYE MUST still respond to the
// requests of that dialog that wait for their final response, and 487
// (Request Terminated) is the response RECOMMENDED for them. The user
// of the call learns from ErrCallEnded that its own answer comes too
// late, and nothing of it goes out.
func TestPendingRequestsAnsweredWhenTheBYEArrives(t *testing.T) {
	arrived := make(chan *InDialogRequest, 2)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) { arrived <- r }})
	contact := HeaderField{Name: "Contact", Value: "<" + p.target + ">"}

	// Both requests wait for the user of the call: the UPDATE refreshes
	// the session (RFC 4028 9), the re-INVITE puts the call on hold.
	p.request(UPDATE, Body{}, contact)
	update := <-arrived
	reinvite := p.request(INVITE, holdOffer, contact)
	held := <-arrived

	p.request(BYE, Body{})
	got := finalByCSeq(t, p, "1 UPDATE", "2 INVITE", "3 BYE")
	want := map[string]int{"1 UPDATE": 487, "2 INVITE": 487, "3 BYE": 200}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the peer got %v, want %v", got, want)
	}
	// RFC 3261 17.2.1: the 487 is a final response of an INVITE server
	// transaction, so the peer acknowledges it.
	ackNonOK(t, p, reinvite)
	waitDone(t, out.Done(), "caller")

	if _, err := held.Accept(holdAnswer); !errors.Is(err, ErrCallEnded) {
		t.Errorf("Accept of the re-INVITE after the BYE: %v, want ErrCallEnded", err)
	}
	if _, err := update.Accept(Body{}); !errors.Is(err, ErrCallEnded) {
		t.Errorf("Accept of the UPDATE after the BYE: %v, want ErrCallEnded", err)
	}
	if err := held.Reject(488); !errors.Is(err, ErrCallEnded) {
		t.Errorf("Reject of the re-INVITE after the BYE: %v, want ErrCallEnded", err)
	}
}

// RFC 3261 15.1.1: a UA that sends a BYE considers the session over as
// soon as the BYE goes to the client transaction. So a request of the
// peer that was still waiting for the user of the call gets 487, the
// same response that the UAS of 15.1.2 gives: a 2xx after the BYE would
// accept a change to a session that has ended.
func TestPendingRequestAnsweredWhenTheBYEGoesOut(t *testing.T) {
	arrived := make(chan *InDialogRequest, 1)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) { arrived <- r }})
	reinvite := p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	held := <-arrived

	hungUp := make(chan error, 1)
	go func() { hungUp <- out.Bye() }()

	// The UA answers the re-INVITE once the BYE is on its way (RFC 3261
	// 15.1.1): a peer that had the 487 first could take its turn for the
	// next request in the dialog and send it to a UA that has hung up.
	// Both leave the same socket, so the peer reads them in that order.
	var bye, terminated *Message
	for bye == nil || terminated == nil {
		switch m := readDatagram(t, p.peer); {
		case m.Method == BYE:
			bye = m
			p.respond(m, 200, Body{})
		case m.StatusCode >= 200 && m.Get("CSeq") == "1 INVITE":
			if bye == nil {
				t.Errorf("the re-INVITE got %s before the BYE came", startLine(m))
			}
			terminated = m
		}
	}
	if terminated.StatusCode != 487 {
		t.Errorf("the re-INVITE got %s after the BYE, want 487", startLine(terminated))
	}
	ackNonOK(t, p, reinvite)
	if err := <-hungUp; err != nil {
		t.Fatalf("Bye: %v", err)
	}
	if _, err := held.Accept(holdAnswer); !errors.Is(err, ErrCallEnded) {
		t.Errorf("Accept of the re-INVITE after the BYE: %v, want ErrCallEnded", err)
	}
}
