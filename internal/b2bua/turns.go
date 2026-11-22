package b2bua

import "sync"

// turns puts the requests that one leg receives within its dialogs in
// the order in which the UA took them (sip.InDialogRequest.Arrival),
// which is the order of their CSeq numbers (RFC 3261 12.2.2). The UA
// hands each request to a goroutine of its own, so a request that came
// later may start first; it waits for its turn, and goes on to the
// other leg once the requests before it have their final responses
// there. Otherwise the far end could get them the other way round, and
// refuse the late one for its lower CSeq number, or find an offer that
// crosses the one before it (RFC 3264).
type turns struct {
	mu sync.Mutex
	// next is the Arrival of the request whose turn it is.
	next uint64
	// taken is the highest Arrival that has taken a place (take).
	taken uint64
	// done holds the requests after next that have finished already:
	// one that gave up waiting, when a leg ended.
	done map[uint64]bool
	// moved is closed, and replaced, each time next moves on.
	moved chan struct{}
}

func newTurns() *turns {
	return &turns{next: 1, done: map[uint64]bool{}, moved: make(chan struct{})}
}

// take gives request n its place. The returned function ends its turn;
// it may be called more than once, and the request calls it in a defer
// as soon as it starts, so that a request that returns early, or
// panics, never holds up the ones after it.
func (t *turns) take(n uint64) (finish func()) {
	t.mu.Lock()
	t.taken = max(t.taken, n)
	t.mu.Unlock()
	return sync.OnceFunc(func() { t.finish(n) })
}

// settle waits until every request that has taken a place has
// finished, so that a request of the Call's own may go between them
// instead of while one of them is on its way to the other leg. It gives
// up when in or out, the ends of the two legs, is closed, and then
// reports false; out is nil when the outgoing leg has not been sent
// yet.
func (t *turns) settle(in, out <-chan struct{}) bool {
	for {
		t.mu.Lock()
		idle, moved := t.next > t.taken, t.moved
		t.mu.Unlock()
		if idle {
			return true
		}
		select {
		case <-moved:
		case <-in:
			return false
		case <-out:
			return false
		}
	}
}

// wait waits for the turn of request n. It gives up when in or out, the
// ends of the two legs, is closed, and then reports false; out is nil
// when the outgoing leg has not been sent yet.
func (t *turns) wait(n uint64, in, out <-chan struct{}) bool {
	for {
		t.mu.Lock()
		next, moved := t.next, t.moved
		t.mu.Unlock()
		if next >= n {
			return true
		}
		select {
		case <-moved:
		case <-in:
			return false
		case <-out:
			return false
		}
	}
}

// finish ends the turn of request n. The next request's turn comes once
// every request before it has finished.
func (t *turns) finish(n uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done[n] = true
	if !t.done[t.next] {
		return
	}
	for t.done[t.next] {
		delete(t.done, t.next)
		t.next++
	}
	close(t.moved)
	t.moved = make(chan struct{})
}
