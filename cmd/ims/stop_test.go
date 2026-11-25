package main

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// assigningHSS is the HSS stub, which takes a SAR only under the
// Server-Name it holds for the user (TS 29.228 8.1.2). It records the
// Origin-Host and the Server-Name of each SAR that it has answered, so
// that a test sees which instance of the S-CSCF sent it and under which
// name. Like the HSS of a network it is reached over the wire.
type assigningHSS struct {
	hss  *hss.HSS
	mu   sync.Mutex
	sars []sentSAR
}

// sentSAR is a SAR as the HSS got it: from which Diameter node, and
// under which Server-Name.
type sentSAR struct {
	originHost, serverName string
	typ                    cx.ServerAssignmentType
}

func newAssigningHSS(t *testing.T) *assigningHSS {
	t.Helper()
	store, err := hss.NewMemoryStore(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h, err := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return &assigningHSS{hss: h}
}

func (h *assigningHSS) Request(_ context.Context, m *diam.Message) (*diam.Message, error) {
	b, err := m.Serialize()
	if err != nil {
		return nil, err
	}
	request, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		return nil, err
	}
	answer := h.hss.Handle(request)
	if sar, err := cx.ParseServerAssignmentRequest(request); err == nil && request.Header.CommandCode == cx.CommandServerAssignment {
		h.mu.Lock()
		h.sars = append(h.sars, sentSAR{sar.Header.OriginHost, sar.ServerName, sar.ServerAssignmentType})
		h.mu.Unlock()
	}
	return answer, nil
}

func (h *assigningHSS) assignments() []sentSAR {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]sentSAR(nil), h.sars...)
}

// register leaves the HSS with the user registered at the S-CSCF of
// serverName, as the MAR and the SAR of a registration through the
// instance named host leave it.
func (h *assigningHSS) register(t *testing.T, host, serverName string) {
	t.Helper()
	hdr := diameter.Header{SessionID: host + ";1;1", OriginHost: host, OriginRealm: domain, DestinationRealm: domain}
	maa, err := cx.ParseMultimediaAuthAnswer(h.hss.Handle(cx.MultimediaAuthRequest{Header: hdr, UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: cx.SchemeSIPDigest, SIPNumberAuthItems: 1, ServerName: serverName}.Message()))
	if err != nil || !maa.Result.Success() {
		t.Fatalf("MAA = %+v (%v)", maa, err)
	}
	saa, err := cx.ParseServerAssignmentAnswer(h.hss.Handle(cx.ServerAssignmentRequest{Header: hdr, UserName: impi,
		PublicIdentities: []string{impu}, ServerName: serverName, ServerAssignmentType: cx.AssignRegistration}.Message()))
	if err != nil || !saa.Result.Success() {
		t.Fatalf("SAA = %+v (%v)", saa, err)
	}
}

// heldBy is the Server-Name that the HSS holds for the user, which the
// I-CSCF gets in the UAA of a REGISTER: "" when it holds none.
func (h *assigningHSS) heldBy(t *testing.T) string {
	t.Helper()
	uaa, err := cx.ParseUserAuthorizationAnswer(h.hss.Handle(cx.UserAuthorizationRequest{
		Header:   diameter.Header{SessionID: "icscf;1;1", OriginHost: "icscf." + domain, OriginRealm: domain, DestinationRealm: domain},
		UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: domain, UserAuthorizationType: cx.Registration,
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return uaa.ServerName
}

// toAddress finds every host at one address: the subscriber of the reg
// event, the only destination of the test.
type toAddress struct{ addr netip.AddrPort }

func (r toAddress) Locate(context.Context, string, int, sip.Transport) ([]sip.Destination, error) {
	return []sip.Destination{{Transport: sip.UDP, Addr: r.addr}}, nil
}

func (toAddress) ENUM(context.Context, string, string) (string, error) {
	return "", errors.New("no ENUM in this test")
}

func listenSIP(t *testing.T, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(), OnRequest: onRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestAnExpiryThatComesWhileANodeStopsGoesToAnotherInstance: the
// registration of a user runs out while an S-CSCF node stops. The node
// stops its sweep before its stack and its Diameter connections, so it
// claims no expiry whose NOTIFY and SAR it could no longer send. Another
// instance on the same store claims it instead, and the subscriber and
// the HSS hear of the end of the registration from that one. Its SAR
// names the S-CSCF under which the HSS assigned the user, the stopping
// instance, so the HSS takes it and holds no S-CSCF for the user after
// it.
func TestAnExpiryThatComesWhileANodeStopsGoesToAnotherInstance(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	stores := roleStores{kv: kvstore.NewMemoryStore(nil), section: config.Store{Kind: config.StoreValkey, Timeout: time.Second}}
	hssStub := newAssigningHSS(t)
	notifies := make(chan *sip.Message, 4)
	subscriber := listenSIP(t, func(st *sip.ServerTransaction) {
		notifies <- st.Request
		st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
	})
	const sweepInterval = 20 * time.Millisecond
	// start runs an instance of the S-CSCF as run makes one: on the
	// shared store of the node, with the lock of each user in it.
	start := func(host string) (*scscf.SCSCF, *sip.Stack) {
		log := quiet()
		node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain},
			[]uint32{diameter.ApplicationIDCx}, diameter.Timers{}, log)
		s, err := scscf.New(scscf.Config{ServerName: "sip:" + host + ":5060", Host: host, Port: 5060, HomeDomain: domain,
			SweepInterval: sweepInterval, UserLocks: stores.locks(config.RoleSCSCF, log)},
			hssStub, node, scscfStore(stores, log), toAddress{subscriber.LocalAddr()}, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		stack := listenSIP(t, s.OnRequest)
		s.SetStack(stack)
		return s, stack
	}
	stopping, stack := start("scscf-1." + domain)

	// The user registered through the stopping instance, as its MAR and
	// its SAR left the HSS and it left the store.
	const assigned = "sip:scscf-1." + domain + ":5060"
	hssStub.register(t, "scscf-1."+domain, assigned)
	ctx := context.Background()
	store := scscfStore(stores, quiet())
	expires := time.Now().Add(200 * time.Millisecond)
	if err := store.PutRegistration(ctx, scscf.Registration{PrivateIdentity: impi, PublicIdentities: []string{impu},
		ServerName: assigned, Contact: "sip:ue1@192.0.2.10", ContactID: "c1", ContactEvent: reginfo.Registered, Expires: expires}); err != nil {
		t.Fatal(err)
	}
	var sub scscf.Subscription
	sub.PrivateIdentity, sub.ICID, sub.Version = impi, "icid-1", 1
	sub.Dialog = sip.Dialog{ID: sip.DialogID{CallID: "sub-1", LocalTag: "l1", RemoteTag: "r1"},
		State: sip.DialogConfirmed, LocalSeq: 1, RemoteSeq: 1, LocalURI: impu, RemoteURI: impu,
		RemoteTarget: "sip:ue1@" + subscriber.LocalAddr().String()}
	sub.Event, sub.Contact = "reg", "sip:scscf-1."+domain
	sub.Expires, sub.State = time.Now().Add(time.Hour), sip.SubscriptionActive
	if err := store.PutSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}

	stopNode([]func(){stopping.Close}, []func(){stack.Close}, func() {
		// The registration runs out while the node closes its Diameter
		// connections, and a sweep that still ran would claim it within
		// a round or two.
		time.Sleep(time.Until(expires) + 10*sweepInterval)
	})
	if got := hssStub.assignments(); len(got) != 0 {
		t.Errorf("SARs %+v while the node stopped, want none", got)
	}

	start("scscf-2." + domain)
	select {
	case notify := <-notifies:
		doc, err := reginfo.Parse(notify.Body)
		if err != nil {
			t.Fatalf("the NOTIFY carries no reginfo document: %v", err)
		}
		for _, r := range doc.Registrations {
			if r.State != reginfo.Terminated || len(r.Contacts) != 1 || r.Contacts[0].Event != reginfo.Expired {
				t.Errorf("the NOTIFY says %+v, want the registration terminated and its contact expired", r)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscriber got no NOTIFY within 5 s of the start of the other instance")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(hssStub.assignments()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the HSS got no SAR within 5 s of the start of the other instance")
		}
		time.Sleep(10 * time.Millisecond)
	}
	want := sentSAR{"scscf-2." + domain, assigned, cx.TimeoutDeregistration}
	if got := hssStub.assignments(); len(got) != 1 || got[0] != want {
		t.Errorf("SARs %+v, want one from the instance in service under the name the HSS holds, %+v", got, want)
	}
	if got := hssStub.heldBy(t); got != "" {
		t.Errorf("the HSS holds %s after the expiry, want no S-CSCF", got)
	}
}
