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
//	sa:<address>                                  SecurityAssociations, time to live until Expires
//
// Every lookup is a read of one or two keys. AssociationOfContact walked
// every association in the MemoryStore; here it reads the index
// item of the host, port and user of the contact, which is what it
// compares.
package pcscfstore

import (
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

// target is the value of an index item that names an association.
func target(source sip.Destination) string {
	return kvjson.Member(string(source.Transport), source.Addr.String())
}

func (s *Store) Association(source sip.Destination) (pcscf.Association, bool) {
	var a pcscf.Association
	_, ok := kvjson.Get(s.c, s.associationKey(source), &a)
	return a, ok
}

// associationOf reads the association that an index item names.
func (s *Store) associationOf(indexKey string) (pcscf.Association, bool) {
	t, _, ok := s.c.IndexTarget(indexKey)
	if !ok {
		return pcscf.Association{}, false
	}
	parts := kvjson.SplitMember(t)
	if len(parts) != 2 {
		return pcscf.Association{}, false
	}
	var a pcscf.Association
	_, ok = kvjson.Get(s.c, s.c.Key("association", parts[0], parts[1]), &a)
	return a, ok
}

// AssociationOfContact reads the index item of the contact, then the
// association. An association whose contact is another is a miss (see
// kvjson.PutIndex).
func (s *Store) AssociationOfContact(contact string) (pcscf.Association, bool) {
	k, ok := s.contactKey(contact)
	if !ok {
		return pcscf.Association{}, false
	}
	a, ok := s.associationOf(k)
	if got, _ := s.contactKey(a.Contact); !ok || got != k {
		return pcscf.Association{}, false
	}
	return a, true
}

func (s *Store) AssociationOfPolicySession(session policy.SessionID) (pcscf.Association, bool) {
	a, ok := s.associationOf(s.c.Key("association-of-session", string(session)))
	if !ok || a.SignallingSession != session {
		return pcscf.Association{}, false
	}
	return a, true
}

// PutAssociation writes the index items of the contact and the policy
// session, then the association, then removes the index items that the
// association no longer has. The order is that of kvjson.PutIndex.
func (s *Store) PutAssociation(a pcscf.Association) {
	old, _ := s.Association(a.Source)
	// pcscf.MemoryStore keeps an association until DeleteAssociation;
	// the time to live also ends one whose end the P-CSCF never heard
	// of, such as when the S-CSCF stopped before its NOTIFY.
	ttl, t := s.c.TTLUntil(a.KeepUntil), target(a.Source)
	contact, hasContact := s.contactKey(a.Contact)
	if hasContact {
		s.c.PutIndex(contact, t, ttl)
	}
	session := s.c.Key("association-of-session", string(a.SignallingSession))
	if a.SignallingSession != "" {
		s.c.PutIndex(session, t, ttl)
	}
	kvjson.Put(s.c, s.associationKey(a.Source), a, ttl)
	if oldContact, ok := s.contactKey(old.Contact); ok && (!hasContact || oldContact != contact) {
		s.c.DeleteIndexIf(oldContact, t)
	}
	if old.SignallingSession != "" && old.SignallingSession != a.SignallingSession {
		s.c.DeleteIndexIf(s.c.Key("association-of-session", string(old.SignallingSession)), t)
	}
}

// DeleteAssociation removes the association, then its index items.
func (s *Store) DeleteAssociation(source sip.Destination) {
	old, ok := s.Association(source)
	s.c.Delete(s.associationKey(source))
	if !ok {
		return
	}
	if k, ok := s.contactKey(old.Contact); ok {
		s.c.DeleteIndexIf(k, target(source))
	}
	if old.SignallingSession != "" {
		s.c.DeleteIndexIf(s.c.Key("association-of-session", string(old.SignallingSession)), target(source))
	}
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
func (s *Store) PutDialog(d pcscf.Dialog) {
	var old pcscf.Dialog
	kvjson.Get(s.c, s.dialogKey(d.Key), &old)
	if d.PolicySession != "" {
		s.c.PutIndex(s.sessionKey(d.PolicySession), kvjson.Member(d.Key.CallID, d.Key.UETag), 0)
	}
	kvjson.Put(s.c, s.dialogKey(d.Key), d, 0)
	s.dropSession(d.Key, old.PolicySession, d.PolicySession)
}

// dropSession removes the index item of the policy session was if the
// dialog of key no longer has it.
func (s *Store) dropSession(key pcscf.DialogKey, was, is policy.SessionID) {
	if was != "" && was != is {
		s.c.DeleteIndexIf(s.sessionKey(was), kvjson.Member(key.CallID, key.UETag))
	}
}

// UpdateDialog is a read, change and compare and swap, again from the
// new value when another instance wrote first; change may therefore run
// more than once, each time on a fresh copy (the change functions of
// pcscf only set fields of the dialog). A policy session that change
// gives the dialog gets its index item before the dialog is written.
func (s *Store) UpdateDialog(key pcscf.DialogKey, change func(*pcscf.Dialog)) (pcscf.Dialog, bool) {
	var before policy.SessionID
	d, err := kvjson.Update(s.c, s.dialogKey(key), func(pcscf.Dialog) time.Duration { return 0 }, func(d *pcscf.Dialog) error {
		before = d.PolicySession
		change(d)
		if d.PolicySession != "" && d.PolicySession != before {
			s.c.PutIndex(s.sessionKey(d.PolicySession), kvjson.Member(key.CallID, key.UETag), 0)
		}
		return nil
	})
	if err != nil {
		return pcscf.Dialog{}, false
	}
	s.dropSession(key, before, d.PolicySession)
	return d, true
}

// TakeDialog is a read and a compare and delete: of the instances that
// take one dialog, one gets it (kvjson.Take).
func (s *Store) TakeDialog(key pcscf.DialogKey) (pcscf.Dialog, bool) {
	d, ok := kvjson.Take[pcscf.Dialog](s.c, s.dialogKey(key))
	if ok {
		s.dropSession(key, d.PolicySession, "")
	}
	return d, ok
}

func (s *Store) DialogOfPolicySession(session policy.SessionID) (pcscf.Dialog, bool) {
	t, _, ok := s.c.IndexTarget(s.sessionKey(session))
	parts := kvjson.SplitMember(t)
	if !ok || len(parts) != 2 {
		return pcscf.Dialog{}, false
	}
	var d pcscf.Dialog
	if _, ok := kvjson.Get(s.c, s.c.Key("dialog", parts[0], parts[1]), &d); !ok || d.PolicySession != session {
		return pcscf.Dialog{}, false
	}
	return d, true
}

func (s *Store) saKey(sas pcscf.SecurityAssociations) string { return s.c.Key("sa", sas.UE.String()) }

// SecurityAssociations returns the entry until its Expires, by the clock
// of the Store, as pcscf.MemoryStore does; the time to live removes it
// soon after.
func (s *Store) SecurityAssociations(ue netip.AddrPort) (pcscf.SecurityAssociations, bool) {
	var sas pcscf.SecurityAssociations
	if _, ok := kvjson.Get(s.c, s.c.Key("sa", ue.String()), &sas); !ok || !s.c.Now().Before(sas.Expires) {
		return pcscf.SecurityAssociations{}, false
	}
	return sas, true
}

func (s *Store) PutSecurityAssociations(sas pcscf.SecurityAssociations) {
	kvjson.Put(s.c, s.saKey(sas), sas, s.c.TTLUntil(sas.Expires))
}

func (s *Store) DeleteSecurityAssociations(ue netip.AddrPort) {
	s.c.Delete(s.c.Key("sa", ue.String()))
}
