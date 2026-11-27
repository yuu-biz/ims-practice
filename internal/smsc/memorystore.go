package smsc

import (
	"container/heap"
	"context"
	"crypto/rand"
	"slices"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sgd"
)

// MemoryStore is a Store in the memory of one process: for tests, and
// for a network with one SC instance. Its mutex guards the maps and is
// held only while they change, never while a message is delivered.
type MemoryStore struct {
	mu           sync.Mutex
	destinations map[string]*destination
	// due orders the destinations by their time, earliest first.
	due dueIndex
	// timestamps is the last TP-SCTS given per destination. stamped
	// lists the same in the order they were given, so that the ones too
	// old to matter can be dropped from the front.
	timestamps map[string]time.Time
	stamped    []stamp
	// ofrs are the records of the OFRs the SC has taken, by request.
	// begun lists the same in the order they began, so that the ones
	// that have expired can be dropped from the front.
	ofrs  map[string]ofrRecord
	begun []string
}

// ofrRecord is what BeginOFR and PutOFA keep of one OFR.
type ofrRecord struct {
	// answer is nil until PutOFA.
	answer  *sgd.MOForwardShortMessageAnswer
	expires time.Time
}

type destination struct {
	name     string
	queue    []Message
	due      time.Time
	attempts int
	// token is the lease in force, or empty.
	token string
	// alerted is the time of an Alert that came during the lease, or
	// zero.
	alerted time.Time
	// index is the position in MemoryStore.due.
	index int
}

type stamp struct {
	destination string
	at          time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{destinations: map[string]*destination{}, timestamps: map[string]time.Time{},
		ofrs: map[string]ofrRecord{}}
}

var _ Store = (*MemoryStore)(nil)

func (s *MemoryStore) Enqueue(_ context.Context, m Message, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.destinations[m.Destination]
	if !ok {
		d = &destination{name: m.Destination, due: now}
		s.destinations[m.Destination] = d
		heap.Push(&s.due, d)
	}
	d.queue = append(d.queue, copyMessage(m))
	return nil
}

func (s *MemoryStore) ServiceCentreTimeStamp(_ context.Context, dest string, now time.Time) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A time stamp more than two seconds before now cannot clash with
	// one given from now on, which is at least now to the second.
	for len(s.stamped) > 0 && now.Sub(s.stamped[0].at) > 2*time.Second {
		if old := s.stamped[0]; s.timestamps[old.destination].Equal(old.at) {
			delete(s.timestamps, old.destination)
		}
		s.stamped = s.stamped[1:]
	}
	t := now.Truncate(time.Second)
	if last, ok := s.timestamps[dest]; ok && !t.After(last) {
		t = last.Add(time.Second)
	}
	s.timestamps[dest] = t
	s.stamped = append(s.stamped, stamp{destination: dest, at: t})
	return t, nil
}

func (s *MemoryStore) ClaimDue(_ context.Context, now time.Time, lease time.Duration) (Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.due) == 0 || s.due[0].due.After(now) {
		return Lease{}, false, nil
	}
	return s.lease(s.due[0], now, lease), true, nil
}

func (s *MemoryStore) Claim(_ context.Context, dest string, now time.Time, lease time.Duration) (Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.destinations[dest]
	if !ok || d.due.After(now) {
		return Lease{}, false, nil
	}
	return s.lease(d, now, lease), true, nil
}

// lease gives d to a new lease, which ends at now plus duration. Until
// then d is not due, and when the lease runs out d is due again, for
// whichever instance asks.
func (s *MemoryStore) lease(d *destination, now time.Time, duration time.Duration) Lease {
	d.token = rand.Text()
	d.alerted = time.Time{}
	d.due = now.Add(duration)
	heap.Fix(&s.due, d.index)
	return Lease{Destination: d.name, Token: d.token, Attempts: d.attempts}
}

// leased returns the destination of l, if l is still the lease in force.
func (s *MemoryStore) leased(l Lease) (*destination, error) {
	d, ok := s.destinations[l.Destination]
	if !ok || d.token != l.Token {
		return nil, ErrLeaseLost
	}
	return d, nil
}

func (s *MemoryStore) Queue(_ context.Context, l Lease) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.leased(l)
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(d.queue))
	for i, m := range d.queue {
		out[i] = copyMessage(m)
	}
	return out, nil
}

func (s *MemoryStore) Remove(_ context.Context, l Lease, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.leased(l)
	if err != nil {
		return err
	}
	for i, m := range d.queue {
		if m.ID == id {
			d.queue = append(d.queue[:i:i], d.queue[i+1:]...)
			break
		}
	}
	return nil
}

func (s *MemoryStore) Release(_ context.Context, l Lease, next time.Time, attempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.leased(l)
	if err != nil {
		return err
	}
	if len(d.queue) == 0 {
		heap.Remove(&s.due, d.index)
		delete(s.destinations, d.name)
		return nil
	}
	if !d.alerted.IsZero() && d.alerted.Before(next) {
		next = d.alerted
	}
	d.token, d.alerted, d.attempts, d.due = "", time.Time{}, attempts, next
	heap.Fix(&s.due, d.index)
	return nil
}

func (s *MemoryStore) Alert(_ context.Context, dest string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.destinations[dest]
	switch {
	case !ok:
		return false, nil
	case d.token != "":
		d.alerted = now
	case d.due.After(now):
		d.due = now
		heap.Fix(&s.due, d.index)
	}
	return true, nil
}

func (s *MemoryStore) BeginOFR(_ context.Context, request string, now, expires time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.begun) > 0 {
		if r, ok := s.ofrs[s.begun[0]]; ok && now.Before(r.expires) {
			break
		}
		delete(s.ofrs, s.begun[0])
		s.begun = s.begun[1:]
	}
	if r, ok := s.ofrs[request]; ok && now.Before(r.expires) {
		return false, nil
	}
	s.ofrs[request] = ofrRecord{expires: expires}
	s.begun = append(s.begun, request)
	return true, nil
}

func (s *MemoryStore) PutOFA(_ context.Context, request string, a sgd.MOForwardShortMessageAnswer, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a = copyOFA(a)
	s.ofrs[request] = ofrRecord{answer: &a, expires: expires}
	return nil
}

func (s *MemoryStore) OFA(_ context.Context, request string, now time.Time) (sgd.MOForwardShortMessageAnswer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.ofrs[request]
	if !ok || r.answer == nil || !now.Before(r.expires) {
		return sgd.MOForwardShortMessageAnswer{}, false, nil
	}
	return copyOFA(*r.answer), true, nil
}

// copyOFA copies what the pointer and the slice of a point to, as
// copyMessage does for a message.
func copyOFA(a sgd.MOForwardShortMessageAnswer) sgd.MOForwardShortMessageAnswer {
	if a.SMDeliveryFailureCause != nil {
		c := *a.SMDeliveryFailureCause
		c.SMDiagnosticInfo = slices.Clone(c.SMDiagnosticInfo)
		a.SMDeliveryFailureCause = &c
	}
	a.SMRPUI = slices.Clone(a.SMRPUI)
	return a
}

// copyMessage copies what the pointers of m point to, so that neither
// the caller nor the store sees the changes of the other.
func copyMessage(m Message) Message {
	if m.Deliver != nil {
		d := *m.Deliver
		m.Deliver = &d
	}
	if m.StatusReport != nil {
		r := *m.StatusReport
		m.StatusReport = &r
	}
	if m.StatusReportRequest != nil {
		r := *m.StatusReportRequest
		m.StatusReportRequest = &r
	}
	return m
}

// dueIndex is a heap of destinations ordered by their time (container/heap).
type dueIndex []*destination

func (q dueIndex) Len() int           { return len(q) }
func (q dueIndex) Less(i, j int) bool { return q[i].due.Before(q[j].due) }
func (q dueIndex) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
}
func (q *dueIndex) Push(x any) {
	d := x.(*destination)
	d.index = len(*q)
	*q = append(*q, d)
}
func (q *dueIndex) Pop() any {
	old := *q
	d := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]
	return d
}
