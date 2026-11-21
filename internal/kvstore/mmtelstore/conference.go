package mmtelstore

import (
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
)

// The Store is also the mmtel.ConferenceStore. The records of the
// conferences are items of their own, after the prefix of the Config
// and "mmtel:":
//
//	conference:<conference ID>   ConferenceRecord, as JSON
//
// Each record is read and written by its conference ID only, so no
// operation walks the conferences, whatever their number.
//
// A conference is written only by the instance that hosts its focus.
// The conference URI names that instance, so every request for the
// conference comes back to it, and that instance changes the record
// under the conference's own lock, one change at a time. No other
// instance writes the record, and none takes the conference over, so
// a write replaces the whole record without losing a change of anyone
// else.
//
// A record has no time to live: a conference lasts as long as its
// participants stay. The subscriptions in it are not timed here either;
// the focus ends them with the conference.
//
// SIMPLIFIED: TS 24.147 5.3.2.7. A focus terminates a conference by
// removing its participants and deallocating its URI. When the instance
// that hosts the focus stops, nothing does either: the dialogs of the
// conference were in its process, and no other instance continues a
// conference that another one hosted. The record then stays in the
// store, as the record of a call does when no instance sees its BYE.
var _ mmtel.ConferenceStore = (*Store)(nil)

func (s *Store) conferenceKey(id string) string { return s.c.Key("conference", id) }

func noTimeToLive(mmtel.ConferenceRecord) time.Duration { return 0 }

// PutConference writes r by a compare and swap on the version that it
// reads first (kvjson.Upsert), not by a plain put. A write whose answer
// did not come back in time may still reach the store later, after the
// host's next write. Written over the version that it read, it then
// fails, and the record keeps the later state: the later participants,
// and the later version of each subscription's conference document,
// which must only grow (RFC 4575 4.3). A plain put would write the older
// state over the newer one.
func (s *Store) PutConference(r mmtel.ConferenceRecord) {
	kvjson.Upsert(s.c, s.conferenceKey(r.ID), noTimeToLive, func(kept *mmtel.ConferenceRecord) { *kept = r })
}

// Conference reads the record of the conference id.
func (s *Store) Conference(id string) (mmtel.ConferenceRecord, bool) {
	var r mmtel.ConferenceRecord
	_, ok := kvjson.Get(s.c, s.conferenceKey(id), &r)
	return r, ok
}

// DeleteConference removes the record. A late write that read a record
// fails after it, as above, since there is no record at that version.
// Only a late first write, which read none, could still land after it
// and leave a record behind; conference IDs are random and never used
// again, so that record names no conference that is up.
func (s *Store) DeleteConference(id string) {
	s.c.Delete(s.conferenceKey(id))
}
