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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
// P-CSCF's stack reads one socket, or one connection, in order, so a
// message it sent for a request that the P-CSCF let in before the
// REGISTER, a 100 (Trying) or a response of the stack, comes before
// the 400. The P-CSCF decides on each request off the loop of its stack
// (sip.Config.Admit), so a test that looks for what a request got back
// waits first until the P-CSCF has decided on it.
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
// even a 100 (Trying), and forwards nothing to the home network. The
// Store has no association for the UE either. A UE retransmits over
// UDP, and a retransmission gets nothing either; the P-CSCF logs each
// copy.
func TestRequestWithoutAnIPAssociationGetsNoAnswer(t *testing.T) {
	for _, transport := range []sip.Transport{sip.UDP, sip.TCP} {
		t.Run(string(transport), func(t *testing.T) {
			logs := newLogRecords()
			h := newAKAHarnessWith(t, false, slog.New(logs), nil)
			stranger := dialRawUE(t, transport, h.unprotected.LocalAddr())
			requests := strangerRequests()
			for _, m := range requests {
				m = stranger.request(m)
				stranger.send(m)
				stranger.send(m)
			}
			// The P-CSCF decides off the loop of its stack, so the barrier
			// shows what came back only once every copy has been decided
			// on.
			logs.waitFor(t, 2*len(requests), discardedWithoutAssociation)
			if got := stranger.barrier(); len(got) != 0 {
				t.Errorf("the P-CSCF sent %d messages for the requests, want none; first: %d %s", len(got), got[0].StatusCode, got[0].Method)
			}
			if got, want := logs.count(discardedWithoutAssociation), 2*len(requests); got != want {
				t.Errorf("%d discards logged, want one for each of the %d copies", got, want)
			}
			select {
			case m := <-h.received:
				t.Errorf("the home network got %s, want nothing", m.Method)
			default:
			}
		})
	}
}

// TestFirstRequestAfterARestartGetsThrough: the association was written
// to the Store before this P-CSCF started, as by a P-CSCF of the same
// name and address before it was made again. The P-CSCF reads the
// association from the Store before it decides on the UE's first
// INVITE, which gets through. Over TCP that one copy is all the UE
// sends: a client transaction over a reliable transport does not
// retransmit (RFC 3261 17.1.1.2).
func TestFirstRequestAfterARestartGetsThrough(t *testing.T) {
	for _, transport := range []sip.Transport{sip.UDP, sip.TCP} {
		t.Run(string(transport), func(t *testing.T) {
			h := newSessionHarness(t)
			close(h.release)
			ue := dialRawUE(t, transport, h.pcscf.LocalAddr())
			h.store.PutAssociation(context.Background(), Association{
				Source:           sip.Destination{Transport: sip.UDP, Addr: ue.addr()},
				SentBy:           ue.addr().String(),
				Contact:          "sip:001010000000003@" + ue.addr().String(),
				PrivateIdentity:  "001010000000003@" + domain,
				PublicIdentities: []string{"sip:001010000000003@" + domain},
				ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
				Expires:          time.Now().Add(time.Hour),
			})
			ue.send(ue.request(invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")))
			select {
			case got := <-h.scscf:
				if got.Get("P-Asserted-Identity") != "<sip:001010000000003@"+domain+">" {
					t.Errorf("P-Asserted-Identity = %q, want the UE's", got.Get("P-Asserted-Identity"))
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the first INVITE did not reach the S-CSCF")
			}
		})
	}
}

// logRecords is a slog.Handler that keeps the message of each record.
type logRecords struct {
	mu       sync.Mutex
	messages []string
	// logged is closed, and replaced, at each record.
	logged chan struct{}
}

func newLogRecords() *logRecords { return &logRecords{logged: make(chan struct{})} }

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, r.Message)
	close(l.logged)
	l.logged = make(chan struct{})
	return nil
}

func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecords) WithGroup(string) slog.Handler      { return l }

// counted is how many records have one of messages, and a channel that
// is closed at the next record.
func (l *logRecords) counted(messages ...string) (int, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.messages {
		if slices.Contains(messages, m) {
			n++
		}
	}
	return n, l.logged
}

func (l *logRecords) count(message string) int {
	n, _ := l.counted(message)
	return n
}

// waitFor waits until n records have one of messages.
func (l *logRecords) waitFor(t *testing.T, n int, messages ...string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		got, logged := l.counted(messages...)
		if got >= n {
			return
		}
		select {
		case <-logged:
		case <-deadline:
			t.Fatalf("%d records of %q, want %d", got, messages, n)
		}
	}
}

const (
	discardedUnprotected        = "discarded an unprotected request from a UE with security associations"
	discardedWithoutAssociation = "discarded a request that does not map to an IP association"
)

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

// forwarded sends m from ue and waits until the home network of h gets
// it and its final response is back at ue, where it would otherwise
// come before a later barrier.
func (h *akaHarness) forwarded(ue *rawUE, m *sip.Message) {
	h.t.Helper()
	ue.send(m)
	select {
	case got := <-h.received:
		if got.Method != m.Method || got.Get("Call-ID") != m.Get("Call-ID") {
			h.t.Errorf("the home network got %s %s, want the %s", got.Method, got.Get("Call-ID"), m.Method)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatalf("the %s did not reach the home network", m.Method)
	}
	for {
		r := ue.read(5 * time.Second)
		if r == nil {
			h.t.Fatalf("no final response to the %s came back", m.Method)
		}
		if r.StatusCode >= 200 && r.Get("Call-ID") == m.Get("Call-ID") {
			return
		}
	}
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
			logs := newLogRecords()
			h := newAKAHarnessWith(t, true, slog.New(logs), nil)
			ue := dialRawUE(t, transport, h.unprotected.LocalAddr())
			a, sas := protectedUE(ue.addr())
			h.p.store.PutAssociation(context.Background(), a)
			h.p.store.PutSecurityAssociations(context.Background(), sas)

			requests := strangerRequests()
			for i, m := range requests {
				requests[i] = ue.request(m)
				ue.send(requests[i])
				ue.send(requests[i])
			}
			// The P-CSCF reads the SA table off the loop of its stack, so
			// the barrier shows what came back only once every copy has
			// been decided on.
			logs.waitFor(t, 2*len(requests), discardedUnprotected)
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

			h.p.store.DeleteSecurityAssociations(context.Background(), ue.addr())
			message := requests[2]
			if message.Method != sip.MESSAGE {
				t.Fatalf("request 2 is %s, want the MESSAGE", message.Method)
			}
			h.forwarded(ue, message)
		})
	}
}

// heldStore is a Store whose reads of an association by source wait
// until release is closed, as a read from a Store on another machine
// may take its time.
type heldStore struct {
	*MemoryStore
	release <-chan struct{}
	ended   <-chan struct{}
	// reads, when not nil, counts the reads of an association by source.
	reads *atomic.Int32
}

func (s heldStore) Association(ctx context.Context, source sip.Destination) (Association, bool, error) {
	if s.reads != nil {
		s.reads.Add(1)
	}
	select {
	case <-s.release:
	case <-s.ended:
	}
	return s.MemoryStore.Association(ctx, source)
}

// TestUnprotectedRequestFromAUEWithSecurityAssociationsAfterARestart:
// the association and the security associations were written to the
// Store before this P-CSCF started. The P-CSCF reads the association
// from the Store before it decides on the UE's unprotected request, and
// here that read waits until the test lets it go on. Meanwhile the
// P-CSCF discards no copy, and answers the REGISTER of the barrier,
// which has a Call-ID of its own. Once it has the association, it
// reads the SA table and discards each copy as unprotected, none for
// want of an IP association. No copy gets through at any point.
func TestUnprotectedRequestFromAUEWithSecurityAssociationsAfterARestart(t *testing.T) {
	logs := newLogRecords()
	release := make(chan struct{})
	h := newAKAHarnessWith(t, true, slog.New(logs), func(s *MemoryStore) Store {
		return heldStore{MemoryStore: s, release: release, ended: t.Context().Done()}
	})
	ue := dialRawUE(t, sip.UDP, h.unprotected.LocalAddr())
	a, sas := protectedUE(ue.addr())
	h.store.PutAssociation(context.Background(), a)
	h.store.PutSecurityAssociations(context.Background(), sas)

	m := ue.request(strangerRequests()[2])
	ue.send(m)
	ue.send(m)
	if got := ue.barrier(); len(got) != 0 {
		t.Errorf("while the Store was read, the P-CSCF sent %d %s, want nothing", got[0].StatusCode, got[0].Method)
	}
	if n := logs.count(discardedWithoutAssociation) + logs.count(discardedUnprotected); n != 0 {
		t.Errorf("%d discards logged while the Store was read, want none", n)
	}
	close(release)
	logs.waitFor(t, 2, discardedUnprotected)
	if n := logs.count(discardedWithoutAssociation); n != 0 {
		t.Errorf("%d copies discarded for want of an IP association, want none", n)
	}
	if got := ue.barrier(); len(got) != 0 {
		t.Errorf("the P-CSCF sent %d %s, want nothing", got[0].StatusCode, got[0].Method)
	}
	select {
	case got := <-h.received:
		t.Errorf("the home network got %s, want nothing", got.Method)
	default:
	}
}

// TestRequestsThatMissTogetherReadTheStoreOnce: the association of a UE
// is only in the Store, whose read here waits until the test lets it go
// on. Two requests of the UE with different Call-IDs are decided on
// side by side, and both miss what the P-CSCF holds in memory: one
// reads the Store, and the other waits for that read rather than read
// the Store too. Once the read is done, both get through.
func TestRequestsThatMissTogetherReadTheStoreOnce(t *testing.T) {
	release := make(chan struct{})
	var reads atomic.Int32
	h := newAKAHarnessWith(t, false, quiet(), func(s *MemoryStore) Store {
		return heldStore{MemoryStore: s, release: release, ended: t.Context().Done(), reads: &reads}
	})
	ue := dialRawUE(t, sip.UDP, h.unprotected.LocalAddr())
	a, _ := protectedUE(ue.addr())
	h.store.PutAssociation(context.Background(), a)

	ue.send(ue.request(strangerRequests()[2]))
	ue.send(ue.request(strangerRequests()[2]))
	if !came(5*time.Second, func() bool { return reads.Load() > 0 }) {
		t.Fatal("the P-CSCF did not read the Store")
	}
	if came(watch, func() bool { return reads.Load() > 1 }) {
		t.Errorf("%d reads of the association began, want one", reads.Load())
	}
	close(release)
	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case m := <-h.received:
			got[m.Get("Call-ID")] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("%d of the 2 requests reached the home network", len(got))
		}
	}
}

// TestProtectedRequestOfAUEWithSecurityAssociationsGetsThrough: the
// same request on the protected server port goes on to the home
// network.
func TestProtectedRequestOfAUEWithSecurityAssociationsGetsThrough(t *testing.T) {
	h := newAKAHarness(t, true)
	ue := dialRawUE(t, sip.UDP, h.protected.LocalAddr())
	a, sas := protectedUE(ue.addr())
	h.p.store.PutAssociation(context.Background(), a)
	h.p.store.PutSecurityAssociations(context.Background(), sas)
	h.forwarded(ue, ue.request(strangerRequests()[2]))
}

// TestUnprotectedRequestDuringAnAuthenticationGetsThrough: temporary
// security associations are those of an authentication under way (TS
// 24.229 5.2.2.2), which 5.2.1 NOTE 8 leaves out, so an unprotected
// request of a UE that has only those goes on to the home network.
func TestUnprotectedRequestDuringAnAuthenticationGetsThrough(t *testing.T) {
	h := newAKAHarness(t, true)
	ue := dialRawUE(t, sip.UDP, h.unprotected.LocalAddr())
	a, sas := protectedUE(ue.addr())
	sas.Temporary = true
	h.p.store.PutAssociation(context.Background(), a)
	h.p.store.PutSecurityAssociations(context.Background(), sas)
	h.forwarded(ue, ue.request(strangerRequests()[2]))
}

// TestUnprotectedRequestFollowsTheSATableOfAllPCSCFs: two P-CSCFs
// share one Store, as the instances of a P-CSCF do, and either writes
// the SA table entry of a UE that registers through it. P-CSCF a knows
// the UE's IP association. While b has set up security associations
// for the UE, a discards the UE's unprotected requests; once b has
// deleted them, a lets them in again, also when a set them up itself.
func TestUnprotectedRequestFollowsTheSATableOfAllPCSCFs(t *testing.T) {
	logs := newLogRecords()
	h := newAKAHarnessWith(t, true, slog.New(logs), nil)
	a := h.p
	b := newPCSCF(t, Config{Host: "pcscf-b." + domain, Port: 5060, VisitedNetworkID: domain}, nil, h.store, nil, quiet())
	ue := dialRawUE(t, sip.UDP, h.unprotected.LocalAddr())
	association, sas := protectedUE(ue.addr())
	a.store.PutAssociation(context.Background(), association)

	discards := 0
	discarded := func(m *sip.Message) {
		t.Helper()
		ue.send(m)
		discards++
		deadline := time.After(5 * time.Second)
		for n, logged := logs.counted(discardedUnprotected); n < discards; n, logged = logs.counted(discardedUnprotected) {
			select {
			case got := <-h.received:
				t.Fatalf("the home network got %s, want nothing", got.Method)
			case <-logged:
			case <-deadline:
				t.Fatal("the P-CSCF neither discarded nor forwarded the request")
			}
		}
		if got := ue.barrier(); len(got) != 0 {
			t.Errorf("the P-CSCF sent %d %s, want nothing", got[0].StatusCode, got[0].Method)
		}
	}

	b.store.PutSecurityAssociations(context.Background(), sas)
	discarded(ue.request(strangerRequests()[2]))
	b.store.DeleteSecurityAssociations(context.Background(), ue.addr())
	h.forwarded(ue, ue.request(strangerRequests()[2]))

	a.store.PutSecurityAssociations(context.Background(), sas)
	discarded(ue.request(strangerRequests()[2]))
	b.store.DeleteSecurityAssociations(context.Background(), ue.addr())
	h.forwarded(ue, ue.request(strangerRequests()[2]))
}
