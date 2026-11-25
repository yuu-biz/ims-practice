package diameter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// testTimers are far below the 6 s floor of RFC 3539 3.4.1 [1] so the
// test runs quickly. Only the config file enforces that floor.
var testTimers = Timers{
	Watchdog:  300 * time.Millisecond,
	Reconnect: 200 * time.Millisecond,
	Answer:    time.Second,
}

// logBuffer collects the JSON log lines that a node writes, which is
// what a learner reads.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		out = append(out, r)
	}
	return out
}

func newTestNode(host string, timers Timers) (*Node, *logBuffer) {
	logs := &logBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	return NewNode(Identity{OriginHost: host, OriginRealm: realm}, []uint32{ApplicationIDCx}, timers, log), logs
}

// fields is a partial match against one log record.
type fields map[string]any

func (f fields) matches(r map[string]any) bool {
	for k, v := range f {
		if r[k] != v {
			return false
		}
	}
	return true
}

func waitForLog(t *testing.T, logs *logBuffer, want fields) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range logs.records(t) {
			if want.matches(r) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no log record matching %v; got:\n%v", want, logs.records(t))
}

func message(direction, command string) fields {
	return fields{"msg": "diameter message", "direction": direction, "command": command}
}

func TestInitiatorAndResponderOpenWatchAndDisconnect(t *testing.T) {
	hssNode, hssLogs := newTestNode(hss, testTimers)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	scscfNode, scscfLogs := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	defer peer.Stop()

	waitForLog(t, scscfLogs, message("send", "CER"))
	waitForLog(t, hssLogs, fields{"msg": "diameter message", "direction": "send", "command": "CEA", "result_code": float64(DiameterSuccess)})
	waitForLog(t, scscfLogs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})
	waitForLog(t, hssLogs, fields{"msg": "peer state", "to": "R-Open (OKAY)"})

	// Both sides run the watchdog, so each sends a DWR after Tw.
	waitForLog(t, scscfLogs, message("send", "DWR"))
	waitForLog(t, scscfLogs, message("recv", "DWA"))
	waitForLog(t, hssLogs, message("send", "DWR"))
	waitForLog(t, hssLogs, message("recv", "DWA"))

	peer.Stop()
	waitForLog(t, scscfLogs, message("send", "DPR"))
	waitForLog(t, hssLogs, message("recv", "DPR"))
	waitForLog(t, hssLogs, message("send", "DPA"))
	waitForLog(t, scscfLogs, message("recv", "DPA"))
	waitForLog(t, scscfLogs, fields{"msg": "peer state", "from": "Closing", "to": "Closed", "event": "Rcv-DPA"})
	waitForLog(t, hssLogs, fields{"msg": "peer state", "from": "Closing", "to": "Closed", "event": "Peer-Disc"})

	if n := countLogs(scscfLogs.records(t), fields{"msg": "connection up"}); n != 1 {
		t.Errorf("initiator opened %d connections, want 1", n)
	}
}

func countLogs(records []map[string]any, want fields) int {
	n := 0
	for _, r := range records {
		if want.matches(r) {
			n++
		}
	}
	return n
}

func TestResponderRefusesCERWithoutCommonApplication(t *testing.T) {
	hssNode, _ := newTestNode(hss, testTimers)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	conn, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A CER that offers only Diameter Credit-Control (Auth-Application-Id 4).
	cer := diam.NewRequest(diam.CapabilitiesExchange, 0, dict.Default)
	cer.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pcrf.example"))
	cer.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example"))
	cer.NewAVP(avp.HostIPAddress, avp.Mbit, 0, datatype.Address(net.IPv4(127, 0, 0, 1)))
	cer.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(0))
	cer.NewAVP(avp.ProductName, 0, 0, datatype.UTF8String("test"))
	cer.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(4))
	if _, err := cer.WriteTo(conn); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	cea, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	if cea.Header.CommandCode != diam.CapabilitiesExchange || isRequest(cea) {
		t.Fatalf("got %s, want CEA", commandName(cea))
	}
	if got := resultCode(cea); got != DiameterNoCommonApplication {
		t.Errorf("Result-Code = %d, want DIAMETER_NO_COMMON_APPLICATION (%d)", got, DiameterNoCommonApplication)
	}
	if cea.Header.HopByHopID != cer.Header.HopByHopID || cea.Header.EndToEndID != cer.Header.EndToEndID {
		t.Errorf("CEA identifiers do not match the CER")
	}
	if _, err := diam.ReadMessage(conn, dict.Default); !errors.Is(err, io.EOF) {
		t.Errorf("after the CEA, read returned %v, want EOF (the HSS closes)", err)
	}
}

// fakeHSS accepts one connection and answers its CER with success.
func fakeHSS(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	cer, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	local := Identity{OriginHost: hss, OriginRealm: realm}
	if _, err := capabilitiesExchangeAnswer(local, []uint32{ApplicationIDCx}, net.IPv4(127, 0, 0, 1), messageIDs(cer), DiameterSuccess).WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

// RFC 6733 5.4.3: after a DPR with REBOOTING the initiator may come
// back, and it does so after Tc, not after some other timer.
func TestInitiatorReconnectsAfterTcWhenHSSReboots(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	timers := testTimers
	timers.Watchdog = 3 * time.Second
	scscfNode, _ := newTestNode(scscf, timers)
	peer := scscfNode.Connect(PeerConfig{Address: listener.Addr().String(), OriginHost: hss})
	defer peer.Stop()

	conn := fakeHSS(t, listener)
	defer conn.Close()
	local := Identity{OriginHost: hss, OriginRealm: realm}
	if _, err := disconnectPeerRequest(local, MessageIDs{HopByHop: 1, EndToEnd: 1}, Rebooting).WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	dpa, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	if dpa.Header.CommandCode != diam.DisconnectPeer || isRequest(dpa) || dpa.Header.HopByHopID != 1 {
		t.Fatalf("got %s with Hop-by-Hop 0x%x, want the DPA for our DPR", commandName(dpa), dpa.Header.HopByHopID)
	}
	conn.Close()
	closedAt := time.Now()

	listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	second, err := listener.Accept()
	if err != nil {
		t.Fatalf("initiator did not reconnect: %v", err)
	}
	second.Close()
	if waited := time.Since(closedAt); waited > timers.Reconnect+500*time.Millisecond {
		t.Errorf("reconnected %v after the close, want about Tc = %v", waited, timers.Reconnect)
	}
}

// RFC 6733 2.1: bytes that are not a Diameter message compromise the
// stream, and the connection is closed with a reset, not a FIN.
func TestUnparseableStreamIsReset(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	scscfNode, _ := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: listener.Addr().String(), OriginHost: hss})
	defer peer.Stop()

	conn := fakeHSS(t, listener)
	defer conn.Close()
	// A header whose Message Length (4) is shorter than the header itself
	// (RFC 6733 3).
	garbage := []byte{1, 0, 0, 4, 0x80, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1}
	if _, err := conn.Write(garbage); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadAll(conn)
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Errorf("read after garbage returned %v, want ECONNRESET", err)
	}
}

// A peer that answers the CER but never a DWR. RFC 3539 3.4.1 [4]: the
// initiator closes only after Tw has expired twice without a DWA, then
// connects again after Tc.
func TestUnansweredWatchdogClosesAndReconnects(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	scscfNode, _ := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: listener.Addr().String(), OriginHost: hss})
	defer peer.Stop()

	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cer, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	local := Identity{OriginHost: hss, OriginRealm: realm}
	if _, err := capabilitiesExchangeAnswer(local, []uint32{ApplicationIDCx}, net.IPv4(127, 0, 0, 1), messageIDs(cer), DiameterSuccess).WriteTo(conn); err != nil {
		t.Fatal(err)
	}

	dwr, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	if dwr.Header.CommandCode != diam.DeviceWatchdog || !isRequest(dwr) {
		t.Fatalf("got %s, want DWR", commandName(dwr))
	}
	dwrAt := time.Now()
	if _, err := diam.ReadMessage(conn, dict.Default); !errors.Is(err, io.EOF) {
		t.Fatalf("read after DWR returned %v, want EOF (the initiator closes)", err)
	}
	if waited := time.Since(dwrAt); waited < 2*testTimers.Watchdog-50*time.Millisecond {
		t.Errorf("closed %v after the DWR, want at least 2*Tw = %v", waited, 2*testTimers.Watchdog)
	}

	listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	second, err := listener.Accept()
	if err != nil {
		t.Fatalf("initiator did not reconnect: %v", err)
	}
	second.Close()
}

// testRequest is a Cx-application request with the minimum header AVPs
// of RFC 6733 6.1. The command code is MAR (TS 29.229), but the peer
// layer only cares that it is not a base protocol command.
func testRequest(node *Node) *diam.Message {
	m := diam.NewRequest(303, ApplicationIDCx, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(node.NewSessionID()))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(node.Identity().OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(node.Identity().OriginRealm))
	return m
}

func openPair(t *testing.T, handler Handler) (*Peer, *logBuffer) {
	t.Helper()
	hssNode, _ := newTestNode(hss, testTimers)
	hssNode.Handle(handler)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	scscfNode, logs := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: server.Addr(), OriginHost: hss})
	t.Cleanup(peer.Stop)
	waitForLog(t, logs, fields{"msg": "peer state", "to": "I-Open (OKAY)"})
	return peer, logs
}

func TestRequestIsAnsweredByThePeersHandler(t *testing.T) {
	peer, _ := openPair(t, func(req *diam.Message) *diam.Message {
		ans := req.Answer(DiameterSuccess)
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(hss))
		return ans
	})

	for range 3 {
		ans, err := peer.Request(t.Context(), testRequest(peer.node))
		if err != nil {
			t.Fatal(err)
		}
		if got := resultCode(ans); got != DiameterSuccess {
			t.Errorf("Result-Code = %d, want %d", got, DiameterSuccess)
		}
		if originHost(ans) != hss {
			t.Errorf("Origin-Host = %q, want %q", originHost(ans), hss)
		}
	}
}

func TestRequestWithoutHandlerGetsCommandUnsupported(t *testing.T) {
	peer, _ := openPair(t, nil)

	ans, err := peer.Request(t.Context(), testRequest(peer.node))
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6733 7.2: a protocol error answer has the E bit set.
	if ans.Header.CommandFlags&diam.ErrorFlag == 0 {
		t.Error("E bit not set")
	}
	if got := resultCode(ans); got != DiameterCommandUnsupported {
		t.Errorf("Result-Code = %d, want %d", got, DiameterCommandUnsupported)
	}
}

func TestRequestFailsFastWhenPeerIsNotOpen(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	// Nothing listens on this port, so the peer stays out of Open.
	peer := node.Connect(PeerConfig{Address: "127.0.0.1:1", OriginHost: hss})
	defer peer.Stop()

	start := time.Now()
	_, err := peer.Request(t.Context(), testRequest(node))
	if !errors.Is(err, ErrPeerUnavailable) {
		t.Fatalf("err = %v, want ErrPeerUnavailable", err)
	}
	if time.Since(start) > testTimers.Answer/2 {
		t.Errorf("took %v; a refused request should not wait for the Answer timer", time.Since(start))
	}
}

func TestPendingRequestFailsWhenConnectionCloses(t *testing.T) {
	release := make(chan struct{})
	arrived := make(chan struct{}, 1)
	peer, _ := openPair(t, func(req *diam.Message) *diam.Message {
		arrived <- struct{}{}
		<-release
		return req.Answer(DiameterSuccess)
	})
	defer close(release)

	errs := make(chan error, 1)
	go func() {
		_, err := peer.Request(t.Context(), testRequest(peer.node))
		errs <- err
	}()
	// The request is pending once the other side has it.
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach the other side")
	}
	go peer.Stop()

	if err := <-errs; !errors.Is(err, errConnectionClosed) {
		t.Errorf("err = %v, want errConnectionClosed", err)
	}
}

// TestTimedOutRequestIsForgotten: a request whose answer does not come
// in time stops waiting, and the peer forgets it, so that a peer that
// answers its DWRs but not some requests does not pile them up. The
// answer that comes after all names a Hop-by-Hop Identifier the peer no
// longer knows, which RFC 6733 3 has it discard.
func TestTimedOutRequestIsForgotten(t *testing.T) {
	release := make(chan struct{})
	hopByHop := make(chan uint32, 1)
	peer, logs := openPair(t, func(req *diam.Message) *diam.Message {
		hopByHop <- req.Header.HopByHopID
		<-release
		return req.Answer(DiameterSuccess)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := peer.Request(ctx, testRequest(peer.node)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	close(release)
	waitForLog(t, logs, fields{"msg": "discarded answer with unknown Hop-by-Hop Identifier",
		"hop_by_hop": fmt.Sprintf("0x%08x", <-hopByHop)})
}

func TestSessionIDFollowsRFC6733Format(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	a, b := node.NewSessionID(), node.NewSessionID()
	if a == b {
		t.Fatalf("two Session-Ids are equal: %q", a)
	}
	parts := strings.Split(a, ";")
	if len(parts) != 3 || parts[0] != scscf {
		t.Errorf("Session-Id %q is not <DiameterIdentity>;<high>;<low>", a)
	}
}

// TestAnswerWithTheWrongCommandIsDiscarded sends, in reply to a DWR, an
// answer that carries the DWR's Hop-by-Hop Identifier but the UAA command
// code. The peer must drop it and keep serving.
func TestAnswerWithTheWrongCommandIsDiscarded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	scscfNode, logs := newTestNode(scscf, testTimers)
	peer := scscfNode.Connect(PeerConfig{Address: listener.Addr().String(), OriginHost: hss})
	defer peer.Stop()

	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cer, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	local := Identity{OriginHost: hss, OriginRealm: realm}
	if _, err := capabilitiesExchangeAnswer(local, []uint32{ApplicationIDCx}, net.IPv4(127, 0, 0, 1), messageIDs(cer), DiameterSuccess).WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	dwr, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	wrong := diam.NewMessage(300, 0, ApplicationIDCx, dwr.Header.HopByHopID, dwr.Header.EndToEndID, dict.Default)
	wrong.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(DiameterSuccess))
	if _, err := wrong.WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	waitForLog(t, logs, fields{"msg": "discarded answer that does not match its request"})

	// The loop is still alive: the DWR stays unanswered, so after 2*Tw
	// the initiator closes the connection (RFC 3539 3.4.1 [4]). A stuck
	// loop would never close it.
	conn.SetReadDeadline(time.Now().Add(10 * testTimers.Watchdog))
	if _, err := diam.ReadMessage(conn, dict.Default); !errors.Is(err, io.EOF) {
		t.Fatalf("read returned %v, want EOF from a live peer loop", err)
	}
}
