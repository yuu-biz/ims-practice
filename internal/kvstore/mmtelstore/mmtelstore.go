// Package mmtelstore is an mmtel.Store on a kvstore.Store, so that any
// MMTel AS instance knows a call that another one set up, and can end
// both of its dialogs.
//
// The items and their keys, after the prefix of the Config and
// "mmtel:":
//
//	call:<Call-ID>:<local tag>:<remote tag>       CallRecord, under its Incoming dialog
//	call-of-leg:<Call-ID>:<local tag>:<remote tag> index: the Incoming dialog, under the Outgoing one
//
// mmtel.MemoryStore writes the record twice, once under each dialog.
// Here it is one item, so that the two copies can never disagree, and
// the other leg is an index item; CallOf by the Outgoing dialog reads
// two keys.
package mmtelstore

import (
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Store is an mmtel.Store on a kvstore.Store.
type Store struct {
	c *kvjson.Client
}

var _ mmtel.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "mmtel")}
}

func dialogParts(id sip.DialogID) []string { return []string{id.CallID, id.LocalTag, id.RemoteTag} }

func (s *Store) callKey(id sip.DialogID) string { return s.c.Key("call", dialogParts(id)...) }

func (s *Store) legKey(id sip.DialogID) string { return s.c.Key("call-of-leg", dialogParts(id)...) }

// PutCall writes the index item of the Outgoing dialog first, then the
// record (kvjson.PutIndex).
//
// A record keeps no time to live: the call lasts until its BYE, however
// long that is, as in mmtel.MemoryStore. The record of a call whose end
// no instance sees stays.
func (s *Store) PutCall(r mmtel.CallRecord) {
	s.c.PutIndex(s.legKey(r.Outgoing.ID), kvjson.Member(dialogParts(r.Incoming.ID)...), 0)
	kvjson.Put(s.c, s.callKey(r.Incoming.ID), r, 0)
}

// CallOf reads the record under the dialog, or else the index item of
// an Outgoing dialog and the record it names. A record whose Outgoing
// dialog is another is a miss.
func (s *Store) CallOf(id sip.DialogID) (mmtel.CallRecord, bool) {
	var r mmtel.CallRecord
	if _, ok := kvjson.Get(s.c, s.callKey(id), &r); ok {
		return r, true
	}
	t, _, ok := s.c.IndexTarget(s.legKey(id))
	parts := kvjson.SplitMember(t)
	if !ok || len(parts) != 3 {
		return mmtel.CallRecord{}, false
	}
	incoming := sip.DialogID{CallID: parts[0], LocalTag: parts[1], RemoteTag: parts[2]}
	if _, ok := kvjson.Get(s.c, s.callKey(incoming), &r); !ok || r.Outgoing.ID != id {
		return mmtel.CallRecord{}, false
	}
	return r, true
}

// DeleteCall removes the record, then the index item.
func (s *Store) DeleteCall(r mmtel.CallRecord) {
	s.c.Delete(s.callKey(r.Incoming.ID))
	s.c.DeleteIndexIf(s.legKey(r.Outgoing.ID), kvjson.Member(dialogParts(r.Incoming.ID)...))
}
