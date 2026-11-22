package lab

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Nothing in a Cx message names the SIP request it was sent for: the
// Session-Id of Diameter and the Call-ID of SIP are unrelated, and HEP
// carries no link either. The Lab links them by the rule of the
// procedures instead. A Cx request that a node sends is caused by the
// SIP request that this node received last, at most causeWindow before,
// and that names the same user:
//
//   - UAR (TS 24.229 5.3.1.2), MAR (5.4.1.2.1) and SAR (5.4.1.2.2A): a
//     REGISTER whose Authorization has the User-Name as username, or
//     whose To is the Public-Identity.
//   - LIR (TS 24.229 5.3.2.1): a request whose Request-URI is the
//     Public-Identity, such as an INVITE or the SUBSCRIBE of the reg
//     event.
//
// An Rx session links the same way (TS 29.214 A.1, A.4). The P-CSCF
// sends its first AAR for the media of a call when it has the SDP
// answer, and its Framed-IP-Address is the address of the UE: the cause
// is the SIP message with SDP that the P-CSCF received from that address
// last, the INVITE of a calling UE or the 183 of a called one. The AAR
// for the signalling path follows a REGISTER from that address. The
// rule takes the newer of the two, so a call made right after a
// registration goes with its INVITE.
//
// Both copies are the node's own, so both times come from one clock.
// The answer, and the HSS's copies, get the link through the Session-Id;
// so do the later requests of an Rx session, its STR, RAR or ASR.
//
// The rule does not ask whether the node still handles the request: the
// S-CSCF answers a deregistration with 200 before it sends the SAR (see
// "Deregistration" in docs/guide/en/registration.md), and that SAR
// still belongs to the REGISTER. A Cx request that a timer starts, such
// as the SAR when a registration expires, is therefore linked to a
// REGISTER of the same user that the node received less than
// causeWindow before, if there was one.
const causeWindow = 2 * time.Second

// identityField is one identity of a SIP request that a Cx request can
// name.
type identityField int

const (
	registerUsername identityField = iota + 1 // the username of the Authorization of a REGISTER
	registerTo                                // the URI of the To of a REGISTER
	requestURI                                // the Request-URI of any request
	sdpFrom                                   // the address a message with SDP came from
	registerFrom                              // the address a REGISTER came from
)

type avpID struct{ code, vendor uint32 }

var (
	avpUserName       = avpID{1, 0}       // RFC 6733 8.14
	avpPublicIdentity = avpID{601, 10415} // TS 29.229 6.3.2
	// From NASREQ, in Rx (TS 29.214 Table 5.4.0.1).
	avpFramedIPAddress = avpID{8, 0}
)

// diameterCauses is, for each Cx and Rx request, the AVPs that name the
// user and the identity of the causing SIP message that each of them
// equals.
var diameterCauses = map[uint32][]struct {
	avp   avpID
	field identityField
}{
	300: {{avpUserName, registerUsername}, {avpPublicIdentity, registerTo}},  // UAR
	303: {{avpUserName, registerUsername}, {avpPublicIdentity, registerTo}},  // MAR
	301: {{avpUserName, registerUsername}, {avpPublicIdentity, registerTo}},  // SAR
	302: {{avpPublicIdentity, requestURI}},                                   // LIR
	265: {{avpFramedIPAddress, sdpFrom}, {avpFramedIPAddress, registerFrom}}, // AAR
}

// causeKey is one identity of a SIP request, as one node received it.
// The node is the address its HEP copies come from: role names repeat
// when a role runs on several hosts.
type causeKey struct {
	node     netip.Addr
	field    identityField
	identity string
}

// cause is a SIP request that a Cx request may have been sent for.
type cause struct {
	id     uint64 // of the message in the log, which the cause goes with
	time   time.Time
	callID string
}

// causeIndex holds, for each identity, the requests in the log that
// named it, in arrival order.
type causeIndex map[causeKey][]cause

// causeKeys are the identities that the SIP message s names, as the node
// at from received it from the address src. A response names only its
// source, when it carries SDP.
func causeKeys(s *sip.Message, from, src netip.Addr) []causeKey {
	var keys []causeKey
	add := func(field identityField, identity string, err error) {
		if err == nil && identity != "" {
			keys = append(keys, causeKey{from, field, identity})
		}
	}
	mediaType, _, _ := strings.Cut(s.Get("Content-Type"), ";")
	if src.IsValid() && strings.EqualFold(strings.TrimSpace(mediaType), "application/sdp") {
		add(sdpFrom, src.String(), nil)
	}
	if !s.IsRequest() {
		return keys
	}
	if src.IsValid() && s.Method == sip.REGISTER {
		add(registerFrom, src.String(), nil)
	}
	// The nodes write the identities in the form the HSS stores
	// (TS 29.229 6.3.2), so the SIP side is put in that form too.
	if s.Method != sip.REGISTER {
		uri, err := ims.CanonicalURI(s.RequestURI)
		add(requestURI, uri, err)
		return keys
	}
	// The Request-URI of a REGISTER names the domain, not a user
	// (RFC 3261 10.2).
	to, err := ims.PublicIdentity(s.Get("To"))
	add(registerTo, to, err)
	add(registerUsername, registerIMPI(s), nil)
	return keys
}

func (ix causeIndex) add(keys []causeKey, c cause) {
	for _, k := range keys {
		ix[k] = append(ix[k], c)
	}
}

// remove drops the cause of message id, which goes from the log first
// as it came first.
func (ix causeIndex) remove(keys []causeKey, id uint64) {
	for _, k := range keys {
		list := ix[k]
		for i, c := range list {
			if c.id == id {
				list = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(ix, k)
		} else {
			ix[k] = list
		}
	}
}

// callIDOf is the Call-ID of the SIP request that caused the Cx request
// d, which the node at from sent at t, or "" when none did.
func (ix causeIndex) callIDOf(d *diameterMessage, from netip.Addr, t time.Time) string {
	var best cause
	for _, rule := range diameterCauses[d.Code] {
		for _, identity := range d.identities(rule.avp) {
			list := ix[causeKey{from, rule.field, identity}]
			// Newest first. The copies of one node arrive in the order it
			// sent them, so an older one ends the search.
			for i := len(list) - 1; i >= 0; i-- {
				c := list[i]
				if c.time.After(t) {
					continue
				}
				if t.Sub(c.time) > causeWindow {
					break
				}
				if best.callID == "" || c.time.After(best.time) || c.time.Equal(best.time) && c.id > best.id {
					best = c
				}
				break
			}
		}
	}
	return best.callID
}

// identities are the values of the top-level AVPs a, in the form the
// SIP side has them: text as it is, and a Framed-IP-Address, four octets
// of an IPv4 address, as the address.
func (d *diameterMessage) identities(a avpID) []string {
	var out []string
	for _, avp := range d.AVPs {
		var s string
		if avp.Code != a.code || avp.Vendor != a.vendor || json.Unmarshal(avp.Value, &s) != nil {
			continue
		}
		if a == avpFramedIPAddress {
			s = framedIPAddress(s)
		}
		out = append(out, s)
	}
	return out
}

// framedIPAddress reads the value of a Framed-IP-Address as the nodes
// write it in JSON: an OctetString in base64, or as text when its
// octets happen to be text (diameter.EncodeJSON).
func framedIPAddress(v string) string {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != 4 {
		b = []byte(v)
	}
	if a, ok := netip.AddrFromSlice(b); ok && a.Is4() {
		return a.String()
	}
	return ""
}
