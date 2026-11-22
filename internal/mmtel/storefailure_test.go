package mmtel_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mmtelstore"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the AS on a store outside the process that fails.
// The AS answers a request whose state it cannot read or write with 500
// (RFC 3261 21.5.1), and goes on with what it has already answered.

// reachableStore is a store outside the process that the AS reaches
// until down is set, and that then fails every operation, as a store
// that the AS can no longer reach does.
type reachableStore struct {
	kvstore.Store
	down atomic.Bool
}

var errUnreachable = errors.New("the store cannot be reached")

func (s *reachableStore) Get(ctx context.Context, key string) (kvstore.Item, error) {
	if s.down.Load() {
		return kvstore.Item{}, errUnreachable
	}
	return s.Store.Get(ctx, key)
}

func (s *reachableStore) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	if s.down.Load() {
		return "", errUnreachable
	}
	return s.Store.Put(ctx, key, value, ttl)
}

func (s *reachableStore) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	if s.down.Load() {
		return "", errUnreachable
	}
	return s.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

func (s *reachableStore) Delete(ctx context.Context, key string) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.Delete(ctx, key)
}

func (s *reachableStore) CompareAndDelete(ctx context.Context, key, version string) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.CompareAndDelete(ctx, key, version)
}

// startASOnAStore starts an AS whose calls and conferences are in a
// store outside the process, and returns the store, which the test
// takes down when it wants.
func startASOnAStore(t *testing.T, cfg mmtel.Config) (testAS, *reachableStore) {
	t.Helper()
	kv := &reachableStore{Store: kvstore.NewMemoryStore(nil)}
	return startASOn(t, noServices{}, cfg, mmtelstore.New(kv, kvjson.Config{})), kv
}

// referStatuses are the status codes of the NOTIFYs of the REFER r,
// until the last one.
func referStatuses(t *testing.T, r *sip.Referral) []int {
	t.Helper()
	var got []int
	timeout := time.After(10 * time.Second)
	for {
		select {
		case s, ok := <-r.Statuses():
			if !ok {
				return got
			}
			got = append(got, s.StatusCode)
		case <-timeout:
			t.Fatalf("the REFER has had NOTIFYs %v, and no last one", got)
		}
	}
}

// TestAnINVITEWhoseReplacesCannotBeRead: the AS cannot read the store,
// so it cannot tell whether the dialog that the Replaces of an INVITE
// names is a leg of one of its calls (TS 24.229 5.7.5.1). It answers
// 500, and the INVITE goes no further.
func TestAnINVITEWhoseReplacesCannotBeRead(t *testing.T) {
	as, kv := startASOnAStore(t, mmtel.Config{})
	kv.down.Store(true)
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { calls <- c })
	focus := newUA(t, "focus", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "term", offer)
	replaces := sip.Replaces{CallID: "call-1@192.0.2.1", ToTag: "as-1", FromTag: "alice-1"}
	inv.Headers = append(inv.Headers, replaces.Header())
	out, err := focus.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); len(got) != 1 || got[0] != 500 {
		t.Errorf("INVITE with Replaces got %v, want 500", got)
	}
	select {
	case c := <-calls:
		t.Errorf("the INVITE went on to the callee with Replaces %q", c.Request.Get("Replaces"))
	default:
	}
}

// TestACallThatCannotBeStored: the store fails once the callee has
// answered. The caller has its 200 and the callee the ACK by then, so
// the call goes on in this instance, and each side's BYE reaches the
// other.
func TestACallThatCannotBeStored(t *testing.T) {
	as, kv := startASOnAStore(t, mmtel.Config{})
	kv.down.Store(true)
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		calls <- c
		c.Accept(answer)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); len(got) == 0 || got[len(got)-1] != 200 {
		t.Fatalf("caller got %v, want 200", got)
	}
	in := receive(t, calls, "call at the callee")
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	receive(t, in.Done(), "BYE at the callee")
}

// TestAConferenceThatCannotBeStored: the store fails as the creator's
// INVITE to the factory URI creates a conference. The focus does not
// host a conference that it cannot keep: the creator gets 500, and the
// focus releases the creator's media at the MRFC.
func TestAConferenceThatCannotBeStored(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as, kv := startASOnAStore(t, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	kv.down.Store(true)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	out, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); slices.Contains(got, 200) || got[len(got)-1] != 500 {
		t.Errorf("the creator got %v, want 500", got)
	}
	media := receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
	receive(t, media.Done(), "BYE of the creator's media at the MRFC")
}

// TestAJoinThatCannotBeStored: the store fails as a user dials in to a
// conference. The user gets 500, the focus releases the user's media
// at the MRFC, and the user takes no place in the conference: once the
// store is back, the same user joins.
func TestAJoinThatCannotBeStored(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as, kv := startASOnAStore(t, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	_, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	receive(t, mrfc.calls, "the creator's INVITE at the MRFC")

	kv.down.Store(true)
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, in)); slices.Contains(got, 200) || got[len(got)-1] != 500 {
		t.Errorf("carol got %v, want 500", got)
	}
	media := receive(t, mrfc.calls, "carol's INVITE at the MRFC")
	receive(t, media.Done(), "BYE of carol's media at the MRFC")

	kv.down.Store(false)
	again, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { again.Bye() })
	if got := statuses(readAll(t, again)); got[len(got)-1] != 200 {
		t.Errorf("carol got %v once the store was back, want 200", got)
	}
}

// TestAnInvitedUserThatCannotBeStored: the store fails while the user
// whom the creator referred to the focus is still to answer. The focus
// does not take in the user who then answers: it ends the user's
// session with a BYE, and the last NOTIFY of the REFER says 500.
func TestAnInvitedUserThatCannotBeStored(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as, kv := startASOnAStore(t, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	invited := make(chan *sip.IncomingCall, 1)
	conf, _, _ := aliceConference(t, as, func(c *sip.IncomingCall) { invited <- c })
	r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain})
	if err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	bob := receive(t, invited, "the focus's INVITE at bob")
	kv.down.Store(true)
	if _, err := bob.Accept(audio("41000", "sendrecv")); err != nil {
		t.Fatalf("bob's 200 got no ACK: %v", err)
	}
	receive(t, bob.Done(), "BYE of the focus at bob")
	if got := referStatuses(t, r); len(got) == 0 || got[len(got)-1] != 500 {
		t.Errorf("REFER got NOTIFYs %v, want the last with 500", got)
	}
}

// TestAReferWhoseReplacesCannotBeRead: the creator refers the focus to
// a user with a Replaces of its call with that user, and the store
// fails. The focus cannot tell whether the call is one that this AS
// relays, whose other leg the user would know it by, so it invites no
// one, and the last NOTIFY of the REFER says 500.
func TestAReferWhoseReplacesCannotBeRead(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as, kv := startASOnAStore(t, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	invited := make(chan *sip.IncomingCall, 1)
	conf, _, _ := aliceConference(t, as, func(c *sip.IncomingCall) { invited <- c })
	kv.down.Store(true)
	replaces := sip.Replaces{CallID: "call-1@192.0.2.1", ToTag: "as-1", FromTag: "alice-1"}
	r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain, Replaces: &replaces})
	if err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	if got := referStatuses(t, r); len(got) == 0 || got[len(got)-1] != 500 {
		t.Errorf("REFER got NOTIFYs %v, want the last with 500", got)
	}
	select {
	case c := <-invited:
		t.Errorf("the focus invited bob with Replaces %q", c.Request.Get("Replaces"))
	default:
	}
}

// TestAConferenceSubscriptionThatCannotBeStored: the store fails as a
// user subscribes to the conference. The focus does not answer 200 for
// a subscription that its record does not hold: the user gets 500 and
// no NOTIFY. Once the store is back, the same user subscribes. The
// store fails again as the user unsubscribes: the focus answers 500,
// and the subscription stays as it was, so the user gets no last
// NOTIFY until it unsubscribes once more with the store back (RFC 6665
// 4.1.2.2, 4.2.1.4).
func TestAConferenceSubscriptionThatCannotBeStored(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as, kv := startASOnAStore(t, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	_, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	subscribe := func() (*sip.Subscription, <-chan sip.Notification) {
		t.Helper()
		notifications := make(chan sip.Notification, 4)
		sub, err := carol.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:carol@" + domain, Event: confinfo.EventPackage,
			Accept: confinfo.ContentType, Expires: time.Hour,
			Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:carol@" + domain + ">"}},
			OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }})
		if err != nil {
			t.Fatal(err)
		}
		return sub, notifications
	}

	kv.down.Store(true)
	sub, notifications := subscribe()
	receive(t, sub.Done(), "end of the subscription that could not be stored")
	var refused *sip.SubscribeRefused
	if err := sub.Err(); !errors.As(err, &refused) || refused.StatusCode != 500 {
		t.Errorf("the subscription ended with %v, want a SUBSCRIBE refused with 500", err)
	}
	select {
	case n := <-notifications:
		t.Errorf("carol got a NOTIFY (%+v) for a subscription that was not stored", n.State)
	case <-time.After(200 * time.Millisecond):
	}

	kv.down.Store(false)
	sub, notifications = subscribe()
	if first := receive(t, notifications, "first NOTIFY once the store was back"); first.State.State != sip.SubscriptionActive {
		t.Errorf("first NOTIFY says %+v, want active", first.State)
	}

	kv.down.Store(true)
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-notifications:
		t.Errorf("carol got a NOTIFY (%+v) for an unsubscribe that was not stored", n.State)
	case <-time.After(300 * time.Millisecond):
	}
	kv.down.Store(false)
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	if last := receive(t, notifications, "last NOTIFY once the store was back"); last.State.State != sip.SubscriptionTerminated {
		t.Errorf("last NOTIFY says %+v, want terminated", last.State)
	}
	receive(t, sub.Done(), "end of the subscription")
}
