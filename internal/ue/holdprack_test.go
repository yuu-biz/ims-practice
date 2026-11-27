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

// TestHoldAnsweredInAReliable183 is a hold whose answer comes in a
// reliable 183 to the re-INVITE (RFC 3262 5). The UE acknowledges the
// 183 with a PRACK, and the 200 that follows carries no SDP (RFC 3261
// 13.2.1), so the hold rests on the answer of the 183.
func TestHoldAnsweredInAReliable183(t *testing.T) { holdAnsweredInAReliable183(t, 200) }

// TestHoldFailedAfterAReliable183IsUndoneWithANewOffer: the peer answers
// the hold in a reliable 183 and then fails the re-INVITE with 500.
// Without preconditions that offer and answer were a change that the
// peer carried out (RFC 6141 3.3), so the UE, which goes back to the
// session before the hold, offers that session in a new re-INVITE, and
// both sides have the same view of it again (RFC 6141 3.4).
func TestHoldFailedAfterAReliable183IsUndoneWithANewOffer(t *testing.T) {
	holdAnsweredInAReliable183(t, 500)
}

func holdAnsweredInAReliable183(t *testing.T, final int) {
	c := newCalledUE(t, ue.CallConfig{})
	sessions := make(chan *ue.Session, 1)
	active := make(chan struct{})
	answered := make(chan error, 1)
	go func() {
		select {
		case ic := <-c.calls:
			answered <- c.ue.Answer(context.Background(), ic, c.media, ue.Speech{PCM: ue.Tone(440, 10*time.Second)}, ue.AnswerOptions{
				PickUp:  time.After(0),
				Session: func(s *ue.Session) { sessions <- s },
				Progress: func(state ue.CallState, _ string) {
					if state == ue.CallActive {
						close(active)
					}
				},
			})
		case <-time.After(5 * time.Second):
			answered <- context.DeadlineExceeded
		}
	}()

	p := newBarePeer(t, c.ua.Stack().LocalAddr())
	offer, err := sdp.AudioOffer("1", loopback(c.callerMedia.Port()), []sdp.Codec{pcmu})
	if err != nil {
		t.Fatal(err)
	}
	invite := &sip.Message{Method: sip.INVITE, RequestURI: c.contact.URI()}
	invite.Add("Max-Forwards", "70")
	invite.Add("From", "<"+impu+">;tag="+sip.NewTag())
	invite.Add("To", "<"+impu2+">")
	invite.Add("Call-ID", sip.NewCallID())
	invite.Add("CSeq", "1 INVITE")
	invite.Add("Contact", "<sip:caller@"+p.addr().String()+">")
	invite.Add("Content-Type", "application/sdp")
	invite.Body = offer.Bytes()
	p.request(invite)
	ok := p.readUntil("200 to the INVITE", func(m *sip.Message) bool { return m.StatusCode == 200 })
	dialog, err := sip.NewUACDialog(invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	ack := &sip.Message{Method: sip.ACK, RequestURI: dialog.RemoteTarget}
	ack.Add("Max-Forwards", "70")
	ack.Add("From", invite.Get("From"))
	ack.Add("To", ok.Get("To"))
	ack.Add("Call-ID", invite.Get("Call-ID"))
	ack.Add("CSeq", "1 ACK")
	p.request(ack)
	s := <-sessions
	select {
	case <-active:
	case <-time.After(5 * time.Second):
		t.Fatal("the UE did not take the call within 5s")
	}

	held := make(chan error, 1)
	go func() { held <- s.Hold() }()
	reinvite := p.readUntil("re-INVITE", func(m *sip.Message) bool { return m.Method == sip.INVITE })
	holdOffer, err := sdp.Parse(reinvite.Body)
	if err != nil {
		t.Fatal(err)
	}
	holdAnswer, err := sdp.Answer(holdOffer, "1", loopback(c.callerMedia.Port()))
	if err != nil {
		t.Fatal(err)
	}
	contact := sip.HeaderField{Name: "Contact", Value: "<sip:caller@" + p.addr().String() + ">"}
	progress := sip.NewResponse(reinvite, 183, "")
	progress.Add(contact.Name, contact.Value)
	progress.Add("Require", sip.Option100rel)
	progress.Add("RSeq", "1")
	progress.Add("Content-Type", "application/sdp")
	progress.Body = holdAnswer.Bytes()
	p.send(progress)

	cseq, _ := sip.ParseCSeq(reinvite.Get("CSeq"))
	prack := p.readUntil("PRACK", func(m *sip.Message) bool { return m.Method == sip.PRACK })
	if want := "1 " + strconv.FormatUint(uint64(cseq.Number), 10) + " INVITE"; prack.Get("RAck") != want || len(prack.Body) > 0 {
		t.Errorf("PRACK has RAck %q and body %q, want %s and none", prack.Get("RAck"), prack.Body, want)
	}
	if prack.Get("User-Agent") != ue.UserAgent {
		t.Errorf("PRACK has User-Agent %q, want the UE's (IR.92 2.6)", prack.Get("User-Agent"))
	}
	p.send(sip.NewResponse(prack, 200, ""))
	reinviteFinal := sip.NewResponse(reinvite, final, "")
	reinviteFinal.Add(contact.Name, contact.Value)
	p.send(reinviteFinal)
	p.readUntil("ACK of the final response to the re-INVITE", func(m *sip.Message) bool { return m.Method == sip.ACK })

	if final >= 300 {
		again := p.readUntil("new re-INVITE after the failed hold", func(m *sip.Message) bool {
			return m.Method == sip.INVITE && m.Get("CSeq") != reinvite.Get("CSeq")
		})
		resync, err := sdp.Parse(again.Body)
		if err != nil {
			t.Fatal(err)
		}
		if dir := resync.Direction(resync.Media[0]); dir != "sendrecv" {
			t.Errorf("the new re-INVITE offers %s, want sendrecv, the session before the hold", dir)
		}
		resyncAnswer, err := sdp.Answer(resync, "1", loopback(c.callerMedia.Port()))
		if err != nil {
			t.Fatal(err)
		}
		ok := sip.NewResponse(again, 200, "")
		ok.Add(contact.Name, contact.Value)
		ok.Add("Content-Type", "application/sdp")
		ok.Body = resyncAnswer.Bytes()
		p.send(ok)
		p.readUntil("ACK of the 200 to the new re-INVITE", func(m *sip.Message) bool { return m.Method == sip.ACK })
	}

	select {
	case err := <-held:
		if final < 300 && err != nil {
			t.Fatalf("Hold: %v", err)
		}
		if final >= 300 && err == nil {
			t.Fatalf("Hold succeeded, want the error of the %d", final)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Hold did not return within 5s of the final response")
	}
	st := s.Status()
	switch {
	case final < 300 && (!st.Held || st.Direction != "sendonly" || st.PeerDirection != "recvonly"):
		t.Errorf("after the hold the session is %+v, want held, sendonly, and recvonly at the peer", st)
	case final >= 300 && (st.Held || st.Direction != "sendrecv"):
		t.Errorf("after the failed hold the session is %+v, want sendrecv as before it", st)
	}

	dialog.LocalSeq = 1
	bye := dialog.NewRequest(sip.BYE)
	bye.Add("Max-Forwards", "70")
	p.request(bye)
	p.readUntil("200 to the BYE", func(m *sip.Message) bool { return m.StatusCode == 200 && m.Get("CSeq") == "2 BYE" })
	// After a hold that failed, the session is sendrecv again, and this
	// peer sends no RTP, which the UE reports as the call ends.
	if err := wait(t, "the answer", answered); err != nil && !(final >= 300 && errors.Is(err, ue.ErrNoMedia)) {
		t.Fatal(err)
	}
}
