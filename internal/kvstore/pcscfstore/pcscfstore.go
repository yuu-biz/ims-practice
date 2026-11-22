// Package pcscfstore is a pcscf.Store on a kvstore.Store, so that the IP
// associations, the dialogs and the SA table of the P-CSCF live outside
// the node and several P-CSCF instances share them. The adapter is
// kept to the interface and no more.
//
// The items and their keys, after the prefix of the Config and
// "pcscf:":
//
//	association:<transport>:<address>             Association, time to live until its KeepUntil
//	association-of-contact:<user>:<host>:<port>   index: the source of the association
//	association-of-session:<policy session>       index: the source of the association
//	dialog:<Call-ID>:<UE tag>                     Dialog, no time to live
//	dialog-of-session:<policy session>            index: the key of the dialog
//	reg-subscription:<IMPI>                       RegSubscription, time to live until its Expires
//	reg-subscription-of-dialog:<Call-ID>          index: the private identity of the subscription
//	sa:<address>                                  SecurityAssociations, time to live until Expires
//
// Every lookup is a read of one or two keys. AssociationOfContact reads
// the index item of the host, port and user of the contact, which is
// what it compares.
//
// Every operation returns the failure of the store, and a lookup does
// not take it for a missing item. A write returns it until the main item
// is written or removed. What fails after that is the removal of an
// index item that the main item no longer names, which is what an
// instance that stops there leaves: a miss (kvjson.WriteIndex).
package pcscfstore

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Store is a pcscf.Store on a kvstore.Store. The P-CSCF writes every
// expiry into what it stores, as a time, and the Store takes the times
// to live from there.
type Store struct {
	c *kvjson.Client
}

var _ pcscf.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "pcscf")}
}

func (s *Store) associationKey(source sip.Destination) string {
	return s.c.Key("association", string(source.Transport), source.Addr.String())
}

// contactKey is the key of the index item of a contact: the user, host
// and port that pcscf.MemoryStore compares, the host without case.
func (s *Store) contactKey(contact string) (string, bool) {
	u, err := sip.ParseURI(contact)
	if err != nil {
		return "", false
	}
	return s.c.Key("association-of-contact", u.User, strings.ToLower(u.Host), strconv.Itoa(u.Port)), true
}

func (s *Store) associationSessionKey(session policy.SessionID) string {
	return s.c.Key("association-of-session", string(session))
}

// target is the value of an index item that names an association.
func target(source sip.Destination) string {
	return kvjson.Member(string(source.Transport), source.Addr.String())
}

// read reads the item under key into v, and reports whether there is
// one.
func read[T any](ctx context.Context, c *kvjson.Client, key string, v *T) (bool, error) {
	_, err := kvjson.Read(ctx, c, key, v)
	if errors.Is(err, kvstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) Association(ctx context.Context, source sip.Destination) (pcscf.Association, bool, error) {
	var a pcscf.Association
	ok, err := read(ctx, s.c, s.associationKey(source), &a)
	return a, ok, err
}

// indexTarget reads the index item key, and reports whether there is
// one.
func (s *Store) indexTarget(ctx context.Context, key string) (string, bool, error) {
	t, _, err := s.c.ReadIndex(ctx, key)
	if errors.Is(err, kvstore.ErrNotFound) {
		return "", false, nil
	}
	return t, err == nil, err
}

// associationOf reads the association that an index item names.
func (s *Store) associationOf(ctx context.Context, indexKey string) (pcscf.Association, bool, error) {
	t, ok, err := s.indexTarget(ctx, indexKey)
	if err != nil || !ok {
		return pcscf.Association{}, false, err
	}
	parts := kvjson.SplitMember(t)
	if len(parts) != 2 {
		return pcscf.Association{}, false, nil
	}
	var a pcscf.Association
	ok, err = read(ctx, s.c, s.c.Key("association", parts[0], parts[1]), &a)
	return a, ok, err
}

// AssociationOfContact reads the index item of the contact, then the
// association. An association whose contact is another is a miss (see
// kvjson.WriteIndex).
func (s *Store) AssociationOfContact(ctx context.Context, contact string) (pcscf.Association, bool, error) {
	k, ok := s.contactKey(contact)
	if !ok {
		return pcscf.Association{}, false, nil
	}
	a, ok, err := s.associationOf(ctx, k)
	if err != nil {
		return pcscf.Association{}, false, err
	}
	if got, _ := s.contactKey(a.Contact); !ok || got != k {
		return pcscf.Association{}, false, nil
	}
	return a, true, nil
}

func (s *Store) AssociationOfPolicySession(ctx context.Context, session policy.SessionID) (pcscf.Association, bool, error) {
	a, ok, err := s.associationOf(ctx, s.associationSessionKey(session))
	if err != nil || !ok || a.SignallingSession != session {
		return pcscf.Association{}, false, err
	}
	return a, true, nil
}

// PutAssociation writes the index items of the contact and the policy
// session, then the association, then removes the index items that the
// association no longer has. The order is that of kvjson.WriteIndex.
func (s *Store) PutAssociation(ctx context.Context, a pcscf.Association) error {
	old, _, err := s.Association(ctx, a.Source)
	if err != nil {
		return err
	}
	ttl, t := s.c.TTLUntil(a.KeepUntil), target(a.Source)
	contact, hasContact := s.contactKey(a.Contact)
	if hasContact {
		if err := s.c.WriteIndex(ctx, contact, t, ttl); err != nil {
			return err
		}
	}
	if a.SignallingSession != "" {
		if err := s.c.WriteIndex(ctx, s.associationSessionKey(a.SignallingSession), t, ttl); err != nil {
			return err
		}
	}
	if err := kvjson.Write(ctx, s.c, s.associationKey(a.Source), a, ttl); err != nil {
		return err
	}
	if oldContact, ok := s.contactKey(old.Contact); ok && (!hasContact || oldContact != contact) {
		s.c.RemoveIndexIf(ctx, oldContact, t)
	}
	if old.SignallingSession != "" && old.SignallingSession != a.SignallingSession {
		s.c.RemoveIndexIf(ctx, s.associationSessionKey(old.SignallingSession), t)
	}
	return nil
}

// DeleteAssociation removes the association, then its index items.
func (s *Store) DeleteAssociation(ctx context.Context, source sip.Destination) error {
	old, ok, err := s.Association(ctx, source)
	if err != nil {
		return err
	}
	if err := s.c.Remove(ctx, s.associationKey(source)); err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if k, ok := s.contactKey(old.Contact); ok {
		s.c.RemoveIndexIf(ctx, k, target(source))
	}
	if old.SignallingSession != "" {
		s.c.RemoveIndexIf(ctx, s.associationSessionKey(old.SignallingSession), target(source))
	}
	return nil
}

func (s *Store) dialogKey(key pcscf.DialogKey) string {
	return s.c.Key("dialog", key.CallID, key.UETag)
}

func (s *Store) sessionKey(session policy.SessionID) string {
	return s.c.Key("dialog-of-session", string(session))
}

// PutDialog writes the index item of the policy session, the dialog,
// and removes the index item of a policy session that the dialog no
// longer has.
//
// A dialog keeps no time to live: it lasts until its BYE, however long
// that is, as in pcscf.MemoryStore. The dialog of a P-CSCF that never
// sees the end of its call stays.
func (s *Store) PutDialog(ctx context.Context, d pcscf.Dialog) error {
	var old pcscf.Dialog
	if _, err := read(ctx, s.c, s.dialogKey(d.Key), &old); err != nil {
		return err
	}
	if d.PolicySession != "" {
		if err := s.c.WriteIndex(ctx, s.sessionKey(d.PolicySession), kvjson.Member(d.Key.CallID, d.Key.UETag), 0); err != nil {
			return err
		}
	}
	if err := kvjson.Write(ctx, s.c, s.dialogKey(d.Key), d, 0); err != nil {
		return err
	}
	s.dropSession(ctx, d.Key, old.PolicySession, d.PolicySession)
	return nil
}

// dropSession removes the index item of the policy session was if the
// dialog of key no longer has it. An index item that stays is a miss.
func (s *Store) dropSession(ctx context.Context, key pcscf.DialogKey, was, is policy.SessionID) {
	if was != "" && was != is {
		s.c.RemoveIndexIf(ctx, s.sessionKey(was), kvjson.Member(key.CallID, key.UETag))
	}
}

// UpdateDialog is a read, change and compare and swap, again from the
// new value when another instance wrote first; change may therefore run
// more than once, each time on a fresh copy (the change functions of
// pcscf only set fields of the dialog). A policy session that change
// gives the dialog gets its index item before the dialog is written.
func (s *Store) UpdateDialog(ctx context.Context, key pcscf.DialogKey, change func(*pcscf.Dialog)) (pcscf.Dialog, bool, error) {
	var before policy.SessionID
	d, err := kvjson.Modify(ctx, s.c, s.dialogKey(key), func(pcscf.Dialog) time.Duration { return 0 }, func(d *pcscf.Dialog) error {
		before = d.PolicySession
		change(d)
		if d.PolicySession != "" && d.PolicySession != before {
			return s.c.WriteIndex(ctx, s.sessionKey(d.PolicySession), kvjson.Member(key.CallID, key.UETag), 0)
		}
		return nil
	})
	if errors.Is(err, kvstore.ErrNotFound) {
		return pcscf.Dialog{}, false, nil
	}
	if err != nil {
		return pcscf.Dialog{}, false, err
	}
	s.dropSession(ctx, key, before, d.PolicySession)
	return d, true, nil
}

// TakeDialog is a read and a compare and delete: of the instances that
// take one dialog, one gets it (kvjson.ReadAndRemove).
func (s *Store) TakeDialog(ctx context.Context, key pcscf.DialogKey) (pcscf.Dialog, bool, error) {
	d, err := kvjson.ReadAndRemove[pcscf.Dialog](ctx, s.c, s.dialogKey(key))
	if errors.Is(err, kvstore.ErrNotFound) {
		return pcscf.Dialog{}, false, nil
	}
	if err != nil {
		return pcscf.Dialog{}, false, err
	}
	s.dropSession(ctx, key, d.PolicySession, "")
	return d, true, nil
}

func (s *Store) DialogOfPolicySession(ctx context.Context, session policy.SessionID) (pcscf.Dialog, bool, error) {
	t, ok, err := s.indexTarget(ctx, s.sessionKey(session))
	parts := kvjson.SplitMember(t)
	if err != nil || !ok || len(parts) != 2 {
		return pcscf.Dialog{}, false, err
	}
	var d pcscf.Dialog
	ok, err = read(ctx, s.c, s.c.Key("dialog", parts[0], parts[1]), &d)
	if err != nil || !ok || d.PolicySession != session {
		return pcscf.Dialog{}, false, err
	}
	return d, true, nil
}

// privateKey is the key under which a private identity is kept, as in
// pcscf.MemoryStore and in the Stores of the other roles.
func privateKey(privateID string) string { return strings.ToLower(privateID) }

func (s *Store) subscriptionKey(privateID string) string {
	return s.c.Key("reg-subscription", privateKey(privateID))
}

func (s *Store) subscriptionDialogKey(callID string) string {
	return s.c.Key("reg-subscription-of-dialog", callID)
}

// RegSubscription returns the subscription until its Expires, by the
// clock of the Store, as pcscf.MemoryStore does; the time to live
// removes it soon after.
func (s *Store) RegSubscription(ctx context.Context, privateID string) (pcscf.RegSubscription, bool, error) {
	var sub pcscf.RegSubscription
	ok, err := read(ctx, s.c, s.subscriptionKey(privateID), &sub)
	if err != nil || !ok || !s.c.Now().Before(sub.Expires) {
		return pcscf.RegSubscription{}, false, err
	}
	return sub, true, nil
}

// RegSubscriptionOfDialog reads the index item of the Call-ID, then the
// subscription. A subscription whose dialog is another is a miss (see
// kvjson.WriteIndex).
func (s *Store) RegSubscriptionOfDialog(ctx context.Context, callID string) (pcscf.RegSubscription, bool, error) {
	privateID, ok, err := s.indexTarget(ctx, s.subscriptionDialogKey(callID))
	if err != nil || !ok {
		return pcscf.RegSubscription{}, false, err
	}
	sub, ok, err := s.RegSubscription(ctx, privateID)
	if err != nil || !ok || sub.Dialog.ID.CallID != callID {
		return pcscf.RegSubscription{}, false, err
	}
	return sub, true, nil
}

// errNoChange stands for a change function that reported false, and for
// a subscription that has run out: UpdateRegSubscription writes nothing
// and reports that there is none.
var errNoChange = errors.New("pcscfstore: the subscription was not changed")

// CreateRegSubscription writes the index item of the dialog, then the
// subscription itself, but only if the item is still at the version that
// held no live subscription. Of several instances that subscribe for one
// user at once, exactly one writes; the losers leave an index item that
// names a private identity whose subscription has another Call-ID, which
// is a miss (see RegSubscriptionOfDialog).
func (s *Store) CreateRegSubscription(ctx context.Context, sub pcscf.RegSubscription) (bool, error) {
	var old pcscf.RegSubscription
	version, err := kvjson.Read(ctx, s.c, s.subscriptionKey(sub.PrivateIdentity), &old)
	switch {
	case errors.Is(err, kvstore.ErrNotFound):
		version = ""
	case err != nil:
		return false, err
	case s.c.Now().Before(old.Expires):
		return false, nil
	}
	ttl := s.c.TTLUntil(sub.Expires)
	if err := s.c.WriteIndex(ctx, s.subscriptionDialogKey(sub.Dialog.ID.CallID), privateKey(sub.PrivateIdentity), ttl); err != nil {
		return false, err
	}
	_, err = kvjson.CompareAndSwap(ctx, s.c, s.subscriptionKey(sub.PrivateIdentity), version, sub, ttl)
	if errors.Is(err, kvstore.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

// UpdateRegSubscription is a read, change, and compare and swap, again
// from the new value when another instance wrote first. The Call-ID of
// the dialog does not change, so the index item stands.
func (s *Store) UpdateRegSubscription(ctx context.Context, privateID string, change func(*pcscf.RegSubscription) bool) (pcscf.RegSubscription, bool, error) {
	sub, err := kvjson.Modify(ctx, s.c, s.subscriptionKey(privateID),
		func(v pcscf.RegSubscription) time.Duration { return s.c.TTLUntil(v.Expires) },
		func(v *pcscf.RegSubscription) error {
			if !s.c.Now().Before(v.Expires) || !change(v) {
				return errNoChange
			}
			return nil
		})
	switch {
	case errors.Is(err, kvstore.ErrNotFound), errors.Is(err, errNoChange):
		return pcscf.RegSubscription{}, false, nil
	case err != nil:
		return pcscf.RegSubscription{}, false, err
	}
	return sub, true, nil
}

// DeleteRegSubscription removes the subscription, then the index item
// of its dialog.
func (s *Store) DeleteRegSubscription(ctx context.Context, privateID string) error {
	var old pcscf.RegSubscription
	ok, err := read(ctx, s.c, s.subscriptionKey(privateID), &old)
	if err != nil {
		return err
	}
	if err := s.c.Remove(ctx, s.subscriptionKey(privateID)); err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.c.RemoveIndexIf(ctx, s.subscriptionDialogKey(old.Dialog.ID.CallID), privateKey(privateID))
}

func (s *Store) saKey(ue netip.AddrPort) string { return s.c.Key("sa", ue.String()) }

// SecurityAssociations returns the entry until its Expires, by the clock
// of the Store, as pcscf.MemoryStore does; the time to live removes it
// soon after.
func (s *Store) SecurityAssociations(ctx context.Context, ue netip.AddrPort) (pcscf.SecurityAssociations, bool, error) {
	var sas pcscf.SecurityAssociations
	ok, err := read(ctx, s.c, s.saKey(ue), &sas)
	if err != nil || !ok || !s.c.Now().Before(sas.Expires) {
		return pcscf.SecurityAssociations{}, false, err
	}
	return sas, true, nil
}

func (s *Store) PutSecurityAssociations(ctx context.Context, sas pcscf.SecurityAssociations) error {
	return kvjson.Write(ctx, s.c, s.saKey(sas.UE), sas, s.c.TTLUntil(sas.Expires))
}

func (s *Store) DeleteSecurityAssociations(ctx context.Context, ue netip.AddrPort) error {
	return s.c.Remove(ctx, s.saKey(ue))
}
