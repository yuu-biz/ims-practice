package b2bua

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds which header fields stay on the leg they came on.
// What a B2BUA relays is a new message in the other dialog, so what
// names that dialog and its transaction, and what routes them, belongs
// to one leg: the UA of each leg writes its own (RFC 3261 8.1.1, 12.1).
// The rest is the role's to pass on (Hooks.Request, Hooks.Response).

// LegFields are the header fields that each leg has of its own: those
// that name the transaction and the dialog, the route of the dialog,
// the hop count, the framing of the body, the reliability of
// provisional responses (RFC 3262 7), what the UA of each leg allows and
// supports, and the session timer of the dialog (RFC 4028). A role adds
// the fields that belong to its own legs, such as the registration
// paths of an II-NNI or the P-Served-User that an S-CSCF writes for an
// AS alone (RFC 5502 6): a union of both roles' fields would drop what
// the other one passes on by policy.
var LegFields = []string{
	"Via", "From", "To", "Call-ID", "CSeq", "Contact", "Record-Route", "Route", "Max-Forwards",
	"Content-Length", "Content-Type", "RSeq", "RAck", "Allow", "Supported", "Session-Expires", "Min-SE",
}

// LegOptions are the option tags of Require that the UA of each leg
// writes for itself: 100rel on a reliable provisional response, and
// timer when the peer refreshes the session (RFC 3262 7.1, RFC 4028 9).
// Any other tag, such as precondition, is the far end's and crosses.
var LegOptions = []string{sip.Option100rel, sip.OptionTimer}

// CrossingFields returns the header fields of m that go on to the other
// leg. own names the leg fields of the role, LegFields and its own;
// keep names those of them that go on all the same, such as the Allow,
// Supported and session interval of an initial INVITE, which tell the
// callee what the caller takes.
func CrossingFields(m *sip.Message, own, keep []string) []sip.HeaderField {
	var out []sip.HeaderField
	for _, h := range m.Headers {
		if oneOf(h.Name, own) && !oneOf(h.Name, keep) {
			continue
		}
		if strings.EqualFold(h.Name, "Require") {
			if h.Value = WithoutOptions(h.Value, LegOptions); h.Value == "" {
				continue
			}
		}
		out = append(out, h)
	}
	return out
}

// WithoutOptions removes the option tags drop from a list of them.
func WithoutOptions(list string, drop []string) string {
	var kept []string
	for _, tag := range strings.Split(list, ",") {
		if tag = strings.TrimSpace(tag); tag != "" && !oneOf(tag, drop) {
			kept = append(kept, tag)
		}
	}
	return strings.Join(kept, ", ")
}

// oneOf reports whether name is one of names, whichever case either is
// written in (RFC 3261 7.3.1).
func oneOf(name string, names []string) bool {
	for _, n := range names {
		if strings.EqualFold(name, n) {
			return true
		}
	}
	return false
}
