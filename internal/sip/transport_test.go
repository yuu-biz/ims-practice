package sip

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(t.Output(), nil))
}

func listen(t *testing.T, log *slog.Logger) *UDPTransport {
	t.Helper()
	tr, err := ListenUDP("127.0.0.1:0", log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

// rawSocket is a plain UDP socket standing in for a peer.
func rawSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addrOf(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

type received struct {
	msg *Message
	src netip.AddrPort
}

func receiveWithin(t *testing.T, tr *UDPTransport) received {
	t.Helper()
	ch := make(chan received, 1)
	go func() {
		m, src, err := tr.Receive()
		if err == nil {
			ch <- received{m, src}
		}
	}()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no message received")
		return received{}
	}
}

// readDatagram returns the next message that c receives, skipping the
// retransmissions of a request it has returned already. Those come from
// the sender's Timers A and E (RFC 3261 17.1.1.2, 17.1.2.2) whenever the
// test, standing in for the peer, takes longer than T1 to answer, as it
// may on a busy machine; the transaction layer of a real peer absorbs
// them. An ACK is never skipped: the UAC core sends the same ACK again
// for each retransmitted 2xx, and the INVITE client transaction for each
// retransmitted 300-699 response, which tests look for.
func readDatagram(t *testing.T, c *net.UDPConn) *Message {
	t.Helper()
	seen, _ := requestsRead.LoadOrStore(c, &sync.Map{})
	buf := make([]byte, 65535)
	for {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			t.Fatalf("no datagram: %v", err)
		}
		m, err := Parse(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if !m.IsRequest() || m.Method == ACK {
			return m
		}
		v, _ := ParseVia(m.Get("Via"))
		if _, again := seen.(*sync.Map).LoadOrStore(v.Branch()+" "+m.Get("CSeq"), true); !again {
			return m
		}
	}
}

// requestsRead holds, for each socket of readDatagram, the requests it
// has returned.
var requestsRead sync.Map

func requestWithVia(via string) string {
	return "OPTIONS sip:" + imsDomain + " SIP/2.0\r\n" +
		"Via: " + via + "\r\n" +
		"Via: SIP/2.0/UDP 198.51.100.7:5060;branch=z9hG4bKlower\r\n" +
		"Call-ID: t1\r\nCSeq: 1 OPTIONS\r\nContent-Length: 0\r\n\r\n"
}

func TestReceiveRecordsTheSourceInTheTopVia(t *testing.T) {
	server := listen(t, testLogger(t))
	peer := rawSocket(t)
	port := strconv.Itoa(int(addrOf(peer).Port()))

	tests := []struct {
		name, via, want string
	}{
		{"empty rport is filled in with received",
			"SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK1;rport",
			"SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK1;rport=" + port + ";received=127.0.0.1"},
		{"rport adds received even when sent-by matches",
			"SIP/2.0/UDP 127.0.0.1:" + port + ";rport;branch=z9hG4bK2",
			"SIP/2.0/UDP 127.0.0.1:" + port + ";rport=" + port + ";branch=z9hG4bK2;received=127.0.0.1"},
		{"sent-by address differs",
			"SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK3",
			"SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK3;received=127.0.0.1"},
		{"sent-by is a domain name",
			"SIP/2.0/UDP ue.example:5060;branch=z9hG4bK4",
			"SIP/2.0/UDP ue.example:5060;branch=z9hG4bK4;received=127.0.0.1"},
		{"sent-by is the source address",
			"SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK5",
			"SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := peer.WriteToUDPAddrPort([]byte(requestWithVia(tt.via)), server.LocalAddr()); err != nil {
				t.Fatal(err)
			}
			r := receiveWithin(t, server)
			if r.src != addrOf(peer) {
				t.Errorf("source = %v, want %v", r.src, addrOf(peer))
			}
			vias := r.msg.GetAll("Via")
			if vias[0] != tt.want {
				t.Errorf("top Via = %q, want %q", vias[0], tt.want)
			}
			if vias[1] != "SIP/2.0/UDP 198.51.100.7:5060;branch=z9hG4bKlower" {
				t.Errorf("second Via changed to %q", vias[1])
			}
		})
	}
}

// A node listens on ":5060", which Go opens as a dual-stack socket when
// the host has IPv6. An IPv4 peer must still appear as an IPv4 address,
// in received and as the destination of the response.
func TestWildcardListenAddressWithIPv4Peer(t *testing.T) {
	server, err := ListenUDP(":0", testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	peer := rawSocket(t)
	port := strconv.Itoa(int(addrOf(peer).Port()))
	serverAddr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), server.LocalAddr().Port())

	peer.WriteToUDPAddrPort([]byte(requestWithVia("SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK1;rport")), serverAddr)
	r := receiveWithin(t, server)
	if r.src != addrOf(peer) {
		t.Errorf("source = %v, want %v", r.src, addrOf(peer))
	}
	if got, want := r.msg.Get("Via"), "SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK1;rport="+port+";received=127.0.0.1"; got != want {
		t.Errorf("top Via = %q, want %q", got, want)
	}
	if err := server.SendResponse(NewResponse(r.msg, 200, "x")); err != nil {
		t.Fatal(err)
	}
	if got := readDatagram(t, peer); got.StatusCode != 200 {
		t.Errorf("peer got %d, want the 200", got.StatusCode)
	}
}

func TestReceiveSkipsDatagramsThatAreNotSIP(t *testing.T) {
	server := listen(t, testLogger(t))
	peer := rawSocket(t)
	for _, d := range []string{
		"\x00\x01 not sip",
		"OPTIONS sip:a SIP/2.0\r\nCall-ID: x\r\n\r\n", // no Via
		requestWithVia("SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKok"),
	} {
		peer.WriteToUDPAddrPort([]byte(d), server.LocalAddr())
	}
	if got := receiveWithin(t, server).msg.Get("Call-ID"); got != "t1" {
		t.Errorf("Call-ID = %q, want the valid request after the bad ones", got)
	}
}

func TestSendResponseFollowsTheTopVia(t *testing.T) {
	server := listen(t, testLogger(t))
	viaPort, rport := rawSocket(t), rawSocket(t)
	viaPortText := strconv.Itoa(int(addrOf(viaPort).Port()))
	rportText := strconv.Itoa(int(addrOf(rport).Port()))

	tests := []struct {
		name string
		via  string
		to   *net.UDPConn
	}{
		{"received and rport", "SIP/2.0/UDP 192.0.2.10:" + viaPortText + ";branch=z9hG4bK1;rport=" + rportText + ";received=127.0.0.1", rport},
		{"received without rport uses the sent-by port", "SIP/2.0/UDP 192.0.2.10:" + viaPortText + ";branch=z9hG4bK2;received=127.0.0.1", viaPort},
		{"sent-by alone", "SIP/2.0/UDP 127.0.0.1:" + viaPortText + ";branch=z9hG4bK3", viaPort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &Message{StatusCode: 200, Reason: "OK"}
			resp.Add("Via", tt.via)
			resp.Add("Call-ID", tt.name)
			if err := server.SendResponse(resp); err != nil {
				t.Fatal(err)
			}
			if got := readDatagram(t, tt.to).Get("Call-ID"); got != tt.name {
				t.Errorf("got Call-ID %q at the expected socket", got)
			}
		})
	}

	resp := &Message{StatusCode: 200, Reason: "OK"}
	resp.Add("Via", "SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK4")
	if err := server.SendResponse(resp); err == nil {
		t.Error("SendResponse over TCP succeeded")
	}
}

func TestTransportLogsEachMessage(t *testing.T) {
	var logs bytes.Buffer
	client := listen(t, slog.New(slog.NewJSONHandler(&logs, nil)))
	peer := rawSocket(t)
	req := mustParse(t, initialRegister)
	if err := client.SendRequest(req, addrOf(peer)); err != nil {
		t.Fatal(err)
	}
	readDatagram(t, peer)

	var line map[string]any
	last := strings.TrimSpace(logs.String())
	last = last[strings.LastIndexByte(last, '\n')+1:]
	if err := json.Unmarshal([]byte(last), &line); err != nil {
		t.Fatalf("log line %q: %v", last, err)
	}
	want := map[string]any{
		"direction": "send",
		"method":    "REGISTER",
		"call_id":   "apb03a0s09dkjdfglkj49111",
		"cseq":      "1 REGISTER",
		"branch":    "z9hG4bKnashds7",
		"src":       client.LocalAddr().String(),
		"dst":       addrOf(peer).String(),
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("log %s = %v, want %v", k, line[k], v)
		}
	}
}

func TestGeneratedIdentifiers(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		b := NewBranch()
		if !strings.HasPrefix(b, BranchMagicCookie) || seen[b] {
			t.Fatalf("branch %q lacks the magic cookie or repeats", b)
		}
		seen[b] = true
		for _, id := range []string{NewTag(), NewCallID()} {
			if id == "" || seen[id] {
				t.Fatalf("identifier %q is empty or repeats", id)
			}
			seen[id] = true
		}
	}
}

func TestCRLFKeepaliveDatagramIsIgnored(t *testing.T) {
	tr := listen(t, testLogger(t))
	peer := rawSocket(t)
	peer.WriteToUDPAddrPort([]byte("\r\n"), tr.LocalAddr())
	peer.WriteToUDPAddrPort([]byte(requestWithVia("SIP/2.0/UDP 198.51.100.7:5060;branch=z9hG4bKkeepalive")), tr.LocalAddr())
	m, _, err := tr.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != OPTIONS {
		t.Errorf("got %v, want the request that followed the keepalive", m)
	}
}
