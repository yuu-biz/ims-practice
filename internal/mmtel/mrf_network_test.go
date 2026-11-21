package mmtel_test

import (
	"bytes"
	"context"
	"maps"
	"net"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// The tests in this file run the services of the AS that play media of
// the MRF through the whole IMS, with an MRFC and an MRFP on loopback.
// The caller's media is a UDP socket, so that the test reads every RTP
// packet that reaches it, early media included.

const frameSize = rtptest.FrameSize

func rtpSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addrOf(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

// pcmuOffer is an SDP offer of audio at addr with codecs.
func pcmuOffer(t *testing.T, addr netip.AddrPort, codecs []sdp.Codec) sip.Body {
	t.Helper()
	o, err := sdp.AudioOffer("1", addr, codecs)
	if err != nil {
		t.Fatal(err)
	}
	return sip.Body{Type: "application/sdp", Content: o.Bytes()}
}

// inviteWithMedia calls target from the subscriber's UA with its media
// at media, offering codecs, and takes early media (RFC 5009).
func (s *subscriber) inviteWithMedia(target string, media netip.AddrPort, codecs []sdp.Codec) *sip.OutgoingCall {
	s.n.t.Helper()
	out, err := s.ua.Invite(sip.Invitation{RequestURI: target, From: s.impu, To: target,
		Route:   append([]string{"<sip:pcscf." + domain + ":5060;lr>"}, s.reg.ServiceRoute...),
		Headers: []sip.HeaderField{sip.PEarlyMedia{Supported: true}.Header()}, Body: pcmuOffer(s.n.t, media, codecs)})
	if err != nil {
		s.n.t.Fatal(err)
	}
	s.n.t.Cleanup(func() { out.Bye() })
	return out
}

var pcmuOnly = []sdp.Codec{{PayloadType: sdp.PCMU, Name: sdp.NamePCMU, ClockRate: 8000}}

// startsWith tells whether got begins with every frame of want.
func startsWith(got, want [][]byte) bool {
	if len(got) < len(want) {
		return false
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			return false
		}
	}
	return true
}

func waitNoContexts(t *testing.T, r *agw.Relay) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(r.Contexts()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the MRFP still holds %v", r.Contexts())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// mrfpAnswer checks that resp has an SDP answer that points at the
// MRFP, sendonly, with the early media authorized, and returns it.
func mrfpAnswer(t *testing.T, n *network, resp *sip.Message) sdp.Session {
	t.Helper()
	answer, err := sdp.Parse(resp.Body)
	if err != nil || answer.AudioMedia() == nil {
		t.Fatalf("%d without an SDP answer: %v\n%s", resp.StatusCode, err, resp)
	}
	audio := *answer.AudioMedia()
	if port := answer.Address(audio).Port(); port < n.mrfpPorts[0] || port > n.mrfpPorts[1] || answer.Direction(audio) != "sendonly" ||
		!slices.Equal(audio.Formats, []int{sdp.PCMU}) {
		t.Errorf("%d has the answer\n%s\nwant PCMU, sendonly, from the MRFP", resp.StatusCode, resp.Body)
	}
	if !resp.Requires(sip.Option100rel) || resp.Get("P-Early-Media") != "sendonly" {
		t.Errorf("%d has Require %q and P-Early-Media %q; want a reliable response that authorizes early media (TS 24.628 G.1)",
			resp.StatusCode, resp.GetAll("Require"), resp.Get("P-Early-Media"))
	}
	return answer
}

// TestAnnouncementBeforeBarring is TS 24.611 4.5.2.6.1 with TS 24.628
// 4.2.4 and G.1: the callee bars all incoming calls, and its AS plays
// the caller the announcement as early media before the 603, which
// carries the Reason of the rejection.
func TestAnnouncementBeforeBarring(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true, announcement: "notice"})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	callee.set(ue.BAIC, "")
	media := rtpSocket(t)
	out := caller.inviteWithMedia(callee.tel, addrOf(media), pcmuOnly)
	done := make(chan []*sip.Message, 1)
	go func() {
		var got []*sip.Message
		for m := range out.Responses() {
			got = append(got, m)
		}
		done <- got
	}()

	resps := receive(t, done, "final response")
	// The whole announcement reached the caller before the 603: it was
	// all there by the time the 603 came.
	var got [][]byte
	for _, p := range rtptest.Queued(t, media) {
		got = append(got, p.Payload)
	}
	if want := rtptest.Frames(t, mrfAnnouncements["notice"]); len(got) != len(want) || !startsWith(got, want) {
		t.Errorf("the caller got %d packets before the 603; want the %d frames of the announcement, in order", len(got), len(want))
	}
	if codes := statuses(resps); !slices.Equal(codes, []int{183, 603}) {
		t.Fatalf("the caller got %v, want the 183 of the announcement and then 603", codes)
	}
	mrfpAnswer(t, n, resps[0])
	if rs := sip.Reasons(resps[1]); len(rs) != 1 || rs[0].Protocol != sip.ReasonSIP || rs[0].Cause != 603 {
		t.Errorf("603 with Reason %q; want Reason SIP cause 603", resps[1].GetAll("Reason"))
	}
	if len(callee.calls) != 0 {
		t.Error("the barred call reached the callee")
	}
	waitNoContexts(t, n.mrfp)
	if extra := rtptest.Queued(t, media); len(extra) != 0 {
		t.Errorf("%d packets after the 603", len(extra))
	}
}

// TestBarringWithoutPCMU: a caller whose offer has no PCMU cannot hear
// the MRFP, and gets the 603 at once.
func TestBarringWithoutPCMU(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true, announcement: "notice"})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	callee.set(ue.BAIC, "")
	amrWB := slices.DeleteFunc(sdp.VoLTECodecs(), func(c sdp.Codec) bool { return strings.EqualFold(c.Name, sdp.NamePCMU) })
	resps := readAll(t, caller.inviteWithMedia(callee.tel, addrOf(rtpSocket(t)), amrWB))
	if codes := statuses(resps); !slices.Equal(codes, []int{603}) || len(sip.Reasons(resps[0])) != 0 {
		t.Errorf("the caller got %v with Reason %q, want 603 alone", codes, resps[len(resps)-1].GetAll("Reason"))
	}
}

// setTone has the operator provision the CAT of subscriber k (TS 24.182
// 4.5.2) with the provisioning API of the HSS.
func (n *network) setTone(k int, tone string, active bool) {
	n.t.Helper()
	body := `{"tone": "` + tone + `", "active": ` + strconv.FormatBool(active) + `}`
	w := httptest.NewRecorder()
	n.provisioning.ServeHTTP(w, httptest.NewRequest("PUT", "/subscriptions/00101000000000"+strconv.Itoa(k)+"@"+domain+"/customized-alerting-tone", strings.NewReader(body)))
	if w.Code != 200 {
		n.t.Fatalf("CAT of subscriber %d: %d %s", k, w.Code, w.Body)
	}
}

// earlyCaller is a caller whose media is a UDP socket, and which answers
// the UPDATE with the callee's media that the AS sends once the callee
// answers (TS 24.182 4.5.5.3.7).
type earlyCaller struct {
	media   *net.UDPConn
	out     *sip.OutgoingCall
	updates chan *sip.Message
	// resps are the responses to the INVITE, with when each came.
	resps chan timed
}

type timed struct {
	m  *sip.Message
	at time.Time
}

func (s *subscriber) callWithEarlyMedia(target string, codecs []sdp.Codec) *earlyCaller {
	s.n.t.Helper()
	c := &earlyCaller{media: rtpSocket(s.n.t), updates: make(chan *sip.Message, 2), resps: make(chan timed, 8)}
	out, err := s.ua.Invite(sip.Invitation{RequestURI: target, From: s.impu, To: target,
		Route:   append([]string{"<sip:pcscf." + domain + ":5060;lr>"}, s.reg.ServiceRoute...),
		Headers: []sip.HeaderField{sip.PEarlyMedia{Supported: true}.Header()}, Body: pcmuOffer(s.n.t, addrOf(c.media), codecs),
		OnRequest: func(r *sip.InDialogRequest) {
			if !r.Offer {
				r.Accept(sip.Body{})
				return
			}
			c.updates <- r.Request
			offer, _ := sdp.Parse(r.Request.Body)
			answer, err := sdp.AudioAnswer(offer, "1", addrOf(c.media), codecs)
			if err != nil {
				r.Reject(488)
				return
			}
			r.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()})
		}})
	if err != nil {
		s.n.t.Fatal(err)
	}
	s.n.t.Cleanup(func() { out.Bye() })
	c.out = out
	go func() {
		for m := range out.Responses() {
			c.resps <- timed{m, time.Now()}
		}
		close(c.resps)
	}()
	return c
}

// next is the next response to the INVITE.
func (c *earlyCaller) next(t *testing.T) timed {
	t.Helper()
	select {
	case r, ok := <-c.resps:
		if !ok {
			t.Fatal("no more responses")
		}
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("no response to the INVITE")
		return timed{}
	}
}

// talk sends n packets of speech, each payload all fill, from c to to.
func talk(t *testing.T, c *net.UDPConn, to netip.AddrPort, n int, fill byte) {
	t.Helper()
	for i := range n {
		p := rtp.Packet{PayloadType: sdp.PCMU, SequenceNumber: uint16(i), Timestamp: uint32(i * frameSize), SSRC: uint32(fill),
			Payload: bytes.Repeat([]byte{fill}, frameSize)}
		if _, err := c.WriteToUDPAddrPort(p.Marshal(), to); err != nil {
			t.Fatal(err)
		}
	}
}

// heard waits for a packet whose payload is all fill: speech of the
// other side, after any packets of the tone that were still on the way.
func heard(t *testing.T, c *net.UDPConn, fill byte) {
	t.Helper()
	buf := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		k, err := c.Read(buf)
		if err != nil {
			t.Fatalf("no speech %#x: %v", fill, err)
		}
		if p, err := rtp.Unmarshal(buf[:k]); err == nil && bytes.Equal(p.Payload, bytes.Repeat([]byte{fill}, frameSize)) {
			return
		}
	}
}

// TestCustomizedAlertingTone is TS 24.182 4.5.5.3.7 without
// preconditions: the callee has CAT on. Its AS answers the caller's
// offer in the callee's reliable 180 with the SDP of the MRFP and the
// content attribute g.3gpp.cat, authorizes the early media, and the
// caller hears the tone before the callee answers. On the answer the
// AS stops the tone, moves the caller to the callee's media with an
// UPDATE, and passes the 200 on without SDP; then the two talk.
func TestCustomizedAlertingTone(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	n.setTone(2, "cat1", true)
	calleeMedia := rtpSocket(t)
	call := caller.callWithEarlyMedia(callee.tel, pcmuOnly)

	ic := receive(t, callee.calls, "call at the callee")
	if ic.Request.Get("P-Early-Media") != "" {
		t.Errorf("the INVITE to the callee has P-Early-Media %q; the AS takes it out (4.5.5.3.7 b)", ic.Request.Get("P-Early-Media"))
	}
	go ic.Progress(180, sip.Body{})
	ringing := call.next(t)
	if ringing.m.StatusCode != 180 {
		t.Fatalf("the caller got %d, want the 180", ringing.m.StatusCode)
	}
	answer := mrfpAnswer(t, n, ringing.m)
	if v, _ := answer.AudioMedia().Get("content"); v != "g.3gpp.cat" {
		t.Errorf("the SDP of the tone has content %q, want g.3gpp.cat (4.5.5.3.7 b 2)", v)
	}
	tone := rtptest.Frames(t, mrfAnnouncements["cat1"])
	if got := rtptest.ReadPCMU(t, call.media, len(tone), 10*time.Second); !startsWith(got, tone) {
		t.Error("the early media is not the tone cat1, frame by frame")
	}

	offer, _ := sdp.Parse(ic.Request.Body)
	calleeAnswer, _ := sdp.AudioAnswer(offer, "2", addrOf(calleeMedia), pcmuOnly)
	if _, err := ic.Accept(sip.Body{Type: "application/sdp", Content: calleeAnswer.Bytes()}); err != nil {
		t.Fatal(err)
	}
	update := receive(t, call.updates, "UPDATE with the callee's media")
	moved, err := sdp.Parse(update.Body)
	if err != nil || moved.Address(*moved.AudioMedia()) != addrOf(calleeMedia) {
		t.Errorf("the UPDATE offers\n%s\nwant the callee's media at %v", update.Body, addrOf(calleeMedia))
	}
	if moved.Origin.SessionID != answer.Origin.SessionID || moved.Origin.Version != answer.Origin.Version+1 {
		t.Errorf("o= went from %+v to %+v; the AS keeps one session toward the caller (RFC 3264 8)", answer.Origin, moved.Origin)
	}
	ok := call.next(t)
	if ok.m.StatusCode != 200 || len(ok.m.Body) != 0 {
		t.Fatalf("the caller got %d with body %q, want 200 without SDP (4.5.5.3.7)", ok.m.StatusCode, ok.m.Body)
	}
	waitNoContexts(t, n.mrfp)

	talk(t, calleeMedia, addrOf(call.media), 5, 0x55)
	heard(t, call.media, 0x55)
	talk(t, call.media, addrOf(calleeMedia), 5, 0x33)
	heard(t, calleeMedia, 0x33)
}

// TestCustomizedAlertingToneThenBusy: the callee rejects the call. The
// AS stops the tone and the caller gets the 486.
func TestCustomizedAlertingToneThenBusy(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	n.setTone(2, "cat1", true)
	call := caller.callWithEarlyMedia(callee.tel, pcmuOnly)
	ic := receive(t, callee.calls, "call at the callee")
	go ic.Progress(180, sip.Body{})
	mrfpAnswer(t, n, call.next(t).m)
	rtptest.ReadPCMU(t, call.media, 10, 10*time.Second)
	ic.Reject(486)
	if final := call.next(t); final.m.StatusCode != 486 {
		t.Errorf("the caller got %d, want 486", final.m.StatusCode)
	}
	waitNoContexts(t, n.mrfp)
}

// TestCustomizedAlertingToneThenCancel: the caller gives up while the
// tone plays. The AS cancels the leg to the callee and stops the tone.
func TestCustomizedAlertingToneThenCancel(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	n.setTone(2, "cat1", true)
	call := caller.callWithEarlyMedia(callee.tel, pcmuOnly)
	ic := receive(t, callee.calls, "call at the callee")
	go ic.Progress(180, sip.Body{})
	mrfpAnswer(t, n, call.next(t).m)
	rtptest.ReadPCMU(t, call.media, 10, 10*time.Second)
	if err := call.out.Cancel(); err != nil {
		t.Fatal(err)
	}
	if final := call.next(t); final.m.StatusCode != 487 {
		t.Errorf("the caller got %d, want 487", final.m.StatusCode)
	}
	receive(t, ic.Done(), "the CANCEL at the callee")
	waitNoContexts(t, n.mrfp)
}

// TestCustomizedAlertingToneNeedsPCMU: the MRFP plays PCMU only,
// so a caller that offers none gets the call as without CAT, and the
// MRF is never asked.
func TestCustomizedAlertingToneNeedsPCMU(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	n.setTone(2, "cat1", true)
	amrWB := slices.DeleteFunc(sdp.VoLTECodecs(), func(c sdp.Codec) bool { return strings.EqualFold(c.Name, sdp.NamePCMU) })
	call := caller.callWithEarlyMedia(callee.tel, amrWB)
	ic := receive(t, callee.calls, "call at the callee")
	if len(n.mrfp.Contexts()) != 0 {
		t.Error("the AS asked the MRF for a tone the caller cannot take")
	}
	go ic.Progress(180, sip.Body{})
	if r := call.next(t); r.m.StatusCode != 180 || len(r.m.Body) != 0 || r.m.Get("P-Early-Media") != "" {
		t.Errorf("the caller got %d with body %q and P-Early-Media %q; want the callee's 180 as it came", r.m.StatusCode, r.m.Body, r.m.Get("P-Early-Media"))
	}
	ic.Reject(486)
}

// TestLocalRingbackWithoutCAT: subscriber 2 has CAT provisioned but
// off, as it ships. The caller gets the 180 without SDP, plays its own
// ringing tone (TS 24.628 4.7.2.1), and no RTP comes before the answer.
func TestLocalRingbackWithoutCAT(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	call := caller.callWithEarlyMedia(callee.tel, pcmuOnly)
	ic := receive(t, callee.calls, "call at the callee")
	go ic.Progress(180, sip.Body{})
	if r := call.next(t); r.m.StatusCode != 180 || len(r.m.Body) != 0 {
		t.Errorf("the caller got %d with body %q, want the 180 without SDP", r.m.StatusCode, r.m.Body)
	}
	ic.Reject(486)
	if final := call.next(t); final.m.StatusCode != 486 {
		t.Errorf("the caller got %d, want 486", final.m.StatusCode)
	}
	if got := rtptest.Queued(t, call.media); len(got) != 0 {
		t.Errorf("the caller got %d packets before the final response", len(got))
	}
}

// TestCustomizedAlertingToneWithPreconditions is TS 24.182 4.5.5.3.7 for
// the VoLTE UEs of the simulator, which use preconditions: the callee's
// 183 with its answer goes to the caller as it came, the two confirm
// their resources with PRACK and UPDATE end to end, and on the callee's
// 180 the AS offers the caller the tone in an UPDATE (a 1). The caller
// hears it before the answer, and then the callee.
func TestCustomizedAlertingToneWithPreconditions(t *testing.T) {
	testRingback(t, true)
}

// TestLocalRingbackWithPreconditions: the same VoLTE call to a callee
// without CAT rings with the caller's own tone, and no RTP comes before
// the answer.
func TestLocalRingbackWithPreconditions(t *testing.T) {
	testRingback(t, false)
}

// testRingback calls subscriber 2 from subscriber 1 with the UE
// simulator, with or without the callee's CAT, and checks how the call
// rang at the caller.
func testRingback(t *testing.T, cat bool) {
	n := startNetworkWith(t, netOptions{mrf: true})
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	if cat {
		n.setTone(2, "cat1", true)
	}
	answered := make(chan error, 1)
	go func() {
		ic := receive(t, callee.calls, "call at the callee")
		answered <- callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{PCM: ue.Tone(880, time.Second)},
			ue.AnswerOptions{PickUp: time.After(2500 * time.Millisecond)})
	}()
	var mu sync.Mutex
	var session *ue.Session
	var statuses []ue.SessionStatus
	opts := ue.CallOptions{
		Session: func(s *ue.Session) { mu.Lock(); session = s; mu.Unlock() },
		Changed: func() {
			mu.Lock()
			defer mu.Unlock()
			if session != nil {
				statuses = append(statuses, session.Status())
			}
		},
	}
	if err := caller.ue.Call(context.Background(), caller.ua, caller.reg, callee.tel, caller.media, ue.Speech{PCM: ue.Tone(440, 500*time.Millisecond)}, opts); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, answered, "end of the callee's call"); err != nil {
		t.Fatal(err)
	}
	if _, packets := callee.media.Received(); packets != 25 {
		t.Errorf("the callee got %d packets, want the 25 the caller sent", packets)
	}
	got, _ := caller.media.Received()
	mu.Lock()
	defer mu.Unlock()
	if !cat {
		// A local ringback needs that no RTP came before the answer.
		if r := statuses[len(statuses)-1].Ringback; r != ue.RingbackLocal || len(n.mrfp.Contexts()) != 0 {
			t.Errorf("the caller says the call rang %q, want %q", r, ue.RingbackLocal)
		}
		return
	}
	tone := bytes.Join(rtptest.Frames(t, mrfAnnouncements["cat1"]), nil)
	if len(got) < len(tone) || !bytes.Equal(got[:len(tone)], tone) {
		t.Errorf("the caller got %d bytes first; want the tone cat1 before the callee", len(got))
	}
	authorized := slices.ContainsFunc(statuses, func(s ue.SessionStatus) bool {
		return s.Preconditions != nil && s.Preconditions.Met && slices.Contains(slices.Collect(maps.Values(s.EarlyMedia)), sip.EarlyMediaSendonly) && !s.LocalRingback
	})
	if !authorized {
		t.Error("no status of the caller shows the early media authorized with the preconditions met")
	}
	if last := statuses[len(statuses)-1]; last.Ringback != ue.RingbackNetwork {
		t.Errorf("the caller says the call rang %q, want %q", last.Ringback, ue.RingbackNetwork)
	}
	waitNoContexts(t, n.mrfp)
}
