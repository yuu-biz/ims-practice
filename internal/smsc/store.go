package smsc

import (
	"context"
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// Message is a short message, or a status report, that the SC holds
// until it is delivered or its validity period ends (TS 23.040 6.1).
type Message struct {
	// ID names the message in the Store.
	ID string
	// Destination is the MSISDN of the recipient, digits only. The
	// messages of one destination form one queue.
	Destination string
	// Exactly one of Deliver and StatusReport is set: the TPDU to send
	// (TS 23.040 9.2.2.1, 9.2.2.3). The SC writes TP-MMS anew at each
	// attempt, from what else waits in the queue (3.2.4).
	Deliver      *sms.Deliver
	StatusReport *sms.StatusReport
	// StatusReportRequest is set when the sender of an SMS-SUBMIT asked
	// for a status report (TP-SRR, TS 23.040 9.2.3.5).
	StatusReportRequest *StatusReportRequest
	// Expires is the end of the validity period (TS 23.040 3.2.1).
	Expires time.Time
}

// StatusReportRequest is what the SC needs to write the
// SMS-STATUS-REPORT of TS 23.040 9.2.2.3 once it knows the fate of the
// short message.
type StatusReportRequest struct {
	// Originator is the MSISDN of the sender, digits only: the report
	// goes there.
	Originator string
	// MessageReference is the TP-MR of the SMS-SUBMIT, which the report
	// repeats (9.2.3.6).
	MessageReference uint8
	// Recipient is the TP-DA of the SMS-SUBMIT, the TP-RA of the report
	// (9.2.3.14).
	Recipient sms.Address
}

// Lease is the right of one SC instance to deliver to one destination
// for a while. TS 23.040 6.2 allows only one outstanding SMS-DELIVER to
// an MS at a time; the lease is how several instances that share a
// Store keep to that, and how none of them delivers a message that
// another one is delivering.
type Lease struct {
	Destination string
	// Token tells this lease from a later one for the same destination.
	// A Store refuses the calls of a lease that has been taken over.
	Token string
	// Attempts is the number of attempts in a row that failed for a
	// reason that may pass, as the last Release recorded it.
	Attempts int
}

// ErrLeaseLost is the error of a call with a lease that ran out and
// that another instance has claimed since.
var ErrLeaseLost = errors.New("smsc: the lease has been taken over")

// Store keeps the messages that the SC holds, outside of any one SC
// instance, so that several instances can serve one network:
// any instance may accept a message, and any instance may deliver it.
//
// The state is a queue of messages for each destination, and one
// time-ordered index of the destinations: the time at which each is due
// for its next attempt. Every operation touches one destination, or
// reads the front of the index; none walks all of them. Each operation
// is atomic on its own. A store such as Redis can do the same with a
// list per destination, a sorted set as the index and a script per
// operation. While a destination is leased its time in the index is the
// end of the lease, so that another instance takes it over if this one
// stops.
//
// MemoryStore keeps the state in the process.
type Store interface {
	// Enqueue appends m to the queue of m.Destination. A destination
	// that had no queue becomes due at now; one that had a queue keeps
	// its time, since its messages go out in order.
	Enqueue(ctx context.Context, m Message, now time.Time) error
	// ServiceCentreTimeStamp returns the TP-SCTS for a message to
	// destination that arrives at now: now to the second, or, if an
	// earlier message to the same destination took that second, the
	// first second after it. TS 23.040 6.2 wants the time stamps of the
	// messages to one MS to differ.
	ServiceCentreTimeStamp(ctx context.Context, destination string, now time.Time) (time.Time, error)
	// ClaimDue leases the destination that has been due longest, if one
	// is due at now, for the duration lease.
	ClaimDue(ctx context.Context, now time.Time, lease time.Duration) (Lease, bool, error)
	// Claim leases destination if it is due at now.
	Claim(ctx context.Context, destination string, now time.Time, lease time.Duration) (Lease, bool, error)
	// Queue returns the messages of the leased destination, oldest
	// first.
	Queue(ctx context.Context, l Lease) ([]Message, error)
	// Remove drops the message id from the queue of the leased
	// destination.
	Remove(ctx context.Context, l Lease, id string) error
	// Release ends the lease. If the queue is empty the destination is
	// forgotten; otherwise it becomes due at next, or at the time of an
	// Alert that came during the lease if that is earlier, and attempts
	// is kept for the next lease.
	Release(ctx context.Context, l Lease, next time.Time, attempts int) error
	// Alert makes destination due at now, if it has a queue. It reports
	// whether it had one.
	Alert(ctx context.Context, destination string, now time.Time) (bool, error)

	// BeginOFR records that an instance has taken the OFR that request
	// names (diameter.DuplicateKey), and keeps the record until expires.
	// It reports false, and changes nothing, when a record of request is
	// there at now already: the OFR is a copy of one the SC has seen.
	// Of several instances that begin the same request at once, one
	// gets true.
	BeginOFR(ctx context.Context, request string, now, expires time.Time) (bool, error)
	// PutOFA keeps the answer to the OFR that request names in its
	// record, until expires.
	PutOFA(ctx context.Context, request string, a sgd.MOForwardShortMessageAnswer, expires time.Time) error
	// OFA returns the answer to the OFR that request names, and whether
	// the record holds one at now. It holds none while the instance that
	// began the request has not answered it yet.
	OFA(ctx context.Context, request string, now time.Time) (sgd.MOForwardShortMessageAnswer, bool, error)
}
