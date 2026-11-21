package h248

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// outgoingTransaction is a request this Endpoint sent, from its first
// transmission until its reply arrives or the Endpoint gives up.
type outgoingTransaction struct {
	id uint32
	// version is the version in the header of the request and of the
	// TransactionResponseAck for its reply.
	version int
	peer    net.Addr
	message []byte
	result  chan outgoingResult

	mu       sync.Mutex
	finished bool
	timer    Timer
	// firstSent is when the request went out the first time. T-MAX
	// counts from here (Annex D.1.5).
	firstSent       time.Time
	retransmissions int
	// aad is the "average acknowledgement delay" of Annex D.1.3,
	// which doubles after each retransmission.
	aad time.Duration
	// pendings counts the TransactionPendings for the request.
	pendings int
}

type outgoingResult struct {
	reply TransactionReply
	err   error
}

// Request sends a TransactionRequest with the actions to peer and
// waits for its reply. The reply may carry an error descriptor: that
// is an answer from the peer, not an error of Request.
//
// Request repeats the request with growing intervals until the reply
// comes (H.248.1 Annex D.1.3, D.1.5). It returns ErrNoReply when the
// timers run out, and the error of ctx when ctx ends first. In both
// cases the peer may still have carried out the request.
func (e *Endpoint) Request(ctx context.Context, peer net.Addr, actions []ActionRequest) (TransactionReply, error) {
	if e.ctx.Err() != nil {
		return TransactionReply{}, net.ErrClosed
	}
	if e.outgoingCount.Add(1) > e.limit {
		e.outgoingCount.Add(-1)
		return TransactionReply{}, ErrTooManyTransactions
	}
	o := &outgoingTransaction{
		version: e.transactionVersion(actions),
		peer:    peer,
		result:  make(chan outgoingResult, 1),
		aad:     e.timers.InitialRetransmissionTimer,
	}
	e.storeOutgoing(o)
	message, err := e.marshal(o.version, TransactionRequest{TransactionID: o.id, Actions: actions})
	if err == nil && len(message) > e.maxSize {
		err = fmt.Errorf("%w: %d octets, the limit is %d", ErrMessageTooLong, len(message), e.maxSize)
	}
	if err != nil {
		e.finish(o, outgoingResult{err: err})
		return TransactionReply{}, err
	}
	o.message = message

	o.mu.Lock()
	o.firstSent = e.clock.Now()
	if !o.finished {
		// The timer starts before the request goes out, so that it
		// is in place whenever the answer comes.
		o.timer = e.clock.AfterFunc(o.aad, func() { e.retransmit(o) })
	}
	o.mu.Unlock()
	e.write(message, peer)

	select {
	case r := <-o.result:
		return r.reply, r.err
	case <-ctx.Done():
		e.finish(o, outgoingResult{err: ctx.Err()})
	case <-e.ctx.Done():
		e.finish(o, outgoingResult{err: net.ErrClosed})
	}
	// A reply may have finished the transaction just before; then its
	// result is the one to return.
	r := <-o.result
	return r.reply, r.err
}

// storeOutgoing gives the request a transaction ID that no other
// outstanding request of this Endpoint has. The IDs count up from
// FirstTransactionID. Zero is skipped: it stands for a request whose ID
// could not be read (H.248.1 8.1.1).
//
// The IDs are unique only within this Endpoint. Two Endpoints that
// share an MId would need to share the number space (Annex D.1.2.1).
func (e *Endpoint) storeOutgoing(o *outgoingTransaction) {
	for {
		id := e.nextTransactionID.Add(1)
		if id == 0 {
			continue
		}
		if _, loaded := e.outgoing.LoadOrStore(id, o); !loaded {
			o.id = id
			return
		}
	}
}

// finish ends the transaction with its result and forgets it. Only the
// first call has an effect; it reports whether it was that one.
func (e *Endpoint) finish(o *outgoingTransaction, r outgoingResult) bool {
	o.mu.Lock()
	if o.finished {
		o.mu.Unlock()
		return false
	}
	o.finished = true
	if o.timer != nil {
		o.timer.Stop()
	}
	o.mu.Unlock()
	e.outgoing.CompareAndDelete(o.id, o)
	e.outgoingCount.Add(-1)
	o.result <- r
	return true
}

// retransmit runs when the retransmission timer of a request expires.
// It gives up when the request has been repeated MaxRetransmissions
// times or T-MAX has passed since the first transmission (Annex
// D.1.5), and otherwise sends the request again.
func (e *Endpoint) retransmit(o *outgoingTransaction) {
	o.mu.Lock()
	if o.finished {
		o.mu.Unlock()
		return
	}
	elapsed := e.clock.Now().Sub(o.firstSent)
	if o.retransmissions >= e.timers.MaxRetransmissions || elapsed > e.timers.TMax {
		o.mu.Unlock()
		e.finish(o, outgoingResult{err: ErrNoReply})
		return
	}
	o.retransmissions++
	// Annex D.1.3: after a retransmission, double AAD and wait for a
	// random time between 0.5 AAD and AAD. The random part keeps
	// requests that started together from being repeated together.
	//
	// SIMPLIFIED: H.248.1 (05/2002) Annex D.1.3 says that the timer
	// should normally be estimated from the time replies take, and
	// gives, as one possibility in a note, the AAD and ADEV of TCP:
	// smoothed averages of the delay and of its deviation, with N
	// times ADEV added to the timer. Every request starts here from
	// InitialRetransmissionTimer, a provisioned value, and ADEV is
	// taken as zero. Measuring would need state for each peer that
	// outlives its transactions, where the Endpoint keeps state for a
	// transaction only.
	o.aad = min(2*o.aad, e.timers.MaxRetransmissionTimer)
	wait := o.aad/2 + rand.N(o.aad/2+1)
	o.timer = e.clock.AfterFunc(wait, func() { e.retransmit(o) })
	o.mu.Unlock()
	e.write(o.message, o.peer)
}

// receiveReply ends the request that the reply answers. A reply that
// follows a Pending is confirmed at once with a
// TransactionResponseAck (Annex D.1.4), so that the peer can drop the
// reply it keeps.
//
// SIMPLIFIED: H.248.1 (05/2002) Annex D.1.2.2. Any message may carry
// confirmations, so a requester may also confirm the other replies, for
// example with its next request to the same peer. Only replies after a Pending or with
// ImmAckRequired are confirmed here. The peer keeps the others until
// LONG-TIMER, which costs it memory but changes no procedure.
func (e *Endpoint) receiveReply(addr net.Addr, t TransactionReply) {
	v, ok := e.outgoing.Load(t.TransactionID)
	if !ok {
		// A second copy of a reply, sent because a repetition of the
		// request crossed the first copy.
		e.log.Debug("h248: dropped a reply to no outstanding request", "transaction", t.TransactionID)
		return
	}
	o := v.(*outgoingTransaction)
	o.mu.Lock()
	afterPending := o.pendings > 0
	o.mu.Unlock()
	if !e.finish(o, outgoingResult{reply: t}) {
		return
	}
	if t.ImmAckRequired || afterPending {
		e.sendResponseAck(addr, o.version, t.TransactionID)
	}
}

func (e *Endpoint) sendResponseAck(addr net.Addr, version int, id uint32) {
	message, err := e.marshal(version, TransactionResponseAck{Acks: []TransactionAck{{FirstAck: id}}})
	if err != nil {
		e.log.Error("h248: the TransactionResponseAck cannot be written", "error", err)
		return
	}
	e.write(message, addr)
}

// receivePending holds back the repetition of a request that the peer
// is still working on. The request then waits for the next Pending or
// the reply on the timer of the peer (Annex D.1.4); when that expires,
// it is repeated as before. A Pending after the reply is ignored.
func (e *Endpoint) receivePending(t TransactionPending) {
	v, ok := e.outgoing.Load(t.TransactionID)
	if !ok {
		e.log.Debug("h248: dropped a Pending for no outstanding request", "transaction", t.TransactionID)
		return
	}
	o := v.(*outgoingTransaction)
	o.mu.Lock()
	if o.finished {
		o.mu.Unlock()
		return
	}
	o.pendings++
	if o.pendings > e.timers.PeerPendingLimit {
		o.mu.Unlock()
		e.finish(o, outgoingResult{err: ErrPendingLimitExceeded})
		return
	}
	if o.timer != nil {
		o.timer.Stop()
	}
	wait := e.timers.PeerProvisionalResponseTimerValue + e.timers.InitialRetransmissionTimer
	o.timer = e.clock.AfterFunc(wait, func() { e.retransmit(o) })
	o.mu.Unlock()
}
