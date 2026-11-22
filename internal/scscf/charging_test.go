package scscf

import (
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file follow the P-Charging-Vector of an initial
// request through the S-CSCF and back (TS 24.229 5.4.3.2, 5.4.3.3).
// The other nodes write IOIs that differ from the S-CSCF's own
// domain, so that each value shows who wrote it.

// answering is a next hop that records the request it gets and answers
// it with code and P-Charging-Vector pcv, none when pcv is empty.
func answering(t *testing.T, code int, pcv string) (*sip.Stack, <-chan *sip.Message) {
	t.Helper()
	got := make(chan *sip.Message, 1)
	s := newStack(t, func(st *sip.ServerTransaction) {
		got <- st.Request
		resp := sip.NewResponse(st.Request, code, sip.NewTag())
		if pcv != "" {
			resp.Add("P-Charging-Vector", pcv)
		}
		st.Respond(resp)
	})
	return s, got
}

// TestIOIsOfAnOriginatingRequest is TS 24.229 5.4.3.2: the request
// leaves with the icid-value it came with and this network as its
// orig-ioi (steps 2, 6 and 7). A response that brings a term-ioi goes
// back with the orig-ioi of the request and this network's term-ioi;
// one without goes back as it came.
func TestIOIsOfAnOriginatingRequest(t *testing.T) {
	for _, tt := range []struct {
		name, answer, want string
	}{
		{"with a term-ioi", "icid-value=orig-1;orig-ioi=" + domain + ";term-ioi=far.example",
			"icid-value=orig-1;orig-ioi=visited.example;term-ioi=" + domain},
		{"without one", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			icscf, got := answering(t, 486, tt.answer)
			h := newServiceHarness(t, nil, map[string]netip.AddrPort{domain: icscf.LocalAddr()})
			h.registerUser(sip.NewCallID())
			m := sms("sip:001010000000002@"+domain, orig, impu)
			m.Add("P-Charging-Vector", "icid-value=orig-1;orig-ioi=visited.example")
			r := h.send(m)
			if pcv := received(t, got).Get("P-Charging-Vector"); pcv != "icid-value=orig-1;orig-ioi="+domain {
				t.Errorf("the I-CSCF got P-Charging-Vector %q", pcv)
			}
			if pcv := r.Get("P-Charging-Vector"); pcv != tt.want {
				t.Errorf("the P-CSCF got P-Charging-Vector %q, want %q", pcv, tt.want)
			}
		})
	}
}

// TestIOIsOfATerminatingRequest is TS 24.229 5.4.3.3 with an application
// server that acts as a proxy. The request reaches the server and the
// P-CSCF with this network as its orig-ioi (steps 4 d, 7A). Every
// response that goes back to the server gets the orig-ioi of the
// request it answers and this network's term-ioi; of those that go
// back towards the caller, the 1xx and 2xx get the orig-ioi that the
// caller's network wrote, and the others go back as the server sent
// them.
func TestIOIsOfATerminatingRequest(t *testing.T) {
	terminating := ifc.TerminatingRegistered
	criterion := ifc.InitialFilterCriteria{
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "MESSAGE"},
			{Group: []int{1}, SessionCase: &terminating},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:as1." + domain + ":5060"},
	}
	fromNetwork := "icid-value=term-1;orig-ioi=" + domain
	toAS := fromNetwork + ";term-ioi=" + domain
	for _, tt := range []struct {
		code        int
		toTheCaller string
	}{
		{200, "icid-value=term-1;orig-ioi=caller.example;term-ioi=" + domain},
		{486, toAS},
	} {
		pcscf, atPCSCF := answering(t, tt.code, fromNetwork+";term-ioi=visited.example")
		as := newChargingAS(t)
		h := newServiceHarness(t, []ifc.InitialFilterCriteria{criterion},
			map[string]netip.AddrPort{"as1." + domain: as.stack.LocalAddr(), "pcscf." + domain: pcscf.LocalAddr()})
		addr := h.scscf.LocalAddr()
		as.scscf.Store(&addr)
		h.registerUser(sip.NewCallID())
		m := newRequest(sip.MESSAGE, impu, "<sip:scscf."+domain+":5060;lr>", "sip:001010000000002@"+domain)
		m.Add("P-Charging-Vector", "icid-value=term-1;orig-ioi=caller.example")
		r := h.send(m)
		if r.StatusCode != tt.code {
			t.Fatalf("got %d, want the %d of the P-CSCF", r.StatusCode, tt.code)
		}
		if pcv := received(t, as.got).Get("P-Charging-Vector"); pcv != fromNetwork {
			t.Errorf("the server got P-Charging-Vector %q, want %q", pcv, fromNetwork)
		}
		if pcv := received(t, atPCSCF).Get("P-Charging-Vector"); pcv != fromNetwork {
			t.Errorf("the P-CSCF got P-Charging-Vector %q, want %q", pcv, fromNetwork)
		}
		if pcv := received(t, as.answered).Get("P-Charging-Vector"); pcv != toAS {
			t.Errorf("the %d reached the server with P-Charging-Vector %q, want %q", tt.code, pcv, toAS)
		}
		if pcv := r.Get("P-Charging-Vector"); pcv != tt.toTheCaller {
			t.Errorf("the %d reached the caller with P-Charging-Vector %q, want %q", tt.code, pcv, tt.toTheCaller)
		}
	}
}

// chargingAS is an application server that acts as a proxy: it sends
// each request back to the S-CSCF and passes on the final response
// unchanged, which it records. The test learns the address of the
// S-CSCF after the server runs, hence the atomic.
type chargingAS struct {
	stack         *sip.Stack
	got, answered chan *sip.Message
	scscf         atomic.Pointer[netip.AddrPort]
}

func newChargingAS(t *testing.T) *chargingAS {
	t.Helper()
	as := &chargingAS{got: make(chan *sip.Message, 1), answered: make(chan *sip.Message, 1)}
	own, _ := ims.LooseRoute("sip:as1." + domain + ":5060")
	as.stack = newStack(t, func(st *sip.ServerTransaction) {
		as.got <- st.Request
		out := st.Request.Clone()
		sip.DecrementMaxForwards(out)
		sip.PopOwnRoute(out, own)
		final, err := st.Proxy(out, sip.Destination{Transport: sip.UDP, Addr: *as.scscf.Load()})
		if final != nil {
			as.answered <- final
		}
		ims.Finish(st, final, err)
	})
	return as
}
