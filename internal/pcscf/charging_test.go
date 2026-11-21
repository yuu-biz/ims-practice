package pcscf

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// received keeps the messages that a user agent of a test received.
type received struct {
	mu       sync.Mutex
	messages []*sip.Message
}

func (r *received) observe(c sip.Capture) {
	if c.Sent {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, c.Message)
}

// all is every message received so far.
func (r *received) all() []*sip.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*sip.Message(nil), r.messages...)
}

// request waits for the first request of method.
func (r *received) request(t *testing.T, method sip.Method) *sip.Message {
	t.Helper()
	return r.wait(t, string(method), func(m *sip.Message) bool { return m.Method == method })
}

// response waits for the first response with code to a request of
// method.
func (r *received) response(t *testing.T, code int, method sip.Method) *sip.Message {
	t.Helper()
	return r.wait(t, strconv.Itoa(code)+" to "+string(method), func(m *sip.Message) bool {
		cseq, _ := sip.ParseCSeq(m.Get("CSeq"))
		return m.StatusCode == code && cseq.Method == method
	})
}

func (r *received) wait(t *testing.T, what string, match func(*sip.Message) bool) *sip.Message {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, m := range r.all() {
			if match(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s arrived", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// noChargingVector checks that none of the messages that a UE got
// carries P-Charging-Vector (TS 24.229 5.2.1).
func noChargingVector(t *testing.T, got *received) {
	t.Helper()
	for _, m := range got.all() {
		if pcv := m.Get("P-Charging-Vector"); pcv != "" {
			cseq, _ := sip.ParseCSeq(m.Get("CSeq"))
			t.Errorf("the UE got a message of %s %d with P-Charging-Vector %q", cseq.Method, m.StatusCode, pcv)
		}
	}
}

// TestChargingVectorOfACallFromTheUE is TS 24.229 5.2.6.3.3 step 7 and
// 5.2.6.3.9 step 3: the INVITE leaves the P-CSCF with an icid-value of
// its own, and the ACK and the BYE of the UE with the same one. The UE
// sees none of it.
func TestChargingVectorOfACallFromTheUE(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	out, in := h.call()
	vector, ok := sip.ChargingVectorOf(in.Request)
	if !ok || vector.OrigIOI != domain || vector.TermIOI != "" {
		t.Fatalf("the INVITE left with P-Charging-Vector %q, want an icid-value and the orig-ioi %s", in.Request.Get("P-Charging-Vector"), domain)
	}
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	waitACK(t, acked)
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the call", in.Done())

	want := sip.ChargingVector{ICID: vector.ICID, OrigIOI: domain}.String()
	for _, method := range []sip.Method{sip.ACK, sip.BYE} {
		if got := h.coreGot.request(t, method).Get("P-Charging-Vector"); got != want {
			t.Errorf("the %s left with P-Charging-Vector %q, want %q", method, got, want)
		}
	}
	noChargingVector(t, h.ueGot)
}

// TestChargingVectorOfACallToTheUE is TS 24.229 5.2.6.4.3 item 9 and
// what follows from it: the P-CSCF stores the icid-value and the
// orig-ioi of the INVITE before it removes them, puts them in each
// response of the UE with its own term-ioi (5.2.6.4.4 item 6), and
// gives the BYE of the UE the icid-value of the INVITE (5.2.6.3.9 step
// 3).
func TestChargingVectorOfACallToTheUE(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	const icid, home = "call-to-ue-1", "home.example"
	out, err := h.core.Invite(sip.Invitation{RequestURI: h.ueContact, From: "sip:001010000000002@" + domain, To: impu,
		Route:   []string{"<sip:term@pcscf." + domain + ":5060;lr>"},
		Headers: []sip.HeaderField{{Name: "P-Charging-Vector", Value: "icid-value=" + icid + ";orig-ioi=" + home}},
		Body:    sdpBody(sdp.Offer("1", coreMedia))})
	if err != nil {
		t.Fatal(err)
	}
	in := receiveCall(t, h.toUE)
	if err := in.Progress(180, sip.Body{}, sip.HeaderField{Name: "P-Charging-Vector", Value: "icid-value=forged-by-the-ue"}); err != nil {
		t.Fatal(err)
	}
	acked := accept(in, answerTo(t, in.Request, ueMedia))
	answered(t, out, 200)
	waitACK(t, acked)

	want := sip.ChargingVector{ICID: icid, OrigIOI: home, TermIOI: domain}.String()
	for _, code := range []int{180, 200} {
		if got := h.coreGot.response(t, code, sip.INVITE).Get("P-Charging-Vector"); got != want {
			t.Errorf("the %d left with P-Charging-Vector %q, want %q", code, got, want)
		}
	}
	if err := in.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the call", out.Done())
	if got, want := h.coreGot.request(t, sip.BYE).Get("P-Charging-Vector"), (sip.ChargingVector{ICID: icid, OrigIOI: domain}).String(); got != want {
		t.Errorf("the BYE left with P-Charging-Vector %q, want %q", got, want)
	}
	noChargingVector(t, h.ueGot)
}

// TestChargingVectorOfARequestToTheUEInTheDialog: the UE's answer to a
// request of the network within the dialog goes on with the icid-value
// of the INVITE, the orig-ioi of that request and the P-CSCF's
// term-ioi (TS 24.229 5.2.6.4.10 item 2).
func TestChargingVectorOfARequestToTheUEInTheDialog(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	out, in := h.call()
	vector, _ := sip.ChargingVectorOf(in.Request)
	acked := accept(in, answerTo(t, in.Request, coreMedia))
	answered(t, out, 200)
	waitACK(t, acked)
	const far = "far.example"
	if err := in.Bye(sip.HeaderField{Name: "P-Charging-Vector", Value: "icid-value=" + vector.ICID + ";orig-ioi=" + far}); err != nil {
		t.Fatal(err)
	}
	ended(t, "the UE's call", out.Done())
	want := sip.ChargingVector{ICID: vector.ICID, OrigIOI: far, TermIOI: domain}.String()
	if got := h.coreGot.response(t, 200, sip.BYE).Get("P-Charging-Vector"); got != want {
		t.Errorf("the 200 to the BYE left with P-Charging-Vector %q, want %q", got, want)
	}
	noChargingVector(t, h.ueGot)
}
