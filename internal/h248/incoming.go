package h248

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
)

// incomingKey names a request that a peer sent. Transaction IDs are
// unique only for each sender, which a message names with its MId
// (H.248.1 (05/2002) Annex D.1.1, D.1.2.1).
type incomingKey struct {
	peer MId
	id   uint32
}

// incomingState is how far the Endpoint is with a request from a peer.
type incomingState int

const (
	// executing: the Handler is carrying out the request.
	executing incomingState = iota
	// replied: the reply is kept, to be sent again if the request is
	// repeated (Annex D.1.1).
	replied
	// acknowledged: the peer has confirmed the reply with a
	// TransactionResponseAck. The reply is dropped, and only the
	// transaction ID is kept, so that late copies of the request are
	// dropped too (Annex D.1.2.2).
	acknowledged
)

// incomingTransaction is a request that a peer sent, from its arrival
// until LONG-TIMER after the reply.
type incomingTransaction struct {
	key incomingKey
	// version is the version in the header of the Pendings and the
	// reply.
	version int
	// unread is set for a request that could be read in part only: it
	// is the error that answers the rest.
	unread *unreadAnswer

	mu    sync.Mutex
	state incomingState
	// addr is where the latest copy of the request came from. Replies
	// go there (H.248.1 9, Annex D.1).
	addr  net.Addr
	reply []byte
	// cancel ends the context of the Handler.
	cancel context.CancelCauseFunc
	// pendingTimer sends the next TransactionPending while the
	// Handler runs.
	pendingTimer Timer
	pendingsSent int
	// limitReached tells that PendingLimit Pendings have gone out, so
	// that no more are sent.
	limitReached bool
}

// receiveRequest carries out a request the first time it arrives and
// answers its repetitions without carrying it out again. That is the
// "At-Most-Once" functionality of Annex D.1.1.
func (e *Endpoint) receiveRequest(m Message, addr net.Addr, t TransactionRequest, unread *unreadAnswer) {
	key := incomingKey{peer: comparableMId(m.MId), id: t.TransactionID}
	if v, ok := e.incoming.Load(key); ok {
		e.receiveRepetition(v.(*incomingTransaction), addr)
		return
	}
	if e.incomingCount.Load() >= e.limit {
		// Dropping keeps the request from being carried out twice:
		// the peer repeats it, and it is carried out once there is
		// room.
		e.log.Warn("h248: dropped a request because too many transactions are open",
			"from", addr, "mid", m.MId, "transaction", t.TransactionID)
		return
	}
	ctx, cancel := context.WithCancelCause(e.ctx)
	in := &incomingTransaction{key: key, version: e.transactionVersion(t.Actions), unread: unread, state: executing, addr: addr, cancel: cancel}
	if v, loaded := e.incoming.LoadOrStore(key, in); loaded {
		cancel(nil)
		e.receiveRepetition(v.(*incomingTransaction), addr)
		return
	}
	e.incomingCount.Add(1)
	in.mu.Lock()
	in.pendingTimer = e.clock.AfterFunc(e.timers.ProvisionalResponseTimerValue, func() { e.sendPending(in) })
	in.mu.Unlock()
	r := &IncomingRequest{From: m.MId, Version: m.Version, Addr: addr, TransactionRequest: t, endpoint: e, transaction: in}
	go e.execute(ctx, in, r)
}

// comparableMId gives the same value for two spellings of one MId:
// names in the text encoding do not depend on case (H.248.1 Annex
// B.2).
func comparableMId(id MId) MId {
	id.DomainName = strings.ToLower(id.DomainName)
	id.DeviceName = strings.ToLower(id.DeviceName)
	return id
}

func (e *Endpoint) execute(ctx context.Context, in *incomingTransaction, r *IncomingRequest) {
	var reply TransactionReply
	switch {
	case len(r.Actions) == 0:
		// Nothing of the request could be read in full.
	case e.handler == nil:
		reply = TransactionReply{Error: &ErrorDescriptor{ErrorCode: 501, ErrorText: "Not Implemented"}}
	default:
		reply = e.handler(ctx, r)
	}
	if in.unread != nil {
		reply = in.unread.complete(r.TransactionRequest, reply)
	}
	e.reply(in, reply)
}

// reply sends the final reply to a request, once, and keeps it for
// LONG-TIMER to answer repetitions (Annex D.1.1). After a Pending, the
// reply asks for an immediate TransactionResponseAck (Annex D.1.4).
func (e *Endpoint) reply(in *incomingTransaction, t TransactionReply) {
	t.TransactionID = in.key.id
	in.mu.Lock()
	if in.state != executing {
		in.mu.Unlock()
		return
	}
	in.pendingTimer.Stop()
	t.ImmAckRequired = in.pendingsSent > 0
	message, err := e.marshal(in.version, t)
	if err != nil {
		e.log.Error("h248: the reply cannot be written", "transaction", t.TransactionID, "error", err)
		message, _ = e.marshal(in.version, TransactionReply{
			TransactionID:  t.TransactionID,
			ImmAckRequired: t.ImmAckRequired,
			Error:          &ErrorDescriptor{ErrorCode: 500, ErrorText: "Internal software Failure in MG or MGC"},
		})
	}
	if len(message) > e.maxSize {
		e.log.Warn("h248: the reply is too long for one message", "transaction", t.TransactionID, "octets", len(message))
		message = e.overflowReply(in.version, t)
	}
	in.state = replied
	in.reply = message
	in.cancel(nil)
	addr := in.addr
	e.clock.AfterFunc(e.timers.LongTimer, func() { e.forget(in) })
	in.mu.Unlock()
	e.write(message, addr)
}

// overflowReply writes a reply that is too long for MaxMessageSize as
// error 533 "Response exceeds maximum transport PDU size", which the
// profiles list among the errors both sides send (TS 29.334 5.7.10, TS
// 29.238 5.7.10, TS 29.333 5.7.9). H.248.8 (03/2013) 4.2.66 treats the
// command whose reply would overflow the message as if it had failed:
// the replies before it stay, and the error takes its place in its
// action. The error stands for the whole transaction only when not
// even the first command reply fits with it.
//
// The clause also leaves the rest of the transaction undone. Only the
// Handler can stop there, by asking IncomingRequest.Fits after each
// command; this is what the Endpoint does with a reply from a Handler
// that did not ask.
func (e *Endpoint) overflowReply(version int, t TransactionReply) []byte {
	overflow := &ErrorDescriptor{ErrorCode: 533, ErrorText: "Response exceeds maximum transport PDU size"}
	fallback, _ := e.marshal(version, TransactionReply{TransactionID: t.TransactionID, ImmAckRequired: t.ImmAckRequired, Error: overflow})
	best := fallback
	var done []ActionReply
	for _, a := range t.Actions {
		for kept := 0; kept <= len(a.Commands); kept++ {
			cut := TransactionReply{
				TransactionID:  t.TransactionID,
				ImmAckRequired: t.ImmAckRequired,
				Actions:        append(slices.Clip(done), ActionReply{ContextID: a.ContextID, Commands: a.Commands[:kept], Error: overflow}),
			}
			message, err := e.marshal(version, cut)
			if err != nil || len(message) > e.maxSize {
				return best
			}
			best = message
		}
		done = append(done, a)
	}
	return best
}

// sendPending tells the peer that the request is still being carried
// out (H.248.1 (05/2002) 8.2.3), and starts the timer for the next
// Pending. When PendingLimit Pendings have gone out, it sends no more,
// and stops the Handler with the cause ErrPendingLimitReached instead.
// The Handler replies with error 506 for the command it was carrying
// out (8.2.3; H.248.8 (03/2013) 4.2.47).
func (e *Endpoint) sendPending(in *incomingTransaction) {
	in.mu.Lock()
	if in.state != executing || in.limitReached {
		in.mu.Unlock()
		return
	}
	if in.pendingsSent >= e.timers.PendingLimit {
		in.limitReached = true
		in.mu.Unlock()
		in.cancel(ErrPendingLimitReached)
		return
	}
	in.pendingsSent++
	in.pendingTimer.Stop()
	in.pendingTimer = e.clock.AfterFunc(e.timers.ProvisionalResponseTimerValue, func() { e.sendPending(in) })
	addr := in.addr
	in.mu.Unlock()
	message, err := e.marshal(in.version, TransactionPending{TransactionID: in.key.id})
	if err != nil {
		e.log.Error("h248: the Pending cannot be written", "error", err)
		return
	}
	e.write(message, addr)
}

// receiveRepetition answers a request that has arrived before.
func (e *Endpoint) receiveRepetition(in *incomingTransaction, addr net.Addr) {
	in.mu.Lock()
	in.addr = addr
	state, reply := in.state, in.reply
	in.mu.Unlock()
	switch state {
	case executing:
		// Annex D.1.4: a repetition of a request that is still being
		// carried out should get a Pending.
		e.sendPending(in)
	case replied:
		e.write(reply, addr)
	case acknowledged:
		// Annex D.1.2.2: commands whose IDs were confirmed are
		// dropped without an answer.
		e.log.Debug("h248: dropped a copy of a request whose reply was confirmed", "from", addr, "transaction", in.key.id)
	}
}

// receiveResponseAck drops the replies that the peer has confirmed
// (Annex D.1.2.2).
//
// Only the transactions the Endpoint holds are affected, and those are
// kept for LONG-TIMER after their reply, as long as the annex asks the
// confirmed ranges to be kept. A range is walked one ID at a time, but
// never for more IDs than MaxTransactions, so that a wide range cannot
// keep the Endpoint busy. The replies that are not reached stay until
// LONG-TIMER.
func (e *Endpoint) receiveResponseAck(from MId, t TransactionResponseAck) {
	peer := comparableMId(from)
	budget := e.limit
	for _, ack := range t.Acks {
		last := ack.LastAck
		if last == 0 {
			last = ack.FirstAck
		}
		for id := ack.FirstAck; budget > 0; id++ {
			budget--
			if v, ok := e.incoming.Load(incomingKey{peer: peer, id: id}); ok {
				in := v.(*incomingTransaction)
				in.mu.Lock()
				if in.state == replied {
					in.state = acknowledged
					in.reply = nil
				}
				in.mu.Unlock()
			}
			if id == last {
				break
			}
		}
	}
}

// forget drops what the Endpoint keeps of a request, LONG-TIMER after
// the reply. A copy of the request that arrives later is taken as a new
// request (Annex D.1.2.2).
func (e *Endpoint) forget(in *incomingTransaction) {
	if e.incoming.CompareAndDelete(in.key, in) {
		e.incomingCount.Add(-1)
	}
}
