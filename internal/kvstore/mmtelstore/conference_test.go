package mmtelstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mmtelstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

func TestConferenceConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.MMTelConferences(t, func(*testing.T) (mmtel.ConferenceStore, mmtel.ConferenceStore) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return mmtelstore.New(kv, cfg), mmtelstore.New(kv, cfg)
			})
		})
	}
}

// TestMemoryConferenceStore runs the suite on the ConferenceStore of the
// mmtel package, which two instances share by sharing the one value. It
// lives here rather than in storetest's memory_test.go, so that the
// suite of the role and the tests that run it stay together.
func TestMemoryConferenceStore(t *testing.T) {
	storetest.MMTelConferences(t, func(*testing.T) (mmtel.ConferenceStore, mmtel.ConferenceStore) {
		s := mmtel.NewMemoryStore()
		return s, s
	})
}

// conference is a record of the conference conf-a1 whose subscriber has
// had the document of version.
func conference(version uint32, status confinfo.EndpointStatus) mmtel.ConferenceRecord {
	uri := "sip:conf-a1@mmtel-1.ims.mnc001.mcc001.3gppnetwork.org:5060"
	return mmtel.ConferenceRecord{
		ID: "conf-a1", URI: uri, Creator: "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org",
		MRFC: "sip:conf=conf-a1@mrfc.ims.mnc001.mcc001.3gppnetwork.org",
		Participants: []mmtel.ConferenceParticipant{{
			Participant: confinfo.Participant{User: "sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org", Status: status, JoiningMethod: confinfo.DialedOut},
			Dialog:      sip.Dialog{ID: sip.DialogID{CallID: "party-2", LocalTag: "focus", RemoteTag: "ue2"}, State: sip.DialogConfirmed},
		}},
		Subscriptions: []mmtel.ConferenceSubscription{{
			NotifierSubscription: sip.NotifierSubscription{Dialog: sip.Dialog{ID: sip.DialogID{CallID: "sub-1", LocalTag: "focus", RemoteTag: "ue1"}},
				Event: confinfo.EventPackage, State: sip.SubscriptionActive},
			Version: version,
		}},
	}
}

// lateWrites is the store as the host instance sees it when the answer
// to its first write is lost: the caller gives up on it at once, and
// the write reaches the server only when release is closed. landed then
// reports how the write ended.
type lateWrites struct {
	kvstore.Store
	once    sync.Once
	release chan struct{}
	landed  chan error
}

// late reports whether this is the first write, and if so sends it on
// later.
func (l *lateWrites) late(write func(context.Context) error) bool {
	first := false
	l.once.Do(func() { first = true })
	if first {
		go func() {
			<-l.release
			l.landed <- write(context.Background())
		}()
	}
	return first
}

func (l *lateWrites) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	if l.late(func(ctx context.Context) error { _, err := l.Store.Put(ctx, key, value, ttl); return err }) {
		return "", context.DeadlineExceeded
	}
	return l.Store.Put(ctx, key, value, ttl)
}

func (l *lateWrites) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	if l.late(func(ctx context.Context) error {
		_, err := l.Store.CompareAndSwap(ctx, key, version, value, ttl)
		return err
	}) {
		return "", context.DeadlineExceeded
	}
	return l.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

// TestALateWriteDoesNotUndoALaterOne: the host puts a change of the
// conference whose answer is lost, and then the next change. When the
// first one reaches the store after all, the record keeps the later
// change, with the later version of the document: what a subscriber was
// last sent.
func TestALateWriteDoesNotUndoALaterOne(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			mmtelstore.New(kv, cfg).PutConference(conference(1, confinfo.Connected))

			slow := &lateWrites{Store: kv, release: make(chan struct{}), landed: make(chan error, 1)}
			host := mmtelstore.New(slow, cfg)
			host.PutConference(conference(2, confinfo.OnHold))
			host.PutConference(conference(3, confinfo.Connected))
			close(slow.release)
			if err := <-slow.landed; !errors.Is(err, kvstore.ErrConflict) {
				t.Fatalf("the late write ended with %v, want a conflict", err)
			}

			got, ok := mmtelstore.New(kv, cfg).Conference("conf-a1")
			if !ok || got.Subscriptions[0].Version != 3 || got.Participants[0].Status != confinfo.Connected {
				t.Errorf("Conference = %+v, %v; want the change of version 3", got, ok)
			}
		})
	}
}

// TestAWriteThatFailsLeavesTheRecordAsItWas: the record is one item, so
// a host that stops before or during a write leaves the last state that
// it wrote in full, never part of a change.
func TestAWriteThatFailsLeavesTheRecordAsItWas(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			mmtelstore.New(kv, cfg).PutConference(conference(1, confinfo.Connected))
			mmtelstore.New(storetest.StopAfter(kv, 0), cfg).PutConference(conference(2, confinfo.Disconnected))

			got, ok := mmtelstore.New(kv, cfg).Conference("conf-a1")
			if !ok || got.Subscriptions[0].Version != 1 || got.Participants[0].Status != confinfo.Connected {
				t.Errorf("Conference after the failed write = %+v, %v; want the record of version 1", got, ok)
			}
		})
	}
}
