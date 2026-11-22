package storetest_test

import (
	"context"
	"math"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The suites run on the MemoryStores of the role packages too: what they
// check is the behaviour of a Store, not of the adapters. Two instances
// of a role share a MemoryStore by sharing the one value.

func TestSCSCFMemoryStore(t *testing.T) {
	storetest.SCSCF(t, func(*testing.T) (scscf.Store, scscf.Store) {
		s := scscf.NewMemoryStore()
		return s, s
	})
}

func TestPCSCFMemoryStore(t *testing.T) {
	storetest.PCSCF(t, func(*testing.T) (pcscf.Store, pcscf.Store) {
		s := pcscf.NewMemoryStore()
		return s, s
	})
}

func TestHSSMemoryStore(t *testing.T) {
	newStore := func(t *testing.T) hss.Store {
		s, err := hss.NewMemoryStore()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	storetest.HSS(t, func(t *testing.T) (hss.Store, hss.Store) {
		s := newStore(t)
		return s, s
	})
}

func TestEPCMemoryStore(t *testing.T) {
	storetest.EPC(t, func(*testing.T) (epc.Store, epc.Store) {
		s := epc.NewMemoryStore()
		return s, s
	})
}

func TestMMTelMemoryStore(t *testing.T) {
	storetest.MMTel(t, func(*testing.T) (mmtel.Store, mmtel.Store) {
		s := mmtel.NewMemoryStore()
		return s, s
	})
}

func TestSCMemoryStore(t *testing.T) {
	storetest.SC(t, func(*testing.T) (sc.Store, sc.Store) {
		s := sc.NewMemoryStore()
		return s, s
	})
}

func TestIPSMGWMemoryStore(t *testing.T) {
	storetest.IPSMGW(t, func(*testing.T) (ipsmgw.Store, ipsmgw.Store) {
		s := ipsmgw.NewMemoryStore()
		return s, s
	})
}

// TestMemoryStoreLookupsDoNotGrow fills the MemoryStores of the S-CSCF
// and the P-CSCF with few users and with many, and times the lookups by
// another identifier than the key: the registration of a public
// identity, the subscriptions to a registration and the association of
// a contact. Each reads an index, so a lookup among many users takes
// about as long as one among few; a walk over all of them would take
// as many times longer as there are more users.
func TestMemoryStoreLookupsDoNotGrow(t *testing.T) {
	const few, many = 100, 20000
	for _, c := range []struct {
		name string
		// fill puts n users into a new store and returns a lookup of
		// the last one, which fails the test when it finds nothing.
		fill func(t *testing.T, n int) func()
	}{
		{"S-CSCF RegistrationOf", func(t *testing.T, n int) func() {
			s := scscf.NewMemoryStore()
			for i := range n {
				if err := s.PutRegistration(context.Background(), scscf.Registration{PrivateIdentity: user(i) + "@" + domain,
					PublicIdentities: []string{"sip:" + user(i) + "@" + domain}}); err != nil {
					t.Fatal(err)
				}
			}
			last := "sip:" + user(n-1) + "@" + domain
			return func() {
				if _, ok, err := s.RegistrationOf(context.Background(), last); !ok || err != nil {
					t.Fatalf("RegistrationOf(%s) found nothing", last)
				}
			}
		}},
		{"S-CSCF SubscriptionsOf", func(t *testing.T, n int) func() {
			s := scscf.NewMemoryStore()
			for i := range n {
				var sub scscf.Subscription
				sub.PrivateIdentity = user(i) + "@" + domain
				sub.Dialog.ID = sip.DialogID{CallID: user(i), LocalTag: "l", RemoteTag: "r"}
				if err := s.PutSubscription(context.Background(), sub); err != nil {
					t.Fatal(err)
				}
			}
			last := user(n-1) + "@" + domain
			return func() {
				if subs, err := s.SubscriptionsOf(context.Background(), last); len(subs) != 1 || err != nil {
					t.Fatalf("SubscriptionsOf(%s) is not the one subscription", last)
				}
			}
		}},
		{"P-CSCF AssociationOfContact", func(t *testing.T, n int) func() {
			s := pcscf.NewMemoryStore()
			for i := range n {
				addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}), 5060)
				if err := s.PutAssociation(context.Background(), pcscf.Association{Source: sip.Destination{Transport: sip.UDP, Addr: addr},
					Contact: "sip:" + user(i) + "@" + addr.String()}); err != nil {
					t.Fatal(err)
				}
			}
			last := netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, byte((n - 1) >> 8), byte(n - 1)}), 5060)
			contact := "sip:" + user(n-1) + "@" + last.String()
			return func() {
				if _, ok, err := s.AssociationOfContact(context.Background(), contact); !ok || err != nil {
					t.Fatalf("AssociationOfContact(%s) found nothing", contact)
				}
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			small, large := perLookup(c.fill(t, few)), perLookup(c.fill(t, many))
			// A walk would take about many/few = 200 times longer. The
			// bound leaves room for the caches and the race detector.
			if large > 20*small {
				t.Errorf("a lookup among %d users takes %v, among %d %v", many, large, few, small)
			}
		})
	}
}

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

func user(i int) string { return "user" + strconv.Itoa(i) }

// perLookup is the shortest mean time of a lookup over a few rounds, so
// that a pause of the machine in one round does not count.
func perLookup(lookup func()) time.Duration {
	const rounds, lookups = 5, 2000
	best := time.Duration(math.MaxInt64)
	for range rounds {
		start := time.Now()
		for range lookups {
			lookup()
		}
		best = min(best, time.Since(start)/lookups)
	}
	return best
}
