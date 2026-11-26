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
// last to leave removes it. If that removal fails, the item stays until
// a party of the conference finds that the MRFP holds its context no
// more, and the MRFC that the party reached removes it. An instance that
// is killed while a party of the conference is in the mix, or whose
// process exits before the goroutine of that party has subtracted its
// termination, leaves the termination at the MRFP (package mrfc says
// why, at leg.release). That context then never empties, and the item
// stays as long as the MRFP keeps it.
package mrfcstore

import (
	"context"
	"errors"
	"fmt"

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
func (s *Store) Conference(ctx context.Context, id string) (mrfc.ConferenceMedia, bool, error) {
	var media mrfc.ConferenceMedia
	_, err := kvjson.Read(ctx, s.c, s.key(id), &media)
	if errors.Is(err, kvstore.ErrNotFound) {
		return mrfc.ConferenceMedia{}, false, nil
	}
	if err != nil {
		return mrfc.ConferenceMedia{}, false, err
	}
	return media, true, nil
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
// has created the conference, and CreateConference returns the failure.
// The MRFC then gives up the context that it made and refuses the
// party, rather than mix the parties of one conference in two contexts
// that do not hear each other.
func (s *Store) CreateConference(ctx context.Context, id string, media mrfc.ConferenceMedia) (mrfc.ConferenceMedia, bool, error) {
	key := s.key(id)
	for range createAttempts {
		_, err := kvjson.CompareAndSwap(ctx, s.c, key, "", media, 0)
		if err == nil {
			return media, true, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return mrfc.ConferenceMedia{}, false, err
		}
		var kept mrfc.ConferenceMedia
		_, err = kvjson.Read(ctx, s.c, key, &kept)
		if err == nil {
			return kept, false, nil
		}
		if !errors.Is(err, kvstore.ErrNotFound) {
			return mrfc.ConferenceMedia{}, false, err
		}
		// Removed between the two operations: try again from the start.
	}
	return mrfc.ConferenceMedia{}, false, fmt.Errorf("mrfcstore: conference %q was removed each of the %d times another instance created it first", id, createAttempts)
}

// RemoveConference reads the item and removes it only if it holds media,
// and only at the version it read (a compare and delete). An instance
// whose last party has left thus never removes a conference that
// another instance created after it, even when that one writes between
// the read and the removal: the version then differs, and the newer
// conference stays.
func (s *Store) RemoveConference(ctx context.Context, id string, media mrfc.ConferenceMedia) error {
	key := s.key(id)
	var kept mrfc.ConferenceMedia
	version, err := kvjson.Read(ctx, s.c, key, &kept)
	if errors.Is(err, kvstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if kept != media {
		return nil
	}
	if err := s.c.RemoveIf(ctx, key, version); err != nil && !errors.Is(err, kvstore.ErrConflict) {
		return err
	}
	return nil
}
