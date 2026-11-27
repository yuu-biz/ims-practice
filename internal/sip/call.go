package sip

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrCallEnded is what a method of a call returns when the call has
// ended already, or ends while the method waits.
var ErrCallEnded = errors.New("sip: the call has ended")

// call is what both ends of a call share: the dialogs of its INVITE,
// and the end of the call.
type call struct {
	ua    *UA
	stack *Stack
	done  chan struct{}

	mu sync.Mutex
	// dialogs are the dialogs of the INVITE, in the order they came to
	// be. The UAS has one, which its first response creates. The UAC has
	// one for each To tag among the responses (RFC 3261 13.2.2.4).
	dialogs []*CallDialog
	// kept is the dialog whose end is the end of the call: the one of
	// the UAS, or at the UAC the one that the 2xx confirmed, nil before
	// that.
	kept *CallDialog
	err  error
	// endedBy is the request of the peer that ended the call: its BYE,
	// or its CANCEL.
	endedBy *Message
	// onRequest answers the requests within the dialogs that the UA
	// leaves to the user of the call; nil when the user takes none.
	onRequest func(*InDialogRequest)
	// arrived counts the requests that the UA has handed to onRequest
	// (InDialogRequest.Arrival).
	arrived uint64
	// contact is the Contact of the messages of the call when its user
	// gave one (Invitation.Contact, IncomingCall.SetContact), "" for the
	// UA's own. recordRoute is set when the UA also stays on the route
	// of the dialog with a Record-Route of its own URI.
	contact     string
	recordRoute bool
}

// handOver starts onRequest for r, numbered after the requests that
// the UA handed over before it (InDialogRequest.Arrival). c.mu is held,
// since the UA, holding it, has just taken r in order (RFC 3261
// 12.2.2).
func (c *call) handOver(r *InDialogRequest) {
	c.arrived++
	r.Arrival = c.arrived
	r.d.addPending(r)
	go c.onRequest(r)
}

// contactLocked is the Contact value of the messages of the call: the
// one its user gave, or the UA's own. c.mu is held.
func (c *call) contactLocked() string {
	if c.contact != "" {
		return c.contact
	}
	return c.ua.contact()
}

// contactValue is contactLocked for a caller that does not hold c.mu.
func (c *call) contactValue() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.contactLocked()
}

// ownRoute is the Record-Route value with which the UA stays on the
// route of a dialog whose Contact is not its own: its own URI, as a
// loose router (RFC 3261 16.6 step 4).
func (ua *UA) ownRoute() string {
	u, err := ParseURI(ua.cfg.Contact)
	if err != nil {
		return "<" + ua.cfg.Contact + ";lr>"
	}
	u.Params.Set("lr", "")
	return NameAddr{URI: u.String()}.String()
}

func newCall(ua *UA, stack *Stack) call {
	return call{ua: ua, stack: stack, done: make(chan struct{})}
}

// open puts c among the UA's calls that have not ended. A call that
// starts while the UA closes, such as for an INVITE the stack had
// passed on just before, ends at once.
func (c *call) open() {
	c.ua.mu.Lock()
	closed := c.ua.closed
	if !closed {
		c.ua.open[c] = true
	}
	c.ua.mu.Unlock()
	if closed {
		c.mu.Lock()
		c.end("UA closed", ErrStackClosed)
		c.mu.Unlock()
	}
}

// Done is closed when the call ends: the INVITE failed or was
// rejected, or a BYE ended the session.
func (c *call) Done() <-chan struct{} { return c.done }

// Err tells why the call ended, once Done is closed. It is nil when the
// call ended the normal way, with a final response other than 2xx or
// with a BYE. Otherwise it is ErrCancelled, ErrNoACK, the error of the
// INVITE client transaction such as ErrTimeout, a 2xx that created no
// dialog, or ErrStackClosed when the UA closed.
func (c *call) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// EndedBy returns the request of the peer that ended the call, once
// Done is closed: its BYE, or the CANCEL of its INVITE. It is nil when
// the call ended another way. A B2BUA reads the Reason of that request
// to pass it on to its other leg (RFC 3326 2).
func (c *call) EndedBy() *Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.endedBy
}

// end ends the call, and every dialog it has but one that lives on for
// its subscriptions after a BYE (endSession). Only the first end
// counts. c.mu is held.
func (c *call) end(event string, err error) {
	select {
	case <-c.done:
		return
	default:
	}
	for _, d := range c.dialogs {
		if d.dialog != nil && !d.sessionEnded {
			d.setState(DialogTerminated, event)
		}
	}
	c.err = err
	close(c.done)
	c.ua.mu.Lock()
	delete(c.ua.open, c)
	c.ua.mu.Unlock()
}

// send starts a client transaction for req at the next hop that its
// Route or Request-URI names (RFC 3261 8.1.2, 12.2.1.1).
func (c *call) send(req *Message) (*ClientTransaction, error) {
	targets, err := c.nextHop(req)
	if err != nil {
		return nil, err
	}
	return c.stack.SendRequest(req, targets...)
}

// sendACK passes the ACK for a 2xx straight to the transport (RFC 3261
// 13.2.2.4).
func (c *call) sendACK(ack *Message) error {
	targets, err := c.nextHop(ack)
	if err != nil {
		return err
	}
	return c.stack.SendACK(ack, targets...)
}

func (c *call) nextHop(req *Message) ([]Destination, error) {
	hop, err := NextHop(req)
	if err != nil {
		return nil, err
	}
	return c.ua.cfg.Locate(hop)
}

// CallDialog is one dialog of a call, as the UA runs it: the Dialog
// state, and what the two UAs do within the dialog between the INVITE
// and the BYE. An IncomingCall has one, and its methods are those of
// the IncomingCall. An OutgoingCall has one for each UAS that answers
// its INVITE.
type CallDialog struct {
	c *call
	// in is the call whose INVITE this UA answers; nil at the UAC.
	in *IncomingCall

	// Guarded by c.mu.

	// dialog is nil at the UAS until its first response creates it.
	dialog *Dialog
	// acks are the ACKs for the 2xx responses to the INVITEs that this
	// UA sent within the dialog, by the CSeq number of the INVITE. Each
	// goes out again for each retransmission of its 2xx (RFC 3261
	// 13.2.2.4).
	acks map[uint32]*Message
	// pendingACK is an ACK that waits for the user of the call: the 2xx
	// carried the offer, so the ACK has to carry the answer.
	pendingACK *Message
	// reliable is RFC 3262 4 for each INVITE of this UA in the dialog
	// whose reliable provisional responses the UA takes, by the CSeq
	// number of the INVITE. The RSeq numbers count within one INVITE
	// transaction (RFC 3262 3), so each INVITE has its own.
	reliable map[uint32]*reliableSeq
	// offer is where the offer/answer exchange of the dialog stands.
	offer OfferState
	// updatePending tells that an UPDATE of the peer waits for its
	// final response (RFC 3311 5.2).
	updatePending bool
	// invite tells which INVITE transaction is in progress within the
	// dialog. There is at most one, in either direction (RFC 3261 14.1,
	// 14.2).
	invite inviteProgress
	// unacked are the re-INVITEs of the peer whose 2xx waits for its
	// ACK, by CSeq number. A second re-INVITE may come before the ACK of
	// the first (RFC 3261 14.2), so there may be more than one.
	unacked map[uint32]*InDialogRequest
	// pending are the requests of the peer within the dialog that wait
	// for their final response, in the order they arrived. A BYE in the
	// dialog answers every one of them (endPending).
	pending []*InDialogRequest

	// session is the session timer state (RFC 4028 7.2, 9). stopRefresh
	// and stopExpire stop its timers, and sessionRun counts how often
	// they were started.
	session     SessionTimer
	stopRefresh func() bool
	stopExpire  func() bool
	sessionRun  uint64
	// peerAllowsUpdate tells that the peer listed UPDATE in an Allow, so
	// that a session refresh may use it (RFC 4028 7.4).
	peerAllowsUpdate bool
	// localSDP is the session description that this UA sent last, which
	// a session refresh by re-INVITE offers again.
	localSDP Body
	// referrals are the REFERs that this UA sent in the dialog whose
	// subscription goes on, by the CSeq number of each; firstReferral is
	// that of the first REFER of the dialog (RFC 3515 2.4.6).
	referrals     map[string]*Referral
	firstReferral string
	// lastTurn is the turn of the last request that this UA sent in
	// the dialog (takeTurn).
	lastTurn turn
	// subscriptions counts the subscriptions that share the dialog with
	// the session (AddSubscriptionUsage), and sessionEnded tells that a
	// BYE has ended the session while one of them was left. The dialog
	// then lives on for them alone (endSession).
	subscriptions int
	sessionEnded  bool
}

// Dialog returns a copy of the dialog state, and false while there is
// none.
func (d *CallDialog) Dialog() (Dialog, bool) {
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	return d.copyDialog()
}

// copyDialog is Dialog with c.mu held.
func (d *CallDialog) copyDialog() (Dialog, bool) {
	if d.dialog == nil {
		return Dialog{}, false
	}
	dialog := *d.dialog
	dialog.RouteSet = slices.Clone(dialog.RouteSet)
	return dialog, true
}

// start gives d its dialog and has the UA route the requests of the
// dialog to d. c.mu is held.
func (d *CallDialog) start(dialog Dialog, event string) {
	d.dialog = &dialog
	ua := d.c.ua
	ua.mu.Lock()
	ua.calls[dialog.ID] = d
	ua.mu.Unlock()
	ua.logDialog(d.dialog, event, "")
}

// setState moves the dialog to s. c.mu is held.
func (d *CallDialog) setState(s DialogState, event string) {
	from := d.dialog.State
	if from == s {
		return
	}
	d.dialog.State = s
	ua := d.c.ua
	if s == DialogTerminated {
		d.stopSessionTimers()
		d.endReferrals()
		ua.mu.Lock()
		ua.forget(d.dialog.ID)
		ua.mu.Unlock()
	}
	ua.logDialog(d.dialog, event, from.String())
}

var errNoConfirmedDialog = errors.New("sip: no confirmed dialog to send the request in")

// AddSubscriptionUsage tells the UA that a subscription shares the
// dialog with the session: one that a SUBSCRIBE within the dialog
// created, at the notifier or at the subscriber (RFC 6665 4.5.2). The
// dialog may still be early, as when a SUBSCRIBE crosses the 2xx that
// confirms it. The dialog then has a usage besides the session, and a
// BYE ends the session only (RFC 5057 5.5). The call ends with the BYE
// as before, but the dialog stays: its SUBSCRIBEs and NOTIFYs still
// reach the user of the call, and Send still sends them, until the user
// calls end once the subscription is over. The dialog ends with the
// last of its usages (RFC 5057 4). A dialog without such a usage ends
// with its BYE, as RFC 3261 15 has it.
//
// A failure of a SUBSCRIBE or a NOTIFY that the dialog has a
// subscription for ends that subscription only (RFC 5057 5.1, 5.2): a
// 481, a 408 or a timeout leaves the session and the other usages
// alone, and the user, which knows which subscription the request was
// of, calls its end.
//
// AddSubscriptionUsage returns ErrCallEnded once the session has
// ended, or the dialog with it: the dialog takes no new usage then.
func (d *CallDialog) AddSubscriptionUsage() (end func(), err error) {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case d.dialog == nil:
		return nil, errNoDialog
	case d.dialog.State == DialogTerminated || d.sessionEnded:
		return nil, ErrCallEnded
	}
	d.subscriptions++
	return sync.OnceFunc(d.endSubscriptionUsage), nil
}

// endSubscriptionUsage takes one subscription out of the dialog, and
// ends the dialog when it was the last usage left (RFC 5057 4.2).
func (d *CallDialog) endSubscriptionUsage() {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	d.subscriptions--
	if d.subscriptions == 0 && d.sessionEnded {
		d.setState(DialogTerminated, "last subscription ended")
	}
}

// endSession ends the session of the dialog with a BYE, and the dialog
// with it unless a subscription is left in it (RFC 5057 4.1, 5.5). The
// timers and the REFERs of the session end either way. c.mu is held.
func (d *CallDialog) endSession(event string) {
	if d.subscriptions == 0 {
		d.setState(DialogTerminated, event)
		return
	}
	d.sessionEnded = true
	d.stopSessionTimers()
	d.endReferrals()
	d.c.ua.logDialog(d.dialog, event+", subscription left", d.dialog.State.String())
}

// ofSubscription tells that a request of method m belongs to a
// subscription, not to the session of the dialog (RFC 5057 5.3).
func ofSubscription(m Method) bool { return m == SUBSCRIBE || m == NOTIFY }

// Bye ends the dialog with a BYE (RFC 3261 15.1.1), and with it the
// call when this is the dialog the call goes on in. The dialog has to
// be confirmed. headers go into the BYE, such as a Reason (RFC 3326 2).
func (d *CallDialog) Bye(headers ...HeaderField) error { return d.bye(nil, headers) }

// offerEvent moves the offer/answer exchange of the dialog on by e, and
// reports why when the rules do not allow e now. c.mu is held.
func (d *CallDialog) offerEvent(e OfferEvent) error {
	next, err := d.offer.Next(e)
	if err != nil {
		return err
	}
	d.c.ua.log.Info("sip offer/answer", "call_id", d.dialog.ID.CallID, "remote_tag", d.dialog.ID.RemoteTag,
		"event", e.String(), "from", d.offer.String(), "to", next.String())
	d.offer = next
	return nil
}

// acknowledge is RFC 3261 13.2.2.4 for a 2xx to an INVITE of this UA
// with the CSeq number seq. The ACK goes out at once, unless the 2xx
// carried the offer (offered): then it waits for the answer of the
// user (Ack). c.mu is held.
func (d *CallDialog) acknowledge(seq uint32, offered bool) {
	ack := d.dialog.newACK(seq)
	if offered {
		d.pendingACK = ack
		return
	}
	addHeaders(ack, d.c.ua.own(ACK, false, nil))
	d.sendACK(seq, ack)
}

// sendACK sends the ACK for the 2xx to the INVITE with the CSeq number
// seq, and keeps it for the retransmissions of that 2xx. c.mu is held.
func (d *CallDialog) sendACK(seq uint32, ack *Message) {
	if d.acks == nil {
		d.acks = map[uint32]*Message{}
	}
	d.acks[seq] = ack
	if err := d.c.sendACK(ack); err != nil {
		d.c.ua.log.Warn("ACK not sent", "error", err.Error())
	}
}

// Ack sends the ACK for a 2xx that carried an offer, with the answer
// in body (RFC 3261 13.2.2.4). That is the 2xx to an INVITE or a
// re-INVITE that this UA sent without a body; the UA acknowledges every
// other 2xx by itself.
func (d *CallDialog) Ack(body Body, headers ...HeaderField) error {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if d.pendingACK == nil {
		return errors.New("sip: no 2xx with an offer waits for its ACK")
	}
	if len(body.Content) == 0 {
		return ErrNoAnswer
	}
	if err := d.offerEvent(AnswerSent); err != nil {
		return err
	}
	d.sent(body)
	ack := d.pendingACK
	d.pendingACK = nil
	addHeaders(ack, headers)
	body.addTo(ack)
	cseq, _ := ParseCSeq(ack.Get("CSeq"))
	d.sendACK(cseq.Number, ack)
	return nil
}

// byeOwn is bye for a BYE that the UA sends on its own, for reason.
func (d *CallDialog) byeOwn(reason error) error {
	return d.bye(reason, d.c.ua.own(BYE, false, reason))
}

// bye is RFC 3261 15.1.1: a BYE within the confirmed dialog. The session
// ends as soon as the BYE goes out, whatever the answer, and with it
// the call, with reason, when this is the dialog the call kept. So does
// the dialog, unless a subscription is left in it (endSession). The
// requests of the peer that were waiting for the user of the call get
// 487 (endPending). bye returns an error when the answer is not a 2xx.
func (d *CallDialog) bye(reason error, headers []HeaderField) error {
	c := d.c
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return ErrCallEnded
	default:
	}
	switch {
	case d.dialog == nil || d.dialog.State != DialogConfirmed:
		c.mu.Unlock()
		return errNoConfirmedDialog
	case d.sessionEnded:
		c.mu.Unlock()
		return ErrCallEnded
	}
	if d.pendingACK != nil {
		// SIMPLIFIED: RFC 3261 13.2.2.4. A UAC that ends a dialog whose
		// 2xx carried an offer MUST put a valid answer in the ACK and
		// then send the BYE. The UA has no session description of its
		// own to answer with, so the ACK goes without a body; the BYE
		// follows at once, and no media flows in between.
		cseq, _ := ParseCSeq(d.pendingACK.Get("CSeq"))
		addHeaders(d.pendingACK, c.ua.own(ACK, false, nil))
		d.sendACK(cseq.Number, d.pendingACK)
		d.pendingACK = nil
	}
	req := d.newRequest(BYE, Body{}, headers)
	d.endSession("BYE sent")
	pending := d.takePending()
	if d == c.kept {
		c.end("BYE sent", reason)
	}
	c.mu.Unlock()

	ct, err := c.send(req)
	// RFC 3261 15.1.1: the session is over once the BYE has gone to the
	// client transaction, so the requests that were waiting are answered
	// after it. A 487 ahead of the BYE would leave the peer holding a
	// dialog it still believes in, free to send the next request into it.
	d.endPending(pending)
	if err != nil {
		return err
	}
	var final *Message
	for resp := range ct.Responses() {
		final = resp
	}
	switch {
	case final == nil:
		return ct.Err()
	case final.StatusCode >= 300:
		return fmt.Errorf("sip: BYE answered with %d", final.StatusCode)
	}
	return nil
}

// receiveRequest takes a request within the dialog (RFC 3261 12.2.2).
// Once a BYE has ended the session, the dialog lives on for its
// subscriptions only, and a request of the session finds none: it gets
// 481 (Call/Transaction Does Not Exist), and a REFER 603 (Decline).
func (d *CallDialog) receiveRequest(st *ServerTransaction) {
	c := d.c
	c.mu.Lock()
	sessionEnded := d.sessionEnded
	c.mu.Unlock()
	if sessionEnded && !ofSubscription(st.Request.Method) && st.Request.Method != OPTIONS {
		// A REFER would create a subscription of its own (RFC 5057 4.2),
		// and 603 refuses a new usage without ending the dialog (5.6).
		// Any other request belongs to the ended session, or to no usage
		// at all (5.3).
		code := 481
		if st.Request.Method == REFER {
			code = 603
		}
		d.respond(st, code)
		return
	}
	// RFC 3261 12.2.2: the checks of a request outside a dialog come
	// first, among them that of Require (8.2.2.3), and a rejected request
	// changes nothing in the dialog. ACK and CANCEL never come here.
	if c.ua.refuseUnknownExtensions(st) {
		return
	}
	switch st.Request.Method {
	case BYE:
		c.mu.Lock()
		var pending []*InDialogRequest
		if d.inOrder(st) {
			pending = d.receiveBYE(st)
		}
		c.mu.Unlock()
		d.endPending(pending)
	case PRACK:
		if d.in == nil {
			// Only a UAS sends reliable provisional responses.
			c.ua.respondNotAllowed(st)
			return
		}
		d.in.receivePRACK(st)
	case OPTIONS:
		// RFC 3261 11.2 and 12.2.2: an OPTIONS within a dialog is
		// answered like one outside, and changes nothing in the dialog.
		go c.ua.answerOptions(st)
	case UPDATE:
		d.receiveUpdate(st)
	case INVITE:
		d.receiveReInvite(st)
	case NOTIFY:
		d.receiveNotify(st)
	default:
		d.receiveOther(st)
	}
}

// receiveACK takes the ACK for a 2xx that this UA sent within the
// dialog: to a re-INVITE, or at the UAS to the INVITE of the call. The
// ACK carries the CSeq number of its INVITE (RFC 3261 13.2.2.4).
func (d *CallDialog) receiveACK(ack *Message) {
	c := d.c
	cseq, _ := ParseCSeq(ack.Get("CSeq"))
	c.mu.Lock()
	if r := d.unacked[cseq.Number]; r != nil {
		if r.ack == nil {
			if len(ack.Body) > 0 && d.offer == OfferLocal {
				// The 2xx carried the offer, and the ACK has the answer.
				d.offerEvent(AnswerReceived)
			}
			r.ack = ack
			close(r.acked)
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if d.in == nil {
		c.ua.log.Info("discarded ACK that matches no 2xx", "call_id", ack.Get("Call-ID"), "cseq", ack.Get("CSeq"))
		return
	}
	d.in.receiveACK(ack)
}

// inOrder is RFC 3261 12.2.2 for a request within the dialog that the
// UA is about to accept. A request out of order gets 500 (Server
// Internal Error). c.mu is held.
func (d *CallDialog) inOrder(st *ServerTransaction) bool {
	err := d.dialog.ReceiveRequest(st.Request)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrOutOfOrder):
		d.c.ua.respondOwn(st, NewResponse(st.Request, 500, ""))
	default:
		d.c.ua.respondOwn(st, NewResponse(st.Request, 400, ""))
	}
	return false
}

// receiveBYE is RFC 3261 15.1.2: the BYE gets 200 and ends the session,
// and the call with it when this is the dialog the call kept. The
// dialog ends too, unless a subscription is left in it (endSession). It
// returns the requests of the peer that were waiting for the user of
// the call, for endPending to answer. c.mu is held.
func (d *CallDialog) receiveBYE(st *ServerTransaction) []*InDialogRequest {
	if d.in != nil && d.in.final == nil {
		// RFC 3261 15.1.2: the INVITE still pending gets 487. Only the
		// caller may send a BYE in an early dialog (15).
		d.in.sendFailure(487, "BYE received", nil, d.c.ua.own(INVITE, true, nil))
	}
	d.c.ua.respondOwn(st, NewResponse(st.Request, 200, ""))
	d.endSession("BYE received")
	pending := d.takePending()
	if d == d.c.kept {
		d.c.endedBy = st.Request
		d.c.end("BYE received", nil)
	}
	return pending
}

// addPending records r among the requests of the dialog that wait for
// the final response of the user of the call, so that a BYE answers it
// (endPending). c.mu is held.
func (d *CallDialog) addPending(r *InDialogRequest) {
	d.pending = append(d.pending, r)
}

// removePending takes r out of those requests, once it has its final
// response. c.mu is held.
func (d *CallDialog) removePending(r *InDialogRequest) {
	if i := slices.Index(d.pending, r); i >= 0 {
		d.pending = slices.Delete(d.pending, i, i+1)
	}
}

// takePending takes the requests that wait for the final response of
// the user of the call out of the dialog, for endPending to answer, and
// marks each so that an Accept or a Reject of the user from now on
// changes nothing and returns ErrCallEnded. c.mu is held, so no answer
// of the user can cross this.
func (d *CallDialog) takePending() []*InDialogRequest {
	pending := d.pending
	d.pending = nil
	for _, r := range pending {
		r.ended = true
	}
	return pending
}

// endPending answers each of the requests that takePending took out of
// the dialog with 487 (Request Terminated).
//
// RFC 3261 15.1.2 asks the UAS that received the BYE to answer the
// requests pending in that dialog, and recommends 487. The UA that
// sends the BYE itself answers them the same way: it has to consider
// the session terminated as soon as the BYE goes to the client
// transaction (15.1.1), so a 2xx after that would accept a change to a
// session that is over. A re-INVITE answered this way is left to its
// INVITE server transaction, which retransmits the 487 until the ACK
// (17.2.1).
func (d *CallDialog) endPending(pending []*InDialogRequest) {
	c := d.c
	for _, r := range pending {
		resp := NewResponse(r.Request, 487, "")
		c.mu.Lock()
		r.settle(resp, false)
		c.mu.Unlock()
		c.ua.respondOwn(r.st, resp)
	}
}

// retransmit passes resp, a response to the INVITE of st, to the
// transaction again at intervals that start at T1 and double up to
// maxInterval, until stopped closes. After 64*T1 it gives up with
// timeout. It also stops when the call ends.
func (c *call) retransmit(st *ServerTransaction, resp *Message, maxInterval time.Duration, stopped <-chan struct{}, timeout error) error {
	t := c.stack.cfg.Timers
	deadline := time.NewTimer(64 * t.T1)
	defer deadline.Stop()
	interval := t.T1
	for {
		wait := time.NewTimer(interval)
		select {
		case <-stopped:
			wait.Stop()
			return nil
		case <-c.done:
			wait.Stop()
			return ErrCallEnded
		case <-deadline.C:
			wait.Stop()
			return timeout
		case <-wait.C:
		}
		if err := st.Respond(resp); err != nil {
			return err
		}
		interval = min(2*interval, maxInterval)
	}
}
