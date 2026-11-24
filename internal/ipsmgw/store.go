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
	// IMSI is the IMSI of the user, which the IP-SM-GW reads over Sh
	// before it registers itself for the user's short messages (TS
	// 29.328 7.6.24). A TFR names the recipient of a short message by it
	// (TS 29.338 Table 6.2.2.1/1), so the Store finds the subscription
	// by it as well. It is empty for a user whose HSS has none.
	IMSI string
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

// ReportTimeout is the name of the time index of TR1N (TS 24.011
// 6.2.2.2): the deliveries that wait for a delivery report, each due at
// its ReportDeadline. It is the one expiry of the IP-SM-GW that needs an
// action when its time comes, since the SC has to hear the outcome of
// every delivery; everything else the Store keeps only has to be
// forgotten. The Store hands out each delivery that is due to one
// instance at a time (ClaimDue), so that the deliveries of an instance
// that has stopped are ended by another one.
const ReportTimeout = "report-timeout"

// Expiry is one expiry that an instance has claimed: which delivery is
// due, and until when this instance alone acts on it.
type Expiry struct {
	// CallID is the Call-ID of the MESSAGE that carried the RP-DATA,
	// which names the delivery.
	CallID string
	// Until is when the claim runs out, after which another instance may
	// claim the same delivery. It also tells this claim from a later one
	// on the same delivery, so that the late Done of an instance that was
	// held up takes nothing away from the instance that has it now.
	Until time.Time
}

var _ ims.Expiry = Expiry{}

func (e Expiry) ClaimedUntil() time.Time { return e.Until }

func (e Expiry) LogFields() []any {
	return []any{"kind", ReportTimeout, "call_id", e.CallID}
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
	// PutDelivery keeps d until its Expires, and puts it in the index of
	// ReportTimeout, due at its ReportDeadline.
	PutDelivery(ctx context.Context, d PendingDelivery) error
	// Delivery returns the delivery whose MESSAGE had callID, and
	// whether there is one.
	Delivery(ctx context.Context, callID string) (PendingDelivery, bool, error)
	// TakeDelivery returns the delivery and removes it, in one step. The
	// delivery report, a failed MESSAGE and TR1N may all try to end one
	// delivery; only the first gets it, so the SC hears the outcome once.
	TakeDelivery(ctx context.Context, callID string) (PendingDelivery, bool, error)

	// ClaimDue claims the deliveries whose TR1N is due at now, at most
	// limit of them and the ones due longest first, each until now plus
	// lease. While a delivery is claimed it is not due, and no other
	// ClaimDue gives it out; when its claim runs out without Done, it is
	// due again, for whichever instance asks next.
	//
	// A claim is no proof that the delivery is still there: a report may
	// have ended it after it was due. The instance that claims it takes
	// it with TakeDelivery, which only one caller wins.
	ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Expiry, error)
	// Done takes the delivery of a claimed expiry out of the index, once
	// the caller has ended it. A claim that has ended in the meantime,
	// because the delivery was taken with its member or because the claim
	// ran out and another instance has it now, leaves the index as it is;
	// that is the ordinary end of an expiry, not a failure.
	Done(ctx context.Context, e Expiry) error

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
	// RegSubscriptionOfIMSI returns the subscription whose IMSI is imsi,
	// and whether there is one. It is how a TFR, which names the
	// recipient by IMSI, finds the user (sgd.go). An empty imsi finds
	// nothing: a subscription without an IMSI is in no such index.
	RegSubscriptionOfIMSI(ctx context.Context, imsi string) (RegSubscription, bool, error)
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
	// regSubscriptions are keyed by Call-ID, and regSubscriptionOf and
	// regSubscriptionOfIMSI give the Call-ID for a public identity and
	// for an IMSI, so that no lookup scans.
	regSubscriptions      map[string]RegSubscription
	regSubscriptionOf     map[string]string
	regSubscriptionOfIMSI map[string]string
	// reportTimeout is the time index of TR1N, as a store outside the
	// process holds one: the Call-ID of each delivery that waits for a
	// report.
	reportTimeout *ims.DueIndex[string]
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{deliveries: map[string]PendingDelivery{}, messageReferences: map[string]messageReferences{},
		regSubscriptions: map[string]RegSubscription{}, regSubscriptionOf: map[string]string{},
		regSubscriptionOfIMSI: map[string]string{},
		reportTimeout:         ims.NewDueIndex[string]()}
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

// PutDelivery keeps d and schedules its TR1N at its ReportDeadline. The
// store removes the delivery at its Expires, which comes later, so that
// the instance which claims the expiry still finds it.
func (s *MemoryStore) PutDelivery(_ context.Context, d PendingDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveries[d.CallID] = d
	s.reportTimeout.Set(d.CallID, d.ReportDeadline)
	time.AfterFunc(time.Until(d.Expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if stored, ok := s.deliveries[d.CallID]; ok && !time.Now().Before(stored.Expires) {
			delete(s.deliveries, d.CallID)
			// The expiry of a delivery that is gone goes with it, claimed
			// or not: nothing is left for an instance to end.
			s.reportTimeout.Remove(d.CallID)
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

// TakeDelivery takes the delivery and its TR1N. A Call-ID is never used
// again, so the expiry can go with it.
func (s *MemoryStore) TakeDelivery(_ context.Context, callID string) (PendingDelivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.delivery(callID)
	delete(s.deliveries, callID)
	s.reportTimeout.Remove(callID)
	return d, ok, nil
}

func (s *MemoryStore) ClaimDue(_ context.Context, now time.Time, lease time.Duration, limit int) ([]Expiry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var claimed []Expiry
	for _, due := range s.reportTimeout.ClaimDue(now, lease, limit) {
		claimed = append(claimed, Expiry{CallID: due.Member, Until: due.Until})
	}
	return claimed, nil
}

func (s *MemoryStore) Done(_ context.Context, e Expiry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reportTimeout.RemoveClaimed(e.CallID, e.Until)
	return nil
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
		s.deleteRegSubscription(old)
	}
	s.regSubscriptions[callID] = sub
	s.regSubscriptionOf[key] = callID
	if sub.IMSI != "" {
		s.regSubscriptionOfIMSI[sub.IMSI] = callID
	}
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

func (s *MemoryStore) RegSubscriptionOfIMSI(_ context.Context, imsi string) (RegSubscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	callID, ok := s.regSubscriptionOfIMSI[imsi]
	if !ok || imsi == "" {
		return RegSubscription{}, false, nil
	}
	sub, ok := s.regSubscription(callID)
	if !ok || sub.IMSI != imsi {
		// The subscription is gone, or it no longer holds this IMSI and
		// the index item has not caught up.
		return RegSubscription{}, false, nil
	}
	return sub, true, nil
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
	if sub.IMSI != "" && s.regSubscriptionOfIMSI[sub.IMSI] == callID {
		delete(s.regSubscriptionOfIMSI, sub.IMSI)
	}
}
