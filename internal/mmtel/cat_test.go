package mmtel_test

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// toneHSS is an HSS whose users have the CAT cat1 on, and no other
// service data.
type toneHSS struct{}

func (toneHSS) RepositoryData(_ context.Context, _, serviceIndication string) (sh.TransparentData, bool, error) {
	if serviceIndication != mmtel.CATServiceIndication {
		return sh.TransparentData{}, false, nil
	}
	t := mmtel.CustomizedAlertingTone{Tone: "cat1", Active: true}
	return sh.TransparentData{ServiceIndication: serviceIndication, ServiceData: &sh.ServiceData{XML: t.Marshal()}}, true, nil
}

// events is the order in which the parties saw the steps of a call.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, s)
}

func (e *events) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.list)
}

// fakeMRFC answers each INVITE as an MRFC answers one for an
// announcement without SDP (RFC 4240 3): a 200 with an offer of PCMU,
// sendonly; then it waits for the ACK and the BYE.
func fakeMRFC(t *testing.T, log *events, uris chan<- string, byed chan<- struct{}) *sip.UA {
	return newUA(t, "mrfc", func(c *sip.IncomingCall) {
		log.add("MRFC INVITE")
		uris <- c.Request.RequestURI
		o, _ := sdp.AudioOffer("9", netip.MustParseAddrPort("192.0.2.9:40000"), []sdp.Codec{{PayloadType: sdp.PCMU, Name: sdp.NamePCMU, ClockRate: 8000}})
		o.AudioMedia().SetDirection("sendonly")
		ack, err := c.Accept(sip.Body{Type: "application/sdp", Content: o.Bytes()})
		if err != nil {
			return
		}
		a, err := sdp.Parse(ack.Body)
		if err == nil && a.Direction(*a.AudioMedia()) == "recvonly" {
			log.add("MRFC ACK with an answer")
		} else {
			log.add("MRFC ACK without an answer")
		}
		<-c.Done()
		log.add("MRFC BYE")
		close(byed)
	})
}

// TestGatewayModelOrder is the order of TS 24.182 4.5.5.3.7, with a fake
// MRFC: the MRF first, then the callee; the tone starts with the ACK to
// the MRFC only after the caller has acknowledged the reliable 180 that
// carries the SDP of the MRFP; on the answer the MRFC gets its BYE, the
// caller an UPDATE with the callee's SDP, and then the 200.
func TestGatewayModelOrder(t *testing.T) {
	var log events
	uris := make(chan string, 1)
	byed := make(chan struct{})
	mrfc := fakeMRFC(t, &log, uris, byed)
	as := startAS(t, toneHSS{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc)})
	calleeMedia := netip.MustParseAddrPort("192.0.2.2:4000")
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		log.add("callee INVITE")
		if err := c.Progress(180, sip.Body{}); err != nil {
			return
		}
		offer, _ := sdp.Parse(c.Request.Body)
		a, _ := sdp.AudioAnswer(offer, "2", calleeMedia, pcmuOnly)
		c.Accept(sip.Body{Type: "application/sdp", Content: a.Bytes()})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "term", pcmuOffer(t, netip.MustParseAddrPort("192.0.2.1:4000"), pcmuOnly))
	inv.ManualPRACK = true
	inv.OnRequest = func(r *sip.InDialogRequest) {
		// The BYE reaches the fake MRFC in a goroutine of its own; the
		// wait puts it in the log before the UPDATE when it came first.
		select {
		case <-byed:
		case <-time.After(time.Second):
		}
		moved, _ := sdp.Parse(r.Request.Body)
		if r.Request.Method == sip.UPDATE && moved.Address(*moved.AudioMedia()) == calleeMedia {
			log.add("caller UPDATE with the callee's SDP")
		}
		a, _ := sdp.AudioAnswer(moved, "1", netip.MustParseAddrPort("192.0.2.1:4000"), pcmuOnly)
		r.Accept(sip.Body{Type: "application/sdp", Content: a.Bytes()})
	}
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	for resp := range out.Responses() {
		switch {
		case resp.StatusCode == 180 && len(resp.Body) > 0:
			log.add("caller 180 with SDP")
			// The caller acknowledges the 180 only after it has logged
			// it, so the log shows whether the tone waited for that.
			out.PRACK(resp, sip.Body{})
		case resp.StatusCode == 200:
			log.add("caller 200")
		}
	}
	if uri := receive(t, uris, "INVITE at the MRFC"); uri != "sip:annc@"+contactOf(t, mrfc)+";play=/provisioned/cat1;repeat=forever" {
		t.Errorf("the INVITE to the MRFC is for %s", uri)
	}
	out.Bye()
	want := []string{"MRFC INVITE", "callee INVITE", "caller 180 with SDP", "MRFC ACK with an answer",
		"MRFC BYE", "caller UPDATE with the callee's SDP", "caller 200"}
	got := log.get()
	if !slices.Equal(got, want) {
		t.Errorf("the steps went\n%q\nwant\n%q", got, want)
	}
}

// TestNoToneForAWaitingCall is TS 24.182 4.6.11 in its second option:
// the callee's 180 says that the call waits, so the caller gets the
// waiting indication and no CAT, and the MRFC gets its BYE before any
// tone has played.
func TestNoToneForAWaitingCall(t *testing.T) {
	var log events
	uris := make(chan string, 1)
	byed := make(chan struct{})
	mrfc := fakeMRFC(t, &log, uris, byed)
	as := startAS(t, toneHSS{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc)})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Progress(180, sip.Body{}, sip.HeaderField{Name: "Alert-Info", Value: "<urn:alert:service:call-waiting>"})
		c.Reject(486)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", pcmuOffer(t, netip.MustParseAddrPort("192.0.2.1:4000"), pcmuOnly)))
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out)
	if !slices.Equal(statuses(got), []int{180, 486}) || len(got[0].Body) != 0 || got[0].Get("Alert-Info") == "" {
		t.Errorf("the caller got %v, the 180 with body %q and Alert-Info %q; want the waiting indication without SDP",
			statuses(got), got[0].Body, got[0].Get("Alert-Info"))
	}
	receive(t, byed, "BYE at the MRFC")
	if l := log.get(); !slices.Equal(l, []string{"MRFC INVITE", "MRFC ACK without an answer", "MRFC BYE"}) {
		t.Errorf("the MRFC saw %q, want its dialog ended without a tone", l)
	}
}

// TestAnnouncementForAWaitingCall is TS 24.615 4.5.5.2.3 a: the callee's
// 180 says that the call waits, and the caller, who is to learn of it,
// gets the 180 with the waiting indication and the announcement of the
// MRF in it; once the callee answers, the caller gets the callee's media
// in an UPDATE.
func TestAnnouncementForAWaitingCall(t *testing.T) {
	var log events
	uris := make(chan string, 1)
	byed := make(chan struct{})
	mrfc := fakeMRFC(t, &log, uris, byed)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc), Announcement: "notice"})
	calleeMedia := netip.MustParseAddrPort("192.0.2.2:4000")
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		if err := c.Progress(180, sip.Body{}, sip.HeaderField{Name: "Alert-Info", Value: "<urn:alert:service:call-waiting>"}); err != nil {
			return
		}
		offer, _ := sdp.Parse(c.Request.Body)
		a, _ := sdp.AudioAnswer(offer, "2", calleeMedia, pcmuOnly)
		c.Accept(sip.Body{Type: "application/sdp", Content: a.Bytes()})
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "term", pcmuOffer(t, netip.MustParseAddrPort("192.0.2.1:4000"), pcmuOnly))
	updates := make(chan *sip.Message, 1)
	inv.OnRequest = func(r *sip.InDialogRequest) {
		updates <- r.Request
		offer, _ := sdp.Parse(r.Request.Body)
		a, _ := sdp.AudioAnswer(offer, "1", netip.MustParseAddrPort("192.0.2.1:4000"), pcmuOnly)
		r.Accept(sip.Body{Type: "application/sdp", Content: a.Bytes()})
	}
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out)
	defer out.Bye()
	if !slices.Equal(statuses(got), []int{180, 200}) || got[0].Get("Alert-Info") == "" || got[0].Get("P-Early-Media") != "sendonly" {
		t.Fatalf("the caller got %v, the 180 with Alert-Info %q and P-Early-Media %q", statuses(got), got[0].Get("Alert-Info"), got[0].Get("P-Early-Media"))
	}
	a, err := sdp.Parse(got[0].Body)
	if err != nil || a.Address(*a.AudioMedia()) != netip.MustParseAddrPort("192.0.2.9:40000") {
		t.Errorf("the 180 has the SDP\n%s\nwant the MRFP's", got[0].Body)
	}
	if v, ok := a.AudioMedia().Get("content"); ok {
		t.Errorf("the announcement has content %q; g.3gpp.cat is for CAT only", v)
	}
	if uri := receive(t, uris, "INVITE at the MRFC"); uri != "sip:annc@"+contactOf(t, mrfc)+";play=/provisioned/notice" {
		t.Errorf("the INVITE to the MRFC is for %s", uri)
	}
	moved, _ := sdp.Parse(receive(t, updates, "UPDATE at the caller").Body)
	if moved.Address(*moved.AudioMedia()) != calleeMedia {
		t.Errorf("the UPDATE offers %v, want the callee's media", moved.Address(*moved.AudioMedia()))
	}
	receive(t, byed, "BYE at the MRFC")
}
