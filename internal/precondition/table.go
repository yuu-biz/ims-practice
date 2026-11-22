package precondition

import (
	"errors"
	"fmt"
)

// Role tells which end of the session the table belongs to. Only the
// terminating side alerts its user, so only it waits for the
// preconditions and asks the other side to confirm (RFC 3312 6).
type Role int

const (
	Originating Role = iota // the UA that sends the INVITE
	Terminating             // the UA that receives it
)

// Row is one row of the local status table (RFC 3312 5.1, Tables 1 and
// 2): one direction of one segment, from the point of view of the UA
// that owns the table.
type Row struct {
	Status    StatusType
	Direction Direction // Send or Recv
	Current   bool      // resources are reserved
	Strength  Strength
	// Confirm tells that the peer asked, with a=conf, to be told when
	// Current becomes true (RFC 3312 7).
	Confirm bool
	// Own tells that this UA learns the row's status itself, from its
	// own access network, rather than from the peer's SDP.
	Own bool
}

// Table is the local status table of one media stream (RFC 3312 5).
// The transaction status table of each offer and answer is not kept:
// it is the Attributes that Offer and Answer return and that Answer and
// ApplyAnswer read.
type Table struct {
	role      Role
	segmented bool
	rows      []Row
	// peerUses is false once the peer has sent an SDP without any
	// precondition: it does not use them, so this UA writes none either.
	peerUses bool
	// confirmReported is whether the last SDP this UA wrote showed every
	// row the peer asked about as reserved (RFC 3312 7).
	confirmReported bool
	// ownReported is the reservation of this UA's own rows as the last
	// SDP it wrote showed it.
	ownReported Direction
}

// NewSegmented is the table of a UA that reserves resources in its own
// access network only, as a UE on EPS does (TS 24.229 6.1.2). The
// strengths are what this UA wants for its own network and the
// peer's; the peer may raise them, but never lower them.
func NewSegmented(role Role, local, remote Strength) *Table {
	t := &Table{role: role, segmented: true, peerUses: true}
	for _, status := range []StatusType{Local, Remote} {
		strength := local
		if status == Remote {
			strength = remote
		}
		for _, dir := range []Direction{Send, Recv} {
			t.rows = append(t.rows, Row{Status: status, Direction: dir, Strength: strength, Own: status == Local})
		}
	}
	return t
}

// NewEndToEnd is the table of a UA that uses the end-to-end status
// type. own lists the directions whose reservation this UA learns
// itself: in RFC 3312 13.1, a UA that receives RSVP RESV messages
// knows its send direction only.
func NewEndToEnd(role Role, strength Strength, own Direction) *Table {
	t := &Table{role: role, peerUses: true}
	for _, dir := range []Direction{Send, Recv} {
		t.rows = append(t.rows, Row{Status: EndToEnd, Direction: dir, Strength: strength, Own: own&dir != 0})
	}
	return t
}

// Rows returns a copy of the table, for logs and the Lab.
func (t *Table) Rows() []Row { return append([]Row(nil), t.rows...) }

// SetReserved records what the access network says about this UA's own
// resources in directions d, such as a dedicated bearer that has been
// set up or lost (TS 24.229 L.2.2.5.1B). Rows the UA does not own are
// left alone.
func (t *Table) SetReserved(d Direction, reserved bool) {
	for i := range t.rows {
		r := &t.rows[i]
		if r.Own && d&r.Direction != 0 {
			r.Current = reserved
		}
	}
}

// Met tells that every mandatory precondition is met, so that a UA
// that suspended the session may alert its user (RFC 3312 6).
func (t *Table) Met() bool {
	for _, r := range t.rows {
		if r.Strength == Mandatory && !r.Current {
			return false
		}
	}
	return true
}

// NeedsOffer tells that this UA must send an offer now to report its
// reservation:
//
//   - the peer asked for confirmation, and the rows it asked about have
//     changed since the last SDP this UA wrote, either all of them
//     reserved at last or one of them lost again (RFC 3312 7);
//   - or this UA is the originating one and its own reservation has
//     changed since its last SDP. The originating UE reports it in the
//     next PRACK or UPDATE even when nobody asked (TS 24.229 5.1.3.1),
//     because the terminating side cannot alert before it knows.
func (t *Table) NeedsOffer() bool {
	if !t.peerUses {
		return false
	}
	if t.confirmed() != t.confirmReported {
		return true
	}
	return t.role == Originating && t.own() != t.ownReported
}

func (t *Table) own() Direction {
	var d Direction
	for _, r := range t.rows {
		if r.Own && r.Current {
			d |= r.Direction
		}
	}
	return d
}

func (t *Table) confirmed() bool {
	asked := false
	for _, r := range t.rows {
		if r.Confirm {
			asked = true
			if !r.Current {
				return false
			}
		}
	}
	return asked
}

// Offer writes the attributes of a new offer: the transaction status
// table is the local one (RFC 3312 5.1).
func (t *Table) Offer() Attributes {
	if !t.peerUses {
		return Attributes{}
	}
	return t.write()
}

// Answer reads the preconditions of an offer the peer sent, updates the
// table and writes the attributes of the answer (RFC 3312 5.2, with the
// table of RFC 4032 4.1). An offer without preconditions gets an
// answer without them.
func (t *Table) Answer(offer Attributes) (Attributes, error) {
	if err := t.checkOffer(offer); err != nil {
		return Attributes{}, err
	}
	if err := t.read(offer); err != nil {
		return Attributes{}, err
	}
	if !t.peerUses {
		return Attributes{}, nil
	}
	return t.write(), nil
}

// ApplyAnswer reads the preconditions of the answer to this UA's offer
// (the last table of RFC 4032 4.1).
func (t *Table) ApplyAnswer(answer Attributes) error {
	return t.read(answer)
}

// ErrUnknownPrecondition means the offer asks for a mandatory
// precondition of a type this UA does not know. The UA refuses the
// offer (RFC 3312 9), with 580 in SIP.
var ErrUnknownPrecondition = errors.New("precondition: mandatory precondition of an unknown type")

// checkOffer is RFC 3312 9. An unknown mandatory precondition on the
// offerer's own network is still fine: the offerer meets it alone and
// confirms it.
func (t *Table) checkOffer(offer Attributes) error {
	for _, d := range offer.Desired {
		if d.Type != QoS && d.Strength == Mandatory && d.Status != Local {
			return fmt.Errorf("%w: %s", ErrUnknownPrecondition, d)
		}
	}
	return nil
}

// read merges an SDP from the peer into the table. Its tags are the
// peer's, so they are inverted first (RFC 3312 5.2, Table 4).
//
// The current status of a row this UA owns is never taken from the
// peer: the UA keeps its table up to date from local information (the
// end of RFC 3312 5.2), and the peer can only echo what this UA said
// before the reservation finished. Every other row takes the peer's
// value, upgrades and downgrades alike (RFC 4032 4.1).
//
// SIMPLIFIED: RFC 4032 4.1 and 4.2. A stream that moves to a new
// address is not detected, so the current status is not reset and the
// desired strength is never lowered. The UE simulator keeps the same
// address for the whole call; hold changes the direction attribute only.
func (t *Table) read(peer Attributes) error {
	if !hasQoS(peer) {
		t.peerUses = false
		return nil
	}
	t.peerUses = true
	for _, d := range peer.Desired {
		if d.Type != QoS {
			continue
		}
		if d.Strength > Mandatory {
			return fmt.Errorf("precondition: des:%s refuses the session; it is not an offer or an answer (RFC 3312 8)", d)
		}
		for _, r := range t.matching(d.Status, d.Direction) {
			r.Strength = max(r.Strength, d.Strength)
		}
	}
	for _, c := range peer.Current {
		if c.Type != QoS {
			continue
		}
		if !t.known(c.Status.invert()) {
			return fmt.Errorf("precondition: curr:%s does not fit a table of this status type", c)
		}
		for _, r := range t.matching(c.Status, SendRecv) {
			if !r.Own {
				r.Current = c.Direction.invert()&r.Direction != 0
			}
		}
	}
	for _, c := range peer.Confirm {
		if c.Type != QoS {
			continue
		}
		for _, r := range t.matching(c.Status, c.Direction) {
			r.Confirm = true
		}
	}
	return nil
}

func hasQoS(a Attributes) bool {
	for _, c := range a.Current {
		if c.Type == QoS {
			return true
		}
	}
	for _, d := range a.Desired {
		if d.Type == QoS {
			return true
		}
	}
	return false
}

// matching returns the rows a line from the peer names, after
// inverting its tags.
func (t *Table) matching(peerStatus StatusType, peerDir Direction) []*Row {
	status, dir := peerStatus.invert(), peerDir.invert()
	var out []*Row
	for i := range t.rows {
		if t.rows[i].Status == status && t.rows[i].Direction&dir != 0 {
			out = append(out, &t.rows[i])
		}
	}
	return out
}

func (t *Table) known(status StatusType) bool {
	if t.segmented {
		return status == Local || status == Remote
	}
	return status == EndToEnd
}

// write encodes the table as RFC 3312 5.1.1 says: one a=curr line per
// segment, and one a=des line per segment when both directions have
// the same strength, or one per direction when they differ.
//
// A terminating UA also asks for confirmation of the mandatory rows it
// cannot learn itself and that are not met yet (RFC 3312 6 and 7). The
// originating UA asks for none (TS 24.229 6.1.2).
func (t *Table) write() Attributes {
	var a Attributes
	for _, status := range t.statusTypes() {
		send, recv := t.row(status, Send), t.row(status, Recv)
		var current, confirm Direction
		for _, r := range []*Row{send, recv} {
			if r.Current {
				current |= r.Direction
			}
			if t.role == Terminating && !r.Own && r.Strength == Mandatory && !r.Current {
				confirm |= r.Direction
			}
		}
		a.Current = append(a.Current, Current{Type: QoS, Status: status, Direction: current})
		if send.Strength == recv.Strength {
			a.Desired = append(a.Desired, Desired{Type: QoS, Strength: send.Strength, Status: status, Direction: SendRecv})
		} else {
			a.Desired = append(a.Desired,
				Desired{Type: QoS, Strength: send.Strength, Status: status, Direction: Send},
				Desired{Type: QoS, Strength: recv.Strength, Status: status, Direction: Recv})
		}
		if confirm != None {
			a.Confirm = append(a.Confirm, Confirm{Type: QoS, Status: status, Direction: confirm})
		}
	}
	t.confirmReported, t.ownReported = t.confirmed(), t.own()
	return a
}

func (t *Table) statusTypes() []StatusType {
	if t.segmented {
		return []StatusType{Local, Remote}
	}
	return []StatusType{EndToEnd}
}

func (t *Table) row(status StatusType, dir Direction) *Row {
	for i := range t.rows {
		if t.rows[i].Status == status && t.rows[i].Direction == dir {
			return &t.rows[i]
		}
	}
	panic("precondition: no row " + string(status) + " " + dir.String())
}
