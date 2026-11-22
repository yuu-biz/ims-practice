package lab

import (
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hep"
)

func TestCollectorOverUDP(t *testing.T) {
	h := newHarness(t, Config{})
	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- h.l.ServeHEP(conn) }()

	raw, err := net.Dial("udp", conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Write([]byte("HEP3 but not really")); err != nil {
		t.Fatal(err)
	}

	sender, err := hep.NewSender(conn.LocalAddr().String(), 8, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	const id = "call-udp@127.0.0.1"
	// The sender is on 127.0.0.1, so a message from 127.0.0.1 is its own.
	err = sender.Send(hep.Packet{
		IPProtocol:    hep.IPProtocolUDP,
		Source:        netip.MustParseAddrPort("127.0.0.1:5060"),
		Destination:   pcscf.addr,
		Time:          start,
		ProtocolType:  hep.ProtocolTypeSIP,
		CorrelationID: id,
		NodeName:      "ue",
		Payload:       []byte(invite(id)),
	})
	if err != nil {
		t.Fatal(err)
	}

	var messages []Message
	for deadline := time.Now().Add(5 * time.Second); len(messages) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("no message reached the API")
		}
		time.Sleep(10 * time.Millisecond)
		h.get("/api/messages", &messages)
	}
	m := messages[0]
	if len(messages) != 1 || m.ID != 1 || m.CallID != id || m.Node != "ue" || m.Direction != DirectionSend || !m.Time.Equal(start) {
		t.Errorf("messages = %+v, want only the INVITE, sent by ue", messages)
	}
	h.wantCall(id, CallCalling, 0)

	conn.Close()
	if err := <-served; err != nil {
		t.Errorf("ServeHEP after Close: %v", err)
	}
}
