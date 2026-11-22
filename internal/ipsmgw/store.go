package ipsmgw

import (
	"context"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// PendingDelivery is a short message, or a status report, that the
// IP-SM-GW has sent to a UE and whose delivery report it waits for (TS
// 24.341 5.3.3.4.1, TS 23.204 6.5).
type PendingDelivery struct {
	// CallID is the Call-ID of the MESSAGE that carried the RP-DATA. The
	// delivery report names it in In-Reply-To (TS 24.341 5.3.2.4 item d).
	CallID string
	// Reference is the SC's name for the delivery, which goes back to the
	// SC with the outcome.
	Reference string
	// Recipient is the public user identity the MESSAGE went to.
	Recipient string
	// MessageReference is the RP-MR of the RP-DATA, which the RP-ACK or
	// RP-ERROR of the UE repeats (TS 24.011 8.2.3).
	MessageReference uint8
	// ReportDeadline is when TR1N runs out (TS 24.011 6.2.2.2): the
	// delivery ends then if no report has ended it before.
	ReportDeadline time.Time
	// Expires is when the Store forgets the delivery.
	Expires time.Time
}

// RegSubscription is the IP-SM-GW's subscription to the reg event
// package of one user (TS 24.341 5.3.3.2, TS 24.229 5.7.1.1). Its
// NOTIFYs tell whether the user has a contact that takes short
// messages, and the IP-SM-GW passes each change on to the HSS.
type RegSubscription struct {
	// PublicIdentity is the resource: the public user identity in the To
	// of the third-party REGISTER.
	PublicIdentity string
	// MSISDN is the number in the service-info of the third-party
	// REGISTER, which TS 24.341 5.3.3.2 has the IP-SM-GW store. It names
	// the user in the PUR; empty, the PUR names the user by
	// PublicIdentity.
	MSISDN string
	// Dialog is the dialog of the subscription. Until the first NOTIFY
	// it is the half that sip.NewSubscriberDialog gives, without a remote
	// tag.
	Dialog sip.Dialog
	// ICID is the icid-value of the first SUBSCRIBE, which every later
	// request of the dialog repeats (TS 24.229 5.7.3).
	ICID string
	// Available is what the HSS was last told: true when the IP-SM-GW has
	// registered itself for the user's short messages.
	Available bool
	// Expires is when the subscription ends unless a refresh comes
	// first. The Store forgets it then.
	Expires time.Time
}

// Store keeps what the IP-SM-GW needs after a SIP transaction has
// ended, so that whichever IP-SM-GW of the network a request reaches
// can handle it. The delivery report in
// particular comes in a MESSAGE of its own, which DNS may lead to
// another IP-SM-GW than the one that sent the short message, and a
// NOTIFY of a reg event subscription may reach another IP-SM-GW than
// the one that sent the SUBSCRIBE. MemoryStore keeps the state in the
// process.
//
// An error is a failure of the store, after which the caller cannot
// tell what the store holds. The IP-SM-GW writes every expiry into what
// it stores, as a time; the Store keeps nothing longer than that.
type Store interface {
	// NextMessageReference gives the RP-MR of the next RP-DATA to a
	// recipient, and keeps the counter of the recipient until expires.
	// TS 24.011 8.2.3 counts 0 to 255; the values only need to differ
	// while a delivery to the same UE waits for its report.
	NextMessageReference(ctx context.Context, recipient string, expires time.Time) (uint8, error)
	// PutDelivery keeps d until its Expires.
	PutDelivery(ctx context.Context, d PendingDelivery) error
	// Delivery returns the delivery whose MESSAGE had callID, and
	// whether there is one.
	Delivery(ctx context.Context, callID string) (PendingDelivery, bool, error)
	// TakeDelivery returns the delivery and removes it, in one step. The
	// delivery report, a failed MESSAGE and the timer of the sending
	// IP-SM-GW may all try to end one delivery; only the first gets it,
	// so the SC hears the outcome once.
	TakeDelivery(ctx context.Context, callID string) (PendingDelivery, bool, error)

	// PutRegSubscription keeps sub until its Expires, in place of the
	// one with the same Call-ID and of any other one to the same public
	// identity.
	PutRegSubscription(ctx context.Context, sub RegSubscription) error
	// RegSubscription returns the subscription whose dialog has callID,
	// and whether there is one.
	RegSubscription(ctx context.Context, callID string) (RegSubscription, bool, error)
	// RegSubscriptionOf returns the subscription to a public identity,
	// and whether there is one.
	RegSubscriptionOf(ctx context.Context, publicID string) (RegSubscription, bool, error)
	// DeleteRegSubscription removes the subscription whose dialog has
	// callID.
	DeleteRegSubscription(ctx context.Context, callID string) error
}

type messageReferences struct {
	next    uint8
	expires time.Time
}

// MemoryStore is a Store in the memory of one process. Two IP-SM-GWs
// share state only when they share one MemoryStore, as in a test; a
// network of several nodes needs a store outside them. It never fails.
type MemoryStore struct {
	mu                sync.Mutex
	deliveries        map[string]PendingDelivery
	messageReferences map[string]messageReferences
	// regSubscriptions are keyed by Call-ID, and regSubscriptionOf gives
	// the Call-ID for a public identity, so that neither lookup scans.
	regSubscriptions  map[string]RegSubscription
	regSubscriptionOf map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{deliveries: map[string]PendingDelivery{}, messageReferences: map[string]messageReferences{},
		regSubscriptions: map[string]RegSubscription{}, regSubscriptionOf: map[string]string{}}
}

// NextMessageReference counts on from the last value given to the
// recipient; after 255 comes 0 again. The counter is that of the public
// identity, so that two ways of writing one recipient share it. The
// store forgets the counter once it has expired, as an external store
// would with a time to live.
func (s *MemoryStore) NextMessageReference(_ context.Context, recipient string, expires time.Time) (uint8, error) {
	recipient = ims.PublicIdentityKey(recipient)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.messageReferences[recipient]
	if !ok || !time.Now().Before(r.expires) {
		r = messageReferences{}
	}
	mr := r.next
	s.messageReferences[recipient] = messageReferences{next: mr + 1, expires: expires}
	time.AfterFunc(time.Until(expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r, ok := s.messageReferences[recipient]; ok && !time.Now().Before(r.expires) {
			delete(s.messageReferences, recipient)
		}
	})
	return mr, nil
}

// PutDelivery keeps d. The store removes it at its Expires.
func (s *MemoryStore) PutDelivery(_ context.Context, d PendingDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveries[d.CallID] = d
	time.AfterFunc(time.Until(d.Expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if stored, ok := s.deliveries[d.CallID]; ok && !time.Now().Before(stored.Expires) {
			delete(s.deliveries, d.CallID)
		}
	})
	return nil
}

func (s *MemoryStore) Delivery(_ context.Context, callID string) (PendingDelivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.delivery(callID)
	return d, ok, nil
}

func (s *MemoryStore) TakeDelivery(_ context.Context, callID string) (PendingDelivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.delivery(callID)
	delete(s.deliveries, callID)
	return d, ok, nil
}

func (s *MemoryStore) delivery(callID string) (PendingDelivery, bool) {
	d, ok := s.deliveries[callID]
	if !ok || !time.Now().Before(d.Expires) {
		return PendingDelivery{}, false
	}
	return d, true
}

// PutRegSubscription keeps sub. The store removes it at its Expires.
func (s *MemoryStore) PutRegSubscription(_ context.Context, sub RegSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	callID, key := sub.Dialog.ID.CallID, ims.PublicIdentityKey(sub.PublicIdentity)
	sub.Dialog.RouteSet = append([]string(nil), sub.Dialog.RouteSet...)
	if old, ok := s.regSubscriptionOf[key]; ok && old != callID {
		delete(s.regSubscriptions, old)
	}
	s.regSubscriptions[callID] = sub
	s.regSubscriptionOf[key] = callID
	time.AfterFunc(time.Until(sub.Expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if stored, ok := s.regSubscriptions[callID]; ok && !time.Now().Before(stored.Expires) {
			s.deleteRegSubscription(callID)
		}
	})
	return nil
}

func (s *MemoryStore) RegSubscription(_ context.Context, callID string) (RegSubscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.regSubscription(callID)
	return sub, ok, nil
}

func (s *MemoryStore) RegSubscriptionOf(_ context.Context, publicID string) (RegSubscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	callID, ok := s.regSubscriptionOf[ims.PublicIdentityKey(publicID)]
	if !ok {
		return RegSubscription{}, false, nil
	}
	sub, ok := s.regSubscription(callID)
	return sub, ok, nil
}

func (s *MemoryStore) DeleteRegSubscription(_ context.Context, callID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteRegSubscription(callID)
	return nil
}

func (s *MemoryStore) regSubscription(callID string) (RegSubscription, bool) {
	sub, ok := s.regSubscriptions[callID]
	if !ok || !time.Now().Before(sub.Expires) {
		return RegSubscription{}, false
	}
	// The route set is a slice: the caller gets a copy of its own.
	sub.Dialog.RouteSet = append([]string(nil), sub.Dialog.RouteSet...)
	return sub, true
}

func (s *MemoryStore) deleteRegSubscription(callID string) {
	sub, ok := s.regSubscriptions[callID]
	if !ok {
		return
	}
	delete(s.regSubscriptions, callID)
	if key := ims.PublicIdentityKey(sub.PublicIdentity); s.regSubscriptionOf[key] == callID {
		delete(s.regSubscriptionOf, key)
	}
}
