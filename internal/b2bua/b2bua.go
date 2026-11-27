// Package b2bua relays a call between the two dialogs of a
// back-to-back user agent (B2BUA): the dialog that ends the
// caller's INVITE, in which the B2BUA is the UAS, and the dialog of the
// INVITE that the B2BUA sends on, in which it is the UAC. Each dialog
// has its own Call-ID, tags, CSeq, Via and Contact, and what one dialog
// receives goes on in a new message of the other: the provisional
// responses with their PRACKs (RFC 3262), the 2xx and its ACK, the
// requests within the dialogs whatever their method, and the end of
// the call.
//
// A role that is a B2BUA, such as the IBCF (TS 24.229 5.10.5), keeps
// the order of its call: what it checks, reserves and decides before
// and after each step. A Call does the steps that every such role does
// the same way, and asks the role (Hooks) what each relayed message
// carries.
package b2bua

import "github.com/yuu-biz/ims-practice/internal/sip"

// Direction is which way a message crosses the B2BUA: toward the
// callee or toward the caller.
type Direction int

const (
	// ToCallee is the way of the INVITE and of the caller's requests.
	ToCallee Direction = iota
	// ToCaller is the way of the responses to the INVITE and of the
	// callee's requests.
	ToCaller
)

// reverse is the way of the responses to a request that crosses in d.
func (d Direction) reverse() Direction {
	if d == ToCallee {
		return ToCaller
	}
	return ToCallee
}

// Message is what a role adds to a message it relays: header fields and
// the body.
type Message struct {
	Headers []sip.HeaderField
	Body    sip.Body
}

// Refusal is the response with which a role refuses a request instead of
// relaying it. Release has the Call end the call once the request has
// its response, for a role that cannot let the call go on as it was. A
// refused PRACK of a reliable response of the callee ends the call in
// any case, since that response gets no PRACK then.
type Refusal struct {
	Status  int
	Headers []sip.HeaderField
	Release bool
}

// Hooks are what a role does to the messages that its Call relays. The
// UA of each dialog writes the header fields that belong to that dialog
// (RFC 3261 8.1.1, 12.1); the role returns the others that cross, and
// the body.
//
// A response or an ACK cannot be refused as a request can. When the
// role cannot pass one on, such as one whose session description it
// cannot rewrite, it returns false, and the Call ends the call instead
// of relaying it (Call.release). The 2xx of a request within a dialog
// is then answered with 500 on the other leg, and so is the caller's
// INVITE while it has no final response.
type Hooks interface {
	// Request prepares req, a request within a dialog that crosses in
	// d, for the other dialog, or refuses it.
	Request(req *sip.Message, d Direction) (Message, *Refusal)
	// Response prepares resp, a response to req that crosses in d. ok
	// is false when the role cannot pass a provisional response or a
	// 2xx on; it is not read for any other.
	Response(resp, req *sip.Message, d Direction) (m Message, ok bool)
	// Answer prepares the callee's 2xx for the caller. session is the
	// last response with a session description of the early dialog
	// that answered, when the caller has the session description of
	// another early dialog; resp then comes without its body, since the
	// caller takes only the first one (RFC 3261 13.2.1). session is nil
	// otherwise. ok is false when the role cannot pass the 2xx on.
	Answer(resp, session *sip.Message) (m Message, ok bool)
	// Ack prepares ack, the ACK with the answer to an offer that a 2xx
	// carried, which crosses in d (RFC 3261 13.2.2.4, 14.1), and returns
	// its body. ok is false when the role cannot pass the answer on.
	Ack(ack *sip.Message, d Direction) (body sip.Body, ok bool)
	// Contact is the Contact that the caller's dialog takes for the
	// Contact of resp, a response of the callee that goes to the caller,
	// with recordRoute when the B2BUA stays on the route of that dialog
	// all the same (sip.IncomingCall.SetContact). ok is false to keep
	// the one the UA writes.
	Contact(resp *sip.Message) (c sip.NameAddr, recordRoute, ok bool)
	// Reasons are the Reason header fields of ended, the request that
	// ended one dialog or cancelled a re-INVITE on one leg, for the
	// request that ends the other dialog or cancels the re-INVITE of the
	// other leg, which crosses in d (RFC 3326 2).
	Reasons(ended *sip.Message, d Direction) []sip.HeaderField
	// KeepDialogs gets the two dialogs once the callee has answered, and
	// again after each request within them: a target refresh request
	// changes the remote target of its dialog (RFC 3261 12.2), and each
	// request its CSeq.
	KeepDialogs(in, out sip.Dialog)
	// Unanswered tells the role that req, a request within a dialog that
	// it prepared for the other dialog (Request), got no 2xx there: a
	// final response other than 2xx, or none at all. The session is then
	// as it was before req (RFC 3261 14.1, RFC 3311 5.3), and the role
	// puts back what it changed for an offer in req. ok is false when it
	// cannot, and the Call then releases the call once req has its
	// response.
	Unanswered(req *sip.Message, d Direction) (ok bool)
	// Refused tells the role that the Call has answered req, a request
	// that crosses in d, with status in the place of relaying it or its
	// answer: with the status of a Refusal, with 500 for a response that
	// the role cannot pass on, or with the 500 to the caller's INVITE of
	// a release. It is called once the response has gone.
	Refused(req *sip.Message, d Direction, status int)
}
