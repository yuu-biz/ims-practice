package ue_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

func nextState(t *testing.T, ev *ue.RegEvent) ue.RegState {
	t.Helper()
	select {
	case s, ok := <-ev.States():
		if !ok {
			t.Fatalf("the subscription ended without another NOTIFY: %v", ev.Err())
		}
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no NOTIFY reached the UE")
		return ue.RegState{}
	}
}

// subscribed registers a UE, subscribes it to its registration state,
// and waits until both the UE and the P-CSCF have the first NOTIFY.
func subscribed(t *testing.T, n testNetwork) (*party, *ue.RegEvent) {
	t.Helper()
	p := newParty(t, n.pcscf, impi, impu, password)
	ev, err := p.ue.SubscribeRegEvent(p.ua, p.reg)
	if err != nil {
		t.Fatal(err)
	}
	first := nextState(t, ev)
	if first.Registration != reginfo.Active || first.Contact != reginfo.Active || first.Event != reginfo.Registered ||
		first.Subscription != sip.SubscriptionActive || first.Deregistered() {
		t.Fatalf("first NOTIFY to the UE: %+v", first)
	}
	n.pcscfLog.waitFor(t, map[string]string{"public_identity": impu, "state": "active", "contact_event": "registered"})
	return p, ev
}

// waitAssociationEnded waits until the P-CSCF takes no more requests
// from the UE (TS 24.229 5.2.5.2).
func waitAssociationEnded(t *testing.T, n testNetwork, contact string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a, ok := n.pcscfStore.AssociationOfContact(contact); !ok || !a.Expires.After(time.Now()) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the P-CSCF kept the IP association (TS 24.229 5.2.5.2)")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRegEventReportsTheExpiryThroughTheWholeIMS lets a registration run
// out: the S-CSCF tells the UE and the P-CSCF, and the P-CSCF forgets
// the UE.
func TestRegEventReportsTheExpiryThroughTheWholeIMS(t *testing.T) {
	n := networkWith(t, time.Second, 2*time.Second)
	p, ev := subscribed(t, n)
	if p.reg.Expires != 2*time.Second {
		t.Fatalf("registered for %v, want the 2 s of MaxExpires", p.reg.Expires)
	}

	last := nextState(t, ev)
	if !last.Deregistered() || last.Registration != reginfo.Terminated || last.Event != reginfo.Expired ||
		last.Subscription != sip.SubscriptionTerminated {
		t.Errorf("last NOTIFY to the UE: %+v", last)
	}
	select {
	case <-ev.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the UE's subscription did not end")
	}
	n.pcscfLog.waitFor(t, map[string]string{"public_identity": impu, "state": "terminated", "contact_event": "expired",
		"subscription_state": "terminated"})
	waitAssociationEnded(t, n, p.contact.URI())
}

// TestLostIsTheNOTIFYThatEndsTheRegistration: Lost skips the NOTIFYs
// that keep the registration and delivers the one that ends it.
func TestLostIsTheNOTIFYThatEndsTheRegistration(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	_, ev := subscribed(t, n)
	lost := ev.Lost()
	if err := n.scscf.Deregister(impi, reginfo.Deactivated); err != nil {
		t.Fatal(err)
	}
	select {
	case state := <-lost:
		if state.Event != reginfo.Deactivated {
			t.Errorf("Lost delivered %+v, want the deactivated NOTIFY", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lost delivered nothing after the network deregistered the UE")
	}
}

// TestNetworkInitiatedDeregistrationReachesTheUE is TS 24.229 5.4.1.5
// and 5.1.1.7: deactivated tells the UE to register again.
func TestNetworkInitiatedDeregistrationReachesTheUE(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	p, ev := subscribed(t, n)
	if err := n.scscf.Deregister(impi, reginfo.Deactivated); err != nil {
		t.Fatal(err)
	}
	last := nextState(t, ev)
	if !last.Deregistered() || last.Event != reginfo.Deactivated {
		t.Errorf("last NOTIFY to the UE: %+v", last)
	}
	waitAssociationEnded(t, n, p.contact.URI())
}

// TestRemovedSubscriptionIsDeregisteredThroughTheWholeIMS removes the
// subscription of a registered user through the HSS provisioning API.
// The HSS sends the S-CSCF an RTR (TS 29.228 6.1.3), the S-CSCF tells
// the UE that its registration is rejected (TS 24.229 5.4.1.5), and the
// HSS no longer knows the user, so a new REGISTER gets 403 from the
// I-CSCF (TS 29.228 6.1.1.1 and TS 24.229 5.3.1.3).
func TestRemovedSubscriptionIsDeregisteredThroughTheWholeIMS(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	p, ev := subscribed(t, n)

	w := httptest.NewRecorder()
	n.provisioning.ServeHTTP(w, httptest.NewRequest("DELETE", "/subscriptions/"+impi, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	last := nextState(t, ev)
	if !last.Deregistered() || last.Registration != reginfo.Terminated || last.Event != reginfo.Rejected ||
		last.Subscription != sip.SubscriptionTerminated {
		t.Errorf("last NOTIFY to the UE: %+v", last)
	}
	n.pcscfLog.waitFor(t, map[string]string{"public_identity": impu, "state": "terminated", "contact_event": "rejected"})
	waitAssociationEnded(t, n, p.contact.URI())

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := p.ue.Register(ctx); !errors.Is(err, ue.ErrForbidden) {
		t.Errorf("REGISTER after the removal: %v, want 403", err)
	}
}
