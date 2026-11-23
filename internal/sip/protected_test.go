package sip

import (
	"bytes"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

// freePort is a port that nothing listens on when the test starts,
// standing in for the protected client port that TS 33.203 7.1 has a
// node choose.
func freePort(t *testing.T) uint16 {
	t.Helper()
	c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	port := addrOf(c).Port()
	c.Close()
	return port
}

// TestProtectedClientPortSendsRequests: on a protected server port, a
// request leaves from the protected client port, and the response that
// comes back there reaches Receive. Each message then rides the pair of
// security associations that TS 33.203 7.1 binds to those two ports.
func TestProtectedClientPortSendsRequests(t *testing.T) {
	log := testLogger(t)
	clientPort := freePort(t)
	tr, err := ListenProtected("127.0.0.1:0", clientPort, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	peer := rawSocket(t)

	req := &Message{Method: REGISTER, RequestURI: "sip:ims.example.net"}
	req.Add("Via", "SIP/2.0/UDP 127.0.0.1:"+strconv.Itoa(int(tr.LocalAddr().Port()))+";branch=z9hG4bK1;rport")
	if err := tr.SendRequest(req, Destination{Transport: UDP, Addr: addrOf(peer)}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := peer.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}
	if from.Port() != clientPort {
		t.Errorf("the request came from port %d, want the protected client port %d", from.Port(), clientPort)
	}
	if !bytes.HasPrefix(buf[:n], []byte("REGISTER ")) {
		t.Fatalf("the peer got %q", buf[:n])
	}

	resp := NewResponse(req, 200, NewTag())
	resp.Remove("Via")
	resp.Add("Via", "SIP/2.0/UDP 127.0.0.1:"+strconv.Itoa(int(tr.LocalAddr().Port()))+
		";branch=z9hG4bK1;received=127.0.0.1;rport="+strconv.Itoa(int(clientPort)))
	to := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), clientPort)
	if _, err := peer.WriteToUDPAddrPort(resp.Bytes(), to); err != nil {
		t.Fatal(err)
	}
	got := make(chan *Message, 1)
	go func() {
		m, _, err := tr.Receive()
		if err == nil {
			got <- m
		}
	}()
	select {
	case m := <-got:
		if m.StatusCode != 200 {
			t.Errorf("Receive gave %d, want the 200 that came to the protected client port", m.StatusCode)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the response to the protected client port never reached Receive")
	}
}

// TestProtectedServerPortAnswers: the response to a request that
// arrived on the protected server port leaves from that port, which is
// the other direction of the same pair of security associations.
func TestProtectedServerPortAnswers(t *testing.T) {
	log := testLogger(t)
	tr, err := ListenProtected("127.0.0.1:0", freePort(t), log, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	peer := rawSocket(t)
	req := &Message{Method: OPTIONS, RequestURI: "sip:ims.example.net"}
	req.Add("Via", "SIP/2.0/UDP "+addrOf(peer).String()+";branch=z9hG4bK2;rport")
	if _, err := peer.WriteToUDPAddrPort(req.Bytes(), tr.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	arrived := make(chan *Message, 1)
	go func() {
		m, _, err := tr.Receive()
		if err == nil {
			arrived <- m
		}
	}()
	var in *Message
	select {
	case in = <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("the request never reached Receive")
	}
	if err := tr.SendResponse(NewResponse(in, 200, NewTag()), Destination{Transport: UDP, Addr: addrOf(peer)}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, from, err := peer.ReadFromUDPAddrPort(buf); err != nil {
		t.Fatal(err)
	} else if from.Port() != tr.LocalAddr().Port() {
		t.Errorf("the response came from port %d, want the protected server port %d", from.Port(), tr.LocalAddr().Port())
	}
}

// TestProtectedClientPortOverTCP: a request over TCP leaves from the
// protected client port too, which TS 33.203 7.1 has the node open the
// connection from.
func TestProtectedClientPortOverTCP(t *testing.T) {
	log := testLogger(t)
	clientPort := freePort(t)
	tr, err := ListenProtected("127.0.0.1:0", clientPort, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	peer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	remote := netip.MustParseAddrPort(peer.Addr().String())

	accepted := make(chan netip.AddrPort, 1)
	go func() {
		c, err := peer.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		accepted <- netip.MustParseAddrPort(c.RemoteAddr().String())
		io := make([]byte, 2048)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		c.Read(io)
	}()
	req := &Message{Method: REGISTER, RequestURI: "sip:ims.example.net"}
	req.Add("Via", "SIP/2.0/TCP 127.0.0.1:"+strconv.Itoa(int(tr.LocalAddr().Port()))+";branch=z9hG4bK3;rport")
	if err := tr.SendRequest(req, Destination{Transport: TCP, Addr: remote}); err != nil {
		t.Fatal(err)
	}
	select {
	case from := <-accepted:
		if from.Port() != clientPort {
			t.Errorf("the connection came from port %d, want the protected client port %d", from.Port(), clientPort)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no connection reached the peer")
	}
}
