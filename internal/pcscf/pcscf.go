// Package pcscf is the P-CSCF role: the first SIP hop of the UE. On
// REGISTER it adds what the home network needs to reach the UE again,
// finds the I-CSCF of the home network through DNS, and remembers the
// registration once the S-CSCF accepts it (TS 24.229 5.2.2). It then
// subscribes to the registration state, to learn when the registration
// ends (5.2.3).
package pcscf

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Locator finds where to send a request for a SIP URI (RFC 3263).
// *sipdns.Resolver is one.
type Locator interface {
	Locate(ctx context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error)
}

type Config struct {
	// Host and Port name the P-CSCF in Path.
	Host string
	Port int
	// ProtectedServerPort is port_ps, the port where UEs that register
	// with IMS AKA send their protected messages (TS 33.203 7.1). Zero
	// means that the P-CSCF does not offer IMS AKA.
	ProtectedServerPort int
	// VisitedNetworkID goes into P-Visited-Network-ID.
	VisitedNetworkID string
	// HostAccess, when set, gives UEs behind the host's NAT the
	// published address of the relay.
	HostAccess *HostAccess
	// SweepInterval is how often this instance claims the expiries that
	// are due, and so how late one may be handled; zero means
	// ims.SweepInterval. Tests shorten it.
	SweepInterval time.Duration
	// ExpiryLease is how long a claimed expiry is this instance's alone,
	// after which another instance may claim it; zero means
	// ims.ExpiryLease. Tests shorten it.
	ExpiryLease time.Duration
}

// Association is the IP association of TS 24.229 5.2.2.3 for SIP Digest
// without TLS: after a successful registration, requests from this
// address and Via sent-by belong to this private identity. It also
// keeps what the P-CSCF learns from the 200 OK (5.2.2.1).
type Association struct {
	Source sip.Destination
	// Contact is the URI the UE registered. Requests to the UE carry it
	// in the Request-URI, but go to Source.
	Contact          string
	SentBy           string
	PrivateIdentity  string
	PublicIdentities []string // from P-Associated-URI, default first
	ServiceRoute     []string
	Expires          time.Time
	// KeepUntil is when the Store forgets the association:
	// associationGrace after Expires (endAssociation), also when the
	// P-CSCF never hears that the registration has ended, such as when
	// the S-CSCF stops before its NOTIFY. Zero keeps it until
	// DeleteAssociation.
	KeepUntil time.Time
	// SignallingSession is the policy session with which the PCRF
	// watches the signalling path of the UE (TS 29.214 4.4.5). It is
	// empty when the P-CSCF has no policy control, or the PCRF knows no
	// IP-CAN session for the UE; the calls of such a UE get no policy
	// control either.
	SignallingSession policy.SessionID
	// SignallingLost is true once the PCRF has reported that the bearer
	// of the UE's signalling is gone. Requests to the UE are then
	// refused, until the UE registers again (TS 24.229 5.2.6.4.3 item 1
	// with NOTE 1).
	SignallingLost bool
}

// RegSubscription is the P-CSCF's subscription to the reg event package
// of one user (TS 24.229 5.2.3). Its NOTIFYs tell when a registration
// ends, also one that the UE did not end itself, and the P-CSCF then
// forgets the UE (5.2.5.2).
//
// The subscription is in the Store, and the Contact of the SUBSCRIBE is
// the P-CSCF's own host and port, which a P-CSCF made again at the same
// address answers at: whichever instance a NOTIFY reaches takes it.
type RegSubscription struct {
	// PrivateIdentity is the user whose registration state it watches.
	// The subscription is made once per private identity (5.2.3 NOTE).
	PrivateIdentity string
	// PublicIdentity is the resource: the default public identity, the
	// first P-Associated-URI of the 200 to the REGISTER.
	PublicIdentity string
	// Dialog is the dialog of the subscription. Until the first NOTIFY
	// it is the half that sip.NewSubscriberDialog gives, without a remote
	// tag.
	Dialog sip.Dialog
	// Requested is the duration every SUBSCRIBE of this subscription asks
	// for, the initial one and each refresh (RFC 6665 4.1.2.2).
	Requested time.Duration
	// Granted is the Expires of the last 2xx to a SUBSCRIBE, which is the
	// duration the notifier gave (RFC 6665 3.1.1). It is the duration a
	// NOTIFY that carries no expires stands for (TS 24.229 5.1.1.3 item
	// 2); zero until a 2xx has said one.
	Granted time.Duration
	// NotifyDeadline is when Timer N runs out for the SUBSCRIBE that went
	// out last, the initial one or a refresh (RFC 6665 4.1.2.4). Every
	// SUBSCRIBE sets it, and the NOTIFY that follows clears it, so a
	// deadline that is set and past means that no NOTIFY has come to any
	// instance since that SUBSCRIBE.
	NotifyDeadline time.Time
	// RefreshAt is when the next refresh falls due (TS 24.229 5.2.3),
	// counted from the duration the last NOTIFY said the subscription has
	// (RFC 6665 4.1.3). It is the zero time while no refresh is to be
	// sent: before the first NOTIFY, after a refresh has gone out and
	// until the NOTIFY that answers it, and for a subscription too short
	// for a refresh to come before its end.
	RefreshAt time.Time
	// Expires is when the subscription ends unless a refresh comes
	// first. The Store forgets it then. Only a 2xx or a NOTIFY moves it:
	// a refresh that goes unanswered must not keep the subscription past
	// the time the notifier drops it.
	Expires time.Time
}

// ExpiryKind names one of the expiries that the P-CSCF has to act on
// when its time comes: each of them sends a SUBSCRIBE or forgets a
// subscription, so the item cannot simply go away by itself. The Store
// keeps the items of one kind in a time index of its own, named after
// the kind, and hands out each expiry that is due to one instance at a
// time (ClaimDue), so that the expiries of an instance that has stopped
// are handled by another one. The IP association and the SA table have
// no such expiry: an association that ends takes no more requests from
// the UE, and the Store forgets both at their time (endAssociation).
type ExpiryKind string

const (
	// RegEventNotifyExpiry is Timer N (RFC 6665 4.1.2.4): the NOTIFY that
	// must follow a SUBSCRIBE has not come, so the subscription never
	// came about or is gone at the notifier. Its item is the
	// subscription, named by the private identity, due at its
	// NotifyDeadline.
	RegEventNotifyExpiry ExpiryKind = "reg-event-notify"
	// RegEventRefreshExpiry is the refresh of a subscription falling due
	// (TS 24.229 5.2.3). Its item is the subscription, due at its
	// RefreshAt.
	RegEventRefreshExpiry ExpiryKind = "reg-event-refresh"
)

// ExpiryKinds are all the kinds, which one sweep goes through.
var ExpiryKinds = []ExpiryKind{RegEventNotifyExpiry, RegEventRefreshExpiry}

// Expiry is one expiry that an instance has claimed: which item is due,
// and until when this instance alone acts on it.
type Expiry struct {
	Kind ExpiryKind
	// PrivateIdentity names the subscription, in the form in which the
	// Store compares one.
	PrivateIdentity string
	// Until is when the claim runs out, after which another instance may
	// claim the same item. It also tells this claim from a later one on
	// the same item, so that the late Done of an instance that was held
	// up takes nothing away from the instance that has the item now.
	Until time.Time
}

var _ ims.Expiry = Expiry{}

func (e Expiry) ClaimedUntil() time.Time { return e.Until }

func (e Expiry) LogFields() []any {
	return []any{"kind", string(e.Kind), "private_identity", e.PrivateIdentity}
}

// Store keeps the associations and the dialogs. It is an interface so
// that the state can live outside the node; MemoryStore keeps
// it in the process.
//
// An error is a failure of the store, after which the caller cannot
// tell what the store holds. The P-CSCF writes every expiry into what
// it stores, as a time; the Store keeps nothing longer than that.
//
// A SIP transaction, an event of the PCRF and a timer carry no context
// of their own, so the P-CSCF passes context.Background() and relies on
// the time limit that an adapter on a store outside the process gives
// each operation.
type Store interface {
	// Association returns the association of a source, and whether
	// there is one.
	Association(ctx context.Context, source sip.Destination) (Association, bool, error)
	// AssociationOfContact finds the association whose registered
	// contact has this host, port and user.
	AssociationOfContact(ctx context.Context, contact string) (Association, bool, error)
	// PutAssociation keeps a, in place of the association of its
	// source, until its KeepUntil.
	PutAssociation(ctx context.Context, a Association) error
	DeleteAssociation(ctx context.Context, source sip.Destination) error
	// AssociationOfPolicySession finds the association whose
	// SignallingSession is session, for an event that the PCRF reports
	// about that session.
	AssociationOfPolicySession(ctx context.Context, session policy.SessionID) (Association, bool, error)
	// PutDialog stores a new dialog under its key.
	PutDialog(ctx context.Context, d Dialog) error
	// UpdateDialog changes the dialog of key with change, as one step
	// that no other change of that dialog comes between, and returns the
	// dialog as it is afterwards. It reports false, and does not call
	// change, when there is no such dialog: a dialog that has ended does
	// not come back. change must not block, and may run more than once,
	// each time on a fresh copy.
	UpdateDialog(ctx context.Context, key DialogKey, change func(*Dialog)) (Dialog, bool, error)
	// TakeDialog removes and returns the dialog of key. Only one caller
	// gets it, and that one ends what the dialog holds; this is what
	// makes the end of a dialog happen once, however many requests and
	// events ask for it.
	TakeDialog(ctx context.Context, key DialogKey) (Dialog, bool, error)
	// DialogOfPolicySession finds the dialog whose PolicySession is
	// session, for an event that the PCRF reports about that session.
	DialogOfPolicySession(ctx context.Context, session policy.SessionID) (Dialog, bool, error)
	// CreateRegSubscription keeps sub until its Expires, but only while
	// its private identity has no subscription that is still live. It
	// reports whether it wrote: false means that another instance
	// subscribed for this user first, and that this caller must not
	// subscribe. One subscription per private identity (TS 24.229 5.2.3
	// NOTE) rests on this.
	CreateRegSubscription(ctx context.Context, sub RegSubscription) (bool, error)
	// UpdateRegSubscription changes the subscription of a private
	// identity with change, as one step that no other change of that
	// subscription comes between, and returns it as it is afterwards. It
	// reports false, having written nothing, when there is no live
	// subscription or when change reports false, which is how a caller
	// stands down once it sees that the subscription has moved on.
	// change must not block, may run more than once, each time on a fresh
	// copy, and must leave the Call-ID of the dialog alone: another
	// subscription is a Delete and a Create.
	UpdateRegSubscription(ctx context.Context, privateID string, change func(*RegSubscription) bool) (RegSubscription, bool, error)
	// RegSubscription returns the subscription of a private identity,
	// and whether there is one.
	RegSubscription(ctx context.Context, privateID string) (RegSubscription, bool, error)
	// RegSubscriptionOfDialog returns the subscription whose dialog has
	// callID, for a NOTIFY that arrives in it. The caller checks the
	// local tag: a NOTIFY of another dialog with the same Call-ID
	// belongs to no subscription of the P-CSCF.
	RegSubscriptionOfDialog(ctx context.Context, callID string) (RegSubscription, bool, error)
	// DeleteRegSubscription removes the subscription of a private
	// identity, and its expiries with it.
	DeleteRegSubscription(ctx context.Context, privateID string) error
	// DeleteRegSubscriptionIfWaiting removes the subscription of
	// privateID if its dialog still has callID and it still waits for a
	// NOTIFY at deadline, and reports whether it did. The instance that
	// handles a Timer N removes the subscription this way, so that it
	// never takes away one that a NOTIFY confirmed, or one that a later
	// SUBSCRIBE made, between its read and the removal.
	DeleteRegSubscriptionIfWaiting(ctx context.Context, privateID, callID string, deadline time.Time) (bool, error)
	// ClaimDue claims the expiries of kind that are due at now, at most
	// limit of them and the ones due longest first, each until now plus
	// lease. While an expiry is claimed it is not due, and no other
	// ClaimDue gives it out; when its claim runs out without Done, it is
	// due again, for whichever instance asks next.
	//
	// A claim is no proof that the item has expired: the subscription may
	// have been refreshed, or a NOTIFY may have come, after it was due.
	// The caller therefore reads the subscription and checks the time it
	// carries, as the handlers of the P-CSCF do.
	ClaimDue(ctx context.Context, kind ExpiryKind, now time.Time, lease time.Duration, limit int) ([]Expiry, error)
	// Done takes the item of a claimed expiry out of its index, once the
	// caller has handled it. A claim that has ended in the meantime, by a
	// write that rescheduled the item, by its removal, or because it ran
	// out and another instance has it now, leaves the index as it is;
	// that is the ordinary end of an expiry, not a failure.
	Done(ctx context.Context, e Expiry) error
	// SecurityAssociations returns the entry of the SA table for a UE's
	// address and protected server port, until it expires;
	// PutSecurityAssociations replaces it.
	SecurityAssociations(ctx context.Context, ue netip.AddrPort) (SecurityAssociations, bool, error)
	PutSecurityAssociations(ctx context.Context, sas SecurityAssociations) error
	DeleteSecurityAssociations(ctx context.Context, ue netip.AddrPort) error
}

// MemoryStore is a Store in the memory of one process. Two P-CSCFs
// share state only when they share one MemoryStore, as in a test; a
// network of several nodes needs a store outside them. It never fails.
// A lookup by another identifier reads an index, as the stores outside
// the node do, so that its time does not grow with the number of UEs.
type MemoryStore struct {
	mu           sync.Mutex
	associations map[sip.Destination]Association
	dialogs      map[DialogKey]Dialog
	// dialogOfSession, associationOfSession and associationOfContact are
	// the indexes of the dialogs and the associations by policy session,
	// and of the associations by contact.
	dialogOfSession      map[policy.SessionID]DialogKey
	associationOfSession map[policy.SessionID]sip.Destination
	associationOfContact map[contactKey]sip.Destination
	// regSubscriptions are keyed by private identity, and
	// regSubscriptionOfDialog gives the private identity for the Call-ID
	// of a dialog, so that neither lookup scans.
	regSubscriptions        map[string]RegSubscription
	regSubscriptionOfDialog map[string]string
	// due holds the time index of each kind of expiry, as a store
	// outside the process holds one. A member is a private identity.
	due map[ExpiryKind]*ims.DueIndex[string]
	saTable
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{associations: map[sip.Destination]Association{}, dialogs: map[DialogKey]Dialog{},
		dialogOfSession: map[policy.SessionID]DialogKey{}, associationOfSession: map[policy.SessionID]sip.Destination{},
		associationOfContact: map[contactKey]sip.Destination{},
		regSubscriptions:     map[string]RegSubscription{}, regSubscriptionOfDialog: map[string]string{},
		due: map[ExpiryKind]*ims.DueIndex[string]{
			RegEventNotifyExpiry:  ims.NewDueIndex[string](),
			RegEventRefreshExpiry: ims.NewDueIndex[string](),
		}}
}

func keyOfContact(contact string) (contactKey, bool) {
	u, err := sip.ParseURI(contact)
	if err != nil {
		return contactKey{}, false
	}
	return contactKeyOf(u), true
}

func (s *MemoryStore) AssociationOfContact(_ context.Context, contact string) (Association, bool, error) {
	k, ok := keyOfContact(contact)
	if !ok {
		return Association{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.associationOfContact[k]
	if !ok {
		return Association{}, false, nil
	}
	a, ok := s.associations[source]
	return a, ok, nil
}

func (s *MemoryStore) PutDialog(_ context.Context, d Dialog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putDialog(d)
	return nil
}

// putDialog stores d and keeps the index by policy session in step.
func (s *MemoryStore) putDialog(d Dialog) {
	if old, ok := s.dialogs[d.Key]; ok && old.PolicySession != d.PolicySession {
		delete(s.dialogOfSession, old.PolicySession)
	}
	s.dialogs[d.Key] = d
	if d.PolicySession != "" {
		s.dialogOfSession[d.PolicySession] = d.Key
	}
}

func (s *MemoryStore) UpdateDialog(_ context.Context, key DialogKey, change func(*Dialog)) (Dialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dialogs[key]
	if !ok {
		return Dialog{}, false, nil
	}
	change(&d)
	s.putDialog(d)
	return d, true, nil
}

func (s *MemoryStore) TakeDialog(_ context.Context, key DialogKey) (Dialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dialogs[key]
	delete(s.dialogs, key)
	delete(s.dialogOfSession, d.PolicySession)
	return d, ok, nil
}

func (s *MemoryStore) DialogOfPolicySession(_ context.Context, session policy.SessionID) (Dialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.dialogOfSession[session]
	if !ok {
		return Dialog{}, false, nil
	}
	d, ok := s.dialogs[key]
	return d, ok, nil
}

func (s *MemoryStore) Association(_ context.Context, source sip.Destination) (Association, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.associations[source]
	return a, ok, nil
}

// PutAssociation keeps a. The store removes it at its KeepUntil, unless
// another association of the source has taken its place by then.
func (s *MemoryStore) PutAssociation(_ context.Context, a Association) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeAssociation(a.Source)
	s.associations[a.Source] = a
	if a.SignallingSession != "" {
		s.associationOfSession[a.SignallingSession] = a.Source
	}
	if k, ok := keyOfContact(a.Contact); ok {
		s.associationOfContact[k] = a.Source
	}
	if a.KeepUntil.IsZero() {
		return nil
	}
	time.AfterFunc(time.Until(a.KeepUntil), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.associations[a.Source]; ok && current.KeepUntil.Equal(a.KeepUntil) {
			s.removeAssociation(a.Source)
		}
	})
	return nil
}

func (s *MemoryStore) DeleteAssociation(_ context.Context, source sip.Destination) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeAssociation(source)
	return nil
}

// removeAssociation removes the association of source and the index
// entries that name it. The lock is held.
func (s *MemoryStore) removeAssociation(source sip.Destination) {
	a, ok := s.associations[source]
	if !ok {
		return
	}
	if s.associationOfSession[a.SignallingSession] == source {
		delete(s.associationOfSession, a.SignallingSession)
	}
	if k, ok := keyOfContact(a.Contact); ok && s.associationOfContact[k] == source {
		delete(s.associationOfContact, k)
	}
	delete(s.associations, source)
}

func (s *MemoryStore) AssociationOfPolicySession(_ context.Context, session policy.SessionID) (Association, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.associationOfSession[session]
	if !ok {
		return Association{}, false, nil
	}
	a, ok := s.associations[source]
	return a, ok, nil
}

// privateKey is the key under which a private identity is kept, as in
// the Stores of the other roles: the realm of a private identity
// compares without case (RFC 4282 2.4).
func privateKey(privateID string) string { return strings.ToLower(privateID) }

// CreateRegSubscription writes sub unless a live subscription of the
// same private identity is there. The lock of the store stands for the
// compare and swap that an external store needs. The store removes the
// subscription at its Expires.
func (s *MemoryStore) CreateRegSubscription(_ context.Context, sub RegSubscription) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := privateKey(sub.PrivateIdentity)
	if _, live := s.regSubscription(key); live {
		return false, nil
	}
	s.putRegSubscription(key, sub)
	return true, nil
}

func (s *MemoryStore) UpdateRegSubscription(_ context.Context, privateID string, change func(*RegSubscription) bool) (RegSubscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := privateKey(privateID)
	sub, live := s.regSubscription(key)
	if !live || !change(&sub) {
		return RegSubscription{}, false, nil
	}
	s.putRegSubscription(key, sub)
	return sub, true, nil
}

// putRegSubscription writes sub under key and keeps the index by the
// Call-ID of its dialog and the time indexes of its expiries in step.
// s.mu is held.
func (s *MemoryStore) putRegSubscription(key string, sub RegSubscription) {
	callID := sub.Dialog.ID.CallID
	sub.Dialog.RouteSet = append([]string(nil), sub.Dialog.RouteSet...)
	if old, ok := s.regSubscriptions[key]; ok && old.Dialog.ID.CallID != callID {
		delete(s.regSubscriptionOfDialog, old.Dialog.ID.CallID)
	}
	s.regSubscriptions[key] = sub
	s.regSubscriptionOfDialog[callID] = key
	s.schedule(RegEventNotifyExpiry, key, sub.NotifyDeadline)
	s.schedule(RegEventRefreshExpiry, key, sub.RefreshAt)
	time.AfterFunc(time.Until(sub.Expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if stored, ok := s.regSubscriptions[key]; ok && !time.Now().Before(stored.Expires) {
			s.deleteRegSubscription(key)
		}
	})
}

func (s *MemoryStore) RegSubscription(_ context.Context, privateID string) (RegSubscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.regSubscription(privateKey(privateID))
	return sub, ok, nil
}

func (s *MemoryStore) RegSubscriptionOfDialog(_ context.Context, callID string) (RegSubscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.regSubscriptionOfDialog[callID]
	if !ok {
		return RegSubscription{}, false, nil
	}
	sub, ok := s.regSubscription(key)
	if !ok || sub.Dialog.ID.CallID != callID {
		return RegSubscription{}, false, nil
	}
	return sub, true, nil
}

func (s *MemoryStore) DeleteRegSubscription(_ context.Context, privateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteRegSubscription(privateKey(privateID))
	return nil
}

// DeleteRegSubscriptionIfWaiting removes the subscription only while it
// is the one the caller read. The lock of the store stands for the
// compare and swap that an external store needs.
func (s *MemoryStore) DeleteRegSubscriptionIfWaiting(_ context.Context, privateID, callID string, deadline time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := privateKey(privateID)
	sub, live := s.regSubscription(key)
	if !live || sub.Dialog.ID.CallID != callID || !sub.NotifyDeadline.Equal(deadline) {
		return false, nil
	}
	s.deleteRegSubscription(key)
	return true, nil
}

func (s *MemoryStore) regSubscription(key string) (RegSubscription, bool) {
	sub, ok := s.regSubscriptions[key]
	if !ok || !time.Now().Before(sub.Expires) {
		return RegSubscription{}, false
	}
	// The route set is a slice: the caller gets a copy of its own.
	sub.Dialog.RouteSet = append([]string(nil), sub.Dialog.RouteSet...)
	return sub, true
}

func (s *MemoryStore) deleteRegSubscription(key string) {
	sub, ok := s.regSubscriptions[key]
	if !ok {
		return
	}
	delete(s.regSubscriptions, key)
	if s.regSubscriptionOfDialog[sub.Dialog.ID.CallID] == key {
		delete(s.regSubscriptionOfDialog, sub.Dialog.ID.CallID)
	}
	// The expiries of a subscription that is gone go with it, claimed or
	// not: nothing is left for an instance to act on.
	for _, ix := range s.due {
		ix.Remove(key)
	}
}

// schedule puts the subscription of key in the index of kind, due at at,
// and ends any claim on it. The zero time is no expiry of that kind, and
// takes it out of the index. s.mu is held.
func (s *MemoryStore) schedule(kind ExpiryKind, key string, at time.Time) {
	if at.IsZero() {
		s.due[kind].Remove(key)
		return
	}
	s.due[kind].Set(key, at)
}

func (s *MemoryStore) ClaimDue(_ context.Context, kind ExpiryKind, now time.Time, lease time.Duration, limit int) ([]Expiry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix, ok := s.due[kind]
	if !ok {
		return nil, nil
	}
	var claimed []Expiry
	for _, due := range ix.ClaimDue(now, lease, limit) {
		claimed = append(claimed, Expiry{Kind: kind, PrivateIdentity: due.Member, Until: due.Until})
	}
	return claimed, nil
}

func (s *MemoryStore) Done(_ context.Context, e Expiry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ix, ok := s.due[e.Kind]; ok {
		ix.RemoveClaimed(privateKey(e.PrivateIdentity), e.Until)
	}
	return nil
}

type PCSCF struct {
	cfg     Config
	locator Locator
	store   Store
	relays  []MediaRelay         // none passes media end to end
	policy  policy.PolicyControl // nil asks nothing of a PCRF
	stack   ims.StackRef
	// admissions is what Screen knows of the IP associations in store.
	admissions *admissions
	log        *slog.Logger

	// protectedStack listens on the protected server port.
	protectedStack ims.StackRef

	// setups are the initial INVITEs that have created a dialog and have
	// no final response yet. An INVITE transaction lives in the process
	// that forwards it, and only there can it be cancelled; the PCRF
	// reports the events of a policy session to the P-CSCF instance that
	// opened it, which is the one that holds the INVITE. An entry lasts
	// as long as its transaction, as the stack's own entries do. The
	// lock is held only to read or change the map.
	setupsMu sync.Mutex
	setups   map[DialogKey]*setup

	// regEventRefresh gives, for the duration a reg event subscription
	// has, when to refresh it (TS 24.229 5.2.3), and notifyWait is Timer
	// N, how long a SUBSCRIBE waits for the NOTIFY that must follow it:
	// 64*T1 (RFC 6665 4.1.2.2). Both times are written into the stored
	// subscription, and the sweep of whichever instance claims one acts
	// on it (regevent.go).
	regEventRefresh func(time.Duration) time.Duration
	notifyWait      time.Duration
	// stopSweep ends the sweep of this instance and waits for the
	// handlers it started.
	stopSweep func()

	// ackWait is how long a release waits for the ACK of a 2xx (release):
	// 64*T1, after which the callee itself gives up waiting for the ACK
	// (RFC 3261 13.3.1.4).
	ackWait time.Duration
}

// New makes a P-CSCF and starts its sweep, which handles the expiries of
// the reg event subscriptions that are due until Close. relays are the
// media relays it may use, the same in every instance; without any, RTP
// goes between the UEs directly. Both stacks of the P-CSCF need its
// Screen in their sip.Config, and the one on the unprotected port its
// AdmitUnprotected as well.
func New(cfg Config, locator Locator, store Store, relays []MediaRelay, log *slog.Logger) *PCSCF {
	if cfg.SweepInterval == 0 {
		cfg.SweepInterval = ims.SweepInterval
	}
	if cfg.ExpiryLease == 0 {
		cfg.ExpiryLease = ims.ExpiryLease
	}
	admitted := newAdmissions()
	p := &PCSCF{cfg: cfg, locator: locator, store: indexedStore{Store: store, admissions: admitted}, admissions: admitted,
		relays: relays, log: log, setups: map[DialogKey]*setup{},
		regEventRefresh: ims.RefreshAfter, notifyWait: 64 * sip.DefaultTimers().T1,
		ackWait: 64 * sip.DefaultTimers().T1}
	p.stopSweep = p.sweep()
	return p
}

// Close stops the sweep of this instance and waits for the handlers it
// started, whose requests to the Store it ends. An expiry that this
// instance had claimed and not handled is handled by another instance of
// the P-CSCF once the claim runs out.
func (p *PCSCF) Close() { p.stopSweep() }

// sweep starts the sweep of this instance and returns the function that
// stops it. Each handler reads the subscription and checks the time it
// carries, since a NOTIFY or a refresh may have moved it since the
// expiry came up; it returns the failure of the Store that left the
// subscription as it was, so that an instance claims the expiry again
// once the claim has run out.
func (p *PCSCF) sweep() (stop func()) {
	kinds := make([]string, 0, len(ExpiryKinds))
	for _, kind := range ExpiryKinds {
		kinds = append(kinds, string(kind))
	}
	return ims.Sweep[Expiry]{
		Kinds: kinds, Interval: p.cfg.SweepInterval, Lease: p.cfg.ExpiryLease,
		Claim: func(ctx context.Context, kind string, now time.Time, lease time.Duration, limit int) ([]Expiry, error) {
			return p.store.ClaimDue(ctx, ExpiryKind(kind), now, lease, limit)
		},
		Handle: p.handleExpiry,
		Done:   p.store.Done,
		Log:    p.log,
	}.Start()
}

// handleExpiry does what one claimed expiry of a reg event subscription
// calls for (regevent.go).
func (p *PCSCF) handleExpiry(ctx context.Context, e Expiry) error {
	switch e.Kind {
	case RegEventNotifyExpiry:
		return p.regEventNotifyWaitOver(ctx, e.PrivateIdentity)
	case RegEventRefreshExpiry:
		return p.refreshRegEventDue(ctx, e.PrivateIdentity)
	}
	return nil
}

// OnRequest is the sip.Config.OnRequest of the P-CSCF, on its
// unprotected port.
func (p *PCSCF) OnRequest(st *sip.ServerTransaction) {
	if st.Request.Method != sip.REGISTER {
		p.session(st)
		return
	}
	p.register(st, false)
}

// SetStack gives the P-CSCF the stack it runs on, which it needs to
// forward ACKs. Call it right after the stack is made.
func (p *PCSCF) SetStack(stack *sip.Stack) { p.stack.Set(stack) }

// register is TS 24.229 5.2.2.1, with the rules of 5.2.2.2 for IMS AKA
// and of 5.2.2.3 for SIP Digest without TLS. protected tells that the
// REGISTER came to the protected server port. After the 200 to the first
// initial REGISTER of a private identity, the P-CSCF subscribes to the
// reg event package (5.2.3).
//
// SIMPLIFIED: TS 24.229 5.2.2.1 item 6. The P-CSCF does not store the
// term-ioi of the P-Charging-Vector of the 200: that IOI names the home
// network in the charging records of the P-CSCF, which no node of this
// project writes, and no later message carries it.
func (p *PCSCF) register(st *sip.ServerTransaction, protected bool) {
	req := st.Request
	publicID, err := ims.PublicIdentity(req.Get("To"))
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	privateID, creds, err := ims.PrivateIdentity(req, publicID)
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	log := p.log.With("private_identity", privateID, "source", st.Source.String(), "call_id", req.Get("Call-ID"))

	out := req.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		st.Respond(sip.NewResponse(req, 483, sip.NewTag()))
		return
	}
	crossBoundary(out, requestFromUE)
	// SIMPLIFIED: TS 24.229 5.2.2.1 item 1 and RFC 5626. The P-CSCF
	// URI in Path should carry a flow token and the "ob" parameter,
	// which belong to SIP outbound. This project does not implement
	// outbound, so the URI only marks the terminating direction.
	path := p.pathURI()
	if protected {
		path = p.protectedPathURI()
	}
	if out.Get("Path") == "" {
		out.Add("Path", path)
	} else {
		// RFC 3327 5.1: a proxy adds its URI as the first value.
		out.Prepend("Path", path)
	}
	// Item 2: Require, not Supported.
	addOptionTag(out, "Require", "path")
	// Item 3: a new charging identifier for this request (TS 32.260).
	p.chargeRequest(out, rand.Text())
	// Item 4.
	out.Set("P-Visited-Network-ID", p.cfg.VisitedNetworkID)

	// 5.2.2.1 items 1 and 2: Security-Client with sec-agree, or the
	// protected server port, is IMS AKA (5.2.2.2). Otherwise 5.2.2.3 tells
	// the I-CSCF and the S-CSCF whether this UE already has an IP
	// association with the P-CSCF.
	assoc, associated, err := p.association(st, privateID)
	if err != nil {
		st.Respond(storeFailed(req, err, log))
		return
	}
	var integrity sip.IntegrityProtected
	var agreement *secAgreement
	switch {
	case protected || asksSecAgree(req):
		var refusal *sip.Message
		if agreement, refusal = p.receiveSecAgree(st, out, privateID, creds, protected, log); refusal != nil {
			st.Respond(refusal)
			return
		}
		integrity = agreement.integrity
	case associated:
		integrity = sip.IPAssociationYes
	case creds != nil && creds.Response != "":
		integrity = sip.IPAssociationPending
	}
	// Whatever the UE wrote is replaced (5.2.2.1 item 4B).
	if creds != nil {
		c := *creds
		c.SetIntegrityProtected(integrity)
		out.Set("Authorization", c.String())
	}

	u, err := sip.ParseURI(req.RequestURI)
	if err != nil {
		st.Respond(sip.NewResponse(req, 400, sip.NewTag()))
		return
	}
	transport, _ := u.Params.Get("transport")
	targets, err := p.locator.Locate(context.Background(), u.Host, u.Port, sip.Transport(strings.ToUpper(transport)))
	if err != nil || len(targets) == 0 {
		// 5.2.2.1: if no I-CSCF can be reached, 504.
		log.Warn("cannot locate the I-CSCF", "domain", u.Host, "error", fmt.Sprint(err))
		st.Respond(sip.NewResponse(req, 504, sip.NewTag()))
		return
	}

	final := p.forward(st, out, targets, log)
	if agreement != nil {
		if final, err = p.answerSecAgree(st, final, agreement, log); err != nil {
			st.Respond(storeFailed(req, err, log))
			return
		}
	}
	var registered *Association
	var interval time.Duration
	switch {
	case final.StatusCode == 200:
		a, d, ok, err := p.registered(st, final, privateID, assoc, log)
		if err != nil {
			// A 200 would stand for a registration that no P-CSCF
			// instance can find.
			st.Respond(storeFailed(req, err, log))
			return
		}
		if ok {
			registered, interval = &a, d
		}
	case final.StatusCode == 500 || final.StatusCode == 504:
		// 5.2.2.3: the network failed, so the association can no longer
		// be trusted.
		if err := p.deleteAssociation(ueSource(st), log); err != nil {
			log.Warn("the IP association stays until it expires: the store failed", "error", err.Error())
		}
	}
	crossBoundary(final, responseToUE)
	st.Respond(final)
	if registered != nil {
		if !associated || assoc.SignallingLost {
			// TS 29.214 A.4: on the initial registration, after its 200
			// (TS 29.213 B.1). A refresh keeps the session of the
			// registration it refreshes, unless the signalling path was
			// lost: this REGISTER shows that the UE has it again (TS
			// 24.229 5.2.6.4.3 NOTE 1).
			*registered = p.watchSignallingPath(*registered, log)
		}
		p.watchRegEvent(*registered, interval, log)
	}
}

// deleteAssociation forgets the IP association of source, and with it
// the watch on its signalling path.
func (p *PCSCF) deleteAssociation(source sip.Destination, log *slog.Logger) error {
	ctx := context.Background()
	a, ok, err := p.store.Association(ctx, source)
	if err != nil || !ok {
		return err
	}
	p.unwatchSignallingPath(&a, log)
	return p.store.DeleteAssociation(ctx, source)
}

// storeFailed is the 500 (Server Internal Error) to req, a request whose
// state the P-CSCF cannot read or write (RFC 3261 21.5.1). The UE or the
// network may send the request again.
func storeFailed(req *sip.Message, err error, log *slog.Logger) *sip.Message {
	log.Warn("the store failed", "method", string(req.Method), "error", err.Error())
	return sip.NewResponse(req, 500, sip.NewTag())
}

// errStore marks a failure of the Store in an error that may also be
// one of routing or of the network, so that the request gets 500 rather
// than the response to those.
var errStore = errors.New("pcscf: the store failed")

func storeError(err error) error { return fmt.Errorf("%w: %w", errStore, err) }

// pathURI is the P-CSCF's URI in Path, which marks the requests to the
// UE (5.2.6.2).
func (p *PCSCF) pathURI() string { return fmt.Sprintf("<sip:term@%s:%d;lr>", p.cfg.Host, p.cfg.Port) }

// forward tries the I-CSCFs in the order DNS gave them. TS 24.229
// 5.2.2.1 item 7 moves on to the next one on a timeout, a 3xx or a 480,
// and answers 504 when none is left. A 503 and a transport error are
// failures of the I-CSCF too, as for any request (RFC 3263 4.3). Only
// this loop knows that a 3xx or a 480 sends the REGISTER on, so it
// hands the stack one I-CSCF at a time.
func (p *PCSCF) forward(st *sip.ServerTransaction, out *sip.Message, targets []sip.Destination, log *slog.Logger) *sip.Message {
	for _, target := range targets {
		final, err := st.ProxyWith(out, func(resp *sip.Message) { crossBoundary(resp, responseToUE) }, target)
		switch {
		case err != nil:
			log.Warn("I-CSCF did not answer", "target", target.String(), "error", err.Error())
		case final.StatusCode/100 == 3 || final.StatusCode == 480 || final.StatusCode == 503:
			log.Info("I-CSCF turned the request away", "target", target.String(), "status", final.StatusCode)
		default:
			return final
		}
	}
	return sip.NewResponse(st.Request, 504, sip.NewTag())
}

// ueSource is ueSourceOf for the request of st.
func ueSource(st *sip.ServerTransaction) sip.Destination {
	source, _ := ueSourceOf(st.Request, st.Source)
	return source
}

// association returns the IP association of the request's source
// (ueSource), if it is still valid and belongs to privateID. An empty
// privateID accepts any user.
func (p *PCSCF) association(st *sip.ServerTransaction, privateID string) (Association, bool, error) {
	via, viaErr := sip.ParseVia(st.Request.Get("Via"))
	source := ueSource(st)
	a, ok, err := p.store.Association(context.Background(), source)
	if err != nil || !ok {
		return Association{}, false, err
	}
	if time.Now().After(a.Expires) {
		return Association{}, false, p.deleteAssociation(source, p.log)
	}
	if viaErr != nil || via.SentBy() != a.SentBy || (privateID != "" && !strings.EqualFold(a.PrivateIdentity, privateID)) {
		return Association{}, false, nil
	}
	return a, true, nil
}

// registered handles the 200 OK to a REGISTER (TS 24.229 5.2.2.1 and
// 5.2.2.3): with a non-zero interval it creates or refreshes the IP
// association and stores Service-Route and P-Associated-URI, and
// returns the association with the interval; with zero it removes the
// association.
func (p *PCSCF) registered(st *sip.ServerTransaction, resp *sip.Message, privateID string, old Association, log *slog.Logger) (Association, time.Duration, bool, error) {
	ctx := context.Background()
	interval := grantedInterval(resp)
	if interval == 0 {
		a, ok, err := p.store.Association(ctx, ueSource(st))
		if err == nil && ok {
			err = p.endAssociation(a, log)
		}
		if err != nil {
			return Association{}, 0, false, err
		}
		log.Info("deregistered")
		return Association{}, 0, false, nil
	}
	via, _ := sip.ParseVia(st.Request.Get("Via"))
	expires := time.Now().Add(interval)
	a := Association{
		Source:          ueSource(st),
		Contact:         contactOf(st.Request),
		SentBy:          via.SentBy(),
		PrivateIdentity: privateID,
		ServiceRoute:    resp.GetAll("Service-Route"),
		Expires:         expires,
		KeepUntil:       expires.Add(associationGrace),
		// A refresh keeps the watch on the signalling path.
		SignallingSession: old.SignallingSession,
	}
	for _, v := range resp.GetAll("P-Associated-URI") {
		if na, err := sip.ParseNameAddr(v); err == nil {
			a.PublicIdentities = append(a.PublicIdentities, na.URI)
		}
	}
	if err := p.store.PutAssociation(ctx, a); err != nil {
		return Association{}, 0, false, err
	}
	log.Info("registered", "public_identities", a.PublicIdentities, "service_route", a.ServiceRoute,
		"expires", int(interval/time.Second), "refresh", old.PrivateIdentity != "")
	return a, interval, true, nil
}

func addOptionTag(m *sip.Message, header, tag string) {
	for _, v := range m.GetAll(header) {
		if strings.EqualFold(strings.TrimSpace(v), tag) {
			return
		}
	}
	m.Add(header, tag)
}

// associationGrace is how long an IP association that has ended still
// carries requests to the UE: 64*T1, the longest a non-INVITE
// transaction lasts over UDP (RFC 3261 17.1.2.2, 17.2.2). TS 24.229
// 5.2.5.1 item 2 deletes the association once the server transaction of
// the deregistration has terminated, and 5.2.5.2 NOTE 2 keeps it until
// the NOTIFY that reports the deregistration has reached the UE.
var associationGrace = 64 * sip.DefaultTimers().T1

// endAssociation ends the IP association of a UE whose registration has
// ended. The P-CSCF takes no more requests from the UE at once, but
// still passes requests to it for associationGrace, until the Store
// forgets the association at its KeepUntil. The watch on the signalling
// path ends at once (TS 29.214 A.4).
func (p *PCSCF) endAssociation(a Association, log *slog.Logger) error {
	ctx := context.Background()
	p.unwatchSignallingPath(&a, log)
	a.Expires = time.Now()
	a.KeepUntil = a.Expires.Add(associationGrace)
	// The association ends before the security associations go, so that
	// a write that fails in between leaves security associations without
	// an association, whose requests are dropped, rather than an
	// association still in force without them, whose unprotected
	// requests would be taken.
	if err := p.store.PutAssociation(ctx, a); err != nil {
		return err
	}
	// TS 33.203 7.4.2a: the security associations of the UE go with its
	// last registered identity.
	return p.store.DeleteSecurityAssociations(ctx, a.Source.Addr)
}

// associationOfContact is the association of a contact, for a request
// to the UE. Its expiry is not checked: the S-CSCF decides whether the
// registration has ended, routes nothing to a user it no longer serves,
// and tells the P-CSCF with the reg event (5.2.5.2), which ends the
// association. The expiry that the P-CSCF noted comes a moment after the
// S-CSCF's, and the NOTIFY that reports the end has to reach the UE.
func (p *PCSCF) associationOfContact(contact string) (Association, bool, error) {
	return p.store.AssociationOfContact(context.Background(), contact)
}

func contactOf(req *sip.Message) string {
	na, err := sip.ParseNameAddr(req.Get("Contact"))
	if err != nil {
		return ""
	}
	return na.URI
}
