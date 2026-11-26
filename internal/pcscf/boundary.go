package pcscf

import (
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The UE is outside the trust domain of the IM CN subsystem (TS 24.229
// 4.4.1), and the P-CSCF is the edge of it towards the UE. This file
// holds what the P-CSCF does to the header fields of every request and
// response that crosses that edge, in one table, so that each crossing
// gets the same treatment wherever the P-CSCF forwards it.

// crossing is one way a message crosses the edge between the UE and the
// trust domain.
type crossing int

const (
	requestFromUE crossing = iota
	responseFromUE
	requestToUE
	responseToUE
)

var (
	fromUE = []crossing{requestFromUE, responseFromUE}
	toUE   = []crossing{requestToUE, responseToUE}
	both   = []crossing{requestFromUE, responseFromUE, requestToUE, responseToUE}
)

// treatment is what a boundary rule does to its header field.
type treatment int

const (
	// remove removes every value of the header field.
	remove treatment = iota
	// removeWithParam removes the values that carry param.
	removeWithParam
	// removeParam removes param from every value.
	removeParam
	// removeIfPrivate removes the header field when the Privacy of the
	// message has "id" (RFC 3325 7).
	removeIfPrivate
)

type boundaryRule struct {
	header    string
	crossings []crossing
	treatment treatment
	param     string
	reference string
}

// ueBoundary is the table. A header field that is not in it crosses as
// it is. The P-CSCF asserts identities after the table has removed what
// came from the UE (assertOriginator, assertResponder).
//
// The table leaves out the header fields whose removal TS 24.229 4.4
// leaves to operator policy, such as History-Info (4.4.4) and
// P-Early-Media (4.4.11), and the procedures of features this P-CSCF
// does not provide: priority (4.4.6 for requests from the UE),
// restoration (Restoration-Info, 5.2.6.4.1), privileged senders and
// external attached networks (5.2.6.3.3 steps 5C and 5D). A Reason in
// a response to the UE stays: TS 24.628 Annex G has the AS that rejects
// a call after an announcement send the cause in a Reason towards the
// originating UE, so the trust domain of Reason, which TS 24.229 4.4.1
// lets have its own boundary, takes in the UE here (4.4.7). Path and
// Service-Route have no rule at the P-CSCF: the UE gets both in the 200
// to its REGISTER (5.2.1 NOTE 2, 5.2.2.1).
//
// SIMPLIFIED: TS 24.229 4.4.2 and RFC 3325 5. A proxy at the edge of
// the trust domain removes or replaces the P-Asserted-Identity of what
// it receives from a node that it does not trust. The P-CSCF tells a
// request of a UE from one of the network only by how the request is
// addressed, as both reach it at the same address and port (screen). So
// any host that reaches that port can send a request in the form that
// the network uses, with the URI that the P-CSCF put in Path as its top
// Route and the contact of a UE that the P-CSCF serves as its
// Request-URI. The P-CSCF takes such a request as one to the UE: it
// asks for no IP association, applies the rules of requestToUE, and the
// P-Asserted-Identity that the host wrote reaches the UE as an identity
// that the network asserts. A real P-CSCF takes Mw on an interface of
// its own, which only the nodes of the network reach. The containers of
// this project share one network, and the security that a commercial
// network puts on that boundary is out of scope.
var ueBoundary = []boundaryRule{
	// The charging information stays in the network; the P-CSCF ignores
	// what a UE sends.
	{header: "P-Charging-Vector", crossings: both, treatment: remove, reference: "TS 24.229 5.2.1"},
	{header: "P-Charging-Function-Addresses", crossings: both, treatment: remove, reference: "TS 24.229 5.2.1"},
	{header: "P-Media-Authorization", crossings: both, treatment: remove, reference: "TS 24.229 5.2.1"},
	// The UE tells its access network itself; only the network may say
	// "network-provided", and nothing of the access network goes to a UE.
	{header: "P-Access-Network-Info", crossings: fromUE, treatment: removeWithParam, param: "network-provided", reference: "TS 24.229 5.2.1 item 3"},
	{header: "P-Access-Network-Info", crossings: toUE, treatment: remove, reference: "TS 24.229 4.4.3, RFC 7315 4.4.2.2 and 6.4"},
	// No UE is a privileged sender here (5.2.1 NOTE 5).
	{header: "Feature-Caps", crossings: fromUE, treatment: remove, reference: "TS 24.229 5.2.1 item 5, 4.4.13"},
	{header: "Geolocation", crossings: fromUE, treatment: removeParam, param: "loc-src", reference: "TS 24.229 5.2.1 item 8"},
	// The network asserts the identities; one from the UE is not taken
	// on trust.
	{header: "P-Asserted-Identity", crossings: fromUE, treatment: remove, reference: "TS 24.229 4.4.2, 5.2.6.3.3 step 6, 5.2.6.4.4 step 1, RFC 3325 5"},
	{header: "P-Preferred-Identity", crossings: fromUE, treatment: remove, reference: "TS 24.229 5.2.6.3.3 step 6, 5.2.6.4.4 step 1"},
	{header: "P-Asserted-Identity", crossings: toUE, treatment: removeIfPrivate, reference: "TS 24.229 4.4.2, RFC 3325 7"},
	{header: "P-Served-User", crossings: both, treatment: remove, reference: "TS 24.229 4.4.9, RFC 5502 7.2"},
	// RFC 6050 5.2: the field has no use in responses.
	{header: "P-Asserted-Service", crossings: []crossing{requestFromUE, requestToUE}, treatment: remove, reference: "TS 24.229 4.4.5, RFC 6050 5.1.2"},
	// RFC 5002 8: the key tells how the services of the network are
	// built, and a UE must not choose the profile.
	{header: "P-Profile-Key", crossings: both, treatment: remove, reference: "TS 24.229 4.4.8, RFC 5002 8"},
	// No UE here may send or receive private network traffic.
	{header: "P-Private-Network-Indication", crossings: []crossing{requestFromUE, requestToUE}, treatment: remove, reference: "TS 24.229 4.4.10, 5.2.6.3.3 step 5A"},
	// This P-CSCF does not verify priority by assertion.
	{header: "Resource-Priority", crossings: toUE, treatment: remove, reference: "TS 24.229 4.4.6"},
	{header: "Relayed-Charge", crossings: both, treatment: remove, reference: "TS 24.229 4.4.17"},
	{header: "Service-Interact-Info", crossings: both, treatment: remove, reference: "TS 24.229 4.4.18"},
	{header: "Cellular-Network-Info", crossings: []crossing{requestToUE}, treatment: remove, reference: "TS 24.229 4.4.19"},
}

// crossBoundary applies ueBoundary to m, which crosses the edge as c.
func crossBoundary(m *sip.Message, c crossing) {
	for _, rule := range ueBoundary {
		if slices.Contains(rule.crossings, c) {
			rule.apply(m)
		}
	}
}

func (r boundaryRule) apply(m *sip.Message) {
	switch r.treatment {
	case remove:
		m.Remove(r.header)
	case removeIfPrivate:
		if sip.ParsePrivacy(m.Get("Privacy")).Has(sip.PrivacyID) {
			m.Remove(r.header)
		}
	case removeWithParam, removeParam:
		values := m.GetAll(r.header)
		if len(values) == 0 {
			return
		}
		var kept []string
		for _, v := range values {
			head, params := splitParams(v)
			p := sip.ParseParams(params)
			if _, has := p.Get(r.param); !has {
				kept = append(kept, v)
				continue
			}
			if r.treatment == removeParam {
				p.Remove(r.param)
				kept = append(kept, head+p.String())
			}
		}
		m.Remove(r.header)
		for _, v := range kept {
			m.Add(r.header, v)
		}
	}
}

// splitParams splits a header field value at its first ";" past a URI
// in angle brackets.
func splitParams(v string) (head, params string) {
	from := strings.IndexByte(v, '>') + 1
	if i := strings.IndexByte(v[from:], ';'); i >= 0 {
		return v[:from+i], v[from+i:]
	}
	return v, ""
}
