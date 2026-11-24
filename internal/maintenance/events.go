package maintenance

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// AlarmEventType is what changed in the alarm list. Each type but
// DROPPED is the change that one notification of TS 28.111 reports, so
// that the OAM server, which sends the notifications, makes one from
// each event.
type AlarmEventType string

const (
	// AlarmAdded: a record was added to the list (notifyNewAlarm, TS
	// 28.111 8.2).
	AlarmAdded AlarmEventType = "ADDED"
	// AlarmChanged: the severity of a record changed, to any but
	// CLEARED, or its additional text did, as when a cleared record is
	// raised again (notifyChangedAlarmGeneral, 8.4). A cleared record
	// that is raised again is the same alarm, so the list updates it and
	// adds no new one (6.14).
	AlarmChanged AlarmEventType = "CHANGED"
	// AlarmCleared: the severity of a record became CLEARED
	// (notifyClearedAlarm, 8.3).
	AlarmCleared AlarmEventType = "CLEARED"
	// AckStateChanged: the acknowledgement of a record changed
	// (notifyAckStateChanged, 8.8), by an operator or at the end of the
	// retention (RetentionAckUserID).
	AckStateChanged AlarmEventType = "ACK_STATE_CHANGED"
	// AlarmDropped: the list was full and removed the record to make
	// room (7.3.2.1). No notification of an alarm reports this: the OSS
	// learns it from the OAM server, which tells it that its copy of the
	// list needs aligning.
	AlarmDropped AlarmEventType = "DROPPED"
)

// AlarmEvent is one change of the alarm list, as the journal of the
// node keeps it.
type AlarmEvent struct {
	// Number orders the events of one journal: 1 for the first, and one
	// more for each after it.
	Number    uint64         `json:"number"`
	EventType AlarmEventType `json:"event_type"`
	// EventTime is when the change happened: the time the record gives
	// it, such as alarmClearedTime for CLEARED.
	EventTime time.Time `json:"event_time"`
	// AlarmRecord is the record as the change left it, with the
	// notification_id of this change; for DROPPED, as it was when the
	// list removed it.
	AlarmRecord AlarmRecord `json:"alarm_record"`
}

// AlarmEvents is the answer to GET /maintenance/v1/alarm-events.
type AlarmEvents struct {
	Node     string `json:"node"`
	Instance string `json:"instance,omitempty"`
	// JournalID is random, chosen when the process of the node starts.
	// A reader that finds another one than before knows that the node
	// has started again, with a journal and an alarm list that begin
	// anew. Instance cannot tell this: it is the name of the host, which
	// stays when the node starts again in the same container.
	JournalID string `json:"journal_id"`
	// LastNumber is the number of the newest event of the journal, 0
	// before the first.
	LastNumber uint64 `json:"last_number"`
	// Lost says that the journal does not hold every event after the
	// number that the request named: the oldest of them have left it to
	// make room, or the number is not one of this journal. Events then
	// starts with the oldest event that the journal holds.
	Lost bool `json:"lost"`
	// Events are the events after the number that the request named,
	// the oldest first.
	Events []AlarmEvent `json:"events"`
}

// The bounds of the event journal.
const (
	// DefaultMaxAlarmEvents bounds the journal: four events for each
	// record of a full list, its raise, a change, its clearance and its
	// acknowledgement, so that a reader that is a little behind loses
	// none.
	DefaultMaxAlarmEvents = 4 * DefaultMaxAlarmRecords
	// MaxAlarmEventsWait bounds how long GET
	// /maintenance/v1/alarm-events waits for an event.
	MaxAlarmEventsWait = 30 * time.Second
)

// alarmJournal is the ring buffer of the events of the alarm list. The
// lock of the list guards it, so that the events are in the order of
// the changes of the list.
type alarmJournal struct {
	id     string
	max    int
	events []AlarmEvent
	// head is the index of the oldest event once the ring is full.
	head int
	// last is the number of the newest event, 0 before the first.
	last uint64
	// appended is closed, and replaced, each time an event is added. A
	// reader that waits for the next event waits on it.
	appended chan struct{}
}

func newAlarmJournal(max int) *alarmJournal {
	return &alarmJournal{id: newAlarmID(), max: max, events: make([]AlarmEvent, 0, max), appended: make(chan struct{})}
}

// add puts the event of r in the journal, in the place of the oldest
// when the journal is full.
func (j *alarmJournal) add(t AlarmEventType, at time.Time, r AlarmRecord) {
	j.last++
	e := AlarmEvent{Number: j.last, EventType: t, EventTime: at, AlarmRecord: r}
	if len(j.events) < j.max {
		j.events = append(j.events, e)
	} else {
		j.events[j.head] = e
		j.head = (j.head + 1) % j.max
	}
	close(j.appended)
	j.appended = make(chan struct{})
}

// after is the events after the number n, the oldest first, and whether
// some of them are lost: no longer in the journal, or n is no number of
// it.
func (j *alarmJournal) after(n uint64) ([]AlarmEvent, bool) {
	oldest := j.last - uint64(len(j.events)) + 1
	lost := n+1 < oldest || n > j.last
	from := n + 1
	if lost {
		from = oldest
	}
	events := []AlarmEvent{}
	for k := from; k <= j.last; k++ {
		events = append(events, j.events[(j.head+int(k-oldest))%len(j.events)])
	}
	return events, lost
}

// AlarmEvents answers with the events of the journal after the number
// after. When there is none yet, it waits for one, at most wait, which
// the caller bounds (MaxAlarmEventsWait), or until ctx ends, and then
// answers with what there is, which may be none. A reader that asks
// again with the LastNumber of the answer gets each event once, in the
// order of the changes of the list; Lost tells it when it cannot.
func (n *Node) AlarmEvents(ctx context.Context, after uint64, wait time.Duration) AlarmEvents {
	expired := make(chan struct{})
	if wait > 0 {
		t := n.clock.AfterFunc(wait, func() { close(expired) })
		defer t.Stop()
	} else {
		close(expired)
	}
	for {
		answer := AlarmEvents{Node: n.node, Instance: n.instance}
		appended := n.alarms.readEvents(after, n.clock.Now(), &answer)
		if len(answer.Events) > 0 || answer.Lost {
			return answer
		}
		select {
		case <-appended:
		case <-expired:
			return n.lastRead(after)
		case <-ctx.Done():
			return n.lastRead(after)
		}
	}
}

// lastRead is the answer at the end of a wait. The retention of a
// cleared alarm may have ended while the reader waited: nothing adds
// its acknowledgement to the journal until the list is read again
// (expire), so the list is read once more before the answer.
func (n *Node) lastRead(after uint64) AlarmEvents {
	answer := AlarmEvents{Node: n.node, Instance: n.instance}
	n.alarms.readEvents(after, n.clock.Now(), &answer)
	return answer
}

// readEvents fills into with the events after the number after, and
// returns what closes when the next event is added.
func (l *alarmList) readEvents(after uint64, now time.Time, into *AlarmEvents) <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	into.JournalID = l.journal.id
	into.LastNumber = l.journal.last
	into.Events, into.Lost = l.journal.after(after)
	return l.journal.appended
}

// AlarmEventsQuery is the query of GET /maintenance/v1/alarm-events.
type AlarmEventsQuery struct {
	// After is the number of the last event that the reader has; 0 for
	// all that the journal holds.
	After uint64
	// Wait is how long the node waits for an event when there is none
	// after After, in whole seconds up to MaxAlarmEventsWait; 0 to answer
	// at once.
	Wait time.Duration
}

// The query parameters of GET /maintenance/v1/alarm-events.
const (
	afterParameter = "after"
	waitParameter  = "wait"
)

// ParseAlarmEventsQuery reads the query of GET
// /maintenance/v1/alarm-events: after=N and wait=S, in seconds. A
// parameter that it does not know is an error.
func ParseAlarmEventsQuery(q url.Values) (AlarmEventsQuery, error) {
	var r AlarmEventsQuery
	for name, values := range q {
		if len(values) != 1 {
			return AlarmEventsQuery{}, fmt.Errorf("%w: query parameter %s more than once", ErrInvalidRequest, name)
		}
		v := values[0]
		switch name {
		case afterParameter:
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return AlarmEventsQuery{}, fmt.Errorf("%w: after: %q is not the number of an event", ErrInvalidRequest, v)
			}
			r.After = n
		case waitParameter:
			s, err := strconv.Atoi(v)
			if err != nil || s < 0 || time.Duration(s)*time.Second > MaxAlarmEventsWait {
				return AlarmEventsQuery{}, fmt.Errorf("%w: wait: %q is not a number of seconds from 0 to %d", ErrInvalidRequest, v, int(MaxAlarmEventsWait/time.Second))
			}
			r.Wait = time.Duration(s) * time.Second
		default:
			return AlarmEventsQuery{}, fmt.Errorf("%w: unknown query parameter %s; the parameters are %s and %s", ErrInvalidRequest, name, afterParameter, waitParameter)
		}
	}
	return r, nil
}

// Query is the query of a request, which ParseAlarmEventsQuery reads
// back. Wait goes in whole seconds.
func (q AlarmEventsQuery) Query() url.Values {
	v := url.Values{}
	if q.After != 0 {
		v.Set(afterParameter, strconv.FormatUint(q.After, 10))
	}
	if q.Wait != 0 {
		v.Set(waitParameter, strconv.Itoa(int(q.Wait/time.Second)))
	}
	return v
}
