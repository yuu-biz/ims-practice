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
	"github.com/yuu-biz/ims-practice/internal/diameter"
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
		{"ServedIndexFollowsTheRegistrationState", hssServedIndexFollowsTheState},
		{"ServedIndexFollowsTheProfileSource", hssServedIndexFollowsTheProfileSource},
		{"ServedIndexIsOrderedAndPaged", hssServedIndexIsPaged},
		{"StoresTheProvisionedRepositoryData", hssStoresRepositoryData},
		{"ReadsAllTheRepositoryDataOfASubscription", hssReadsAllRepositoryData},
		{"KeepsSQNOutOfPut", hssKeepsSQNOutOfPut},
		{"ReplaceWritesTheWholeSubscription", hssReplace},
		{"ReplaceRefusesAnIdentityOfAnotherSubscription", hssReplaceRefusesTakenIdentity},
		{"CompareAndSwapRepositoryData", hssCompareAndSwapRepositoryData},
		{"ConcurrentSQN", hssConcurrentSQN},
		{"ConcurrentAddsOfOneIdentity", hssConcurrentAdds},
		{"ConcurrentReplacesOfOneIdentity", hssConcurrentReplaces},
		{"ConcurrentReplaceAndSQN", hssConcurrentReplaceAndSQN},
		{"CountsFollowWhatIsStored", hssCountsFollowWhatIsStored},
		{"Settings", hssSettings},
		{"ConcurrentSettings", hssConcurrentSettings},
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

func replace(t *testing.T, s hss.Store, sub hss.Subscriber) error {
	t.Helper()
	err := s.Replace(context.Background(), sub)
	if err != nil && !errors.Is(err, hss.ErrIdentityTaken) {
		t.Errorf("Replace(%s): %v", sub.PrivateIdentity, err)
	}
	return err
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

// servedPage is one page of the private identities in the index of the
// served subscribers of source.
func servedPage(t *testing.T, s hss.Store, source hss.ProfileSource, after string, limit int) ([]string, string) {
	t.Helper()
	ids, next, err := s.ListServed(context.Background(), source, after, limit)
	if err != nil {
		t.Errorf("ListServed(%s, %q, %d): %v", source, after, limit, err)
	}
	return ids, next
}

// listServed is every private identity in the index of source, in one
// page large enough for the subscribers of these tests.
func listServed(t *testing.T, s hss.Store, source hss.ProfileSource) []string {
	t.Helper()
	ids, _ := servedPage(t, s, source, "", 100)
	return ids
}

// servedSubscriber is sub in a state in which an S-CSCF has its user
// profile: the state and the name and Diameter identity of that S-CSCF,
// as a SAR stores them (hss.ServedSource).
func servedSubscriber(sub hss.Subscriber, state hss.RegistrationState) hss.Subscriber {
	sub.State, sub.ServerName = state, "sip:scscf."+realm
	sub.SCSCF = diameter.Identity{OriginHost: "scscf." + realm, OriginRealm: realm}
	return sub
}

// servedOnly checks that the index of source holds impi and that no
// other index holds anything.
func servedOnly(t *testing.T, s hss.Store, source hss.ProfileSource) {
	t.Helper()
	for _, each := range hss.ProfileSources {
		got := listServed(t, s, each)
		switch {
		case each == source && !slices.Equal(got, []string{impi}):
			t.Errorf("the served subscribers of %s = %v, want the subscriber", each, got)
		case each != source && len(got) != 0:
			t.Errorf("the served subscribers of %s = %v, want none", each, got)
		}
	}
}

// hssServedIndexFollowsTheState: the index of the served subscribers
// holds a subscriber from the moment an S-CSCF has its user profile
// until it has it no longer, and either instance reads what the other
// wrote. Those are the subscribers whose profile the HSS pushes when a
// setting names another one (TS 29.228 6.6.1).
func hssServedIndexFollowsTheState(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if got := listServed(t, b, hss.ProfileOfOtherUsers); len(got) != 0 {
		t.Errorf("a subscription that was only added is already served: %v", got)
	}
	plain, _ := subscriber(t, b, impi)
	// Registered, and the unregistered state in which an S-CSCF keeps
	// the profile of a user that is not registered, are the two states
	// in which it has the profile.
	for _, state := range []hss.RegistrationState{hss.Registered, hss.Unregistered} {
		put(t, b, servedSubscriber(plain, state))
		if got := listServed(t, a, hss.ProfileOfOtherUsers); !slices.Equal(got, []string{impi}) {
			t.Errorf("the served subscribers of a user in the state %v = %v, want the subscriber", state, got)
		}
	}
	// A state without an S-CSCF of its own: nothing holds the profile,
	// so nothing is to be pushed either.
	unregistered := plain
	unregistered.State = hss.Unregistered
	put(t, a, unregistered)
	if got := listServed(t, b, hss.ProfileOfOtherUsers); len(got) != 0 {
		t.Errorf("a subscriber in the unregistered state with no S-CSCF is served: %v", got)
	}
	put(t, b, servedSubscriber(plain, hss.Registered))
	put(t, a, plain)
	if got := listServed(t, b, hss.ProfileOfOtherUsers); len(got) != 0 {
		t.Errorf("a subscriber that was deregistered is still served: %v", got)
	}
	// No subscription takes its profile name from nowhere.
	if got := listServed(t, a, ""); len(got) != 0 {
		t.Errorf("the empty source has %v", got)
	}
}

// hssServedIndexFollowsTheProfileSource: the mark and the service
// profile name of a subscription say which setting names its profile,
// so a write that changes either moves the subscriber to the index of
// its new source and leaves it in that one alone. The provisioning API
// changes the mark with a Put and the whole subscription with a
// Replace.
func hssServedIndexFollowsTheProfileSource(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	plain, _ := subscriber(t, b, impi)
	put(t, a, servedSubscriber(plain, hss.Registered))
	servedOnly(t, b, hss.ProfileOfOtherUsers)

	marked, _ := subscriber(t, b, impi)
	marked.TestUser = true
	put(t, b, marked)
	servedOnly(t, a, hss.ProfileOfTestUsers)

	named, _ := subscriber(t, a, impi)
	named.ServiceProfile = "trial"
	if err := replace(t, a, named); err != nil {
		t.Fatalf("Replace with a service profile name: %v", err)
	}
	servedOnly(t, b, hss.ProfileOfSubscription)

	named.ServiceProfile = ""
	if err := replace(t, b, named); err != nil {
		t.Fatalf("Replace without a service profile name: %v", err)
	}
	servedOnly(t, a, hss.ProfileOfTestUsers)

	deleteSubscriber(t, a, impi)
	for _, source := range hss.ProfileSources {
		if got := listServed(t, b, source); len(got) != 0 {
			t.Errorf("the served subscribers of %s after the Delete = %v, want none", source, got)
		}
	}
}

// hssServedIndexIsPaged: the served subscribers come in the pages of a
// listing, in the order of their private identities, each page after
// the cursor of the one before it, so that a change of a setting reads
// them a page at a time (hss.Store.ListServed).
func hssServedIndexIsPaged(t *testing.T, a, b hss.Store) {
	for _, name := range []string{"c", "a", "b"} {
		id := name + "@" + realm
		mustAdd(t, a, hss.Subscription{PrivateIdentity: id, PublicIdentities: []string{"sip:" + name + "@" + realm}, Password: "p"})
		sub, _ := subscriber(t, a, id)
		put(t, a, servedSubscriber(sub, hss.Registered))
	}
	got, next := servedPage(t, b, hss.ProfileOfOtherUsers, "", 2)
	if want := []string{"a@" + realm, "b@" + realm}; !slices.Equal(got, want) {
		t.Errorf("the first page of two = %v, want %v", got, want)
	}
	if next == "" {
		t.Fatal("a full page has no next cursor")
	}
	got, next = servedPage(t, b, hss.ProfileOfOtherUsers, next, 2)
	if want := []string{"c@" + realm}; !slices.Equal(got, want) {
		t.Errorf("the second page = %v, want %v", got, want)
	}
	if next != "" {
		t.Errorf("the last page has a next cursor %q", next)
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

// Replace writes a whole subscription, which the provisioning API does
// for a user whose credentials or identities change: the caller reads
// the subscriber, puts the new subscription in its place and leaves the
// state of the running subscriber alone. The store keeps SQN_HE
// whatever the call carries, keeps the repository data, and moves the
// index with the public identities.
func hssReplace(t *testing.T, a, b hss.Store) {
	data := sh.TransparentData{ServiceIndication: "MMTEL-Services", ServiceData: &sh.ServiceData{XML: []byte("<a/>")}}
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p",
		AKA: &hss.AKACredentials{Secret: akaSecret}, RepositoryData: []sh.TransparentData{data}})
	if !compareAndSwapSQN(t, a, impi, 0, 9) {
		t.Fatal("CompareAndSwapSQN from the stored SQN failed")
	}
	registered, _ := subscriber(t, b, impi)
	registered.State, registered.ServerName = hss.Registered, "sip:scscf."+realm
	put(t, b, registered)

	next, _ := subscriber(t, a, impi)
	const moved = "tel:+12025550199"
	next.Subscription = hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, moved},
		BarredIdentities: []string{impu}, Password: "q", TestUser: true, ServiceProfile: "trial",
		AKA: &hss.AKACredentials{Secret: akaSecret, AMF: aka.AMF{0x80, 0x00}, SQN: 1}}
	if err := replace(t, a, next); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, ok := subscriber(t, b, impi)
	if !ok || got.Password != "q" || !got.TestUser || got.ServiceProfile != "trial" || got.AKA.AMF != (aka.AMF{0x80, 0x00}) {
		t.Errorf("after Replace: %+v, %v", got.Subscription, ok)
	}
	if !got.Barred(impu) {
		t.Errorf("the barred identities are %v", got.BarredIdentities)
	}
	if got.AKA.SQN != 9 {
		t.Errorf("SQN_HE = %d after a Replace that carried 1, want the stored 9", got.AKA.SQN)
	}
	if got.State != hss.Registered || got.ServerName != "sip:scscf."+realm {
		t.Errorf("the state of the running subscriber: %v, %q", got.State, got.ServerName)
	}
	if d := allRepositoryData(t, b, impi); len(d) != 1 || string(d[0].ServiceData.XML) != "<a/>" {
		t.Errorf("the repository data after Replace: %+v", d)
	}
	if _, ok := subscriberOf(t, b, moved); !ok {
		t.Errorf("SubscriberOf(%s) finds nothing", moved)
	}
	if _, ok := subscriberOf(t, b, tel); ok {
		t.Errorf("the telephone number %s that the subscription let go still finds it", tel)
	}
	// The identity it let go is free for another subscription.
	if err := add(t, b, hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{tel}, Password: "p"}); err != nil {
		t.Errorf("Add of the identity that the Replace let go: %v", err)
	}
	if c, kept := counts(t, a); kept {
		want := hss.Counts{Subscriptions: 2, SIPPublicIdentities: 1, TelPublicIdentities: 2, RegisteredPublicIdentities: 2, RegisteredPrivateIdentities: 1}
		if c != want {
			t.Errorf("Counts = %+v, want %+v", c, want)
		}
	}
}

// A Replace that would take a public identity of another subscription
// writes nothing, and one of a subscriber that is gone brings it back no
// more than a Put does.
func hssReplaceRefusesTakenIdentity(t *testing.T, a, b hss.Store) {
	mustAdd(t, a,
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"},
		hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:other@" + realm, tel}, Password: "p"},
	)
	// The new identity before the taken one is claimed first, so the
	// refusal has something to take back.
	free := "sip:new@" + realm
	next, _ := subscriber(t, b, impi)
	next.PublicIdentities = []string{impu, free, tel}
	next.Password = "q"
	if err := replace(t, b, next); !errors.Is(err, hss.ErrIdentityTaken) {
		t.Fatalf("Replace with the telephone number of another subscription = %v, want ErrIdentityTaken", err)
	}
	if owner, _ := subscriberOf(t, a, tel); owner.PrivateIdentity != otherIMPI {
		t.Errorf("%s now belongs to %q", tel, owner.PrivateIdentity)
	}
	if got, _ := subscriber(t, a, impi); got.Password != "p" || len(got.PublicIdentities) != 1 {
		t.Errorf("the refused Replace wrote %+v", got.Subscription)
	}
	if _, ok := subscriberOf(t, a, free); ok {
		t.Errorf("%s finds the subscription of the refused Replace", free)
	}
	if err := add(t, a, hss.Subscription{PrivateIdentity: "001010000000003@" + realm, PublicIdentities: []string{free}, Password: "p"}); err != nil {
		t.Errorf("Add of the identity that the refused Replace named: %v", err)
	}

	if _, ok := deleteSubscriber(t, a, impi); !ok {
		t.Fatal("Delete found no subscriber")
	}
	next.PublicIdentities = []string{impu}
	if err := replace(t, b, next); err != nil {
		t.Fatalf("Replace of a subscriber that is gone: %v", err)
	}
	if _, ok := subscriber(t, a, impi); ok {
		t.Error("Replace brought back a deleted subscriber")
	}
	if _, ok := subscriberOf(t, a, impu); ok {
		t.Error("Replace of a subscriber that is gone wrote the index of its public identity")
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

// hssSettings has one instance store the settings of its configuration
// file in an empty store and a change replace them, and the other read
// each: a swap from what is no longer there, or of a first setting where
// one is there already, stores nothing.
func hssSettings(t *testing.T, a, b hss.Store) {
	ctx := context.Background()
	if _, ok, err := b.Settings(ctx); err != nil || ok {
		t.Fatalf("Settings of an empty store = %v, %v, want none", ok, err)
	}
	file := hss.StoredSettings{Admission: hss.AdmitAll, DefaultServiceProfile: "default", TestUserServiceProfile: "default"}
	changed := file
	changed.Generation, changed.Admission = 1, hss.AdmitTestUsers
	for _, tc := range []struct {
		name      string
		store     hss.Store
		old       *hss.StoredSettings
		next      hss.StoredSettings
		swapped   bool
		afterward hss.StoredSettings
	}{
		{"those of the file into the empty store", a, nil, file, true, file},
		{"another first setting", b, nil, changed, false, file},
		{"a change", b, &file, changed, true, changed},
		{"a change from what is gone", a, &file, file, false, changed},
	} {
		swapped, err := tc.store.CompareAndSwapSettings(ctx, tc.old, tc.next)
		if err != nil || swapped != tc.swapped {
			t.Errorf("%s: CompareAndSwapSettings = %v, %v, want %v", tc.name, swapped, err, tc.swapped)
		}
		for _, s := range []hss.Store{a, b} {
			if got, ok, err := s.Settings(ctx); err != nil || !ok || got != tc.afterward {
				t.Errorf("%s: Settings = %+v, %v, %v, want %+v", tc.name, got, ok, err, tc.afterward)
			}
		}
	}
}

// hssConcurrentSettings has two instances change the settings at once,
// each from what it read: every change is one step on top of the last,
// so the generations they write follow each other with none lost.
func hssConcurrentSettings(t *testing.T, a, b hss.Store) {
	ctx := context.Background()
	const n = 20
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		for range n {
			for {
				stored, ok, err := s.Settings(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				var old *hss.StoredSettings
				if ok {
					old = &stored
				}
				next := hss.StoredSettings{Generation: stored.Generation + 1, Admission: hss.AdmitAll}
				swapped, err := s.CompareAndSwapSettings(ctx, old, next)
				if err != nil {
					t.Error(err)
					return
				}
				if swapped {
					break
				}
			}
		}
	})
	if got, _, err := a.Settings(ctx); err != nil || got.Generation != 2*n {
		t.Errorf("the settings are at generation %d after %d changes, %v", got.Generation, 2*n, err)
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

// hssConcurrentReplaceAndSQN has the provisioning API of one instance
// replace a subscription over and over while another instance hands out
// authentication vectors for it. Each Replace carries the credentials
// with the sqn that the operator provisioned, the start condition of TS
// 33.102 C.3.2, so a Replace that wrote it would take SQN_HE back and
// let a vector be used twice. SQN_HE belongs to CompareAndSwapSQN
// alone.
func hssConcurrentReplaceAndSQN(t *testing.T, a, b hss.Store) {
	mustAdd(t, a, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: akaSecret}})
	const n = 20
	var mu sync.Mutex
	used := map[aka.SQN]bool{}
	parallel(2, func(i int) {
		if i == 0 {
			for range n {
				for {
					sub, ok := subscriber(t, a, impi)
					if !ok {
						t.Error("the subscriber is gone")
						return
					}
					next := sub.AKA.SQN.Next()
					if !compareAndSwapSQN(t, a, impi, sub.AKA.SQN, next) {
						continue
					}
					mu.Lock()
					if used[next] {
						t.Errorf("SQN %d used twice", next)
					}
					used[next] = true
					mu.Unlock()
					break
				}
			}
			return
		}
		for j := range n {
			sub, ok := subscriber(t, b, impi)
			if !ok {
				t.Error("the subscriber is gone")
				return
			}
			sub.Password = fmt.Sprintf("test-password-%d", j)
			sub.AKA = &hss.AKACredentials{Secret: akaSecret}
			if err := replace(t, b, sub); err != nil {
				t.Errorf("Replace: %v", err)
				return
			}
		}
	})
	want := aka.SQN(0)
	for range n {
		want = want.Next()
	}
	got, _ := subscriber(t, a, impi)
	if got.AKA.SQN != want {
		t.Errorf("SQN_HE = %d after %d vectors and %d replacements, want %d", got.AKA.SQN, n, n, want)
	}
	if got.AKA.Secret != akaSecret || got.Password == "" {
		t.Errorf("the credentials after the replacements: %+v", got.Subscription)
	}
}

// hssConcurrentReplaces has the provisioning API of two instances give
// the same new public identity to two subscriptions at once: one of them
// gets it, the other is told that it is taken, and the one that lost
// does not keep the identity among its own. A subscription that named
// an identity the index gives to another would answer for an identity
// that is not its, which is what ErrIdentityTaken is there to stop.
func hssConcurrentReplaces(t *testing.T, a, b hss.Store) {
	for round := range 5 {
		shared := fmt.Sprintf("tel:+1202555012%d", round)
		ids := [2]string{fmt.Sprintf("00101000001%d0@%s", round, realm), fmt.Sprintf("00101000001%d1@%s", round, realm)}
		for _, id := range ids {
			mustAdd(t, a, hss.Subscription{PrivateIdentity: id, PublicIdentities: []string{"sip:" + id}, Password: "p"})
		}
		var took atomic.Int32
		parallel(2, func(i int) {
			s := a
			if i == 1 {
				s = b
			}
			sub, ok := subscriber(t, s, ids[i])
			if !ok {
				t.Errorf("the subscriber %s is gone", ids[i])
				return
			}
			sub.PublicIdentities = []string{"sip:" + ids[i], shared}
			if replace(t, s, sub) == nil {
				took.Add(1)
			}
		})
		if got := took.Load(); got != 1 {
			t.Fatalf("%d of the two subscriptions took %s, want 1", got, shared)
		}
		owner, ok := subscriberOf(t, a, shared)
		if !ok {
			t.Fatalf("SubscriberOf(%s) finds nothing", shared)
		}
		for _, id := range ids {
			sub, ok := subscriber(t, a, id)
			if !ok {
				t.Fatalf("the subscriber %s is gone", id)
			}
			names := slices.Contains(sub.PublicIdentities, shared)
			if names != (id == owner.PrivateIdentity) {
				t.Fatalf("%s has the public identities %v, and the index gives %s to %s",
					id, sub.PublicIdentities, shared, owner.PrivateIdentity)
			}
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
