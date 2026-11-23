package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
		{"KeepsTheMarkAndTheProfileOfASubscription", hssKeepsTheMarkAndTheProfile},
		{"RefusesAnIdentityOfAnotherSubscription", hssRefusesTakenIdentity},
		{"DeleteDropsTheIndex", hssDeleteDropsTheIndex},
		{"ListIsOrderedAndPaged", hssListIsOrderedAndPaged},
		{"ListGoesOnFromASubscriberThatIsGone", hssListGoesOnFromASubscriberThatIsGone},
		{"StoresTheProvisionedRepositoryData", hssStoresRepositoryData},
		{"ReadsAllTheRepositoryDataOfASubscription", hssReadsAllRepositoryData},
		{"KeepsSQNOutOfPut", hssKeepsSQNOutOfPut},
		{"CompareAndSwapRepositoryData", hssCompareAndSwapRepositoryData},
		{"ConcurrentSQN", hssConcurrentSQN},
		{"ConcurrentAddsOfOneIdentity", hssConcurrentAdds},
		{"CountsFollowWhatIsStored", hssCountsFollowWhatIsStored},
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
		if err := s.Add(context.Background(), sub); err != nil {
			t.Fatalf("Add(%s): %v", sub.PrivateIdentity, err)
		}
	}
}

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent cases call them on goroutines of
// their own.

func subscriber(t *testing.T, s hss.Store, privateID string) (hss.Subscriber, bool) {
	t.Helper()
	sub, ok, err := s.Subscriber(context.Background(), privateID)
	if err != nil {
		t.Errorf("Subscriber(%s): %v", privateID, err)
	}
	return sub, ok
}

func subscriberOf(t *testing.T, s hss.Store, publicID string) (hss.Subscriber, bool) {
	t.Helper()
	sub, ok, err := s.SubscriberOf(context.Background(), publicID)
	if err != nil {
		t.Errorf("SubscriberOf(%s): %v", publicID, err)
	}
	return sub, ok
}

func add(t *testing.T, s hss.Store, sub hss.Subscription) error {
	t.Helper()
	err := s.Add(context.Background(), sub)
	if err != nil && !errors.Is(err, hss.ErrIdentityTaken) {
		t.Errorf("Add(%s): %v", sub.PrivateIdentity, err)
	}
	return err
}

func put(t *testing.T, s hss.Store, sub hss.Subscriber) {
	t.Helper()
	if err := s.Put(context.Background(), sub); err != nil {
		t.Errorf("Put(%s): %v", sub.PrivateIdentity, err)
	}
}

func compareAndSwapSQN(t *testing.T, s hss.Store, privateID string, old, new aka.SQN) bool {
	t.Helper()
	swapped, err := s.CompareAndSwapSQN(context.Background(), privateID, old, new)
	if err != nil {
		t.Errorf("CompareAndSwapSQN(%s): %v", privateID, err)
	}
	return swapped
}

func repositoryData(t *testing.T, s hss.Store, privateID, serviceIndication string) (sh.TransparentData, bool) {
	t.Helper()
	d, ok, err := s.RepositoryData(context.Background(), privateID, serviceIndication)
	if err != nil {
		t.Errorf("RepositoryData(%s, %s): %v", privateID, serviceIndication, err)
	}
	return d, ok
}

func allRepositoryData(t *testing.T, s hss.Store, privateID string) []sh.TransparentData {
	t.Helper()
	data, err := s.AllRepositoryData(context.Background(), privateID)
	if err != nil {
		t.Errorf("AllRepositoryData(%s): %v", privateID, err)
	}
	return data
}

func compareAndSwapRepositoryData(t *testing.T, s hss.Store, privateID string, old *sh.TransparentData, data sh.TransparentData) bool {
	t.Helper()
	swapped, err := s.CompareAndSwapRepositoryData(context.Background(), privateID, old, data)
	if err != nil {
		t.Errorf("CompareAndSwapRepositoryData(%s): %v", privateID, err)
	}
	return swapped
}

func deleteSubscriber(t *testing.T, s hss.Store, privateID string) (hss.Subscriber, bool) {
	t.Helper()
	sub, ok, err := s.Delete(context.Background(), privateID)
	if err != nil {
		t.Errorf("Delete(%s): %v", privateID, err)
	}
	return sub, ok
}

func list(t *testing.T, s hss.Store, after string, limit int) ([]hss.Subscriber, string) {
	t.Helper()
	subs, next, err := s.List(context.Background(), after, limit)
	if err != nil {
		t.Errorf("List(%q, %d): %v", after, limit, err)
	}
	return subs, next
}

// privateIdentities are the private identities of subs, for comparing a
// page against what it should hold.
func privateIdentities(subs []hss.Subscriber) []string {
	ids := make([]string, len(subs))
	for i, s := range subs {
		ids[i] = s.PrivateIdentity
	}
	return ids
}

func hssFindsByEachIdentity(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	// RFC 3261 19.1.4: the scheme and the host compare without case,
	// the user part does not. A NAI compares without case. A tel URI
	// compares by its canonical form (TS 29.229 6.3.2).
	for _, id := range []string{impu, tel, "SIP:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org", "tel:+1-202-555-0101"} {
		if got, ok := subscriberOf(t, b, id); !ok || got.PrivateIdentity != impi {
			t.Errorf("SubscriberOf(%s) = %+v, %v", id, got, ok)
		}
	}
	if _, ok := subscriberOf(t, b, "sip:001010000000001X@"+realm); ok {
		t.Error("found a subscriber for a user part it does not have")
	}
	mustAdd(t, a, hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:Alice@" + realm}, Password: "p"})
	if _, ok := subscriberOf(t, b, "sip:alice@"+realm); ok {
		t.Error("found the subscriber of sip:Alice for sip:alice")
	}
	if got, ok := subscriber(t, b, "001010000000001@IMS.MNC001.MCC001.3GPPNETWORK.ORG"); !ok || got.PrivateIdentity != impi {
		t.Errorf("Subscriber by the private identity in upper case = %+v, %v", got, ok)
	}
}

// hssKeepsTheMarkAndTheProfile: the test user mark and the name of the
// service profile are data of the subscription, so an instance that did
// not add it reads both back, by either identity, and a write of the
// registration state leaves them as they are. A subscription with
// neither comes back with neither, which is the subscription that takes
// the profile of its kind of user.
func hssKeepsTheMarkAndTheProfile(t *testing.T, a, b hss.Store) {
	mustAdd(t, a,
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p", TestUser: true, ServiceProfile: "trial"},
		hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:other@" + realm}, Password: "p"},
	)
	got, ok := subscriber(t, b, impi)
	if !ok || !got.TestUser || got.ServiceProfile != "trial" {
		t.Errorf("Subscriber = %+v, %v", got.Subscription, ok)
	}
	if byTel, ok := subscriberOf(t, b, tel); !ok || !byTel.TestUser || byTel.ServiceProfile != "trial" {
		t.Errorf("SubscriberOf = %+v, %v", byTel.Subscription, ok)
	}
	got.State = hss.Registered
	put(t, b, got)
	if registered, ok := subscriber(t, a, impi); !ok || !registered.TestUser || registered.ServiceProfile != "trial" {
		t.Errorf("after a Put of the registration state: %+v, %v", registered.Subscription, ok)
	}
	if plain, ok := subscriber(t, b, otherIMPI); !ok || plain.TestUser || plain.ServiceProfile != "" {
		t.Errorf("a subscription that is neither marked nor named: %+v, %v", plain.Subscription, ok)
	}
}

func hssRefusesTakenIdentity(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	for _, sub := range []hss.Subscription{
		{PrivateIdentity: impi, PublicIdentities: []string{"sip:new@" + realm}, Password: "p"},
		{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:new@" + realm, tel}, Password: "p"},
	} {
		if err := add(t, b, sub); !errors.Is(err, hss.ErrIdentityTaken) {
			t.Errorf("Add(%+v) = %v, want ErrIdentityTaken", sub, err)
		}
	}
	// The refused subscription left nothing behind.
	if _, ok := subscriberOf(t, a, "sip:new@"+realm); ok {
		t.Error("a refused subscription was indexed")
	}
	if _, ok := subscriber(t, a, otherIMPI); ok {
		t.Error("a refused subscription was stored")
	}
	if err := add(t, a, hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:new@" + realm}, Password: "p"}); err != nil {
		t.Errorf("Add of the identity that a refused subscription named: %v", err)
	}
}

func hssDeleteDropsTheIndex(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	got, ok := deleteSubscriber(t, b, impi)
	if !ok || got.PrivateIdentity != impi {
		t.Fatalf("Delete = %+v, %v", got, ok)
	}
	if _, ok := subscriberOf(t, a, tel); ok {
		t.Error("a public identity of the deleted subscription still finds it")
	}
	// The identities are free for a new subscription.
	if err := add(t, a, hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{tel}, Password: "p"}); err != nil {
		t.Errorf("Add after Delete: %v", err)
	}
	put(t, b, hss.Subscriber{Subscription: hss.Subscription{PrivateIdentity: impi}, State: hss.Registered})
	if _, ok := subscriber(t, a, impi); ok {
		t.Error("Put brought back a deleted subscriber")
	}
	if _, ok := deleteSubscriber(t, a, impi); ok {
		t.Error("a second Delete reported true")
	}
}

// hssListIsOrderedAndPaged: a page holds the subscribers after its
// cursor, in the order of their private identities, and its next cursor
// leads to the page after it. A page that is not full is the last one.
func hssListIsOrderedAndPaged(t *testing.T, a, b hss.Store) {
	for _, name := range []string{"c", "a", "b"} {
		mustAdd(t, a, hss.Subscription{PrivateIdentity: name + "@" + realm, PublicIdentities: []string{"sip:" + name + "@" + realm}, Password: "p"})
	}
	got, next := list(t, b, "", 2)
	if want := []string{"a@" + realm, "b@" + realm}; !slices.Equal(privateIdentities(got), want) {
		t.Errorf("the first page of two = %v, want %v", privateIdentities(got), want)
	}
	if next == "" {
		t.Fatal("a full page has no next cursor")
	}
	got, next = list(t, b, next, 2)
	if want := []string{"c@" + realm}; !slices.Equal(privateIdentities(got), want) {
		t.Errorf("the second page = %v, want %v", privateIdentities(got), want)
	}
	if next != "" {
		t.Errorf("the last page has a next cursor %q", next)
	}
	deleteSubscriber(t, a, "a@"+realm)
	if got, _ := list(t, b, "", 10); !slices.Equal(privateIdentities(got), []string{"b@" + realm, "c@" + realm}) {
		t.Errorf("List after Delete = %v", privateIdentities(got))
	}
}

// hssListGoesOnFromASubscriberThatIsGone: the operator who pages
// through the subscribers sees the rest of them even when the one the
// page before it ended at has been removed since.
func hssListGoesOnFromASubscriberThatIsGone(t *testing.T, a, b hss.Store) {
	for _, name := range []string{"a", "b", "c"} {
		mustAdd(t, a, hss.Subscription{PrivateIdentity: name + "@" + realm, PublicIdentities: []string{"sip:" + name + "@" + realm}, Password: "p"})
	}
	got, next := list(t, b, "", 1)
	if want := []string{"a@" + realm}; !slices.Equal(privateIdentities(got), want) {
		t.Fatalf("the first page of one = %v, want %v", privateIdentities(got), want)
	}
	deleteSubscriber(t, a, "a@"+realm)
	if got, _ := list(t, b, next, 10); !slices.Equal(privateIdentities(got), []string{"b@" + realm, "c@" + realm}) {
		t.Errorf("the page after the one that is gone = %v", privateIdentities(got))
	}
	// A private identity compares without case, wherever the cursor has
	// been (TS 29.229 6.3.1 is a NAI, RFC 7542 2.4).
	if got, _ := list(t, b, "B@"+strings.ToUpper(realm), 10); !slices.Equal(privateIdentities(got), []string{"c@" + realm}) {
		t.Errorf("the page after the private identity in upper case = %v", privateIdentities(got))
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
	got, ok := repositoryData(t, b, impi, "MMTEL-Services")
	if !ok || got.SequenceNumber != 0 || string(got.ServiceData.XML) != "<a/>" {
		t.Errorf("MMTEL-Services = %+v, %v", got, ok)
	}
	if _, ok := repositoryData(t, b, impi, data[1].ServiceIndication); !ok {
		t.Error("no Ut credential")
	}
	if sub, _ := subscriber(t, b, impi); sub.RepositoryData != nil {
		t.Errorf("the stored subscription keeps %v", sub.RepositoryData)
	}
}

// The provisioning API reads the repository data of a subscription
// whole, to replace it or to write it out: every Service-Indication the
// subscription has, with the Sequence Number that is stored, in the
// order the indications sort in. A subscription with no data, and a
// private identity that no subscription has, have none.
func hssReadsAllRepositoryData(t *testing.T, a, b hss.Store) {
	mmtel := sh.TransparentData{ServiceIndication: "MMTEL-Services", ServiceData: &sh.ServiceData{XML: []byte("<a/>")}}
	mustAdd(t, a,
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p",
			RepositoryData: []sh.TransparentData{hss.UtCredentialData(impi, realm, "test-password-1"), mmtel}},
		hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:other@" + realm}, Password: "p"},
	)
	got := allRepositoryData(t, b, impi)
	if want := []string{"MMTEL-Services", "Ut-Digest-Credential"}; !slices.Equal(indications(got), want) {
		t.Fatalf("AllRepositoryData = %v, want %v", indications(got), want)
	}
	if string(got[0].ServiceData.XML) != "<a/>" || got[0].SequenceNumber != 0 {
		t.Errorf("the MMTelServices data = %+v", got[0])
	}
	// What an Sh update wrote comes back with the Sequence Number it gave.
	next := mmtel
	next.SequenceNumber, next.ServiceData = 1, &sh.ServiceData{XML: []byte("<b/>")}
	if !compareAndSwapRepositoryData(t, a, impi, &mmtel, next) {
		t.Fatal("the update of the MMTelServices data was refused")
	}
	if got := allRepositoryData(t, b, impi); len(got) != 2 || string(got[0].ServiceData.XML) != "<b/>" || got[0].SequenceNumber != 1 {
		t.Errorf("after an update, AllRepositoryData = %+v", got)
	}
	if got := allRepositoryData(t, b, otherIMPI); len(got) != 0 {
		t.Errorf("a subscription provisioned with no repository data has %+v", got)
	}
	if got := allRepositoryData(t, b, "nobody@"+realm); len(got) != 0 {
		t.Errorf("a private identity of no subscription has %+v", got)
	}
}

func indications(data []sh.TransparentData) []string {
	names := make([]string, 0, len(data))
	for _, d := range data {
		names = append(names, d.ServiceIndication)
	}
	return names
}

func hssKeepsSQNOutOfPut(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: akaSecret}})
	stale, _ := subscriber(t, b, impi)
	if !compareAndSwapSQN(t, a, impi, 0, 5) {
		t.Fatal("CompareAndSwapSQN from the stored SQN failed")
	}
	if compareAndSwapSQN(t, b, impi, 0, 6) {
		t.Fatal("CompareAndSwapSQN from a stale SQN succeeded")
	}
	stale.State = hss.Registered
	stale.AKA.SQN = 0
	put(t, b, stale)
	if u, _ := subscriber(t, a, impi); u.State != hss.Registered || u.AKA.SQN != 5 || u.AKA.Secret != akaSecret {
		t.Errorf("after Put: state %v, SQN %d; want Registered and SQN 5", u.State, u.AKA.SQN)
	}
	if compareAndSwapSQN(t, a, "nobody@"+realm, 0, 1) {
		t.Error("CompareAndSwapSQN for an unknown subscriber succeeded")
	}
}

func hssCompareAndSwapRepositoryData(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	v1 := sh.TransparentData{ServiceIndication: "MMTEL-Services", SequenceNumber: 0, ServiceData: &sh.ServiceData{XML: []byte("<v1/>")}}
	if !compareAndSwapRepositoryData(t, a, impi, nil, v1) {
		t.Fatal("the first data was refused")
	}
	if compareAndSwapRepositoryData(t, b, impi, nil, v1) {
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
		if compareAndSwapRepositoryData(t, s, impi, &v1, v2) {
			wins.Add(1)
		}
	})
	if got := wins.Load(); got != 1 {
		t.Errorf("%d updates from one version succeeded, want 1", got)
	}
	if got, _ := repositoryData(t, a, impi, "MMTEL-Services"); got.SequenceNumber != 1 || string(got.ServiceData.XML) != "<v2/>" {
		t.Errorf("RepositoryData = %+v", got)
	}
	// Data without ServiceData removes it.
	if !compareAndSwapRepositoryData(t, b, impi, &v2, sh.TransparentData{ServiceIndication: "MMTEL-Services", SequenceNumber: 2}) {
		t.Fatal("the removal was refused")
	}
	if _, ok := repositoryData(t, a, impi, "MMTEL-Services"); ok {
		t.Error("RepositoryData after its removal")
	}
	if compareAndSwapRepositoryData(t, a, "nobody@"+realm, nil, v1) {
		t.Error("CompareAndSwapRepositoryData for an unknown subscriber succeeded")
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
				sub, ok := subscriber(t, s, impi)
				if !ok {
					t.Error("the subscriber is gone")
					return
				}
				next := sub.AKA.SQN.Next()
				if compareAndSwapSQN(t, s, impi, sub.AKA.SQN, next) {
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
	if u, _ := subscriber(t, a, impi); u.AKA.SQN != want {
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
			if add(t, s, hss.Subscription{PrivateIdentity: id, PublicIdentities: []string{"sip:" + id, shared}, Password: "p"}) == nil {
				added.Add(1)
			}
		})
		if got := added.Load(); got != 1 {
			t.Fatalf("%d subscriptions with %s were added, want 1", got, shared)
		}
		if _, ok := subscriberOf(t, a, shared); !ok {
			t.Errorf("SubscriberOf(%s) finds nothing", shared)
		}
	}
}

// counts reads the numbers of TS 32.409 4.2.1 and reports whether the
// store keeps them.
func counts(t *testing.T, s hss.Store) (hss.Counts, bool) {
	t.Helper()
	c, kept, err := s.Counts(context.Background())
	if err != nil {
		t.Errorf("Counts: %v", err)
	}
	return c, kept
}

// A store that keeps the numbers of TS 32.409 4.2.1 follows what it
// holds: Add raises the provisioned numbers, a Put that registers a
// subscriber raises the registered ones, and Delete takes both away. A
// store that keeps none says so and gives no numbers, which the HSS
// reads as having no database usage measurement to serve.
func hssCountsFollowWhatIsStored(t *testing.T, a, b hss.Store) {
	if _, kept := counts(t, a); !kept {
		if c, _ := counts(t, b); c != (hss.Counts{}) {
			t.Errorf("Counts = %+v for a store that keeps none, want the zero value", c)
		}
		return
	}
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	want := hss.Counts{Subscriptions: 1, SIPPublicIdentities: 1, TelPublicIdentities: 1}
	if got, _ := counts(t, b); got != want {
		t.Errorf("after Add, Counts = %+v, want %+v", got, want)
	}

	sub, _ := subscriber(t, a, impi)
	sub.State = hss.Registered
	put(t, a, sub)
	want.RegisteredPublicIdentities, want.RegisteredPrivateIdentities = 2, 1
	if got, _ := counts(t, b); got != want {
		t.Errorf("after a registration, Counts = %+v, want %+v", got, want)
	}
	// A second Put of a subscriber that is registered already counts it
	// once.
	put(t, a, sub)
	if got, _ := counts(t, b); got != want {
		t.Errorf("after a second Put, Counts = %+v, want %+v", got, want)
	}

	sub.State = hss.NotRegistered
	put(t, a, sub)
	want.RegisteredPublicIdentities, want.RegisteredPrivateIdentities = 0, 0
	if got, _ := counts(t, b); got != want {
		t.Errorf("after a deregistration, Counts = %+v, want %+v", got, want)
	}

	// Deleting a registered subscription takes it out of both.
	sub.State = hss.Registered
	put(t, a, sub)
	if _, ok := deleteSubscriber(t, b, impi); !ok {
		t.Fatal("Delete found no subscriber")
	}
	if got, _ := counts(t, a); got != (hss.Counts{}) {
		t.Errorf("after Delete, Counts = %+v, want the zero value", got)
	}
}
