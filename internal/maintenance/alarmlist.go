package maintenance

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	mathrand "math/rand/v2"
	"slices"
	"sync"
	"time"
)

// The defaults of Config for the alarm list.
const (
	// DefaultMaxAlarmRecords bounds the alarm list. The list holds one
	// record per condition of a link or a role, never one per user, so
	// a node has far fewer; the bound is for a fault that keeps making
	// new identities.
	DefaultMaxAlarmRecords = 256
	// DefaultClearedAlarmRetention is how long a cleared alarm that no
	// operator acknowledges stays in the list before the node
	// acknowledges it itself.
	DefaultClearedAlarmRetention = time.Hour
)

// RetentionAckUserID is the ackUserId with which the node acknowledges
// a cleared alarm that nobody acknowledged within the retention
// (Config.ClearedAlarmRetention). The name is this project's own, so
// that a reader of the record or of its notification tells this
// acknowledgement from one of an operator.
const RetentionAckUserID = "retention"

// alarmList is the alarm list of a node (TS 28.111 7.3.2), with the
// journal of its changes. Its lock guards this node's alarms and events
// only, at most max of the one and the bound of the journal of the
// other, never the state of a user.
type alarmList struct {
	// node is the name of the node, which acknowledges a record at the
	// end of its retention as its ackSystemId.
	node      string
	max       int
	retention time.Duration
	log       *slog.Logger

	mu               sync.Mutex
	byIdentity       map[AlarmIdentity]*AlarmRecord
	byID             map[string]*AlarmRecord
	lastModification time.Time
	dropped          uint64
	journal          *alarmJournal
}

func newAlarmList(node string, max, maxEvents int, retention time.Duration, start time.Time, log *slog.Logger) *alarmList {
	return &alarmList{
		journal:          newAlarmJournal(maxEvents),
		node:             node,
		max:              max,
		retention:        retention,
		log:              log,
		byIdentity:       map[AlarmIdentity]*AlarmRecord{},
		byID:             map[string]*AlarmRecord{},
		lastModification: start,
	}
}

// RaiseAlarm puts the alarm in the node's alarm list, as the node does
// when it detects a fault (TS 28.111 6.14): when the list has a record
// with the same identity (6.4), the record is updated, and otherwise a
// new one is added, UNACKNOWLEDGED (6.7). Raising an alarm that the list
// has with the same severity and text changes nothing, so a role may
// raise it each time it sees the fault. A cleared record that is raised
// again is no longer cleared, and keeps its id and raised time.
//
// When the list is full, a new record takes the place of the oldest
// cleared one or, when none is cleared, of the oldest one (TS 28.111
// 7.3.2.1). The answers count such records in dropped_records.
//
// It refuses an alarm with an unknown value, a role that the node does
// not run, a severity of CLEARED (ClearAlarm clears), or a field that may
// hold a secret or a subscriber (AlarmIdentity, Alarm).
func (n *Node) RaiseAlarm(a Alarm) error {
	if err := n.checkIdentity(a.AlarmIdentity); err != nil {
		return err
	}
	if _, ok := severityRanks[a.PerceivedSeverity]; !ok || a.PerceivedSeverity == Cleared {
		return fmt.Errorf("%w: severity %q is none of %s, %s, %s, %s, %s; ClearAlarm clears an alarm",
			ErrInvalidAlarm, a.PerceivedSeverity, Critical, Major, Minor, Warning, Indeterminate)
	}
	if err := checkAlarmText(a.AdditionalText); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAlarm, err)
	}
	n.alarms.raise(a, n.clock.Now())
	return nil
}

// ClearAlarm clears the alarm of id, as the node does when the
// condition is no longer present (TS 28.111 6.8: every alarm here is
// cleared by the node that raised it). The record stays in the list,
// CLEARED, until an operator acknowledges it (6.14) or, when nobody
// does, until the node acknowledges it itself at the end of
// Config.ClearedAlarmRetention; an acknowledged one leaves at once
// (7.3.1.1). Clearing an alarm that the list does not have, or has
// cleared already, changes nothing, so a role may clear it each time it
// sees the condition gone.
func (n *Node) ClearAlarm(id AlarmIdentity) error {
	if err := n.checkIdentity(id); err != nil {
		return err
	}
	n.alarms.clear(id, n.clock.Now())
	return nil
}

// Alarms is the alarm list, with the records that the filter keeps.
func (n *Node) Alarms(f AlarmFilter) AlarmList {
	l := AlarmList{Node: n.node, Instance: n.instance, AlarmRecords: []AlarmRecord{}}
	n.alarms.read(f, n.clock.Now(), &l)
	return l
}

// SetAckState acknowledges an alarm, or takes the acknowledgement back
// (TS 28.111 6.7). An acknowledged alarm that is cleared leaves the list
// (6.14); the answer is the record as it was when it left. Asking for
// the present state changes nothing.
func (n *Node) SetAckState(alarmID string, req AckStateRequest) (AlarmRecord, error) {
	change, err := n.SetAckStates(AckStatesRequest{AckStates: map[string]AckStateRequest{alarmID: req}})
	if err != nil {
		return AlarmRecord{}, err
	}
	return change.AlarmRecords[0], nil
}

// SetAckStates changes the acknowledgement of several alarms of the
// list at once, each as SetAckState does: all of them, or none when one
// of them is not in the list or asks for no state that there is. The
// list checks every one before it changes any, under one hold of its
// lock, so that an OSS that acknowledges several alarms of the node in
// one request sees the request done as a whole or not at all.
func (n *Node) SetAckStates(req AckStatesRequest) (AckStatesChange, error) {
	if len(req.AckStates) == 0 {
		return AckStatesChange{}, fmt.Errorf("%w: ack_states names no alarm", ErrInvalidRequest)
	}
	for id, r := range req.AckStates {
		if err := r.Check(); err != nil {
			return AckStatesChange{}, fmt.Errorf("%w: ack_states: %s: %v", ErrInvalidRequest, id, err)
		}
	}
	return n.alarms.setAckStates(req.AckStates, n.clock.Now())
}

func (l *alarmList) raise(a Alarm, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	if r := l.byIdentity[a.AlarmIdentity]; r != nil {
		if r.PerceivedSeverity == a.PerceivedSeverity && r.AdditionalText == a.AdditionalText {
			return
		}
		r.PerceivedSeverity = a.PerceivedSeverity
		r.AdditionalText = a.AdditionalText
		r.AlarmChangedTime = &now
		r.AlarmClearedTime = nil
		r.NotificationID = newNotificationID()
		l.lastModification = now
		l.journal.add(AlarmChanged, now, *r)
		l.log.Warn("alarm changed", alarmAttrs(r)...)
		return
	}
	if len(l.byID) >= l.max {
		l.drop(now)
	}
	r := &AlarmRecord{
		AlarmID:           newAlarmID(),
		ObjectInstance:    ObjectInstance(l.node, a.AlarmIdentity),
		Role:              a.Role,
		Peer:              a.Peer,
		NotificationID:    newNotificationID(),
		AlarmRaisedTime:   now,
		AlarmType:         a.AlarmType,
		ProbableCause:     a.ProbableCause,
		SpecificProblem:   a.SpecificProblem,
		PerceivedSeverity: a.PerceivedSeverity,
		AdditionalText:    a.AdditionalText,
		AckState:          Unacknowledged,
	}
	l.byIdentity[a.AlarmIdentity] = r
	l.byID[r.AlarmID] = r
	l.lastModification = now
	l.journal.add(AlarmAdded, now, *r)
	l.log.Warn("alarm raised", alarmAttrs(r)...)
}

func (l *alarmList) clear(id AlarmIdentity, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	r := l.byIdentity[id]
	if r == nil || r.PerceivedSeverity == Cleared {
		return
	}
	r.PerceivedSeverity = Cleared
	r.AlarmChangedTime = &now
	r.AlarmClearedTime = &now
	r.NotificationID = newNotificationID()
	l.lastModification = now
	l.journal.add(AlarmCleared, now, *r)
	l.log.Info("alarm cleared", alarmAttrs(r)...)
	if r.AckState == Acknowledged {
		l.remove(r)
	}
}

func (l *alarmList) setAckStates(reqs map[string]AckStateRequest, now time.Time) (AckStatesChange, error) {
	ids := slices.Sorted(maps.Keys(reqs))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	for _, id := range ids {
		if l.byID[id] == nil {
			return AckStatesChange{}, fmt.Errorf("%w: %q", ErrUnknownAlarm, id)
		}
	}
	change := AckStatesChange{AlarmRecords: make([]AlarmRecord, 0, len(ids))}
	for _, id := range ids {
		change.AlarmRecords = append(change.AlarmRecords, l.setAckState(l.byID[id], reqs[id], now))
	}
	return change, nil
}

// setAckState changes the acknowledgement of r, with l.mu held, and
// returns the record after the change.
func (l *alarmList) setAckState(r *AlarmRecord, req AckStateRequest, now time.Time) AlarmRecord {
	if r.AckState == req.AckState {
		return *r
	}
	// The operator's change sets ackTime but not alarmChangedTime, which
	// only the alarmed resource changes (TS 28.111 7.4.1).
	r.AckState = req.AckState
	r.AckTime = &now
	r.AckUserID = req.Operator
	r.AckSystemID = req.AckSystemID
	r.NotificationID = newNotificationID()
	l.lastModification = now
	l.journal.add(AckStateChanged, now, *r)
	l.log.Info("alarm acknowledgement changed", append(alarmAttrs(r), "operator", req.Operator, "ack_system_id", req.AckSystemID)...)
	if r.AckState == Acknowledged && r.PerceivedSeverity == Cleared {
		l.remove(r)
	}
	return *r
}

func (l *alarmList) read(f AlarmFilter, now time.Time, into *AlarmList) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	into.NumOfAlarmRecords = len(l.byID)
	into.LastModification = l.lastModification
	into.DroppedRecords = l.dropped
	for _, r := range l.byID {
		if f.keeps(*r) {
			into.AlarmRecords = append(into.AlarmRecords, *r)
		}
	}
	slices.SortFunc(into.AlarmRecords, func(a, b AlarmRecord) int {
		return cmp.Or(CompareAlarmRecords(a, b), cmp.Compare(a.AlarmID, b.AlarmID))
	})
}

// expire acknowledges the cleared records that nobody acknowledged
// within the retention, with l.mu held, which takes them out of the
// list (7.3.1.1). It walks the list, which holds at most max records.
//
// With acknowledgement supported, a cleared alarm stays in the list
// until it is acknowledged (TS 28.111 6.14), and a record should not
// leave the list without an acknowledgement of an operator or a machine
// (6.7). A list that no operator watches would then keep the faults that
// are over until it is full. The node is the machine here: at the end
// of the retention it acknowledges the record itself, with
// RetentionAckUserID as the user and itself as the system, at the time
// the retention ended. The record then leaves as any acknowledged
// cleared record does, and an OSS that follows the notifications learns
// that from the clearance and the acknowledgement, as 6.12 has it.
// TS 28.111 asks a producer that takes no acknowledgements from its
// consumers to acknowledge by itself (REQ-FM-MC-12 in clause 5), and
// says nothing of one that does, as this one: the acknowledgement at
// the end of the retention is this project's own.
//
// The records are acknowledged in the order their retention ended, so
// that the journal holds the events in the order of their times.
func (l *alarmList) expire(now time.Time) {
	var due []*AlarmRecord
	for _, r := range l.byID {
		if r.AlarmClearedTime != nil && !now.Before(r.AlarmClearedTime.Add(l.retention)) {
			due = append(due, r)
		}
	}
	slices.SortFunc(due, func(a, b *AlarmRecord) int {
		return cmp.Or(a.AlarmClearedTime.Compare(*b.AlarmClearedTime), cmp.Compare(a.AlarmID, b.AlarmID))
	})
	for _, r := range due {
		l.setAckState(r, AckStateRequest{AckState: Acknowledged, Operator: RetentionAckUserID, AckSystemID: l.node}, r.AlarmClearedTime.Add(l.retention))
	}
}

// drop makes room for a new record in a full list, with l.mu held: the
// oldest cleared record goes, or the oldest record when none is cleared
// (TS 28.111 7.3.2.1).
func (l *alarmList) drop(now time.Time) {
	var oldest *AlarmRecord
	for _, r := range l.byID {
		if oldest == nil || dropsBefore(r, oldest) {
			oldest = r
		}
	}
	l.remove(oldest)
	l.dropped++
	l.journal.add(AlarmDropped, now, *oldest)
	l.log.Warn("alarm list full: record dropped", alarmAttrs(oldest)...)
}

// dropsBefore says whether a full list drops r before s: a cleared
// record before one that is not, and the older first.
func dropsBefore(r, s *AlarmRecord) bool {
	if rc, sc := r.PerceivedSeverity == Cleared, s.PerceivedSeverity == Cleared; rc != sc {
		return rc
	}
	return r.AlarmRaisedTime.Before(s.AlarmRaisedTime)
}

func (l *alarmList) remove(r *AlarmRecord) {
	delete(l.byID, r.AlarmID)
	delete(l.byIdentity, AlarmIdentity{Role: r.Role, Peer: r.Peer, AlarmType: r.AlarmType, ProbableCause: r.ProbableCause, SpecificProblem: r.SpecificProblem})
}

func alarmAttrs(r *AlarmRecord) []any {
	return []any{
		"alarm_id", r.AlarmID, "object_instance", r.ObjectInstance, "alarm_type", string(r.AlarmType),
		"probable_cause", string(r.ProbableCause), "specific_problem", string(r.SpecificProblem),
		"perceived_severity", string(r.PerceivedSeverity),
	}
}

// maxNotificationID is the largest integer that a JSON number holds
// without loss in every reader: 2^53 - 1, the largest safe integer of
// ECMAScript, which the OAM screen is written in.
const maxNotificationID = 1<<53 - 1

// newNotificationID is a random id from 1 to maxNotificationID. A
// random id needs no counter that would have to survive a restart of
// the node to stay unique (TS 28.532 11.0.2).
func newNotificationID() int64 { return mathrand.Int64N(maxNotificationID) + 1 }

// newAlarmID is 64 random bits in hexadecimal: unique within the list
// and, with no coordination, across the lists of the nodes and over time
// (TS 28.111 6.4).
func newAlarmID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
