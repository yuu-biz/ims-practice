package b2bua

import (
	"slices"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the order in which one side of the call acknowledges
// the copies of the reliable provisional responses that the B2BUA sent
// it for an INVITE of the other side: the INVITE of the call, whose
// copies the Call keeps, or a re-INVITE within the dialogs, whose
// copies its relay keeps (reinvite.go).

// copied is a reliable provisional response that the B2BUA sent one
// side. of is the response of the other side that it copies, when that
// side wants that one acknowledged too (RFC 3262 4); nil when it does
// not, for a response that came without 100rel. own tells that the role
// sent the response itself (Call.Progress), so that it copies no
// response of the other side at all.
type copied struct {
	of  *sip.Message
	own bool
}

// unacked are the copies that went to one side and whose PRACK has not
// come, in the order they went. That side acknowledges them in that
// order, one PRACK for each (RFC 3262 3).
type unacked struct {
	mu     sync.Mutex
	copies []*copied
}

// sent puts k last in the order.
func (u *unacked) sent(k *copied) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.copies = append(u.copies, k)
}

// dropped takes k out of the order: no PRACK comes for a copy that did
// not go.
func (u *unacked) dropped(k *copied) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.copies = slices.DeleteFunc(u.copies, func(c *copied) bool { return c == k })
}

// acknowledged is the response of the other side whose copy the next
// PRACK, in the order the copies went, acknowledges: nil when that copy
// was of a response that the other side wants no PRACK for. own tells
// that the response was the role's own (Call.Progress), and copies none
// of the other side's.
func (u *unacked) acknowledged() (acked *sip.Message, own bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.copies) == 0 {
		return nil, false
	}
	next := u.copies[0]
	u.copies = u.copies[1:]
	return next.of, next.own
}
