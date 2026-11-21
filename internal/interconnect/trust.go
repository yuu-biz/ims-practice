package interconnect

import (
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the trust domain boundary (TS 24.229 4.4, TS 29.165
// 6.1.1.3.1) and the privacy protection that goes with it (TS 24.229
// 5.10.8). A trusted peer is inside the trust domain, so nothing here
// changes a message to or from one.

// treatment is what the IBCF does with a header field of the trust
// domain at the border with an untrusted peer.
type treatment int

const (
	keep treatment = iota
	// removeByPolicy is a removal that the specification leaves to
	// operator policy ("will need to determine whether to remove"). A
	// HeaderPass rule keeps the header field.
	removeByPolicy
	// removeAlways is a removal that the specification asks for.
	removeAlways
)

// trustDomainRow is one header field of the trust domain, the way TS
// 29.165 Table 6.2 lists them, with what TS 24.229 says for the column
// "Not trust relationship".
type trustDomainRow struct {
	header      string
	exit, entry treatment
	// responsesOnly limits the row to responses.
	responsesOnly bool
	// value limits the row to header fields with this value.
	value     string
	reference string
}

// trustDomainTable is TS 29.165 Table 6.2 for a non-roaming II-NNI,
// without the rows that cannot cross it at all (RemoveNotApplicable).
// History-Info is not a row: towards an untrusted peer it is
// anonymized (ApplyPrivacy), not removed.
var trustDomainTable = []trustDomainRow{
	// RFC 3325 5 has a proxy remove, or replace, a P-Asserted-Identity
	// it receives from a node it does not trust. Towards such a node,
	// RFC 3325 7 removes it when Privacy has id and leaves it to policy
	// otherwise; TS 29.165 Table 6.1.3.1 item 27 (c4) makes it not
	// applicable at an II-NNI without trust, so it is always removed.
	{header: "P-Asserted-Identity", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.2, RFC 3325 5 and 7, TS 29.165 Table 6.1.3.1 item 27"},
	{header: "P-Access-Network-Info", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.3"},
	// This project does not support priority verification by
	// assertion, so 4.4.6 asks for removal in both directions.
	{header: "Resource-Priority", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.6"},
	{header: "P-Asserted-Service", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.5"},
	// A Reason received from outside the trust domain stays (4.4.7
	// NOTE).
	{header: "Reason", exit: removeAlways, entry: keep, responsesOnly: true, reference: "TS 24.229 4.4.7"},
	{header: "P-Private-Network-Indication", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.10"},
	{header: "P-Early-Media", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.11, TS 29.165 Table 6.1.3.1 item 69"},
	{header: "Priority", value: "psap-callback", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.14"},
	{header: "Feature-Caps", exit: keep, entry: removeAlways, reference: "TS 24.229 4.4.13, 5.10.3.2, 5.10.3.3 step 5"},
	{header: "Service-Interact-Info", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.18"},
	{header: "Cellular-Network-Info", exit: removeAlways, entry: removeAlways, reference: "TS 24.229 4.4.19"},
	{header: "Response-Source", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.20"},
	{header: "Attestation-Info", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.21"},
	{header: "Origination-Id", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.22"},
	{header: "Additional-Identity", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.23"},
	{header: "Priority-Verstat", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.24"},
	{header: "DC-Info", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.25"},
	{header: "Server", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.26"},
	{header: "User-Agent", exit: removeByPolicy, entry: removeByPolicy, reference: "TS 24.229 4.4.27"},
	// The exit point removes P-Charging-Vector, or some of its
	// parameters, by operator policy (5.10.2.2 step 8); the entry point
	// removes it from an untrusted peer.
	{header: "P-Charging-Vector", exit: removeByPolicy, entry: removeAlways, reference: "TS 24.229 5.10.2.2 step 8, 5.10.3.2, 5.10.3.3 step 6"},
}

func trustRow(header string) (trustDomainRow, bool) {
	i := slices.IndexFunc(trustDomainTable, func(r trustDomainRow) bool { return strings.EqualFold(r.header, header) })
	if i < 0 {
		return trustDomainRow{}, false
	}
	return trustDomainTable[i], true
}

// ApplyTrustDomain removes what may not cross the border between the
// trust domain and an untrusted peer, for a message going in direction
// d: the header fields of trustDomainTable, and the iotl parameter
// (TS 24.229 4.4.15).
//
// The cpc and oli parameters (4.4.12) need no step of their own: TS
// 29.165 Table 6.2 NOTE 5 places them in P-Asserted-Identity, which
// does not cross either.
func ApplyTrustDomain(m *sip.Message, p Profile, d Direction) {
	if p.Trusted {
		return
	}
	for _, row := range trustDomainTable {
		t := row.entry
		if d == Exit {
			t = row.exit
		}
		if t == keep || row.responsesOnly && m.IsRequest() {
			continue
		}
		if t == removeByPolicy && p.passes(row.header) {
			continue
		}
		if row.value == "" {
			m.Remove(row.header)
			continue
		}
		replaceValues(m, row.header, slices.DeleteFunc(m.GetAll(row.header), func(v string) bool {
			return strings.EqualFold(strings.TrimSpace(v), row.value)
		}))
	}
	if m.IsRequest() {
		m.RequestURI = withoutURIParam(m.RequestURI, "iotl")
		routes := m.GetAll("Route")
		for i, v := range routes {
			if na, err := sip.ParseNameAddr(v); err == nil {
				na.URI = withoutURIParam(na.URI, "iotl")
				routes[i] = na.String()
			}
		}
		if len(routes) > 0 {
			replaceValues(m, "Route", routes)
		}
	}
}

func (p Profile) passes(header string) bool {
	return slices.ContainsFunc(p.Headers, func(r HeaderRule) bool {
		return r.Action == HeaderPass && strings.EqualFold(r.Header, header)
	})
}

// withoutURIParam removes a URI parameter from a SIP URI. Any other URI
// is returned as it is.
func withoutURIParam(uri, name string) string {
	u, err := sip.ParseURI(uri)
	if err != nil {
		return uri
	}
	if _, ok := u.Params.Get(name); !ok {
		return uri
	}
	u.Params.Remove(name)
	return u.String()
}

// CheckOrig is the first check of TS 24.229 5.10.3.2: an initial
// request from an untrusted peer whose topmost Route has the orig
// parameter asks for originating services of the home network, which
// only the trust domain may ask for. It is rejected with 403
// (Forbidden). The caller checks before it removes its own URI from
// Route.
func CheckOrig(req *sip.Message, p Profile) error {
	if p.Trusted || isInDialog(req) {
		return nil
	}
	na, err := sip.ParseNameAddr(req.Get("Route"))
	if err != nil {
		return nil
	}
	u, err := sip.ParseURI(na.URI)
	if err != nil {
		return nil
	}
	if _, orig := u.Params.Get("orig"); orig {
		return reject(403, ErrOrigFromUntrusted)
	}
	return nil
}

// RemoveReceivedRealm removes the received-realm parameter of RFC 8055
// from every Via before a request leaves (TS 24.229 5.10.2.2 step 10):
// it names a network that the home network was adjacent to.
func RemoveReceivedRealm(req *sip.Message) {
	vias := req.GetAll("Via")
	changed := false
	for i, v := range vias {
		via, err := sip.ParseVia(v)
		if err != nil {
			continue
		}
		if _, ok := via.Params.Get("received-realm"); ok {
			via.Params.Remove("received-realm")
			vias[i], changed = via.String(), true
		}
	}
	if changed {
		replaceValues(req, "Via", vias)
	}
}

// anonymousURI is the anonymous URI of RFC 3323 4.1.1.3.
const anonymousURI = "sip:anonymous@anonymous.invalid"

// ApplyPrivacy is the privacy protection that TS 24.229 5.10.8 asks of
// an IBCF before a message leaves the trust domain, on top of 4.4,
// following the Privacy header field (RFC 3323 4.2):
//
//   - none: no protection.
//   - user: the From of a request becomes anonymous, in the form that
//     5.10.8 gives and the examples of RFC 3325 10 use.
//   - header or history: every History-Info entry becomes anonymous
//     (RFC 7044 10.1.2). An entry whose own Privacy has history becomes
//     anonymous in any case.
//
// header needs no step here: Via, Record-Route and Contact belong to
// one leg of the IBCF, a B2BUA, and the peer sees only the IBCF's own
// (CrossingFields), which is what 5.10.8 item 1 asks. id needs none
// either: P-Asserted-Identity does not cross towards an untrusted peer
// at all (ApplyTrustDomain). The anonymous From goes only into the leg
// towards the peer; the leg in the home network keeps the real one, as
// the transparent B2BUA of 5.10.8 item 3 and RFC 3323 5.3 does.
//
// SIMPLIFIED: TS 24.229 5.10.8. The local policy that treats a request
// without Privacy as if it asked for id, user, header and history is
// not offered.
func ApplyPrivacy(m *sip.Message, p Profile) {
	if p.Trusted {
		return
	}
	privacy := sip.ParsePrivacy(m.Get("Privacy"))
	if privacy.Has(sip.PrivacyNone) {
		return
	}
	if m.IsRequest() && privacy.Has(sip.PrivacyUser) {
		anonymizeFrom(m)
	}
	if anonymizeHistory(m, privacy.Has(sip.PrivacyHeader) || privacy.Has(sip.PrivacyHistory)) {
		// RFC 7044 10.1.2: history is done once the entries are
		// anonymous, and an empty Privacy goes away.
		privacy = slices.DeleteFunc(privacy, func(v string) bool { return v == sip.PrivacyHistory })
		if len(privacy) == 0 {
			m.Remove("Privacy")
		} else {
			m.Set("Privacy", privacy.String())
		}
	}
}

func anonymizeFrom(m *sip.Message) {
	from, err := sip.ParseNameAddr(m.Get("From"))
	if err != nil {
		return
	}
	anonymous := sip.NameAddr{DisplayName: "Anonymous", URI: anonymousURI}
	if tag, ok := from.Params.Get("tag"); ok {
		anonymous.Params.Set("tag", tag)
	}
	m.Set("From", anonymous.String())
}

// anonymizeHistory applies RFC 7044 10.1.2 to History-Info. all makes
// every entry anonymous. It reports whether there were entries.
func anonymizeHistory(m *sip.Message, all bool) bool {
	entries, err := sip.ParseHistoryInfo(m.GetAll("History-Info"))
	if err != nil || len(entries) == 0 {
		return false
	}
	for i, e := range entries {
		target, display := e.Target(), e.DisplayName
		if all || e.Privacy().Has(sip.PrivacyHistory) {
			target, display = anonymousURI, ""
		}
		// The Privacy in the headers part of an entry goes in any case;
		// the Reason stays.
		entry := sip.NewHistoryInfoEntry(target, e.Index, e.Reasons(), nil)
		entry.DisplayName, entry.Params = display, e.Params
		entries[i] = entry
	}
	values := make([]string, len(entries))
	for i, e := range entries {
		values[i] = e.String()
	}
	replaceValues(m, "History-Info", values)
	return true
}
