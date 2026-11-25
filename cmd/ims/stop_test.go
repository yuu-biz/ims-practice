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
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// assigningHSS answers every Cx request with success and records the
// Server-Name of each SAR, so that a test sees which S-CSCF sent it.
// Like the HSS of a network it is reached over the wire.
type assigningHSS struct {
	mu      sync.Mutex
	servers []string
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
	if sar, err := cx.ParseServerAssignmentRequest(request); err == nil && request.Header.CommandCode == cx.CommandServerAssignment {
		h.mu.Lock()
		h.servers = append(h.servers, sar.ServerName)
		h.mu.Unlock()
	}
	identity := diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}
	return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, identity), nil
}

func (h *assigningHSS) assignedBy() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.servers...)
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
// the HSS hear of the end of the registration from that one.
func TestAnExpiryThatComesWhileANodeStopsGoesToAnotherInstance(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	stores := roleStores{kv: kvstore.NewMemoryStore(nil), section: config.Store{Kind: config.StoreValkey, Timeout: time.Second}}
	hss := &assigningHSS{}
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
			hss, node, scscfStore(stores, log), toAddress{subscriber.LocalAddr()}, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		stack := listenSIP(t, s.OnRequest)
		s.SetStack(stack)
		return s, stack
	}
	stopping, stack := start("scscf-1." + domain)

	ctx := context.Background()
	store := scscfStore(stores, quiet())
	expires := time.Now().Add(200 * time.Millisecond)
	if err := store.PutRegistration(ctx, scscf.Registration{PrivateIdentity: impi, PublicIdentities: []string{impu},
		Contact: "sip:ue1@192.0.2.10", ContactID: "c1", ContactEvent: reginfo.Registered, Expires: expires}); err != nil {
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
	if got := hss.assignedBy(); len(got) != 0 {
		t.Errorf("SARs from %v while the node stopped, want none", got)
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
	for len(hss.assignedBy()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the HSS got no SAR within 5 s of the start of the other instance")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := hss.assignedBy(); len(got) != 1 || got[0] != "sip:scscf-2."+domain+":5060" {
		t.Errorf("SARs from %v, want one from the instance in service", got)
	}
}
