package maintenance

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	// operator acknowledges stays in the list.
	DefaultClearedAlarmRetention = time.Hour
)

// alarmList is the alarm list of a node (TS 28.111 7.3.2). Its lock
// guards this node's alarms only, at most max of them, never the state
// of a user.
type alarmList struct {
	max       int
	retention time.Duration
	log       *slog.Logger

	mu               sync.Mutex
	byIdentity       map[AlarmIdentity]*AlarmRecord
	byID             map[string]*AlarmRecord
	lastModification time.Time
	dropped          uint64
}

func newAlarmList(max int, retention time.Duration, start time.Time, log *slog.Logger) *alarmList {
	return &alarmList{
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
	n.alarms.raise(n.node, a, n.clock.Now())
	return nil
}

// ClearAlarm clears the alarm of id, as the node does when the
// condition is no longer present (TS 28.111 6.8: every alarm here is
// cleared by the node that raised it). The record stays in the list,
// CLEARED, until an operator acknowledges it (6.14) or for
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
	if err := req.check(); err != nil {
		return AlarmRecord{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return n.alarms.setAckState(alarmID, req, n.clock.Now())
}

func (l *alarmList) raise(node string, a Alarm, now time.Time) {
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
		l.log.Warn("alarm changed", alarmAttrs(r)...)
		return
	}
	if len(l.byID) >= l.max {
		l.drop()
	}
	r := &AlarmRecord{
		AlarmID:           newAlarmID(),
		ObjectInstance:    ObjectInstance(node, a.AlarmIdentity),
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
	l.log.Info("alarm cleared", alarmAttrs(r)...)
	if r.AckState == Acknowledged {
		l.remove(r)
	}
}

func (l *alarmList) setAckState(alarmID string, req AckStateRequest, now time.Time) (AlarmRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expire(now)
	r := l.byID[alarmID]
	if r == nil {
		return AlarmRecord{}, fmt.Errorf("%w: %q", ErrUnknownAlarm, alarmID)
	}
	if r.AckState == req.AckState {
		return *r, nil
	}
	// The operator's change sets ackTime but not alarmChangedTime, which
	// only the alarmed resource changes (TS 28.111 7.4.1).
	r.AckState = req.AckState
	r.AckTime = &now
	r.AckUserID = req.Operator
	r.AckSystemID = req.AckSystemID
	r.NotificationID = newNotificationID()
	l.lastModification = now
	l.log.Info("alarm acknowledgement changed", append(alarmAttrs(r), "operator", req.Operator, "ack_system_id", req.AckSystemID)...)
	if r.AckState == Acknowledged && r.PerceivedSeverity == Cleared {
		l.remove(r)
	}
	return *r, nil
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

// expire removes the cleared records that have stayed for the
// retention, with l.mu held. It walks the list, which holds at most max
// records.
//
// SIMPLIFIED: TS 28.111 V20.0.0 6.14. With acknowledgement supported, a
// cleared alarm stays in the list until it is acknowledged. Here it also
// leaves after Config.ClearedAlarmRetention, so that a list that no
// operator watches still forgets the faults that are over.
func (l *alarmList) expire(now time.Time) {
	for _, r := range l.byID {
		if r.AlarmClearedTime == nil {
			continue
		}
		if at := r.AlarmClearedTime.Add(l.retention); !now.Before(at) {
			l.remove(r)
			if at.After(l.lastModification) {
				l.lastModification = at
			}
		}
	}
}

// drop makes room for a new record in a full list, with l.mu held: the
// oldest cleared record goes, or the oldest record when none is cleared
// (TS 28.111 7.3.2.1).
func (l *alarmList) drop() {
	var oldest *AlarmRecord
	for _, r := range l.byID {
		if oldest == nil || dropsBefore(r, oldest) {
			oldest = r
		}
	}
	l.remove(oldest)
	l.dropped++
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
