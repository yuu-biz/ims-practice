package interconnect

import (
	"slices"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds which header fields an IBCF copies from one of its
// dialogs to the other. The IBCF is a B2BUA (TS 24.229 5.10.5, TS
// 29.165 5.2.1): a request that crosses the border ends in one dialog
// and a new request starts in the other, and the IBCF maps the header
// fields between them. What identifies a dialog and a transaction, and
// what routes them, belongs to one leg and does not cross
// (b2bua.LegFields). That is how the B2BUA hides the topology of either
// network: the Via, Route, Record-Route and Contact values of the home
// network, and its Call-ID and tags, never reach the peer, so the
// encryption of TS 24.229 5.10.4 is not needed.

// legFields are the fields of each leg of the IBCF: those of every
// B2BUA, and the registration paths, which a request that crosses a
// non-roaming II-NNI does not carry (TS 29.165 5.3.1).
var legFields = append(slices.Clone(b2bua.LegFields), "Path", "Service-Route")

// CrossingFields returns the header fields of m that the IBCF copies
// into the message it sends on the other leg, after the Border has
// prepared m. keep names leg fields that cross all the same: an initial
// INVITE carries the caller's Allow, Supported and session interval, so
// that the callee uses no extension that the caller does not take, as
// the screening of option tags left them (ApplyOptionTags).
func CrossingFields(m *sip.Message, keep ...string) []sip.HeaderField {
	return b2bua.CrossingFields(m, legFields, keep)
}
