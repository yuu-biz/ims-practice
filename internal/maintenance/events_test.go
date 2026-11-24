package maintenance

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// eventsOf is the type of each event, with the peer of its record when
// that is not the P-CSCF's PCRF.
func eventsOf(e AlarmEvents) string {
	var s []string
	for _, ev := range e.Events {
		name := fmt.Sprintf("%d:%s", ev.Number, ev.EventType)
		if ev.AlarmRecord.Peer != pcrfDown.Peer {
			name += "(" + strings.TrimSuffix(string(ev.AlarmRecord.Peer), ".ims.mnc001.mcc001.3gppnetwork.org") + ")"
		}
		s = append(s, name)
	}
	return strings.Join(s, " ")
}

// Each change of the list is one event, numbered in order, with the
// record as the change left it and the time the record gives the change.
func TestTheJournalHoldsEachChangeInOrder(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	empty := tn.node.AlarmEvents(ctx, 0, 0)
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(empty.JournalID) || empty.LastNumber != 0 || empty.Lost || len(empty.Events) != 0 ||
		empty.Node != "pcscf-1" || empty.Instance != "pcscf-1-7d9f" {
		t.Fatalf("a new journal: %+v", empty)
	}

	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	raised := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0]
	// The same alarm again changes nothing, so it is no event.
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	tn.clock.Advance(time.Second)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Critical})
	changed := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0]
	tn.clock.Advance(time.Second)
	mustClear(t, tn.node, pcrfDown)
	cleared := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0]
	tn.clock.Advance(time.Second)
	acked, err := tn.node.SetAckState(raised.AlarmID, AckStateRequest{AckState: Acknowledged, Operator: "alice", AckSystemID: "oam"})
	if err != nil {
		t.Fatal(err)
	}

	e := tn.node.AlarmEvents(ctx, 0, 0)
	if got := eventsOf(e); got != "1:ADDED 2:CHANGED 3:CLEARED 4:ACK_STATE_CHANGED" || e.LastNumber != 4 || e.Lost || e.JournalID != empty.JournalID {
		t.Fatalf("events %q, %+v", got, e)
	}
	for i, want := range []AlarmRecord{raised, changed, cleared, acked} {
		got := e.Events[i]
		if got.AlarmRecord.NotificationID != want.NotificationID || got.AlarmRecord.PerceivedSeverity != want.PerceivedSeverity || got.AlarmRecord.AckState != want.AckState {
			t.Errorf("event %d holds %+v, want the record as the change left it, %+v", got.Number, got.AlarmRecord, want)
		}
	}
	for i, want := range []time.Time{raised.AlarmRaisedTime, *changed.AlarmChangedTime, *cleared.AlarmClearedTime, *acked.AckTime} {
		if !e.Events[i].EventTime.Equal(want) {
			t.Errorf("event %d at %v, want %v", e.Events[i].Number, e.Events[i].EventTime, want)
		}
	}
	if after := tn.node.AlarmEvents(ctx, 2, 0); eventsOf(after) != "3:CLEARED 4:ACK_STATE_CHANGED" || after.Lost {
		t.Errorf("after 2: %+v", after)
	}
	if after := tn.node.AlarmEvents(ctx, 4, 0); len(after.Events) != 0 || after.Lost || after.LastNumber != 4 {
		t.Errorf("after the last: %+v", after)
	}
}

// The journal is a ring: the oldest events leave it, and a reader that
// asks for one of them, or for a number that is not of this journal,
// learns that it has lost some.
func TestTheJournalIsBoundedAndSaysWhatIsLost(t *testing.T) {
	clock := newFakeClock()
	node, err := New(Config{
		Node:           "scscf-1",
		LogLevel:       new(slog.LevelVar),
		Functions:      []Function{{Role: "pcscf"}},
		Configuration:  func() any { return struct{}{} },
		MaxAlarmEvents: 3,
		Clock:          clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		mustRaise(t, node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
		mustClear(t, node, pcrfDown)
	}
	mustRaise(t, node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Minor})
	ctx := context.Background()
	for _, tc := range []struct {
		after uint64
		want  string
		lost  bool
	}{
		{0, "3:CHANGED 4:CLEARED 5:CHANGED", true},
		{1, "3:CHANGED 4:CLEARED 5:CHANGED", true},
		{2, "3:CHANGED 4:CLEARED 5:CHANGED", false},
		{4, "5:CHANGED", false},
		{5, "", false},
		{9, "3:CHANGED 4:CLEARED 5:CHANGED", true},
	} {
		e := node.AlarmEvents(ctx, tc.after, 0)
		if got := eventsOf(e); got != tc.want || e.Lost != tc.lost || e.LastNumber != 5 {
			t.Errorf("after %d: %q lost %v, want %q lost %v", tc.after, got, e.Lost, tc.want, tc.lost)
		}
	}
}

// The full list drops a record and says so in the journal; the end of a
// retention is an acknowledgement by the node, at the time the retention
// ended.
func TestTheJournalHoldsTheDropAndTheEndOfTheRetention(t *testing.T) {
	clock := newFakeClock()
	node, err := New(Config{
		Node:            "scscf-1",
		LogLevel:        new(slog.LevelVar),
		Functions:       []Function{{Role: "scscf"}},
		Configuration:   func() any { return struct{}{} },
		MaxAlarmRecords: 2,
		Clock:           clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer := func(i int) AlarmIdentity {
		id := pcrfDown
		id.Role = "scscf"
		id.Peer = Peer(fmt.Sprintf("hss-%d.ims.mnc001.mcc001.3gppnetwork.org", i))
		return id
	}
	for i := 1; i <= 3; i++ {
		mustRaise(t, node, Alarm{AlarmIdentity: peer(i), PerceivedSeverity: Major})
		clock.Advance(time.Second)
	}
	ctx := context.Background()
	if got := eventsOf(node.AlarmEvents(ctx, 0, 0)); got != "1:ADDED(hss-1) 2:ADDED(hss-2) 3:DROPPED(hss-1) 4:ADDED(hss-3)" {
		t.Fatalf("events %q", got)
	}
	mustClear(t, node, peer(3))
	clock.Advance(time.Second)
	mustClear(t, node, peer(2))
	clock.Advance(DefaultClearedAlarmRetention + time.Minute)
	e := node.AlarmEvents(ctx, 6, 0)
	if got := eventsOf(e); got != "7:ACK_STATE_CHANGED(hss-3) 8:ACK_STATE_CHANGED(hss-2)" {
		t.Fatalf("events after the retention %q", got)
	}
	for _, ev := range e.Events {
		r := ev.AlarmRecord
		if r.AckState != Acknowledged || r.AckUserID != RetentionAckUserID || r.AckSystemID != "scscf-1" ||
			!ev.EventTime.Equal(r.AlarmClearedTime.Add(DefaultClearedAlarmRetention)) || !r.AckTime.Equal(ev.EventTime) {
			t.Errorf("acknowledgement at the end of the retention: %+v", ev)
		}
	}
	if l := node.Alarms(AlarmFilter{}); l.NumOfAlarmRecords != 0 {
		t.Errorf("the acknowledged records are still listed: %+v", l)
	}
}

// Each process has a journal of its own, so that a reader tells a
// node that has started again from the one before.
func TestEachProcessHasItsOwnJournal(t *testing.T) {
	ids := map[string]bool{}
	for range 2 {
		ids[newTestNode(t).node.AlarmEvents(context.Background(), 0, 0).JournalID] = true
	}
	if len(ids) != 2 {
		t.Errorf("two nodes share the journal id %v", ids)
	}
}

// waitForTimer waits, at most five seconds, until the clock holds the
// timer of a reader that waits for an event.
func waitForTimer(t *testing.T, clock *fakeClock) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for clock.pendingTimers() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no reader started to wait for an event within 5 s")
		}
		time.Sleep(time.Millisecond)
	}
}

// answerWithin waits, at most five seconds, for the answer of a reader.
func answerWithin(t *testing.T, answers <-chan AlarmEvents, what string) AlarmEvents {
	t.Helper()
	select {
	case e := <-answers:
		return e
	case <-time.After(5 * time.Second):
		t.Fatalf("no answer %s within 5 s", what)
		return AlarmEvents{}
	}
}

// A reader with nothing new waits for the next event, and no longer
// than it asked to.
func TestAReaderWaitsForTheNextEvent(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})

	answers := make(chan AlarmEvents, 1)
	go func() { answers <- tn.node.AlarmEvents(ctx, 1, 5*time.Second) }()
	waitForTimer(t, tn.clock)
	select {
	case e := <-answers:
		t.Fatalf("answered before any event: %+v", e)
	default:
	}
	mustClear(t, tn.node, pcrfDown)
	if e := answerWithin(t, answers, "to the clearance"); eventsOf(e) != "2:CLEARED" {
		t.Errorf("after the clearance: %+v", e)
	}
	if n := tn.clock.pendingTimers(); n != 0 {
		t.Errorf("the reader left %d timers behind", n)
	}

	go func() { answers <- tn.node.AlarmEvents(ctx, 2, 5*time.Second) }()
	waitForTimer(t, tn.clock)
	tn.clock.Advance(5 * time.Second)
	if e := answerWithin(t, answers, "at the end of the wait"); len(e.Events) != 0 || e.Lost || e.LastNumber != 2 {
		t.Errorf("at the end of the wait: %+v", e)
	}

	// The retention of a cleared alarm ends while the reader waits: the
	// answer at the end of the wait holds the acknowledgement.
	go func() { answers <- tn.node.AlarmEvents(ctx, 2, 2*DefaultClearedAlarmRetention) }()
	waitForTimer(t, tn.clock)
	tn.clock.Advance(2 * DefaultClearedAlarmRetention)
	if e := answerWithin(t, answers, "at the end of a wait in which the retention ended"); eventsOf(e) != "3:ACK_STATE_CHANGED" {
		t.Errorf("at the end of a wait in which the retention ended: %+v", e)
	}

	cancelled, cancel := context.WithCancel(ctx)
	go func() { answers <- tn.node.AlarmEvents(cancelled, 3, 5*time.Second) }()
	waitForTimer(t, tn.clock)
	cancel()
	if e := answerWithin(t, answers, "when the reader went away"); len(e.Events) != 0 || e.LastNumber != 3 {
		t.Errorf("when the reader went away: %+v", e)
	}
}

// The HTTP API waits as the Node does, and reads its query strictly.
func TestAlarmEventsOverTheAPI(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	answers := make(chan AlarmEvents, 1)
	go func() {
		e, err := tn.client.AlarmEvents(ctx, AlarmEventsQuery{After: 0, Wait: 30 * time.Second})
		if err != nil {
			t.Error(err)
		}
		answers <- e
	}()
	waitForTimer(t, tn.clock)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major, AdditionalText: "no answer to 3 DWR"})
	e := answerWithin(t, answers, "over the API")
	if eventsOf(e) != "1:ADDED" || e.Events[0].AlarmRecord.AdditionalText != "no answer to 3 DWR" {
		t.Errorf("over the API: %+v", e)
	}

	for _, query := range []string{"wait=31", "wait=-1", "wait=1.5", "after=-1", "after=x", "since=1", "after=1&after=2"} {
		err := tn.client.do(ctx, http.MethodGet, AlarmEventsPath+"?"+query, nil, nil)
		wantAPIError(t, err, http.StatusBadRequest, strings.Split(query, "=")[0])
	}
	q, _ := url.ParseQuery("after=17&wait=5")
	if p, err := ParseAlarmEventsQuery(q); err != nil || p != (AlarmEventsQuery{After: 17, Wait: 5 * time.Second}) || p.Query().Encode() != "after=17&wait=5" {
		t.Errorf("round trip: %+v %v", p, err)
	}
}

func TestNewChecksTheJournalBound(t *testing.T) {
	if _, err := New(Config{Node: "n", LogLevel: new(slog.LevelVar), Functions: []Function{{Role: "pcscf"}},
		Configuration: func() any { return nil }, MaxAlarmEvents: -1}); err == nil {
		t.Error("New took a negative bound of the journal")
	}
}

// Readers that wait and roles that raise, side by side: every reader
// sees the numbers in order, without a gap or a repeat.
func TestTheJournalUnderConcurrentUse(t *testing.T) {
	tn := newTestNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			var next uint64 = 1
			for next <= 400 {
				e := tn.node.AlarmEvents(ctx, next-1, time.Hour)
				if ctx.Err() != nil {
					return
				}
				if e.Lost {
					t.Errorf("a reader that keeps up lost events after %d", next-1)
					return
				}
				for _, ev := range e.Events {
					if ev.Number != next {
						t.Errorf("event %d after %d", ev.Number, next-1)
						return
					}
					next++
				}
			}
		})
	}
	var roles sync.WaitGroup
	for i := range 4 {
		roles.Go(func() {
			id := pcrfDown
			id.Peer = Peer(fmt.Sprintf("pcrf-%d", i))
			for range 50 {
				_ = tn.node.RaiseAlarm(Alarm{AlarmIdentity: id, PerceivedSeverity: Major})
				_ = tn.node.ClearAlarm(id)
			}
		})
	}
	roles.Wait()
	done := make(chan struct{})
	go func() { readers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Errorf("the readers did not see the 400 events within 10 s; the journal ends at %d", tn.node.AlarmEvents(context.Background(), 0, 0).LastNumber)
	}
}
