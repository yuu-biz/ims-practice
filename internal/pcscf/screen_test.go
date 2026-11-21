package pcscf

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// rawUE is a UE that writes SIP by hand, so that a test sees every
// message the P-CSCF sends back to it, a 100 (Trying) included.
type rawUE struct {
	t    *testing.T
	conn net.Conn
	// tcp reads the stream of a TCP connection.
	tcp *bufio.Reader
}

func dialRawUE(t *testing.T, transport sip.Transport, pcscf netip.AddrPort) *rawUE {
	t.Helper()
	network := "udp"
	if transport == sip.TCP {
		network = "tcp"
	}
	conn, err := net.Dial(network, pcscf.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	u := &rawUE{t: t, conn: conn}
	if transport == sip.TCP {
		u.tcp = bufio.NewReader(conn)
	}
	return u
}

func (u *rawUE) addr() netip.AddrPort { return netip.MustParseAddrPort(u.conn.LocalAddr().String()) }

func (u *rawUE) transport() sip.Transport {
	if u.tcp != nil {
		return sip.TCP
	}
	return sip.UDP
}

// request is m with a Via of this UE.
func (u *rawUE) request(m *sip.Message) *sip.Message {
	m = m.Clone()
	m.Prepend("Via", sip.Via{Transport: string(u.transport()), Host: u.addr().Addr().String(), Port: int(u.addr().Port()),
		Params: sip.Params{{Name: "branch", Value: sip.NewBranch()}, {Name: "rport"}}}.String())
	return m
}

func (u *rawUE) send(m *sip.Message) {
	u.t.Helper()
	if _, err := u.conn.Write(m.Bytes()); err != nil {
		u.t.Fatal(err)
	}
}

// read returns the next message from the P-CSCF, or nil when none comes
// before the deadline.
func (u *rawUE) read(within time.Duration) *sip.Message {
	u.t.Helper()
	u.conn.SetReadDeadline(time.Now().Add(within))
	var m *sip.Message
	var err error
	if u.tcp != nil {
		m, err = readStream(u.tcp)
	} else {
		buf := make([]byte, 65535)
		var n int
		if n, err = u.conn.Read(buf); err == nil {
			m, err = sip.Parse(buf[:n])
		}
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return nil
	}
	if err != nil {
		u.t.Fatal(err)
	}
	return m
}

// readStream reads one message from a TCP stream: the header up to the
// empty line, then as many bytes of body as Content-Length says (RFC
// 3261 18.3).
func readStream(r *bufio.Reader) (*sip.Message, error) {
	var raw []byte
	length := 0
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		raw = append(raw, line...)
		text := strings.TrimSpace(string(line))
		if text == "" {
			break
		}
		if name, value, ok := strings.Cut(text, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return sip.Parse(append(raw, body...))
}

// barrier sends a REGISTER that the P-CSCF refuses at once with 400, as
// its To is no public user identity, and returns what came before the 400. The
// P-CSCF's stack reads one socket, or one connection, in order, and
// screens each request before it reads the next, so a message it sent
// for a request before the REGISTER, a 100 (Trying) or a response of
// the stack, comes before the 400.
func (u *rawUE) barrier() []*sip.Message {
	u.t.Helper()
	register := u.request(&sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain})
	register.Add("Max-Forwards", "70")
	register.Add("From", "<"+impu+">;tag="+sip.NewTag())
	register.Add("To", "<urn:service:sos>")
	register.Add("Call-ID", sip.NewCallID())
	register.Add("CSeq", "1 REGISTER")
	u.send(register)
	var before []*sip.Message
	for {
		m := u.read(3 * time.Second)
		if m == nil {
			u.t.Fatal("no answer to the REGISTER of the barrier")
		}
		if m.Get("Call-ID") == register.Get("Call-ID") {
			return before
		}
		before = append(before, m)
	}
}

// strangerRequests are requests of every kind that a UE sends outside
// and inside a dialog, none from an address with an IP association.
func strangerRequests() []*sip.Message {
	inDialog := func(method sip.Method) *sip.Message {
		m := &sip.Message{Method: method, RequestURI: "sip:001010000000002@192.0.2.20:5060"}
		m.Add("Max-Forwards", "70")
		m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<sip:001010000000002@"+domain+">;tag=callee")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "2 "+string(method))
		return m
	}
	message := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	message.Method = sip.MESSAGE
	message.Set("CSeq", "1 MESSAGE")
	initial := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	cancel := initial.Clone()
	cancel.Method = sip.CANCEL
	cancel.Set("CSeq", "1 CANCEL")
	return []*sip.Message{initial, cancel, message, inDialog(sip.BYE), inDialog(sip.ACK)}
}

// TestRequestWithoutAnIPAssociationGetsNoAnswer is TS 24.229 5.2.6.3.2A:
// the P-CSCF sends nothing back to a UE that has no IP association, not
// even a 100 (Trying), and forwards nothing to the S-CSCF. A UE
// retransmits over UDP, and a retransmission gets nothing either.
func TestRequestWithoutAnIPAssociationGetsNoAnswer(t *testing.T) {
	for _, transport := range []sip.Transport{sip.UDP, sip.TCP} {
		t.Run(string(transport), func(t *testing.T) {
			h := newSessionHarness(t)
			close(h.release)
			stranger := dialRawUE(t, transport, h.pcscf.LocalAddr())
			for _, m := range strangerRequests() {
				m = stranger.request(m)
				stranger.send(m)
				stranger.send(m)
			}
			if got := stranger.barrier(); len(got) != 0 {
				t.Errorf("the P-CSCF sent %d messages for the requests, want none; first: %d %s", len(got), got[0].StatusCode, got[0].Method)
			}
			select {
			case m := <-h.scscf:
				t.Errorf("the S-CSCF got %s, want nothing", m.Method)
			default:
			}
		})
	}
}

// TestRegisteredUEGetsThroughAfterARestart: the association was written
// to the Store before this P-CSCF started, as by a P-CSCF of the same
// name and address before it restarted. The first copy of a request is
// discarded, since the P-CSCF does not wait for the Store when it
// screens; once it has read the association, the UE's retransmission
// gets through.
func TestRegisteredUEGetsThroughAfterARestart(t *testing.T) {
	h := newSessionHarness(t)
	close(h.release)
	ue := dialRawUE(t, sip.UDP, h.pcscf.LocalAddr())
	h.store.PutAssociation(Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: ue.addr()},
		SentBy:           ue.addr().String(),
		Contact:          "sip:001010000000003@" + ue.addr().String(),
		PrivateIdentity:  "001010000000003@" + domain,
		PublicIdentities: []string{"sip:001010000000003@" + domain},
		ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires:          time.Now().Add(time.Hour),
	})
	m := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	m.Method = sip.MESSAGE
	m.Set("CSeq", "1 MESSAGE")
	m = ue.request(m)
	ue.send(m)
	if got := ue.barrier(); len(got) != 0 {
		t.Errorf("the first copy got %d %s, want nothing", got[0].StatusCode, got[0].Method)
	}
	deadline := time.Now().Add(5 * time.Second)
	for retransmissions := 1; ; retransmissions++ {
		ue.send(m)
		select {
		case got := <-h.scscf:
			if got.Get("P-Asserted-Identity") != "<sip:001010000000003@"+domain+">" {
				t.Errorf("P-Asserted-Identity = %q, want the UE's", got.Get("P-Asserted-Identity"))
			}
			t.Logf("through after %d retransmissions", retransmissions)
			return
		case <-time.After(sip.DefaultTimers().T1):
		}
		if time.Now().After(deadline) {
			t.Fatal("no retransmission got through")
		}
	}
}

// logRecords is a slog.Handler that keeps the message of each record.
type logRecords struct {
	mu       sync.Mutex
	messages []string
}

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, r.Message)
	return nil
}

func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecords) WithGroup(string) slog.Handler      { return l }

func (l *logRecords) count(message string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.messages {
		if m == message {
			n++
		}
	}
	return n
}

const discardedUnprotected = "discarded an unprotected request from a UE with security associations"

// protectedUE is the IP association and the established security
// associations of a UE whose protected server port is at ue, as the
// P-CSCF keeps them after an IMS AKA registration.
func protectedUE(ue netip.AddrPort) (Association, SecurityAssociations) {
	a := Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: ue},
		SentBy:           ue.String(),
		Contact:          "sip:001010000000001@" + ue.String(),
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu},
		ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires:          time.Now().Add(time.Hour),
	}
	return a, SecurityAssociations{UE: ue, PrivateIdentity: impi, Expires: time.Now().Add(time.Hour)}
}

// TestUnprotectedRequestFromAUEWithSecurityAssociationsGetsNoAnswer is
// TS 24.229 5.2.1 NOTE 8: a request other than REGISTER that reaches the
// unprotected port from a UE with established security associations is
// not protected, and the P-CSCF discards it. It sends nothing back, not
// even a 100 (Trying), forwards nothing, and logs each copy. No
// transaction is left of the request: once the associations are gone,
// the same copy, with the same branch, gets through.
func TestUnprotectedRequestFromAUEWithSecurityAssociationsGetsNoAnswer(t *testing.T) {
	for _, transport := range []sip.Transport{sip.UDP, sip.TCP} {
		t.Run(string(transport), func(t *testing.T) {
			logs := &logRecords{}
			h := newAKAHarnessWith(t, true, slog.New(logs))
			ue := dialRawUE(t, transport, h.unprotected.LocalAddr())
			a, sas := protectedUE(ue.addr())
			h.p.store.PutAssociation(a)
			h.p.store.PutSecurityAssociations(sas)

			requests := strangerRequests()
			for i, m := range requests {
				requests[i] = ue.request(m)
				ue.send(requests[i])
				ue.send(requests[i])
			}
			if got := ue.barrier(); len(got) != 0 {
				t.Errorf("the P-CSCF sent %d messages for the requests, want none; first: %d %s", len(got), got[0].StatusCode, got[0].Method)
			}
			if got, want := logs.count(discardedUnprotected), 2*len(requests); got != want {
				t.Errorf("%d discards logged, want one for each of the %d copies", got, want)
			}
			select {
			case m := <-h.received:
				t.Errorf("the home network got %s, want nothing", m.Method)
			default:
			}

			h.p.store.DeleteSecurityAssociations(ue.addr())
			message := requests[2]
			if message.Method != sip.MESSAGE {
				t.Fatalf("request 2 is %s, want the MESSAGE", message.Method)
			}
			ue.send(message)
			select {
			case got := <-h.received:
				if got.Method != sip.MESSAGE || got.Get("Call-ID") != message.Get("Call-ID") {
					t.Errorf("the home network got %s %s, want the MESSAGE", got.Method, got.Get("Call-ID"))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the MESSAGE did not reach the home network once the security associations were gone")
			}
		})
	}
}

// TestUnprotectedRequestFromAUEWithSecurityAssociationsAfterARestart:
// the association and the security associations were written to the
// Store before this P-CSCF started. Screen reads both from the Store
// after the first request, which it discards for want of an IP
// association, and goes on discarding the UE's unprotected requests.
func TestUnprotectedRequestFromAUEWithSecurityAssociationsAfterARestart(t *testing.T) {
	h := newAKAHarness(t, true)
	ue := dialRawUE(t, sip.UDP, h.unprotected.LocalAddr())
	a, sas := protectedUE(ue.addr())
	h.store.PutAssociation(a)
	h.store.PutSecurityAssociations(sas)

	m := ue.request(strangerRequests()[2])
	ue.send(m)
	deadline := time.Now().Add(5 * time.Second)
	for !h.p.admissions.admitsFrom(a.Source, a.SentBy, time.Now()) {
		if time.Now().After(deadline) {
			t.Fatal("the P-CSCF did not read the association from the Store")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ue.send(m)
	if got := ue.barrier(); len(got) != 0 {
		t.Errorf("the P-CSCF sent %d %s, want nothing", got[0].StatusCode, got[0].Method)
	}
	select {
	case got := <-h.received:
		t.Errorf("the home network got %s, want nothing", got.Method)
	default:
	}
}

// TestProtectedRequestOfAUEWithSecurityAssociationsGetsThrough: the
// same request on the protected server port goes on to the home
// network.
func TestProtectedRequestOfAUEWithSecurityAssociationsGetsThrough(t *testing.T) {
	h := newAKAHarness(t, true)
	ue := dialRawUE(t, sip.UDP, h.protected.LocalAddr())
	a, sas := protectedUE(ue.addr())
	h.p.store.PutAssociation(a)
	h.p.store.PutSecurityAssociations(sas)
	m := ue.request(strangerRequests()[2])
	ue.send(m)
	select {
	case got := <-h.received:
		if got.Get("Call-ID") != m.Get("Call-ID") {
			t.Errorf("the home network got %s %s, want the MESSAGE", got.Method, got.Get("Call-ID"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the protected MESSAGE did not reach the home network")
	}
}
