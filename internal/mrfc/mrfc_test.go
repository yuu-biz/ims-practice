package mrfc_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// These tests run the MRF in one process: MRFPs of internal/agw behind
// their HTTP API, MRFC instances on loopback, and a UA that plays the
// AS. The parties whose media the AS connects to the MRF are UDP
// sockets, whose addresses the AS puts into the SDP it sends, as a B2BUA
// in third party call control does (TS 24.229 5.7.5).

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ipLocator finds a host that is an IP address, which is all these
// tests use.
type ipLocator struct{}

func (ipLocator) Locate(_ context.Context, host string, port int, _ sip.Transport) ([]sip.Destination, error) {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil, err
	}
	return []sip.Destination{{Transport: sip.UDP, Addr: netip.AddrPortFrom(addr, uint16(port))}}, nil
}

// announcements are those of deploy/announcements, which the MRFP of
// the network plays.
var announcements = map[string]string{
	"notice": "../../deploy/announcements/notice.wav",
	"cat1":   "../../deploy/announcements/cat1.wav",
}

// testMRFP is an MRFP and the client of its control API.
type testMRFP struct {
	relay  *agw.Relay
	client *agw.Client
}

// startMRFP starts an MRFP on a range of RTP ports of its own
// (rtptest.PortRange).
func startMRFP(t *testing.T) testMRFP {
	t.Helper()
	clips, err := agw.LoadAnnouncements(announcements)
	if err != nil {
		t.Fatal(err)
	}
	minPort, maxPort := rtptest.PortRange(t, 40)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, clips, quiet())
	if err != nil {
		t.Fatal(err)
	}
	// A dialog that is still up when the test ends leaves its
	// termination behind, holding RTP ports of the range, once the API
	// that the MRFC would subtract it through has closed. Let go of them,
	// so that the next run of the test finds the range free.
	t.Cleanup(func() {
		for _, c := range relay.Contexts() {
			relay.Subtract(c.ID)
		}
	})
	api := httptest.NewServer(agw.Handler(relay))
	t.Cleanup(api.Close)
	return testMRFP{relay: relay, client: agw.NewClient(api.URL)}
}

// processor is the MRFP as the MRFC knows it by name.
func (m testMRFP) processor(name string) mrfc.Processor {
	return mrfc.Processor{Name: name, Control: m.client, Announcements: m.client}
}

// testMRFC is an MRFC instance on loopback, and the URI to reach it.
type testMRFC struct {
	*mrfc.MRFC
	host string
}

func startMRFC(t *testing.T, store mrfc.Store, processors ...mrfc.Processor) testMRFC {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	notify := http.NewServeMux()
	notifyServer := httptest.NewServer(notify)
	t.Cleanup(notifyServer.Close)
	addr := tr.LocalAddr()
	m, err := mrfc.New(mrfc.Config{Host: addr.Addr().String(), Port: int(addr.Port()), NotifyURL: notifyServer.URL + "/notify"},
		processors, store, ipLocator{}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	notify.Handle("/notify", m.NotifyHandler())
	if err := m.Listen(tr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return testMRFC{MRFC: m, host: addr.String()}
}

// newUA is a UA on loopback: the AS, or a caller. Without onCall it
// takes no calls.
func newUA(t *testing.T, user string, onCall func(*sip.IncomingCall)) *sip.UA {
	t.Helper()
	if onCall == nil {
		onCall = func(c *sip.IncomingCall) { c.Reject(480) }
	}
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy: tr.LocalAddr().String(), Contact: "sip:" + user + "@" + tr.LocalAddr().String(), OnCall: onCall,
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua
}

func party(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addrOf(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

// offerFrom is the SDP offer of a party at addr, as the AS passes it
// on: AMR-WB first and PCMU among the others, as the UE offers.
func offerFrom(t *testing.T, addr netip.AddrPort, direction string) sip.Body {
	t.Helper()
	o, err := sdp.AudioOffer("1", addr, sdp.VoLTECodecs())
	if err != nil {
		t.Fatal(err)
	}
	o.AudioMedia().SetDirection(direction)
	return sip.Body{Type: "application/sdp", Content: o.Bytes()}
}

func invite(t *testing.T, as *sip.UA, requestURI string, body sip.Body) *sip.OutgoingCall {
	t.Helper()
	out, err := as.Invite(sip.Invitation{RequestURI: requestURI, From: "sip:mmtel@ims.example.net", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// final waits for the final response to out.
func final(t *testing.T, out *sip.OutgoingCall) *sip.Message {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-out.Responses():
			if !ok {
				t.Fatalf("no final response: %v", out.Err())
			}
			if m.StatusCode >= 200 {
				return m
			}
		case <-timeout:
			t.Fatal("no final response")
		}
	}
}

func sameFrames(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d packets, want the %d frames of the file", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("packet %d differs from frame %d of the file", i, i)
		}
	}
}

func waitDone(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

func waitNoContexts(t *testing.T, r *agw.Relay) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(r.Contexts()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the MRFP still holds %v", r.Contexts())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAnnouncementAfterAnswer is RFC 4240 3 and 3.1: the AS sends
// sip:annc with play=, the MRFC answers 200 with the SDP of its MRFP,
// the party gets the announcement in RTP once the ACK has come, and the
// MRFC sends BYE when it has played (TS 24.229 5.8.2.2.1, 5.8.3.2.1).
func TestAnnouncementAfterAnswer(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	as := newUA(t, "mmtel", nil)
	caller := party(t)

	out := invite(t, as, "sip:annc@"+m.host+";play=/provisioned/notice", offerFrom(t, addrOf(caller), "sendrecv"))
	ok := final(t, out)
	if ok.StatusCode != 200 {
		t.Fatalf("got %d, want 200", ok.StatusCode)
	}
	answer, err := sdp.Parse(ok.Body)
	if err != nil {
		t.Fatal(err)
	}
	audio := answer.AudioMedia()
	local := media.relay.Contexts()[0].Terminations["t1"]
	if audio == nil || answer.Address(*audio) != local.Local || !slicesEqual(audio.Formats, []int{sdp.PCMU}) || answer.Direction(*audio) != "sendonly" {
		t.Errorf("answer\n%s\nwant PCMU from the MRFP termination %v, sendonly", ok.Body, local.Local)
	}
	if pai := ok.Get("P-Asserted-Identity"); pai != "<sip:annc@"+m.host+">" {
		t.Errorf("P-Asserted-Identity %q (TS 24.229 5.8.2.1.1.1)", pai)
	}

	want := rtptest.Frames(t, announcements["notice"])
	sameFrames(t, rtptest.ReadPCMU(t, caller, len(want), 10*time.Second), want)
	waitDone(t, out.Done(), "the dialog with the MRFC")
	if bye := out.EndedBy(); bye == nil || bye.Method != sip.BYE {
		t.Errorf("the dialog ended by %v, want the MRFC's BYE", bye)
	}
	waitNoContexts(t, media.relay)
	if extra := rtptest.Queued(t, caller); len(extra) != 0 {
		t.Errorf("%d packets after the announcement", len(extra))
	}
}

// TestAnnouncementAsEarlyMedia is the gateway model of TS 24.628 Annex G
// and TS 24.182 4.5.5.3.7: the AS holds the caller's dialog early, sends
// it a reliable 183 with the SDP of the MRFP and P-Early-Media sendonly,
// and rejects the call once the announcement has played. The AS sends
// the MRFC an INVITE without SDP, so that it can hold back the ACK, and
// with it the start of the announcement, until the caller has the 183.
func TestAnnouncementAsEarlyMedia(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	callerMedia := party(t)
	// The AS takes the caller's INVITE on one UA and sends its own to
	// the MRFC from another, so that the call handler needs no UA that
	// is set after it.
	toMRFC := newUA(t, "mmtel", nil)
	as := newUA(t, "mmtel", func(c *sip.IncomingCall) {
		callerOffer, _ := sdp.Parse(c.Request.Body)
		out, err := toMRFC.Invite(sip.Invitation{RequestURI: "sip:annc@" + m.host + ";play=/provisioned/notice", From: "sip:mmtel@ims.example.net"})
		if err != nil {
			c.Reject(500)
			return
		}
		var ok *sip.Message
		for resp := range out.Responses() {
			ok = resp
		}
		mrfpOffer, err := sdp.Parse(ok.Body)
		if err != nil || ok.StatusCode != 200 {
			c.Reject(500)
			return
		}
		mrfpAddr, _, _ := mrfpOffer.Audio()
		toCaller, _ := sdp.Answer(callerOffer, "2", mrfpAddr)
		toCaller.AudioMedia().SetDirection("sendonly")
		pem := sip.PEarlyMedia{Directions: []string{sip.EarlyMediaSendonly}}.Header()
		if err := c.Progress(183, sip.Body{Type: "application/sdp", Content: toCaller.Bytes()}, pem); err != nil {
			c.Reject(500)
			return
		}
		callerAddr, _, _ := callerOffer.Audio()
		toMRFP, _ := sdp.Answer(mrfpOffer, "2", callerAddr)
		toMRFP.AudioMedia().SetDirection("recvonly")
		if err := out.Ack(sip.Body{Type: "application/sdp", Content: toMRFP.Bytes()}); err != nil {
			c.Reject(500)
			return
		}
		<-out.Done()
		c.Reject(603, sip.NewReason(sip.ReasonSIP, 603, "Decline").Header())
	})
	caller := newUA(t, "alice", nil)
	call, err := caller.Invite(sip.Invitation{
		RequestURI: "sip:" + as.Stack().LocalAddr().String(), From: "sip:alice@ims.example.net",
		Headers: []sip.HeaderField{sip.PEarlyMedia{Supported: true}.Header()}, Body: offerFrom(t, addrOf(callerMedia), "sendrecv"),
	})
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for final := false; !final; {
		select {
		case resp, ok := <-call.Responses():
			if !ok {
				t.Fatalf("no final response to the caller: %v", call.Err())
			}
			if resp.StatusCode == 183 && resp.Get("P-Early-Media") != "sendonly" {
				t.Errorf("183 without P-Early-Media sendonly:\n%s", resp)
			}
			if final = resp.StatusCode >= 200; final && resp.StatusCode != 603 {
				t.Errorf("the caller got %d, want 603 after the announcement", resp.StatusCode)
			}
		case <-timeout:
			t.Fatal("no final response to the caller")
		}
	}
	// The whole announcement reached the caller in the early dialog: it
	// was all there by the time the final response came.
	var got [][]byte
	for _, p := range rtptest.Queued(t, callerMedia) {
		got = append(got, p.Payload)
	}
	sameFrames(t, got, rtptest.Frames(t, announcements["notice"]))
	waitNoContexts(t, media.relay)
	if extra := rtptest.Queued(t, callerMedia); len(extra) != 0 {
		t.Errorf("%d packets after the 603", len(extra))
	}
}

// TestLoopStoppedByBYE is TS 24.229 5.8.3.1.1: a BYE from the AS stops
// an announcement that loops, such as a customized alerting tone (TS
// 24.182 4.5.5.3.7), and the MRFP lets go of its termination.
func TestLoopStoppedByBYE(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	as := newUA(t, "mmtel", nil)
	caller := party(t)
	out := invite(t, as, "sip:annc@"+m.host+";play=/provisioned/cat1;repeat=forever;delay=100", offerFrom(t, addrOf(caller), "sendrecv"))
	if ok := final(t, out); ok.StatusCode != 200 {
		t.Fatalf("got %d, want 200", ok.StatusCode)
	}
	tune := rtptest.Frames(t, announcements["cat1"])
	silence := bytes.Repeat([]byte{0xFF}, mixer.FrameSize)
	want := append(append(append([][]byte{}, tune...), silence, silence, silence, silence, silence), tune[:10]...)
	buf := make([]byte, 1500)
	for i, frame := range want {
		caller.SetReadDeadline(time.Now().Add(time.Second))
		n, err := caller.Read(buf)
		if err != nil {
			t.Fatalf("packet %d of the loop: %v", i, err)
		}
		p, _ := rtp.Unmarshal(buf[:n])
		if !bytes.Equal(p.Payload, frame) {
			t.Fatalf("packet %d is not frame %d of the loop", i, i)
		}
	}
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	// The MRFP closes the socket of a termination before it lets go of
	// it, so once the context is gone the loop can send nothing more.
	waitNoContexts(t, media.relay)
}

// speaker is a party that sends a tone to the MRFP termination of its
// leg, every 20 ms, until the test ends.
func speak(t *testing.T, c *net.UDPConn, to netip.AddrPort, hz float64) {
	samples := rtptest.Tone(hz, 50*mixer.FrameSize)
	stop := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() { close(stop); <-done })
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			f := i % 50
			p := rtp.Packet{PayloadType: sdp.PCMU, SequenceNumber: uint16(i), Timestamp: uint32(i * mixer.FrameSize), SSRC: uint32(hz),
				Payload: g711.Encode(samples[f*mixer.FrameSize : (f+1)*mixer.FrameSize])}
			c.WriteToUDPAddrPort(p.Marshal(), to)
		}
	}()
}

// mrfpAddress is where the answer in a 2xx asks the party to send.
func mrfpAddress(t *testing.T, resp *sip.Message) netip.AddrPort {
	t.Helper()
	s, err := sdp.Parse(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Audio()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestConferenceMixesTheOthers is RFC 4240 5 and TS 23.333 6.2.9: three
// INVITEs to sip:conf=x go into one context, and each party hears the
// other two and not itself. The conference ends with its last party.
func TestConferenceMixesTheOthers(t *testing.T) {
	media := startMRFP(t)
	store := mrfc.NewMemoryStore()
	m := startMRFC(t, store, media.processor("mrfp1"))
	as := newUA(t, "mmtel", nil)
	hz := []float64{400, 1000, 1700}
	var calls []*sip.OutgoingCall
	var parties []*net.UDPConn
	for i := range hz {
		p := party(t)
		out := invite(t, as, "sip:conf=X@"+m.host, offerFrom(t, addrOf(p), "sendrecv"))
		ok := final(t, out)
		if ok.StatusCode != 200 {
			t.Fatalf("party %d got %d", i, ok.StatusCode)
		}
		speak(t, p, mrfpAddress(t, ok), hz[i])
		calls, parties = append(calls, out), append(parties, p)
	}
	if cs := media.relay.Contexts(); len(cs) != 1 || len(cs[0].Terminations) != 3 {
		t.Fatalf("the MRFP holds %v, want one context of three terminations", cs)
	}
	if _, ok := store.Conference("x"); !ok {
		t.Error("the Store has no conference x (the ID does not depend on case)")
	}
	// Each party's socket keeps what comes while the test reads the
	// others.
	for i, party := range parties {
		others := slices.Delete(slices.Clone(hz), i, i+1)
		samples := rtptest.Heard(t, party, others, 30, 10*time.Second)
		for j, f := range hz {
			p := rtptest.Power(samples, f)
			if j == i && p > 1e4 {
				t.Errorf("party %d hears itself at %v Hz (%.0f)", i, f, p)
			}
			if j != i && p < 1e5 {
				t.Errorf("party %d hears party %d at %v Hz only at %.0f", i, j, f, p)
			}
		}
	}
	for _, out := range calls {
		out.Bye()
	}
	waitNoContexts(t, media.relay)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := store.Conference("x"); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the Store still has the conference after its last party left")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestConferenceAcrossInstances: a party of a conference that
// reaches another MRFC instance joins the same context on the same
// MRFP, which the Store names. The second instance lists the MRFPs the
// other way round, so that its own choice would have been the other
// MRFP.
func TestConferenceAcrossInstances(t *testing.T) {
	first, second := startMRFP(t), startMRFP(t)
	p1 := first.processor("mrfp1")
	p2 := second.processor("mrfp2")
	store := mrfc.NewMemoryStore()
	m1 := startMRFC(t, store, p1, p2)
	m2 := startMRFC(t, store, p2, p1)
	as := newUA(t, "mmtel", nil)

	// A conference ID that the first instance puts on mrfp1 and the
	// second, by its own choice, on mrfp2.
	id := ""
	for i := 0; id == ""; i++ {
		candidate := "room" + strconv.Itoa(i)
		out := invite(t, as, "sip:conf="+candidate+"@"+m1.host, offerFrom(t, addrOf(party(t)), "sendrecv"))
		if ok := final(t, out); ok.StatusCode != 200 {
			t.Fatalf("got %d", ok.StatusCode)
		}
		if media, _ := store.Conference(candidate); media.MRFP == "mrfp1" {
			id = candidate
			defer out.Bye()
			continue
		}
		out.Bye()
		waitDone(t, out.Done(), "the trial dialog")
	}
	a, b := party(t), party(t)
	out := invite(t, as, "sip:conf="+id+"@"+m2.host, offerFrom(t, addrOf(b), "sendrecv"))
	ok := final(t, out)
	if ok.StatusCode != 200 {
		t.Fatalf("got %d", ok.StatusCode)
	}
	defer out.Bye()
	contexts := first.relay.Contexts()
	if len(contexts) != 1 || len(contexts[0].Terminations) != 2 || len(second.relay.Contexts()) != 0 {
		t.Fatalf("mrfp1 holds %v and mrfp2 %v; want both parties in one context of mrfp1", contexts, second.relay.Contexts())
	}
	// The two parties now talk through the context.
	var firstParty netip.AddrPort
	for tid, term := range contexts[0].Terminations {
		if term.Local != mrfpAddress(t, ok) {
			firstParty = term.Local
			if _, err := first.relay.Modify(contexts[0].ID, tid, agw.TerminationRequest{Remote: addrOf(a)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	b.WriteToUDPAddrPort((rtp.Packet{PayloadType: sdp.PCMU, Payload: []byte("hello")}).Marshal(), mrfpAddress(t, ok))
	if got := rtptest.Next(t, a, 5*time.Second, "the second party's packet"); string(got.Payload) != "hello" {
		t.Errorf("the first party got %q from %v, want the second party's packet", got.Payload, firstParty)
	}
}

func TestRefusals(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	as := newUA(t, "mmtel", nil)
	pcmuOffer := offerFrom(t, netip.MustParseAddrPort("127.0.0.1:4000"), "sendrecv")
	amrOnly, _ := sdp.AudioOffer("1", netip.MustParseAddrPort("127.0.0.1:4000"), sdp.VoLTECodecs()[:4])
	tests := []struct {
		uri     string
		body    sip.Body
		status  int
		warning string
	}{
		{"sip:annc@" + m.host, pcmuOffer, 400, "Mandatory play parameter missing"},                           // RFC 4240 3
		{"sip:annc@" + m.host + ";play=/provisioned/busy", pcmuOffer, 404, "Announcement content not found"}, // RFC 4240 3
		{"sip:annc@" + m.host + ";play=http://as.example.net/busy.wav", pcmuOffer, 400, "Announcement content could not be retrieved"},
		{"sip:conf@" + m.host, pcmuOffer, 404, "Not Found"},                                                        // RFC 4240 5
		{"sip:dialog@" + m.host + ";voicexml=http://as.example.net/a.vxml", pcmuOffer, 488, "Not Acceptable Here"}, // RFC 4240 2
		{"sip:annc@" + m.host + ";play=/provisioned/notice", sip.Body{Type: "application/sdp", Content: amrOnly.Bytes()}, 488, "Incompatible media format"},
		{"sip:conf=1@" + m.host, sip.Body{Type: "application/sdp", Content: amrOnly.Bytes()}, 488, "Incompatible media format"},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			resp := final(t, invite(t, as, tt.uri, tt.body))
			if resp.StatusCode != tt.status || !bytes.Contains([]byte(resp.Get("Warning")), []byte(strconv.Quote(tt.warning))) {
				t.Errorf("got %d with Warning %q, want %d with %q", resp.StatusCode, resp.Get("Warning"), tt.status, tt.warning)
			}
		})
	}
	if n := len(media.relay.Contexts()); n != 0 {
		t.Errorf("the refusals left %d contexts", n)
	}
}

// TestHoldInAConference is TS 24.229 5.8.4.1.1.1: a re-INVITE that
// offers sendonly changes the mode of the party's termination, and the
// MRFC answers recvonly.
func TestHoldInAConference(t *testing.T) {
	media := startMRFP(t)
	m := startMRFC(t, mrfc.NewMemoryStore(), media.processor("mrfp1"))
	as := newUA(t, "mmtel", nil)
	p := party(t)
	out := invite(t, as, "sip:conf=hold@"+m.host, offerFrom(t, addrOf(p), "sendrecv"))
	if ok := final(t, out); ok.StatusCode != 200 {
		t.Fatalf("got %d", ok.StatusCode)
	}
	defer out.Bye()
	resp, err := out.ReInvite(offerFrom(t, addrOf(p), "sendonly"))
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := sdp.Parse(resp.Body)
	if resp.StatusCode != 200 || answer.Direction(*answer.AudioMedia()) != "recvonly" {
		t.Errorf("re-INVITE got %d with\n%s\nwant 200 with recvonly", resp.StatusCode, resp.Body)
	}
	for _, term := range media.relay.Contexts()[0].Terminations {
		if term.Mode != agw.ReceiveOnly {
			t.Errorf("termination in mode %q, want recvonly", term.Mode)
		}
	}
}

func slicesEqual(a, b []int) bool { return fmt.Sprint(a) == fmt.Sprint(b) }
