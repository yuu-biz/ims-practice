package oam

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	mathrand "math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/jex"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The times of the watches of the nodes. The lease is longer than the
// three others, so that a holder that renews on time never loses it,
// and two renewals in a row may fail within it.
const (
	// DefaultWatchLease is how long a watch lasts unless it is renewed.
	DefaultWatchLease = 15 * time.Second
	// DefaultWatchRenewal is how often a holder renews its watch: a third
	// of the lease. It also bounds how long an instance that has lost a
	// watch goes on before it finds out.
	DefaultWatchRenewal = 5 * time.Second
	// DefaultAlarmEventsWait is how long a request for the events of a
	// node waits for one (maintenance.AlarmEventsQuery.Wait), no longer
	// than a renewal.
	DefaultAlarmEventsWait = 5 * time.Second
	// DefaultNotificationTimeout bounds the POST of one notification to
	// one recipient.
	DefaultNotificationTimeout = 5 * time.Second
	// DefaultNotificationConcurrency bounds how many recipients one
	// notification is sent to at once.
	DefaultNotificationConcurrency = 8
	// DefaultMissedRetryInterval is how often a recipient that missed a
	// notification is sent the notifyAlarmListRebuilt it is owed, by
	// any watcher, until it takes it.
	DefaultMissedRetryInterval = 30 * time.Second
)

// alarmNotification is a notification that the OAM sends, with its
// type and its body as the JSON document that a notificationFilter is
// evaluated against (TS 28.622 4.3.22.1).
type alarmNotification struct {
	notificationType mns.NotificationType
	body             any
	document         map[string]any
}

func newAlarmNotification(t mns.NotificationType, body any) (alarmNotification, error) {
	doc, err := jex.Document(body)
	if err != nil {
		return alarmNotification{}, err
	}
	return alarmNotification{notificationType: t, body: body, document: doc}, nil
}

// alarmNotificationID is the notificationId of a notification that the
// OAM makes itself, about the alarm list of a node rather than about one
// alarm: a hash of what the notification is about, in the 53 bits that a
// JSON number holds without loss. An instance that takes over a watch
// and sends the same notification again gives it the same id, so that
// the OSS can tell the two apart from two notifications (TS 28.532
// 11.0.2).
func alarmNotificationID(parts ...string) int64 {
	h := fnv.New64a()
	for _, p := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	id := int64(h.Sum64() & (1<<53 - 1))
	if id == 0 {
		id = 1
	}
	return id
}

// header is the NotificationHeader of a notification about the object
// d (TS 28.532 11.0.2, 12.0.2.1).
func (nb *northbound) header(d mns.DN, t mns.NotificationType, id int64, at time.Time) mns.NotificationHeader {
	return mns.NotificationHeader{
		Href:             nb.root.CanonicalURI(d),
		NotificationId:   id,
		NotificationType: t,
		EventTime:        at,
		SystemDN:         nb.subNetwork.String(),
	}
}

// alarmListRebuilt is notifyAlarmListRebuilt about the part of the list
// below d (TS 28.111 8.5). Every one that the OAM sends asks the OSS to
// read that part again, as notifications of it were not sent.
func (nb *northbound) alarmListRebuilt(d mns.DN, reason string, id int64, at time.Time) (alarmNotification, error) {
	return newAlarmNotification(mns.NotifyAlarmListRebuiltType, mns.NotifyAlarmListRebuilt{
		NotificationHeader:            nb.header(d, mns.NotifyAlarmListRebuiltType, id, at),
		Reason:                        reason,
		AlarmListAlignmentRequirement: mns.AlignmentRequired,
	})
}

// potentialFaultyAlarmList is notifyPotentialFaultyAlarmList about the
// part of the list below d (TS 28.111 8.10).
func (nb *northbound) potentialFaultyAlarmList(d mns.DN, id int64, at time.Time) (alarmNotification, error) {
	return newAlarmNotification(mns.NotifyPotentialFaultyAlarmListType, mns.NotifyPotentialFaultyAlarmList{
		NotificationHeader: nb.header(d, mns.NotifyPotentialFaultyAlarmListType, id, at),
		Reason:             mns.ReasonServiceProviderNECommunicationError,
	})
}

// eventNotification is the notification of one event of the journal of
// node (TS 28.111 8.2 to 8.4, 8.8). Its notificationId is the one that
// the node gave the change, which the record of the alarm list also
// carries. A DROPPED event, a record that the full list removed, is a
// notifyAlarmListRebuilt about the node, as is an event whose record the
// OAM cannot pass on.
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.2.1 and 8.5. A record that the full
// list removed is told with notifyAlarmListRebuilt, which 8.5.1 sends
// when a list has been rebuilt; here nothing was rebuilt. 7.3.2.1 lets
// the list remove records and asks for no notification, and none of
// TS 28.111 says that a record went without being cleared and
// acknowledged. notifyAlarmListRebuilt with ALIGNMENT_REQUIRED is the
// one whose meaning, that some alarm changes were not notified (8.5.2),
// fits, so that the OSS reads that part of the list again rather than
// keep a record that the node no longer has.
func (nb *northbound) eventNotification(node string, journal string, e maintenance.AlarmEvent) (alarmNotification, error) {
	lost := func() (alarmNotification, error) {
		return nb.alarmListRebuilt(nb.managedElement(node), mns.ReasonIndeterminate,
			alarmNotificationID(node, journal, fmt.Sprint(e.Number)), e.EventTime)
	}
	if e.EventType == maintenance.AlarmDropped {
		return lost()
	}
	r := e.AlarmRecord
	if err := maintenance.CheckAlarmRecord(r); err != nil {
		return lost()
	}
	rec, err := nb.alarmRecord(node, r)
	if err != nil {
		return lost()
	}
	instance, err := mns.ParseDN(rec.ObjectInstance)
	if err != nil {
		return lost()
	}
	id := alarmRecordKey(node, r.AlarmID)
	switch e.EventType {
	case maintenance.AlarmAdded:
		t := mns.NotifyNewAlarmType
		return newAlarmNotification(t, mns.NotifyNewAlarm{
			NotificationHeader: nb.header(instance, t, rec.NotificationId, e.EventTime),
			AlarmId:            id, AlarmType: rec.AlarmType, ProbableCause: rec.ProbableCause,
			SpecificProblem: rec.SpecificProblem, PerceivedSeverity: rec.PerceivedSeverity, AdditionalText: rec.AdditionalText,
		})
	case maintenance.AlarmChanged:
		t := mns.NotifyChangedAlarmGeneralType
		return newAlarmNotification(t, mns.NotifyChangedAlarmGeneral{
			NotificationHeader: nb.header(instance, t, rec.NotificationId, e.EventTime),
			AlarmId:            id, AlarmType: rec.AlarmType, ProbableCause: rec.ProbableCause,
			SpecificProblem: rec.SpecificProblem, PerceivedSeverity: rec.PerceivedSeverity, AdditionalText: rec.AdditionalText,
		})
	case maintenance.AlarmCleared:
		t := mns.NotifyClearedAlarmType
		return newAlarmNotification(t, mns.NotifyClearedAlarm{
			NotificationHeader: nb.header(instance, t, rec.NotificationId, e.EventTime),
			AlarmId:            id, AlarmType: rec.AlarmType, ProbableCause: rec.ProbableCause,
			SpecificProblem: rec.SpecificProblem, PerceivedSeverity: rec.PerceivedSeverity,
		})
	case maintenance.AckStateChanged:
		t := mns.NotifyAckStateChangedType
		return newAlarmNotification(t, mns.NotifyAckStateChanged{
			NotificationHeader: nb.header(instance, t, rec.NotificationId, e.EventTime),
			AlarmId:            id, AlarmType: rec.AlarmType, ProbableCause: rec.ProbableCause,
			SpecificProblem: rec.SpecificProblem, PerceivedSeverity: rec.PerceivedSeverity,
			AckState: rec.AckState, AckUserId: rec.AckUserId, AckSystemId: rec.AckSystemId,
		})
	}
	return lost()
}

// watchStep is what the watcher of a node does with one answer of the
// node: send the notification, if any, then save the state.
type watchStep struct {
	notification *alarmNotification
	state        AlarmWatchState
}

// planEvents turns an answer of the node with the events of its journal
// into the steps of the watcher, from the state st that the watcher has.
// It does nothing else, so that the order of what the OAM sends follows
// from the answer and the state alone.
//
//   - The first answer for a node that has never been watched starts at
//     the end of the journal: what happened before is in the alarm list,
//     which the OSS reads. So does the first answer after a state that
//     the store could not read (Lost), with a notifyAlarmListRebuilt with
//     "indeterminate", as what happened since the last state is not
//     notified.
//   - Another journal than before means that the node started again,
//     with an alarm list and a journal that begin anew: one
//     notifyAlarmListRebuilt with "System restarts", which asks the OSS
//     to read the node's part of the list again. The watch goes on from
//     the end of the new journal: the events of the answer, and the
//     earlier ones of the new journal, are in the list that the OSS
//     reads, and sent after the notifyAlarmListRebuilt they would reach
//     the OSS twice and after it.
//   - A node that comes back after it stopped answering gets one
//     notifyAlarmListRebuilt with "System-NE communication error".
//   - Events lost from the journal give one with "indeterminate", unless
//     one of the two above was sent for the same answer.
//   - Then each event gives its notification, and a step of its own, so
//     that the state moves on with each one that is sent.
func (nb *northbound) planEvents(node string, st AlarmWatchState, ev maintenance.AlarmEvents, now time.Time) []watchStep {
	me := nb.managedElement(node)
	var first watchStep
	rebuilt := func(reason string, id int64) {
		n, err := nb.alarmListRebuilt(me, reason, id, now)
		if err == nil {
			first.notification = &n
		}
	}
	switch {
	case st.JournalID == "":
		st.JournalID, st.LastNumber = ev.JournalID, ev.LastNumber
		if st.Lost {
			st.Lost = false
			rebuilt(mns.ReasonIndeterminate, alarmNotificationID(st.Seed, node, "lost state", fmt.Sprint(st.Generation)))
		} else if st.Unreachable {
			st.Unreachable = false
			st.Transitions++
			rebuilt(mns.ReasonSystemNECommunicationError, alarmNotificationID(st.Seed, node, "reachable", fmt.Sprint(st.Transitions)))
		}
		first.state = st
		return []watchStep{first}
	case ev.JournalID != st.JournalID:
		rebuilt(mns.ReasonSystemRestarts, alarmNotificationID(node, "journal", ev.JournalID))
		st.JournalID, st.LastNumber = ev.JournalID, ev.LastNumber
		if st.Unreachable {
			st.Unreachable = false
			st.Transitions++
		}
		first.state = st
		return []watchStep{first}
	case st.Unreachable:
		st.Unreachable = false
		st.Transitions++
		rebuilt(mns.ReasonSystemNECommunicationError, alarmNotificationID(st.Seed, node, "reachable", fmt.Sprint(st.Transitions)))
	case ev.Lost:
		rebuilt(mns.ReasonIndeterminate, alarmNotificationID(node, "lost", ev.JournalID, fmt.Sprint(st.LastNumber)))
	}
	first.state = st
	steps := []watchStep{first}
	for _, e := range ev.Events {
		st.LastNumber = e.Number
		step := watchStep{state: st}
		if n, err := nb.eventNotification(node, ev.JournalID, e); err == nil {
			step.notification = &n
		}
		steps = append(steps, step)
	}
	return steps
}

// RunNotifications watches the nodes of the inventory and sends the
// notifications of their alarms to the subscriptions, until ctx ends. It
// is ErrUnavailable for an OAM without the 3GPP management services, a
// subscription store or a watch store (Config.AlarmWatches).
//
// Every WatchRenewal the instance says that it is present
// (AlarmWatchStore.Present), and works out its share of the nodes: the
// number of nodes divided by the number of instances present, rounded
// up. It takes the watches of nodes that no instance holds, in a new
// random order each time, until it holds its share, and watches each
// node it holds on a goroutine of its own (watchNode). When it holds
// more than its share, as when another instance has started, it gives
// the extra watches up, each once the events in hand have been sent,
// for the others to take.
//
// An instance may be unable to reach a node that the others reach, as
// when the link between the two breaks. Its watcher finds that the node
// has left handOverAfter requests in a row unanswered, and the instance
// says so in its presence. While some instance present has not said
// that it cannot reach the node, the instance gives the watch up, and
// the instances that have not said so take it, beyond their share if
// need be, and keep it when they give watches up for their share. At
// each round the instance asks each node that it cannot reach and does
// not watch for its events, with the request of a watcher (probe), and
// once the node answers it no longer says that it cannot reach it. When every instance present says that it cannot reach the
// node, as when the node is down, the watcher keeps the watch, so that
// it does not go round the instances.
//
// Each round of an instance reads the presence of every instance, makes
// at most one attempt per node, and asks the nodes that it cannot reach,
// so the work of all the instances together grows with the square of
// their number and with the product of their number and the number of
// nodes, and with neither the subscribers nor the subscriptions. Each
// round also sends the notifications that misses owe (sendOwed).
func (s *Server) RunNotifications(ctx context.Context) error {
	if s.mns == nil || s.ntfSubscriptions == nil || s.watches == nil {
		return fmt.Errorf("%w: the notifications need the 3GPP management services, a subscription store and a watch store", ErrUnavailable)
	}
	var (
		mu sync.Mutex
		// watching holds, for each node watched, what tells its watcher
		// to give the watch up.
		watching = map[string]chan struct{}{}
		// unreachable holds the nodes that this instance cannot reach:
		// each left handOverAfter requests of its watcher in a row
		// unanswered, and has answered no request of this instance since.
		unreachable = map[string]bool{}
		// probing holds a token while the probe of a round runs, on a
		// goroutine of its own, so that a probe that waits out the time
		// limit of a node holds up no round; a round that comes before it
		// ends sends no probe.
		probing = make(chan struct{}, 1)
		wg      sync.WaitGroup
	)
	reached := func(node string, answered bool) {
		mu.Lock()
		defer mu.Unlock()
		if answered {
			delete(unreachable, node)
		} else {
			unreachable[node] = true
		}
	}
	round := func() {
		mu.Lock()
		said := maps.Clone(unreachable)
		mu.Unlock()
		p, err := s.watches.Present(ctx, s.instance, slices.Sorted(maps.Keys(said)))
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("the presence of the instance could not be written", "error", err.Error())
			}
			return
		}
		instances := max(p.Instances, 1)
		share := (len(s.nodes) + instances - 1) / instances
		// handOver: this instance cannot reach the node, and another may.
		handOver := func(node string) bool { return said[node] && p.Unreachable[node] < instances }
		// wanted: another instance cannot reach the node, and this one
		// has not found that it cannot.
		wanted := func(node string) bool { return !said[node] && p.Unreachable[node] > 0 }
		mu.Lock()
		for node, release := range watching {
			select {
			case <-release:
			default:
				if handOver(node) {
					close(release)
					s.log.Info("giving the watch of a node that this instance cannot reach up for another instance", "node", node)
				}
			}
		}
		held := giveUp(watching, share, wanted, func(node string) {
			s.log.Info("giving the watch of a node up for another instance", "node", node, "share", share)
		})
		mu.Unlock()
		for _, k := range mathrand.Perm(len(s.nodes)) {
			if ctx.Err() != nil {
				return
			}
			e := s.nodes[k]
			mu.Lock()
			_, busy := watching[e.Name]
			mu.Unlock()
			if busy || handOver(e.Name) || (held >= share && !wanted(e.Name)) {
				continue
			}
			w, ok, err := s.watches.Watch(ctx, e.Name, s.instance)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Warn("the watch of a node could not be taken", "node", e.Name, "error", err.Error())
				}
				continue
			}
			if !ok {
				continue
			}
			release := make(chan struct{})
			mu.Lock()
			watching[e.Name] = release
			mu.Unlock()
			held++
			wg.Go(func() {
				s.watchNode(ctx, e, w, release, func(answered bool) { reached(e.Name, answered) })
				mu.Lock()
				delete(watching, e.Name)
				mu.Unlock()
			})
		}
		var probe []Endpoint
		mu.Lock()
		for _, e := range s.nodes {
			if _, busy := watching[e.Name]; said[e.Name] && !busy {
				probe = append(probe, e)
			}
		}
		mu.Unlock()
		if len(probe) == 0 {
			return
		}
		select {
		case probing <- struct{}{}:
			wg.Go(func() {
				defer func() { <-probing }()
				for _, node := range s.probe(ctx, probe) {
					reached(node, true)
				}
			})
		default:
		}
	}
	round()
	s.sendOwed(ctx)
	t := time.NewTicker(s.watchRenewal)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			s.owed.Wait()
			return nil
		case <-t.C:
			round()
			s.sendOwed(ctx)
		}
	}
}

// giveUp tells the watchers beyond share to give their watches up, and
// says how many watches are then held. A watch that its watcher is
// giving up already, whose release is closed, is held no more: it
// counts neither towards share nor as one to give up, so that a round
// that comes while a watcher finishes its events does not give up a
// second watch for the same one too many. A watch of a node that keep
// names is not given up, as the instances that would take it may be
// unable to reach the node; it counts towards share all the same.
func giveUp(watching map[string]chan struct{}, share int, keep func(node string) bool, gave func(node string)) (held int) {
	var spare []string
	for node, release := range watching {
		select {
		case <-release:
		default:
			held++
			if !keep(node) {
				spare = append(spare, node)
			}
		}
	}
	for ; held > share && len(spare) > 0; held-- {
		node := spare[len(spare)-1]
		spare = spare[:len(spare)-1]
		close(watching[node])
		gave(node)
	}
	return held
}

// handOverAfter is how many requests in a row a node leaves unanswered
// before its watcher finds that this instance cannot reach it, and the
// watch may go to an instance that can (RunNotifications). A node that
// is away for a moment does not move its watch.
const handOverAfter = 3

// probe asks each node of nodes for its events after the last one that
// its state says was sent, at most NodeConcurrency nodes at once, with
// the request of a watcher (alarmEvents), and returns the names of the
// nodes that answered. A request that differed, as one without the wait,
// could be answered by a node that leaves the requests of its watcher
// unanswered, and the watch would go round the instances.
func (s *Server) probe(ctx context.Context, nodes []Endpoint) []string {
	var (
		mu       sync.Mutex
		answered []string
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, s.nodeConcurrency)
	for _, e := range nodes {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			st, err := s.watches.AlarmWatchState(ctx, e.Name)
			if err != nil {
				return
			}
			if _, err := s.alarmEvents(ctx, maintenance.NewClient(e.URL, s.httpClient), st.LastNumber); err != nil {
				return
			}
			mu.Lock()
			answered = append(answered, e.Name)
			mu.Unlock()
		})
	}
	wg.Wait()
	return answered
}

// alarmEvents asks the node of client for the events after the number
// after, which it waits up to AlarmEventsWait for, within that wait and
// NodeTimeout. A node that does not answer it within them, or answers
// with another status than 200, is a node that does not answer.
func (s *Server) alarmEvents(ctx context.Context, client *maintenance.Client, after uint64) (maintenance.AlarmEvents, error) {
	ctx, done := context.WithTimeout(ctx, s.alarmEventsWait+s.nodeTimeout)
	defer done()
	return client.AlarmEvents(ctx, maintenance.AlarmEventsQuery{After: after, Wait: s.alarmEventsWait})
}

// watchNode sends the notifications of the node e while it holds the
// watch w: it asks the node for the events after the last one sent, and
// waits up to the wait of the request for one, then sends what the
// answer gives (planEvents) and saves the state after each event. A node
// that does not answer is told to the OSSs once, with
// notifyPotentialFaultyAlarmList, and asked again after a wait. The
// watcher tells reached when the node has left handOverAfter requests
// in a row unanswered, and whenever it answers. The watch is renewed on
// a goroutine of its own, so that a slow node or a slow OSS does not
// let it run out; once it is lost, the watcher stops. So it does when
// release closes, before it asks the node again.
func (s *Server) watchNode(ctx context.Context, e Endpoint, w AlarmWatch, release <-chan struct{}, reached func(answered bool)) {
	ctx, cancel := context.WithCancel(ctx)
	renewed := make(chan struct{})
	// On the way out the renewals stop first, then the watch is given
	// up, so that no renewal follows the release.
	defer func() {
		cancel()
		<-renewed
		releaseCtx, done := context.WithTimeout(context.WithoutCancel(ctx), s.nodeTimeout)
		defer done()
		_ = w.Release(releaseCtx)
	}()
	go func() {
		defer close(renewed)
		t := time.NewTicker(s.watchRenewal)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := w.Renew(ctx); errors.Is(err, ErrWatchLost) {
					s.log.Info("the watch of a node was lost", "node", e.Name)
					cancel()
					return
				} else if err != nil && ctx.Err() == nil {
					s.log.Warn("the watch of a node could not be renewed", "node", e.Name, "error", err.Error())
				}
			}
		}
	}()
	s.log.Info("watching the alarms of a node", "node", e.Name, "generation", w.State().Generation)
	client := maintenance.NewClient(e.URL, s.httpClient)
	st := w.State()
	// save writes the state after notifications were sent. It writes
	// nothing once ctx has ended: the sending may have stopped half way,
	// and the next holder sends those notifications again. A failure of
	// the store is tried again after a wait, as in deliver, until the
	// state is saved or the watch is lost. A write whose answer was lost
	// reached the store, and the next try finds it its own
	// (AlarmWatch.Save).
	save := func(next AlarmWatchState) bool {
		for backoff := s.alarmEventsWait / 8; ctx.Err() == nil; backoff = min(2*backoff, s.alarmEventsWait) {
			err := w.Save(ctx, next)
			switch {
			case err == nil:
				st = next
				return true
			case ctx.Err() != nil:
			case errors.Is(err, ErrWatchLost):
				s.log.Info("the watch of a node was lost", "node", e.Name)
				cancel()
				return false
			default:
				s.log.Warn("the watch state of a node could not be saved; it is saved again", "node", e.Name, "error", err.Error(), "wait", backoff.String())
				s.sleep(ctx, backoff)
			}
		}
		return false
	}
	// pollCtx also ends with release, so that a watch is given up
	// without waiting out a request; what the request would have
	// answered is asked again by the next holder.
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	go func() {
		select {
		case <-release:
			stopPolling()
		case <-pollCtx.Done():
		}
	}()
	for unanswered := 0; pollCtx.Err() == nil; {
		ev, err := s.alarmEvents(pollCtx, client, st.LastNumber)
		if pollCtx.Err() != nil {
			return
		}
		// Each unanswered request past handOverAfter says so again: the
		// answer to a probe sent before this watcher began may come after
		// it.
		if err == nil {
			unanswered = 0
			reached(true)
		} else if unanswered++; unanswered >= handOverAfter {
			if unanswered == handOverAfter {
				s.log.Warn("this instance cannot reach a node", "node", e.Name, "requests", unanswered)
			}
			reached(false)
		}
		if err != nil {
			if !st.Unreachable {
				s.log.Warn("a node does not answer for its alarm events", "node", e.Name, "error", s.nodeError(err).Error)
				next := st
				next.Unreachable = true
				next.Transitions++
				n, nerr := s.mns.potentialFaultyAlarmList(s.mns.managedElement(e.Name),
					alarmNotificationID(st.Seed, e.Name, "unreachable", fmt.Sprint(next.Transitions)), s.now())
				if nerr == nil && !s.deliver(ctx, pollCtx, e.Name, n) {
					return
				}
				if !save(next) {
					return
				}
			}
			s.sleep(pollCtx, s.alarmEventsWait)
			continue
		}
		for _, step := range s.mns.planEvents(e.Name, st, ev, s.now()) {
			if step.notification != nil && !s.deliver(ctx, pollCtx, e.Name, *step.notification) {
				return
			}
			// An answer without events, as when the wait ends, leaves the
			// state as it was, and costs the store no write.
			if step.state == st {
				continue
			}
			if !save(step.state) {
				return
			}
		}
	}
}

// deliver sends the notification n (notify) until it has gone, and says
// whether it has: false when ctx or stop ends first. A failure is tried
// again after a wait that doubles each time, from an eighth of
// AlarmEventsWait up to the whole of it, so that a store that has
// stopped answering is asked a few times a wait and no more. A try
// sends nothing to a recipient that took the notification at an
// earlier one.
func (s *Server) deliver(ctx, stop context.Context, node string, n alarmNotification) bool {
	backoff := s.alarmEventsWait / 8
	taken := deliveries{}
	for {
		err := s.notify(ctx, taken, n)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		s.log.Warn("the notifications of a node could not be sent; they are sent again", "node", node, "error", err.Error(), "wait", backoff.String())
		s.sleep(stop, backoff)
		if stop.Err() != nil {
			return false
		}
		backoff = min(2*backoff, s.alarmEventsWait)
	}
}

// sleep waits d, or until ctx ends.
func (s *Server) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
