package hepagent

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hep"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const options = "OPTIONS sip:ims.mnc001.mcc001.3gppnetwork.org SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKhep1;rport\r\n" +
	"Max-Forwards: 70\r\n" +
	"Call-ID: hep-call-1\r\n" +
	"CSeq: 1 OPTIONS\r\n" +
	"Content-Length: 0\r\n\r\n"

func udpSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addrOf(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

func readDatagram(t *testing.T, c *net.UDPConn) []byte {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("no datagram: %v", err)
	}
	return buf[:n]
}

func readHEP(t *testing.T, collector *net.UDPConn) hep.Packet {
	t.Helper()
	p, err := hep.Unmarshal(readDatagram(t, collector))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newAgent(t *testing.T, collector *net.UDPConn) *Agent {
	t.Helper()
	a, err := New(addrOf(collector).String(), 7, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

// The transport listens on every interface, as the nodes in Compose do.
// Go makes that a dual-stack socket, which reports its own address as
// [::] and may be handed an IPv4 peer as ::ffff:a.b.c.d. The copies
// must name the IPv4 addresses that the peer sees.
func TestSIPCopiesWhatCrossesTheWire(t *testing.T) {
	collector := udpSocket(t)
	agent := newAgent(t, collector)
	tr, err := sip.ListenObserved("0.0.0.0:0", slog.New(slog.NewTextHandler(io.Discard, nil)), agent.SIP("pcscf"))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	local := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), tr.LocalAddr().Port())
	peer := udpSocket(t)

	req, err := sip.Parse([]byte(options))
	if err != nil {
		t.Fatal(err)
	}
	mapped := netip.AddrPortFrom(netip.AddrFrom16(addrOf(peer).Addr().As16()), addrOf(peer).Port())
	if err := tr.SendRequest(req, sip.Destination{Transport: sip.UDP, Addr: mapped}); err != nil {
		t.Fatal(err)
	}
	sent := readDatagram(t, peer)
	p := readHEP(t, collector)
	if !bytes.Equal(p.Payload, sent) {
		t.Errorf("payload\n%q\nwant the datagram\n%q", p.Payload, sent)
	}
	want := hep.Packet{IPProtocol: hep.IPProtocolUDP, Source: local, Destination: addrOf(peer), ProtocolType: hep.ProtocolTypeSIP,
		CaptureAgentID: 7, CorrelationID: "hep-call-1", NodeName: "pcscf"}
	checkHeader(t, p, want)

	if _, err := peer.WriteToUDPAddrPort([]byte(options), local); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.Receive(); err != nil {
		t.Fatal(err)
	}
	p = readHEP(t, collector)
	if string(p.Payload) != options {
		t.Errorf("payload\n%q\nwant the datagram\n%q", p.Payload, options)
	}
	want.Source, want.Destination = addrOf(peer), local
	checkHeader(t, p, want)
}

// The HEP packet carries the time at which the message crossed the wire,
// which the transport keeps in the capture, and not the time at which
// the observer ran: a sent message is observed only once its write has
// returned, and the collector orders the copies by this time.
func TestSIPCarriesTheTimeOfTheWire(t *testing.T) {
	collector := udpSocket(t)
	agent := newAgent(t, collector)
	req, err := sip.Parse([]byte(options))
	if err != nil {
		t.Fatal(err)
	}
	crossed := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	agent.SIP("pcscf")(sip.Capture{Transport: sip.UDP, Sent: true, Source: addrOf(collector), Destination: addrOf(collector),
		Message: req, Wire: []byte(options), Time: crossed})
	if p := readHEP(t, collector); !p.Time.Equal(crossed) {
		t.Errorf("time %v, want %v, when the message crossed the wire", p.Time, crossed)
	}
}

func checkHeader(t *testing.T, got, want hep.Packet) {
	t.Helper()
	if time.Since(got.Time) > time.Minute {
		t.Errorf("time %v, want about now", got.Time)
	}
	got.Time, got.Payload = time.Time{}, nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("packet %+v\nwant   %+v", got, want)
	}
}

func TestDiameterCopiesTheJSONRendering(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	collector := udpSocket(t)
	agent := newAgent(t, collector)
	m := diam.NewMessage(300, diam.ProxiableFlag, diameter.ApplicationIDCx, 1, 2, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("icscf;1;2"))
	src, dst := netip.MustParseAddrPort("172.28.0.10:3868"), netip.MustParseAddrPort("172.28.0.20:40000")

	agent.Diameter("hss")(diameter.Capture{Sent: true, Source: src, Destination: dst, Message: m})

	p := readHEP(t, collector)
	wantPayload, err := diameter.EncodeJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Payload, wantPayload) {
		t.Errorf("payload %s, want %s", p.Payload, wantPayload)
	}
	checkHeader(t, p, hep.Packet{IPProtocol: hep.IPProtocolTCP, Source: src, Destination: dst, ProtocolType: hep.ProtocolTypeDiameterJSON,
		CaptureAgentID: 7, CorrelationID: "icscf;1;2", NodeName: "hss"})
}

func TestNilAgentObservesNothing(t *testing.T) {
	var a *Agent
	if a.SIP("pcscf") != nil || a.Diameter("hss") != nil || a.Close() != nil {
		t.Error("a nil Agent must give nil observers")
	}
}
