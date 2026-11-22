package storetest

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// HSS runs the suite of hss.Store. newPair returns two empty instances.
func HSS(t *testing.T, newPair func(t *testing.T) (hss.Store, hss.Store)) {
	run(t, newPair, []testCase[hss.Store]{
		{"FindsASubscriberByEachIdentity", hssFindsByEachIdentity},
		{"RefusesAnIdentityOfAnotherSubscription", hssRefusesTakenIdentity},
		{"DeleteDropsTheIndex", hssDeleteDropsTheIndex},
		{"ListIsBoundedAndOrdered", hssListIsBoundedAndOrdered},
		{"StoresTheProvisionedRepositoryData", hssStoresRepositoryData},
		{"KeepsSQNOutOfPut", hssKeepsSQNOutOfPut},
		{"CompareAndSwapRepositoryData", hssCompareAndSwapRepositoryData},
		{"ConcurrentSQN", hssConcurrentSQN},
		{"ConcurrentAddsOfOneIdentity", hssConcurrentAdds},
	})
}

const realm = "ims.mnc001.mcc001.3gppnetwork.org"

// akaSecret is K and OPc of test set 1 of TS 35.208.
var akaSecret = aka.Secret{
	K:   [16]byte{0x46, 0x5b, 0x5c, 0xe8, 0xb1, 0x99, 0xb4, 0x9f, 0xaa, 0x5f, 0x0a, 0x2e, 0xe2, 0x38, 0xa6, 0xbc},
	OPc: [16]byte{0xcd, 0x63, 0xcb, 0x71, 0x95, 0x4a, 0x9f, 0x4e, 0x48, 0xa5, 0x99, 0x4e, 0x37, 0xa0, 0x2b, 0xaf},
}

func mustAdd(t *testing.T, s hss.Store, subs ...hss.Subscription) {
	t.Helper()
	for _, sub := range subs {
		if err := s.Add(sub); err != nil {
			t.Fatalf("Add(%s): %v", sub.PrivateIdentity, err)
		}
	}
}

func hssFindsByEachIdentity(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	// RFC 3261 19.1.4: the scheme and the host compare without case,
	// the user part does not. A NAI compares without case. A tel URI
	// compares by its canonical form (TS 29.229 6.3.2).
	for _, id := range []string{impu, tel, "SIP:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org", "tel:+1-202-555-0101"} {
		if got, ok := b.SubscriberOf(id); !ok || got.PrivateIdentity != impi {
			t.Errorf("SubscriberOf(%s) = %+v, %v", id, got, ok)
		}
	}
	if _, ok := b.SubscriberOf("sip:001010000000001X@" + realm); ok {
		t.Error("found a subscriber for a user part it does not have")
	}
	mustAdd(t, a, hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:Alice@" + realm}, Password: "p"})
	if _, ok := b.SubscriberOf("sip:alice@" + realm); ok {
		t.Error("found the subscriber of sip:Alice for sip:alice")
	}
	if got, ok := b.Subscriber("001010000000001@IMS.MNC001.MCC001.3GPPNETWORK.ORG"); !ok || got.PrivateIdentity != impi {
		t.Errorf("Subscriber by the private identity in upper case = %+v, %v", got, ok)
	}
}

func hssRefusesTakenIdentity(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	for _, sub := range []hss.Subscription{
		{PrivateIdentity: impi, PublicIdentities: []string{"sip:new@" + realm}, Password: "p"},
		{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:new@" + realm, tel}, Password: "p"},
	} {
		if err := b.Add(sub); !errors.Is(err, hss.ErrIdentityTaken) {
			t.Errorf("Add(%+v) = %v, want ErrIdentityTaken", sub, err)
		}
	}
	// The refused subscription left nothing behind.
	if _, ok := a.SubscriberOf("sip:new@" + realm); ok {
		t.Error("a refused subscription was indexed")
	}
	if _, ok := a.Subscriber(otherIMPI); ok {
		t.Error("a refused subscription was stored")
	}
	if err := a.Add(hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:new@" + realm}, Password: "p"}); err != nil {
		t.Errorf("Add of the identity that a refused subscription named: %v", err)
	}
}

func hssDeleteDropsTheIndex(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	got, ok := b.Delete(impi)
	if !ok || got.PrivateIdentity != impi {
		t.Fatalf("Delete = %+v, %v", got, ok)
	}
	if _, ok := a.SubscriberOf(tel); ok {
		t.Error("a public identity of the deleted subscription still finds it")
	}
	// The identities are free for a new subscription.
	if err := a.Add(hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{tel}, Password: "p"}); err != nil {
		t.Errorf("Add after Delete: %v", err)
	}
	b.Put(hss.Subscriber{Subscription: hss.Subscription{PrivateIdentity: impi}, State: hss.Registered})
	if _, ok := a.Subscriber(impi); ok {
		t.Error("Put brought back a deleted subscriber")
	}
	if _, ok := a.Delete(impi); ok {
		t.Error("a second Delete reported true")
	}
}

func hssListIsBoundedAndOrdered(t *testing.T, a, b hss.Store) {
	for _, name := range []string{"c", "a", "b"} {
		mustAdd(t, a, hss.Subscription{PrivateIdentity: name + "@" + realm, PublicIdentities: []string{"sip:" + name + "@" + realm}, Password: "p"})
	}
	got := b.List(2)
	if len(got) != 2 || got[0].PrivateIdentity != "a@"+realm || got[1].PrivateIdentity != "b@"+realm {
		t.Errorf("List(2) = %+v", got)
	}
	a.Delete("a@" + realm)
	if got := b.List(10); len(got) != 2 || got[0].PrivateIdentity != "b@"+realm {
		t.Errorf("List after Delete = %+v", got)
	}
}

// hssStoresRepositoryData: the operator's first data is version 0 (TS
// 29.328 6.1.2.1), and the subscription keeps no copy that could
// disagree with what Sh changes later.
func hssStoresRepositoryData(t *testing.T, a, b hss.Store) {
	data := []sh.TransparentData{
		{ServiceIndication: "MMTEL-Services", SequenceNumber: 7, ServiceData: &sh.ServiceData{XML: []byte("<a/>")}},
		hss.UtCredentialData(impi, realm, "test-password-1"),
	}
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p", RepositoryData: data})
	got, ok := b.RepositoryData(impi, "MMTEL-Services")
	if !ok || got.SequenceNumber != 0 || string(got.ServiceData.XML) != "<a/>" {
		t.Errorf("MMTEL-Services = %+v, %v", got, ok)
	}
	if _, ok := b.RepositoryData(impi, data[1].ServiceIndication); !ok {
		t.Error("no Ut credential")
	}
	if sub, _ := b.Subscriber(impi); sub.RepositoryData != nil {
		t.Errorf("the stored subscription keeps %v", sub.RepositoryData)
	}
}

func hssKeepsSQNOutOfPut(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: akaSecret}})
	stale, _ := b.Subscriber(impi)
	if !a.CompareAndSwapSQN(impi, 0, 5) {
		t.Fatal("CompareAndSwapSQN from the stored SQN failed")
	}
	if b.CompareAndSwapSQN(impi, 0, 6) {
		t.Fatal("CompareAndSwapSQN from a stale SQN succeeded")
	}
	stale.State = hss.Registered
	stale.AKA.SQN = 0
	b.Put(stale)
	if u, _ := a.Subscriber(impi); u.State != hss.Registered || u.AKA.SQN != 5 || u.AKA.Secret != akaSecret {
		t.Errorf("after Put: state %v, SQN %d; want Registered and SQN 5", u.State, u.AKA.SQN)
	}
	if a.CompareAndSwapSQN("nobody@"+realm, 0, 1) {
		t.Error("CompareAndSwapSQN for an unknown subscriber succeeded")
	}
}

func hssCompareAndSwapRepositoryData(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	v1 := sh.TransparentData{ServiceIndication: "MMTEL-Services", SequenceNumber: 0, ServiceData: &sh.ServiceData{XML: []byte("<v1/>")}}
	if !a.CompareAndSwapRepositoryData(impi, nil, v1) {
		t.Fatal("the first data was refused")
	}
	if b.CompareAndSwapRepositoryData(impi, nil, v1) {
		t.Error("new data was taken over data that is there")
	}
	v2 := v1
	v2.SequenceNumber, v2.ServiceData = 1, &sh.ServiceData{XML: []byte("<v2/>")}
	// Two instances update from version 0 at once: one wins.
	var wins atomic.Int32
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		if s.CompareAndSwapRepositoryData(impi, &v1, v2) {
			wins.Add(1)
		}
	})
	if got := wins.Load(); got != 1 {
		t.Errorf("%d updates from one version succeeded, want 1", got)
	}
	if got, _ := a.RepositoryData(impi, "MMTEL-Services"); got.SequenceNumber != 1 || string(got.ServiceData.XML) != "<v2/>" {
		t.Errorf("RepositoryData = %+v", got)
	}
	// Data without ServiceData removes it.
	if !b.CompareAndSwapRepositoryData(impi, &v2, sh.TransparentData{ServiceIndication: "MMTEL-Services", SequenceNumber: 2}) {
		t.Fatal("the removal was refused")
	}
	if _, ok := a.RepositoryData(impi, "MMTEL-Services"); ok {
		t.Error("RepositoryData after its removal")
	}
}

// hssConcurrentSQN has two HSS instances take vectors of one user at
// once (TS 33.102 C.3.2): SQN_HE must never be used twice.
func hssConcurrentSQN(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: akaSecret}})
	const n = 20
	var mu sync.Mutex
	used := map[aka.SQN]bool{}
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		for range n {
			for {
				sub, _ := s.Subscriber(impi)
				next := sub.AKA.SQN.Next()
				if s.CompareAndSwapSQN(impi, sub.AKA.SQN, next) {
					mu.Lock()
					if used[next] {
						t.Errorf("SQN %d used twice", next)
					}
					used[next] = true
					mu.Unlock()
					break
				}
			}
		}
	})
	want := aka.SQN(0)
	for range 2 * n {
		want = want.Next()
	}
	if u, _ := a.Subscriber(impi); u.AKA.SQN != want {
		t.Errorf("SQN_HE = %d after %d vectors, want %d", u.AKA.SQN, 2*n, want)
	}
}

// hssConcurrentAdds has the provisioning API of two instances add two
// subscriptions that share a public identity: one of them gets it.
func hssConcurrentAdds(t *testing.T, a, b hss.Store) {
	for round := range 5 {
		shared := fmt.Sprintf("tel:+1202555011%d", round)
		var added atomic.Int32
		parallel(2, func(i int) {
			s := a
			if i == 1 {
				s = b
			}
			id := fmt.Sprintf("00101000000%d%d@%s", round, i, realm)
			err := s.Add(hss.Subscription{PrivateIdentity: id, PublicIdentities: []string{"sip:" + id, shared}, Password: "p"})
			switch {
			case err == nil:
				added.Add(1)
			case !errors.Is(err, hss.ErrIdentityTaken):
				t.Errorf("Add: %v", err)
			}
		})
		if got := added.Load(); got != 1 {
			t.Fatalf("%d subscriptions with %s were added, want 1", got, shared)
		}
		if _, ok := a.SubscriberOf(shared); !ok {
			t.Errorf("SubscriberOf(%s) finds nothing", shared)
		}
	}
}
