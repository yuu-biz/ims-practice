// Package mrfcstore is an mrfc.Store on a kvstore.Store, so that the
// MRFC instances share which MRFP and which context hold the mix of each
// conference: a party that reaches any instance joins the same mix.
//
// The one item and its key, after the prefix of the Config and "mrfc:":
//
//	conference:<conference ID>   the ConferenceMedia, as JSON
//
// Every operation reads or writes that one key. None walks the keys of
// the store, so the work of an operation does not grow with the number
// of conferences or of instances.
//
// The rest of what an MRFC holds stays in the process of the instance
// that took the INVITE: the dialogs of announcements and of conference
// parties, their timers, and the waits for the Notify by which the MRFP
// reports that an announcement has ended. The Contact of each dialog
// names that instance, so every request within the dialog comes back
// there, and none of that state is of use to another instance.
//
// The item has no time to live. A conference lasts as long as its
// parties stay, however long that is; the instance whose party is the
// last to leave removes it.
package mrfcstore

import (
	"errors"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// Store is an mrfc.Store on a kvstore.Store.
type Store struct {
	c *kvjson.Client
}

var _ mrfc.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their conferences.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "mrfc")}
}

func (s *Store) key(id string) string { return s.c.Key("conference", id) }

// Conference reads the item of the conference id.
func (s *Store) Conference(id string) (mrfc.ConferenceMedia, bool) {
	var media mrfc.ConferenceMedia
	_, ok := kvjson.Get(s.c, s.key(id), &media)
	return media, ok
}

// createAttempts bounds how often CreateConference starts over when the
// conference it lost to was removed before it could read it. Each round
// means that the last party of a conference left in between, which
// happens once per conference, so a few rounds are plenty.
const createAttempts = 10

// CreateConference writes media only if there is no item, by a compare
// and swap with no version: of several instances that create the same
// conference at once, only one writes. The others read what that one
// wrote, and join its context.
//
// When the store fails, the instance cannot tell whether another one
// has created the conference. CreateConference then reports that it did
// not put media there, and returns none: the MRFC gives up the context
// that it made and tries again, rather than mix the parties of one
// conference in two contexts that do not hear each other.
func (s *Store) CreateConference(id string, media mrfc.ConferenceMedia) (mrfc.ConferenceMedia, bool) {
	key := s.key(id)
	for range createAttempts {
		_, err := kvjson.Swap(s.c, key, "", media, 0)
		if err == nil {
			return media, true
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return mrfc.ConferenceMedia{}, false
		}
		var kept mrfc.ConferenceMedia
		if _, ok := kvjson.Get(s.c, key, &kept); ok {
			return kept, false
		}
		// Removed between the two operations, or the read failed: try
		// again from the start.
	}
	return mrfc.ConferenceMedia{}, false
}

// RemoveConference reads the item and removes it only if it holds media,
// and only at the version it read (a compare and delete). An instance
// whose last party has left thus never removes a conference that
// another instance created after it, even when that one writes between
// the read and the removal: the version then differs, and the newer
// conference stays.
func (s *Store) RemoveConference(id string, media mrfc.ConferenceMedia) {
	key := s.key(id)
	var kept mrfc.ConferenceMedia
	version, ok := kvjson.Get(s.c, key, &kept)
	if !ok || kept != media {
		return
	}
	s.c.DeleteIf(key, version)
}
