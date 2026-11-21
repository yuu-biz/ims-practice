package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// MMTel runs the suite of mmtel.Store.
func MMTel(t *testing.T, newPair func(t *testing.T) (mmtel.Store, mmtel.Store)) {
	run(t, newPair, []testCase[mmtel.Store]{
		{"CallOfEitherLeg", mmtelCallOf},
		{"DeleteCall", mmtelDeleteCall},
		{"ManyCalls", mmtelManyCalls},
	})
}

func callRecord(n int) mmtel.CallRecord {
	return mmtel.CallRecord{
		Incoming: sip.Dialog{ID: sip.DialogID{CallID: fmt.Sprintf("call:%d@192.0.2.10", n), LocalTag: "as-in", RemoteTag: "ue1"},
			State: sip.DialogConfirmed, RemoteURI: impu, RemoteTarget: "sip:ue1@192.0.2.10", RouteSet: []string{"<sip:scscf;lr>"}},
		Outgoing: sip.Dialog{ID: sip.DialogID{CallID: fmt.Sprintf("as-%d", n), LocalTag: "as-out", RemoteTag: "ue2"},
			State: sip.DialogConfirmed, RemoteURI: otherIMPU, RemoteTarget: "sip:ue2@192.0.2.20"},
		ServedUser: impu, SessionCase: sip.SessionCaseOrig, Services: []string{"OIR"},
	}
}

func mmtelCallOf(t *testing.T, a, b mmtel.Store) {
	r := callRecord(1)
	a.PutCall(r)
	for _, id := range []sip.DialogID{r.Incoming.ID, r.Outgoing.ID} {
		if got, ok := b.CallOf(id); !ok || !sameJSON(t, got, r) {
			t.Errorf("CallOf(%v) = %+v, %v; want %+v", id, got, ok, r)
		}
	}
	unknown := r.Outgoing.ID
	unknown.RemoteTag = "other"
	if _, ok := b.CallOf(unknown); ok {
		t.Error("CallOf of a dialog with another remote tag found the call")
	}
}

func mmtelDeleteCall(t *testing.T, a, b mmtel.Store) {
	r := callRecord(1)
	a.PutCall(r)
	b.DeleteCall(r)
	for _, id := range []sip.DialogID{r.Incoming.ID, r.Outgoing.ID} {
		if _, ok := a.CallOf(id); ok {
			t.Errorf("CallOf(%v) after DeleteCall", id)
		}
	}
}

// mmtelManyCalls has two instances set up and end calls at once.
func mmtelManyCalls(t *testing.T, a, b mmtel.Store) {
	parallel(20, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		s.PutCall(callRecord(i))
	})
	for i := range 20 {
		r := callRecord(i)
		if got, ok := a.CallOf(r.Outgoing.ID); !ok || got.Incoming.ID != r.Incoming.ID {
			t.Errorf("CallOf of call %d = %+v, %v", i, got, ok)
		}
	}
}

// MMTelConferences runs the suite of mmtel.ConferenceStore. The instance
// that hosts the focus of a conference is the only one that writes its
// record; the others may read it.
func MMTelConferences(t *testing.T, newPair func(t *testing.T) (mmtel.ConferenceStore, mmtel.ConferenceStore)) {
	run(t, newPair, []testCase[mmtel.ConferenceStore]{
		{"NoConference", mmtelNoConference},
		{"PutReadDelete", mmtelPutReadDelete},
		{"CopiesApart", mmtelConferenceCopiesApart},
		{"HostWritesInOrder", mmtelHostWritesInOrder},
		{"ManyConferences", mmtelManyConferences},
	})
}

const conferenceHost = "mmtel-1.ims.mnc001.mcc001.3gppnetwork.org:5060"

// conferenceRecord is the conference id at the focus of conferenceHost
// with the given number of participants, the creator first, and one
// subscription outside the dialogs, whose last document had version.
func conferenceRecord(id string, participants int, version uint32) mmtel.ConferenceRecord {
	uri := "sip:" + id + "@" + conferenceHost
	r := mmtel.ConferenceRecord{
		ID: id, URI: uri, Creator: impu,
		MRFC: "sip:conf=" + id + "@mrfc.ims.mnc001.mcc001.3gppnetwork.org",
	}
	for i := range participants {
		user := fmt.Sprintf("sip:00101000000000%d@ims.mnc001.mcc001.3gppnetwork.org", i+1)
		contact := fmt.Sprintf("sip:ue%d@192.0.2.%d:5060", i+1, 10+i)
		method := confinfo.DialedOut
		if i == 0 {
			method = confinfo.DialedIn
		}
		r.Participants = append(r.Participants, mmtel.ConferenceParticipant{
			Participant: confinfo.Participant{User: user, DisplayText: user, Endpoint: contact,
				Status: confinfo.Connected, JoiningMethod: method},
			Dialog: sip.Dialog{ID: sip.DialogID{CallID: fmt.Sprintf("%s-party-%d", id, i), LocalTag: "focus", RemoteTag: fmt.Sprintf("ue%d", i+1)},
				State: sip.DialogConfirmed, LocalSeq: 1, RemoteSeq: 2, LocalURI: uri, RemoteURI: user,
				RemoteTarget: contact, RouteSet: []string{"<sip:scscf.ims.mnc001.mcc001.3gppnetwork.org;lr>"}},
			Media: sip.Dialog{ID: sip.DialogID{CallID: fmt.Sprintf("%s-media-%d", id, i), LocalTag: "focus-media", RemoteTag: "mrfc"},
				State: sip.DialogConfirmed, LocalSeq: 1, LocalURI: uri, RemoteURI: r.MRFC, RemoteTarget: "sip:mrfc-1.ims.mnc001.mcc001.3gppnetwork.org:5060"},
		})
	}
	r.Subscriptions = []mmtel.ConferenceSubscription{{
		NotifierSubscription: sip.NotifierSubscription{
			Dialog: sip.Dialog{ID: sip.DialogID{CallID: id + "-subscription", LocalTag: "focus-sub", RemoteTag: "ue1-sub"},
				State: sip.DialogConfirmed, LocalSeq: version, RemoteSeq: 1, LocalURI: uri, RemoteURI: impu, RemoteTarget: "sip:ue1@192.0.2.10:5060"},
			Event: confinfo.EventPackage, Contact: uri, Expires: time.UnixMilli(1_790_003_600_000).UTC(), State: sip.SubscriptionActive,
		},
		Subscriber: impu, Version: version,
	}}
	return r
}

func mmtelNoConference(t *testing.T, a, b mmtel.ConferenceStore) {
	if got, ok := a.Conference("conf-none"); ok {
		t.Errorf("Conference of an empty store = %+v", got)
	}
	b.DeleteConference("conf-none")
	if got, ok := a.Conference("conf-none"); ok {
		t.Errorf("Conference after deleting a missing one = %+v", got)
	}
}

// mmtelPutReadDelete: the host writes a conference and then a change of
// it; another instance reads each in full, and the record is gone once
// the conference has ended.
func mmtelPutReadDelete(t *testing.T, a, b mmtel.ConferenceStore) {
	r := conferenceRecord("conf-a1", 3, 2)
	a.PutConference(r)
	if got, ok := b.Conference(r.ID); !ok || !sameJSON(t, got, r) {
		t.Fatalf("Conference = %+v, %v; want %+v", got, ok, r)
	}
	// The third participant leaves, and the subscriber is told.
	changed := conferenceRecord("conf-a1", 3, 3)
	changed.Participants[2].Status = confinfo.Disconnected
	a.PutConference(changed)
	if got, ok := b.Conference(r.ID); !ok || !sameJSON(t, got, changed) {
		t.Fatalf("Conference after the change = %+v, %v; want %+v", got, ok, changed)
	}
	a.DeleteConference(r.ID)
	if got, ok := b.Conference(r.ID); ok {
		t.Errorf("Conference after DeleteConference = %+v", got)
	}
}

// mmtelConferenceCopiesApart: the record in the store is not the
// caller's. Changing the participants of a record after it was put, or
// of one that was read, changes nothing in the store.
func mmtelConferenceCopiesApart(t *testing.T, a, b mmtel.ConferenceStore) {
	r := conferenceRecord("conf-a1", 2, 1)
	a.PutConference(r)
	want := conferenceRecord("conf-a1", 2, 1)
	r.Participants[1].Status = confinfo.OnHold
	r.Subscriptions[0].Version = 9
	if got, _ := b.Conference(r.ID); !sameJSON(t, got, want) {
		t.Fatalf("the record changed with the caller's copy: %+v", got)
	}
	got, _ := b.Conference(r.ID)
	got.Participants[0].Status = confinfo.Disconnected
	got.Subscriptions[0].Version = 9
	if again, _ := a.Conference(r.ID); !sameJSON(t, again, want) {
		t.Errorf("the record changed with a copy that was read: %+v", again)
	}
}

// mmtelHostWritesInOrder has the host write one change after another,
// as participants join and leave and each change goes to the subscriber
// with the next version of the document, while another instance reads.
// The reader never sees the version go back, nor participants that do
// not belong to the version it reads; and it ends on the last change.
func mmtelHostWritesInOrder(t *testing.T, a, b mmtel.ConferenceStore) {
	const changes = 40
	// participantsAt is how many participants the record of version v
	// has: one more with each change up to six, the most that the AS
	// takes, and then one again.
	participantsAt := func(v uint32) int { return 1 + int(v%6) }
	a.PutConference(conferenceRecord("conf-a1", participantsAt(1), 1))
	done := make(chan struct{})
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		var last uint32
		for {
			select {
			case <-done:
				return
			default:
			}
			got, ok := b.Conference("conf-a1")
			if !ok {
				errs <- fmt.Errorf("the reader lost the conference")
				return
			}
			v := got.Subscriptions[0].Version
			if v < last {
				errs <- fmt.Errorf("the reader saw version %d after %d", v, last)
				return
			}
			if len(got.Participants) != participantsAt(v) {
				errs <- fmt.Errorf("version %d has %d participants, want %d", v, len(got.Participants), participantsAt(v))
				return
			}
			last = v
		}
	}()
	for v := uint32(2); v <= changes; v++ {
		a.PutConference(conferenceRecord("conf-a1", participantsAt(v), v))
	}
	close(done)
	for err := range errs {
		t.Error(err)
	}
	if got, ok := b.Conference("conf-a1"); !ok || !sameJSON(t, got, conferenceRecord("conf-a1", participantsAt(changes), changes)) {
		t.Errorf("Conference after the last change = %+v, %v", got, ok)
	}
}

// mmtelManyConferences has two instances host conferences of their own
// at once; ending some leaves the others as they were.
func mmtelManyConferences(t *testing.T, a, b mmtel.ConferenceStore) {
	const n = 20
	parallel(n, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		s.PutConference(conferenceRecord(fmt.Sprintf("conf-%d", i), 1+i%6, uint32(i+1)))
	})
	for i := 0; i < n; i += 3 {
		b.DeleteConference(fmt.Sprintf("conf-%d", i))
	}
	for i := range n {
		id := fmt.Sprintf("conf-%d", i)
		got, ok := a.Conference(id)
		switch {
		case i%3 == 0 && ok:
			t.Errorf("conference %s is there after DeleteConference", id)
		case i%3 != 0 && (!ok || !sameJSON(t, got, conferenceRecord(id, 1+i%6, uint32(i+1)))):
			t.Errorf("Conference(%s) = %+v, %v", id, got, ok)
		}
	}
}
