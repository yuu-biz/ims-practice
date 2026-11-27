package ue_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// barePeer is the other party of a call with the UE on a bare UDP
// socket, so that the test writes each message of that party itself.
type barePeer struct {
	t    *testing.T
	conn *net.UDPConn
	ue   netip.AddrPort
}

func newBarePeer(t *testing.T, ueAddr netip.AddrPort) *barePeer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &barePeer{t: t, conn: conn, ue: ueAddr}
}

func (p *barePeer) addr() netip.AddrPort { return p.conn.LocalAddr().(*net.UDPAddr).AddrPort() }

func (p *barePeer) send(m *sip.Message) {
	p.t.Helper()
	if _, err := p.conn.WriteToUDPAddrPort(m.Bytes(), p.ue); err != nil {
		p.t.Fatal(err)
	}
}

// request sends a request of the peer with a Via of its own.
func (p *barePeer) request(m *sip.Message) {
	p.t.Helper()
	via := sip.Via{Transport: "UDP", Host: "127.0.0.1", Port: int(p.addr().Port()),
		Params: sip.Params{{Name: "branch", Value: sip.NewBranch()}}}
	m.Prepend("Via", via.String())
	p.send(m)
}

// readUntil returns the next message that the UE sends for which want
// is true, and skips the others, such as the retransmissions of a 200
// until its ACK has arrived.
func (p *barePeer) readUntil(what string, want func(*sip.Message) bool) *sip.Message {
	p.t.Helper()
	buf := make([]byte, 65535)
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.conn.SetReadDeadline(deadline)
		n, err := p.conn.Read(buf)
		if err != nil {
			p.t.Fatalf("no %s from the UE within 5s: %v", what, err)
		}
		m, err := sip.Parse(buf[:n])
		if err != nil {
			p.t.Fatal(err)
		}
		if want(m) {
			return m
		}
	}
}

// bareCall is a call of the bare peer that the UE has answered, without
// preconditions: the offer of the peer has none.
type bareCall struct {
	t        *testing.T
	c        *calledUE
	p        *barePeer
	s        *ue.Session
	dialog   sip.Dialog // as the peer has it
	contact  sip.HeaderField
	answered chan error
}

func newBareCall(t *testing.T) *bareCall {
	t.Helper()
	b := &bareCall{t: t, c: newCalledUE(t, ue.CallConfig{}), answered: make(chan error, 1)}
	sessions := make(chan *ue.Session, 1)
	active := make(chan struct{})
	go func() {
		select {
		case ic := <-b.c.calls:
			b.answered <- b.c.ue.Answer(context.Background(), ic, b.c.media, ue.Speech{PCM: ue.Tone(440, 10*time.Second)}, ue.AnswerOptions{
				PickUp:  time.After(0),
				Session: func(s *ue.Session) { sessions <- s },
				Progress: func(state ue.CallState, _ string) {
					if state == ue.CallActive {
						close(active)
					}
				},
			})
		case <-time.After(5 * time.Second):
			b.answered <- context.DeadlineExceeded
		}
	}()

	b.p = newBarePeer(t, b.c.ua.Stack().LocalAddr())
	b.contact = sip.HeaderField{Name: "Contact", Value: "<sip:caller@" + b.p.addr().String() + ">"}
	offer, err := sdp.AudioOffer("1", loopback(b.c.callerMedia.Port()), []sdp.Codec{pcmu})
	if err != nil {
		t.Fatal(err)
	}
	invite := &sip.Message{Method: sip.INVITE, RequestURI: b.c.contact.URI()}
	invite.Add("Max-Forwards", "70")
	invite.Add("From", "<"+impu+">;tag="+sip.NewTag())
	invite.Add("To", "<"+impu2+">")
	invite.Add("Call-ID", sip.NewCallID())
	invite.Add("CSeq", "1 INVITE")
	invite.Add(b.contact.Name, b.contact.Value)
	invite.Add("Content-Type", "application/sdp")
	invite.Body = offer.Bytes()
	b.p.request(invite)
	ok := b.p.readUntil("200 to the INVITE", func(m *sip.Message) bool { return m.StatusCode == 200 })
	b.dialog, err = sip.NewUACDialog(invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	b.dialog.LocalSeq = 1
	ack := &sip.Message{Method: sip.ACK, RequestURI: b.dialog.RemoteTarget}
	ack.Add("Max-Forwards", "70")
	ack.Add("From", invite.Get("From"))
	ack.Add("To", ok.Get("To"))
	ack.Add("Call-ID", invite.Get("Call-ID"))
	ack.Add("CSeq", "1 ACK")
	b.p.request(ack)
	b.s = <-sessions
	select {
	case <-active:
	case <-time.After(5 * time.Second):
		t.Fatal("the UE did not take the call within 5s")
	}
	return b
}

// hold has the UE put the call on hold, and returns what Hold returns
// and the re-INVITE.
func (b *bareCall) hold() (<-chan error, *sip.Message) {
	b.t.Helper()
	held := make(chan error, 1)
	go func() { held <- b.s.Hold() }()
	return held, b.p.readUntil("re-INVITE", func(m *sip.Message) bool { return m.Method == sip.INVITE })
}

// answer is the peer's answer to offer, an SDP of the UE, with the media
// of the peer at port.
func (b *bareCall) answer(offer []byte, port uint16) sip.Body {
	b.t.Helper()
	o, err := sdp.Parse(offer)
	if err != nil {
		b.t.Fatal(err)
	}
	a, err := sdp.Answer(o, "1", loopback(port))
	if err != nil {
		b.t.Fatal(err)
	}
	return sip.Body{Type: "application/sdp", Content: a.Bytes()}
}

// progress sends a reliable 183 with body to reinvite, and answers the
// PRACK of the UE, which carries no body.
func (b *bareCall) progress(reinvite *sip.Message, body sip.Body) {
	b.t.Helper()
	progress := sip.NewResponse(reinvite, 183, "")
	progress.Add(b.contact.Name, b.contact.Value)
	progress.Add("Require", sip.Option100rel)
	progress.Add("RSeq", "1")
	progress.Add("Content-Type", body.Type)
	progress.Body = body.Content
	b.p.send(progress)

	cseq, _ := sip.ParseCSeq(reinvite.Get("CSeq"))
	prack := b.p.readUntil("PRACK", func(m *sip.Message) bool { return m.Method == sip.PRACK })
	if want := "1 " + strconv.FormatUint(uint64(cseq.Number), 10) + " INVITE"; prack.Get("RAck") != want || len(prack.Body) > 0 {
		b.t.Errorf("PRACK has RAck %q and body %q, want %s and none", prack.Get("RAck"), prack.Body, want)
	}
	if prack.Get("User-Agent") != ue.UserAgent {
		b.t.Errorf("PRACK has User-Agent %q, want the UE's (IR.92 2.6)", prack.Get("User-Agent"))
	}
	b.p.send(sip.NewResponse(prack, 200, ""))
}

// final answers reinvite with code and body, and reads the ACK.
func (b *bareCall) final(reinvite *sip.Message, code int, body sip.Body) {
	b.t.Helper()
	resp := sip.NewResponse(reinvite, code, "")
	resp.Add(b.contact.Name, b.contact.Value)
	if len(body.Content) > 0 {
		resp.Add("Content-Type", body.Type)
		resp.Body = body.Content
	}
	b.p.send(resp)
	b.p.readUntil("ACK of the final response to the re-INVITE", func(m *sip.Message) bool { return m.Method == sip.ACK })
}

// returned is what Hold returned.
func (b *bareCall) returned(held <-chan error) error {
	b.t.Helper()
	select {
	case err := <-held:
		return err
	case <-time.After(5 * time.Second):
		b.t.Fatal("Hold did not return within 5s of the final response")
		return nil
	}
}

// hangUp ends the call with a BYE of the peer. After a hold that
// failed, the session is sendrecv again, and the peer sends no RTP,
// which the UE reports as the call ends; noMediaOK takes that.
func (b *bareCall) hangUp(noMediaOK bool) {
	b.t.Helper()
	bye := b.dialog.NewRequest(sip.BYE)
	bye.Add("Max-Forwards", "70")
	b.p.request(bye)
	b.p.readUntil("200 to the BYE", func(m *sip.Message) bool { return m.StatusCode == 200 && m.Get("CSeq") == bye.Get("CSeq") })
	if err := wait(b.t, "the answer", b.answered); err != nil && !(noMediaOK && errors.Is(err, ue.ErrNoMedia)) {
		b.t.Fatal(err)
	}
}

// TestHoldAnsweredInAReliable183 is a hold whose answer comes in a
// reliable 183 to the re-INVITE (RFC 3262 5). The UE acknowledges the
// 183 with a PRACK, and the 200 that follows carries no SDP (RFC 3261
// 13.2.1), so the hold rests on the answer of the 183.
func TestHoldAnsweredInAReliable183(t *testing.T) {
	b := newBareCall(t)
	held, reinvite := b.hold()
	b.progress(reinvite, b.answer(reinvite.Body, b.c.callerMedia.Port()))
	b.final(reinvite, 200, sip.Body{})
	if err := b.returned(held); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if st := b.s.Status(); !st.Held || st.Direction != "sendonly" || st.PeerDirection != "recvonly" {
		t.Errorf("after the hold the session is %+v, want held, sendonly, and recvonly at the peer", st)
	}
	b.hangUp(false)
}

// TestHoldFailedAfterAReliable183IsUndoneWithANewOffer: the peer answers
// the hold in a reliable 183 and then fails the re-INVITE with 500.
// Without preconditions that offer and answer were a change that the
// peer carried out (RFC 6141 3.3), so the UE, which goes back to the
// session before the hold, offers that session in a new re-INVITE, and
// both sides have the same view of it again (RFC 6141 3.4).
func TestHoldFailedAfterAReliable183IsUndoneWithANewOffer(t *testing.T) {
	b := newBareCall(t)
	held, reinvite := b.hold()
	b.progress(reinvite, b.answer(reinvite.Body, b.c.callerMedia.Port()))
	b.final(reinvite, 500, sip.Body{})

	again := b.p.readUntil("new re-INVITE after the failed hold", func(m *sip.Message) bool {
		return m.Method == sip.INVITE && m.Get("CSeq") != reinvite.Get("CSeq")
	})
	resync, err := sdp.Parse(again.Body)
	if err != nil {
		t.Fatal(err)
	}
	if dir := resync.Direction(resync.Media[0]); dir != "sendrecv" {
		t.Errorf("the new re-INVITE offers %s, want sendrecv, the session before the hold", dir)
	}
	b.final(again, 200, b.answer(again.Body, b.c.callerMedia.Port()))

	if err := b.returned(held); err == nil {
		t.Fatal("Hold succeeded, want the error of the 500")
	}
	if st := b.s.Status(); st.Held || st.Direction != "sendrecv" {
		t.Errorf("after the failed hold the session is %+v, want sendrecv as before it", st)
	}
	b.hangUp(true)
}

// TestHoldTakesNoAnswerFromABodyThatIsNotSDP: a reliable 183 whose body
// is of another type than SDP carries no answer (RFC 3261 13.2.1), as
// the SIP stack takes it too, so the answer of the hold is that of the
// 200.
func TestHoldTakesNoAnswerFromABodyThatIsNotSDP(t *testing.T) {
	b := newBareCall(t)
	held, reinvite := b.hold()
	b.progress(reinvite, sip.Body{Type: "text/plain", Content: []byte("connecting")})
	b.final(reinvite, 200, b.answer(reinvite.Body, b.c.callerMedia.Port()))
	if err := b.returned(held); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if st := b.s.Status(); !st.Held || st.PeerDirection != "recvonly" {
		t.Errorf("after the hold the session is %+v, want held, with the answer of the 200", st)
	}
	b.hangUp(false)
}
