package diameter

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// rawPeer is a peer written by hand, so that a test can read the
// messages a server sends it AVP by AVP. It has opened the connection
// and had its CER answered, so the server holds it in R-Open.
func rawPeer(t *testing.T, server *Server, originHost string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	local := Identity{OriginHost: originHost, OriginRealm: realm}
	cer := capabilitiesExchangeRequest(local, []uint32{ApplicationIDCx}, net.IPv4(127, 0, 0, 1), MessageIDs{HopByHop: 1, EndToEnd: 1})
	if _, err := cer.WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	cea, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultCode(cea); got != DiameterSuccess {
		t.Fatalf("CEA Result-Code = %d, want DIAMETER_SUCCESS", got)
	}
	return conn
}

// RFC 6733 5.4: a node that is going out of service tells each peer
// with a DPR and closes the connection when the DPA comes back. 5.4.3:
// the cause is REBOOTING when the node means to come back.
func TestDisconnectPeersSendsDPRWithRebooting(t *testing.T) {
	node, _ := newTestNode(hss, testTimers)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	conn := rawPeer(t, server, scscf)

	disconnected := make(chan struct{})
	go func() {
		server.DisconnectPeers()
		close(disconnected)
	}()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	dpr, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	event, ok := parseEvent(dpr).(RcvDPR)
	if !ok {
		t.Fatalf("got %s, want a DPR", commandName(dpr))
	}
	if event.Cause != Rebooting {
		t.Errorf("Disconnect-Cause = %d, want REBOOTING (%d)", event.Cause, Rebooting)
	}
	if got := originHost(dpr); got != hss {
		t.Errorf("Origin-Host = %q, want %q", got, hss)
	}

	local := Identity{OriginHost: scscf, OriginRealm: realm}
	if _, err := disconnectPeerAnswer(local, messageIDs(dpr)).WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	// RFC 6733 5.4: the receiver of the DPA closes the transport.
	if _, err := diam.ReadMessage(conn, dict.Default); !errors.Is(err, io.EOF) {
		t.Errorf("read after the DPA returned %v, want EOF", err)
	}
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("DisconnectPeers did not return after the DPA")
	}
}

// A peer that never answers the DPR must not hold the drain open: RFC
// 6733 5.6 leaves the wait in Closing to an application timer, which
// here is the Answer timer.
func TestDisconnectPeersGivesUpOnAPeerThatSendsNoDPA(t *testing.T) {
	timers := testTimers
	timers.Answer = 300 * time.Millisecond
	node, _ := newTestNode(hss, timers)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	rawPeer(t, server, scscf)

	done := make(chan struct{})
	go func() {
		server.DisconnectPeers()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DisconnectPeers waited for a DPA that never came")
	}
}

// heldPeers is the Origin-Hosts the server runs a state machine for,
// which is what DisconnectPeers sends a DPR to.
func heldPeers(s *Server) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts := make([]string, 0, len(s.peers))
	for host := range s.peers {
		hosts = append(hosts, host)
	}
	return hosts
}

// A connection is accepted before its CER arrives (RFC 6733 5.6.1), so
// one that was accepted while the server still listened can send its
// CER after the server stopped accepting and after DisconnectPeers went
// through the peers. Answering it with a CEA would leave a node that is
// out of service holding a peer that no DPR reached.
func TestServerRefusesACERThatArrivesAfterItStoppedAccepting(t *testing.T) {
	timers := testTimers
	// The held connection waits this long for its CER, which has to
	// outlast the steps below.
	timers.Answer = 2 * time.Second
	node, logs := newTestNode(hss, timers)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	held, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { held.Close() })
	// A second connection, dialled after the first and closed at once.
	// One accept loop takes connections in the order the transport
	// completed them, so the log of this one says that the first has been
	// accepted and is waiting for its CER.
	probe, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	probe.Close()
	waitForLog(t, logs, fields{"msg": "closed connection that sent no CER"})

	if err := server.SetAccepting("hss", false); err != nil {
		t.Fatal(err)
	}
	server.DisconnectPeers()

	local := Identity{OriginHost: scscf, OriginRealm: realm}
	cer := capabilitiesExchangeRequest(local, []uint32{ApplicationIDCx}, net.IPv4(127, 0, 0, 1), MessageIDs{HopByHop: 1, EndToEnd: 1})
	if _, err := cer.WriteTo(held); err != nil {
		t.Fatal(err)
	}
	held.SetReadDeadline(time.Now().Add(5 * time.Second))
	if m, err := diam.ReadMessage(held, dict.Default); err == nil {
		t.Errorf("the server answered %s with Result-Code %d to a CER it took after it stopped accepting, want the connection closed",
			commandName(m), resultCode(m))
	}
	if hosts := heldPeers(server); len(hosts) != 0 {
		t.Errorf("the server holds the peers %v, want none: a node out of service must hold no peer that its DPRs missed", hosts)
	}
}

// A node out of service has no listening socket, so the transport
// refuses the peer (RFC 6733 2.1), and it binds the same address again
// when it comes back.
func TestServerStopsAcceptingAndListensAgain(t *testing.T) {
	node, _ := newTestNode(hss, testTimers)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	address := server.Addr()

	if err := server.SetAccepting("hss", false); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", address, 5*time.Second); err == nil {
		conn.Close()
		t.Fatal("the server accepted a connection while it was not accepting")
	}

	if err := server.SetAccepting("hss", true); err != nil {
		t.Fatal(err)
	}
	if server.Addr() != address {
		t.Errorf("came back on %s, want the address it had, %s", server.Addr(), address)
	}
	// The connection gets a CEA, so the server runs the state machine
	// for it again.
	rawPeer(t, server, scscf)
}

// An open connection is the peer's, not the listener's: a server that
// stops accepting keeps serving the peers it has, which is what
// SHUTTINGDOWN means for the node that holds it.
func TestStopAcceptingKeepsTheConnectionsTheServerHas(t *testing.T) {
	node, _ := newTestNode(hss, testTimers)
	node.Handle(func(request *diam.Message) *diam.Message { return CommandUnsupportedAnswer(node.Identity(), request) })
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	conn := rawPeer(t, server, scscf)

	if err := server.SetAccepting("hss", false); err != nil {
		t.Fatal(err)
	}
	request := diam.NewRequest(diam.DeviceWatchdog, 0, dict.Default)
	Identity{OriginHost: scscf, OriginRealm: realm}.AddOrigin(request)
	request.Header.HopByHopID, request.Header.EndToEndID = 7, 7
	if _, err := request.WriteTo(conn); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	answer, err := diam.ReadMessage(conn, dict.Default)
	if err != nil {
		t.Fatalf("the server answered nothing on the connection it had: %v", err)
	}
	if answer.Header.HopByHopID != 7 {
		t.Errorf("got %s with Hop-by-Hop 0x%x, want the answer to our DWR", commandName(answer), answer.Header.HopByHopID)
	}
}

// The roles of one node share its listener, so a node that runs two
// roles that its peers connect to, the HSS and the Service Centre,
// accepts again only once neither holds the listener closed. The drain
// of a role in service says that the role cannot be reached while the
// other has the listener closed.
func TestTheListenerAcceptsOnceNoRoleHoldsItClosed(t *testing.T) {
	node, _ := newTestNode(hss, testTimers)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	hssDrain := NewListenerDrain(server, "hss", node.log)
	scDrain := NewListenerDrain(server, "sc", node.log)

	hssDrain.Change(false, false)
	scDrain.Change(false, false)
	scDrain.Change(true, false)
	if server.Accepting() {
		t.Fatal("the server accepts while the HSS still holds its listener closed")
	}
	if conn, err := net.DialTimeout("tcp", server.Addr(), 5*time.Second); err == nil {
		conn.Close()
		t.Fatal("the transport took a connection while the HSS holds the listener closed")
	}
	if !hssDrain.Reachable() {
		t.Error("the HSS out of service reads as unreachable, which is no fault of its own")
	}
	if scDrain.Reachable() {
		t.Error("the SC in service reads as reachable, but no peer can connect to it")
	}

	hssDrain.Change(true, false)
	if !server.Accepting() || !hssDrain.Reachable() || !scDrain.Reachable() {
		t.Fatal("the server does not accept once both roles are back in service")
	}
	rawPeer(t, server, scscf)
}
