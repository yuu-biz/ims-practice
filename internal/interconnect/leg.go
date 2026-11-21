package interconnect

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds which header fields an IBCF copies from one of its
// dialogs to the other. The IBCF is a B2BUA (TS 24.229 5.10.5, TS
// 29.165 5.2.1): a request that crosses the border ends in one dialog
// and a new request starts in the other, and the IBCF maps the header
// fields between them. What identifies a dialog and a transaction, and
// what routes them, belongs to one leg and does not cross. That is how
// the B2BUA hides the topology of either network: the Via, Route,
// Record-Route and Contact values of the home network, and its Call-ID
// and tags, never reach the peer, so the encryption of TS 24.229 5.10.4
// is not needed.

// legFields are the header fields that each leg has of its own: those
// that name the transaction and the dialog (RFC 3261 8.1.1, 12.1), the
// route of the dialog, the hop count, the framing of the body, the
// reliability of provisional responses (RFC 3262 7), what the UA of
// each leg allows and supports, its session timer (RFC 4028), and the
// registration paths, which a request that crosses a non-roaming
// II-NNI does not carry (TS 29.165 5.3.1).
var legFields = []string{
	"Via", "From", "To", "Call-ID", "CSeq", "Contact", "Record-Route", "Route", "Max-Forwards",
	"Content-Length", "Content-Type", "RSeq", "RAck", "Allow", "Supported", "Session-Expires", "Min-SE",
	"Path", "Service-Route",
}

// legOptions are the option tags of Require that the UA of each leg
// writes for itself: 100rel on a reliable provisional response, and
// timer when the peer refreshes the session (RFC 3262 7.1, RFC 4028 9).
// Any other tag, such as precondition, is the far end's and crosses.
var legOptions = []string{sip.Option100rel, sip.OptionTimer}

// CrossingFields returns the header fields of m that the IBCF copies
// into the message it sends on the other leg, after the Border has
// prepared m. keep names leg fields that cross all the same: an initial
// INVITE carries the caller's Allow, Supported and session interval, so
// that the callee uses no extension that the caller does not take, as
// the screening of option tags left them (ApplyOptionTags).
func CrossingFields(m *sip.Message, keep ...string) []sip.HeaderField {
	var out []sip.HeaderField
	for _, h := range m.Headers {
		if isOneOf(h.Name, legFields) && !isOneOf(h.Name, keep) {
			continue
		}
		if strings.EqualFold(h.Name, "Require") {
			if h.Value = withoutOptions(h.Value, legOptions); h.Value == "" {
				continue
			}
		}
		out = append(out, h)
	}
	return out
}

// withoutOptions removes the option tags drop from a list of them.
func withoutOptions(list string, drop []string) string {
	var kept []string
	for _, tag := range strings.Split(list, ",") {
		if tag = strings.TrimSpace(tag); tag != "" && !isOneOf(tag, drop) {
			kept = append(kept, tag)
		}
	}
	return strings.Join(kept, ", ")
}

func isOneOf(name string, names []string) bool {
	for _, n := range names {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}
