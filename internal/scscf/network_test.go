package scscf

import (
	"context"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file runs the I-CSCF, the S-CSCF and the HSS stub together with
// a fake MMTel AS, for call forwarding when the callee is not logged in
// (CFNL, TS 24.604): the call reaches the S-CSCF of a callee who is not
// registered (TS 29.228 6.1.4.1 step 3), the AS diverts it, the
// diverting user's originating services after retargeting run (TS
// 24.229 5.4.3.3 step 3 b), and the call goes on through ENUM and the
// I-CSCF to the diverted-to user, who is registered.

// divertingAS is a fake MMTel AS that acts as a proxy (TS 24.229
// 5.7.4). On the terminating leg of divertedFrom it changes the
// Request-URI to divertedTo; any other request it sends back as it came.
type divertingAS struct {
	stack                    *sip.Stack
	got                      chan *sip.Message
	scscf                    atomic.Pointer[netip.AddrPort]
	divertedFrom, divertedTo string
}

func newDivertingAS(t *testing.T, host, divertedFrom, divertedTo string) *divertingAS {
	t.Helper()
	as := &divertingAS{got: make(chan *sip.Message, 8), divertedFrom: divertedFrom, divertedTo: divertedTo}
	own, _ := ims.LooseRoute("sip:" + host + ":5060")
	as.stack = newStack(t, func(st *sip.ServerTransaction) {
		as.got <- st.Request
		out := st.Request.Clone()
		sip.DecrementMaxForwards(out)
		sip.PopOwnRoute(out, own)
		// TS 24.229 5.7.1.3A: the served user and the case come from
		// P-Served-User.
		if psu, err := sip.ParsePServedUser(out.Get("P-Served-User")); err == nil &&
			psu.SessionCase == sip.SessionCaseTerm && psu.URI == as.divertedFrom {
			out.RequestURI = as.divertedTo
		}
		final, err := st.Proxy(out, sip.Destination{Transport: sip.UDP, Addr: *as.scscf.Load()})
		ims.Finish(st, final, err)
	})
	return as
}

// names is a fakeResolver that may learn hosts while the nodes run:
// the S-CSCF and the I-CSCF are made after the resolver that they use.
type names struct {
	mu sync.Mutex
	fakeResolver
}

func (n *names) add(host string, addr netip.AddrPort) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hosts[host] = addr
}

func (n *names) Locate(ctx context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.fakeResolver.Locate(ctx, host, port, transport)
}

func TestCallForwardingWhenNotLoggedIn(t *testing.T) {
	const (
		impi2  = "001010000000002@" + domain
		impu2  = "sip:001010000000002@" + domain
		tel2   = "tel:+12025550102"
		mmtel  = "mmtel." + domain
		server = "sip:" + mmtel + ":5060"
	)
	as := newDivertingAS(t, mmtel, tel2, telURI)
	pcscf, pcscfGot := nextHop(t)
	resolver := &names{fakeResolver: fakeResolver{
		hosts: map[string]netip.AddrPort{"pcscf." + domain: pcscf.LocalAddr(), mmtel: as.stack.LocalAddr()},
		enum:  map[string]string{"+12025550101": impu},
	}}
	h := newHarnessWithHSS(t, Config{}, resolver, hss.Config{
		ServerCapabilities: cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}},
		InitialFilterCriteria: []ifc.InitialFilterCriteria{
			callCriterion(0, ifc.TerminatingUnregistered, server),
			callCriterion(1, ifc.OriginatingCDIV, server),
		},
	},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI}, Password: password},
		hss.Subscription{PrivateIdentity: impi2, PublicIdentities: []string{impu2, tel2}, Password: "test-password-2"},
	)
	scscfAddr := h.scscf.LocalAddr()
	as.scscf.Store(&scscfAddr)
	resolver.add("scscf."+domain, scscfAddr)
	node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	i := icscf.New(icscf.Config{HomeDomain: domain, Host: domain, Port: 5060,
		SCSCFs: []icscf.SCSCF{{Name: "sip:scscf." + domain + ":5060", Capabilities: []uint32{1}}}}, h.hss, node, resolver, quiet())
	ic := newStack(t, i.OnRequest)
	// The S-CSCF finds the I-CSCF as the entry point of the home domain.
	resolver.add(domain, ic.LocalAddr())
	h.registerUser(sip.NewCallID())

	call := newRequest(sip.INVITE, tel2, "", "")
	call.Remove("Route")
	call.Set("From", "<sip:caller@visited.example>;tag="+sip.NewTag())
	call.Add("Contact", "<sip:caller@192.0.2.20:5060>")
	ct, err := h.ue.SendRequest(call, sip.Destination{Transport: sip.UDP, Addr: ic.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil || final.StatusCode != 486 {
		t.Fatalf("caller got %v, want the 486 of the diverted-to user's P-CSCF", final)
	}

	terminating, afterwards := received(t, as.got), received(t, as.got)
	if got := terminating.Get("P-Served-User"); got != "<"+tel2+">;sescase=term;regstate=unreg" {
		t.Errorf("first request to the AS has P-Served-User %q", got)
	}
	if got := afterwards.Get("P-Served-User"); got != "<"+tel2+">;orig-cdiv;regstate=unreg" || afterwards.RequestURI != telURI {
		t.Errorf("second request to the AS has P-Served-User %q and Request-URI %q", got, afterwards.RequestURI)
	}
	toUE := received(t, pcscfGot)
	if toUE.RequestURI != ueContact || toUE.Get("P-Called-Party-ID") != "<"+impu+">" || toUE.Get("P-Served-User") != "" {
		t.Errorf("P-CSCF got Request-URI %q, P-Called-Party-ID %q and P-Served-User %q",
			toUE.RequestURI, toUE.Get("P-Called-Party-ID"), toUE.Get("P-Served-User"))
	}
	if sars := h.hss.assignments(); !slices.Contains(sars, cx.UnregisteredUser) {
		t.Errorf("SARs = %v, want one of UNREGISTERED_USER for the diverting user", sars)
	}
}
