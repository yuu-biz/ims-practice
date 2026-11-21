package mmtel

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds which header fields go from one leg of a call to the
// other. TS 24.229 5.7.5.1 has a routeing B2BUA pass every signalling
// element on, supported or not, except those that a service changes.
// What the AS does not pass on are the header fields that belong to one
// leg: the UA writes its own for each leg.

// legFields are the header fields that each leg has of its own: those
// that name the transaction and the dialog (RFC 3261 8.1.1, 12.1), the
// route of the dialog, the body's framing, the reliability of
// provisional responses (RFC 3262 7), what the UA itself allows and
// supports, the session timer of the dialog (RFC 4028), and
// P-Served-User, which the S-CSCF writes for the AS only (RFC 5502 6).
var legFields = []string{
	"Via", "From", "To", "Call-ID", "CSeq", "Contact", "Record-Route", "Route", "Max-Forwards",
	"Content-Length", "Content-Type", "RSeq", "RAck", "Allow", "Supported", "Session-Expires", "Min-SE",
	"P-Served-User", "P-Charging-Vector",
}

// legOptions are the option tags of Require that the UA writes for its
// own leg: 100rel on a reliable provisional response, and timer when
// the peer refreshes the session (RFC 3262 7.1, RFC 4028 9). Any other
// tag, such as precondition, is the callee's and goes on.
var legOptions = []string{sip.Option100rel, sip.OptionTimer}

// carried returns the header fields of m that go on to the other leg,
// as a message to read and change them with. keep names leg fields that
// go on all the same.
func carried(m *sip.Message, keep ...string) *sip.Message {
	out := &sip.Message{}
	for _, h := range m.Headers {
		if isOneOf(h.Name, legFields) && !isOneOf(h.Name, keep) {
			continue
		}
		if strings.EqualFold(h.Name, "Require") && !isOneOf(h.Name, keep) {
			if h.Value = withoutOptions(h.Value, legOptions); h.Value == "" {
				continue
			}
		}
		out.Headers = append(out.Headers, h)
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

// chargingVector is the P-Charging-Vector of a request that the AS
// sends on: TS 24.229 5.7.5.1 has a routeing B2BUA keep the orig-ioi of
// the request it received and take it out of the request it sends. The
// icid-value stays, so that both legs are charged as one call.
func chargingVector(req *sip.Message) string {
	var kept []string
	for _, p := range strings.Split(req.Get("P-Charging-Vector"), ";") {
		name, _, _ := strings.Cut(strings.TrimSpace(p), "=")
		if name != "" && !strings.EqualFold(name, "orig-ioi") && !strings.EqualFold(name, "term-ioi") {
			kept = append(kept, strings.TrimSpace(p))
		}
	}
	return strings.Join(kept, ";")
}

// responseChargingVector is the P-Charging-Vector of a response that
// the AS sends to req (TS 24.229 5.7.5.1): the icid-value and the
// orig-ioi that req carried, and a type 3 term-ioi that names the home
// network.
func responseChargingVector(req *sip.Message, homeDomain string) string {
	var icid, origIOI string
	for _, p := range strings.Split(req.Get("P-Charging-Vector"), ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch strings.ToLower(name) {
		case "icid-value":
			icid = value
		case "orig-ioi":
			origIOI = value
		}
	}
	if icid == "" {
		return ""
	}
	pcv := "icid-value=" + icid
	if origIOI != "" {
		pcv += ";orig-ioi=" + origIOI
	}
	return pcv + ";term-ioi=" + homeDomain
}

// reasons are the Reason header fields of the request that ended a leg,
// to pass on in the request that ends the other leg (RFC 3326 2).
func reasons(ended *sip.Message) []sip.HeaderField {
	var h []sip.HeaderField
	if ended != nil {
		for _, r := range sip.Reasons(ended) {
			h = append(h, r.Header())
		}
	}
	return h
}
