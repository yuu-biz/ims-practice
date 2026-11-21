package pcscf

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// ueResponse is what the P-CSCF does to resp, a response to req, an
// initial request that passed the P-CSCF in the direction l, before resp
// goes on. A response to a request from the UE goes to the UE; a
// response to a request to the UE comes from it. Either way it crosses
// the edge of the trust domain (crossBoundary). A response from the UE
// goes on with the charging vector of req (chargeResponse).
func (p *PCSCF) ueResponse(l leg, req, resp *sip.Message) {
	if l == originating {
		crossBoundary(resp, responseToUE)
		return
	}
	crossBoundary(resp, responseFromUE)
	charging, _ := sip.ChargingVectorOf(req)
	p.chargeResponse(resp, charging)
	// 5.2.6.4.4 asserts the identity in the 1xx and 2xx to an initial
	// request for a dialog, 5.2.6.4.8 in any response to a request for a
	// standalone transaction.
	if resp.StatusCode < 300 || !ims.CreatesDialog(req.Method) {
		assertResponder(req, resp)
	}
}

// assertResponder is step 1 of TS 24.229 5.2.6.4.4 and step 2 of
// 5.2.6.4.8: the identity of the UE that answers is the one the network
// asserts, the public user identity of the P-Called-Party-ID in req
// (5.2.6.4.3 step 11), not one that the UE put in; crossBoundary has
// removed that one. This is the identity that TIP shows the caller (TS
// 24.608 4.5.2.4).
//
// The clause adds the display name stored at registration. The S-CSCF
// puts none in P-Associated-URI, so the P-CSCF has none to add.
func assertResponder(req, resp *sip.Message) {
	called, err := sip.ParseNameAddr(req.Get("P-Called-Party-ID"))
	if err != nil || called.URI == "" {
		return
	}
	resp.Set("P-Asserted-Identity", "<"+called.URI+">")
}

// assertedIdentities are the identities of the caller that the P-CSCF
// asserts for req, an initial request or a request for a standalone
// transaction from the UE of a (TS 24.229 5.2.6.3.1). The registered
// identities are those of P-Associated-URI, so a barred one, such as
// the temporary public identity of a UE without an ISIM, is never
// among them. A P-Preferred-Identity that names a registered identity
// picks it; a second one that does is the alternative identity of the
// caller. Without one that does, the caller is the default public
// identity. The From of the request plays no part (NOTE 3).
//
// No UE of this project is a privileged sender, and none registers a
// wildcarded identity: the S-CSCF lists only distinct identities in
// P-Associated-URI.
func assertedIdentities(req *sip.Message, a Association) []string {
	var ids []string
	for _, v := range req.GetAll("P-Preferred-Identity") {
		preferred, err := sip.ParseNameAddr(v)
		if err != nil {
			continue
		}
		if id, ok := registeredIdentity(preferred.URI, a.PublicIdentities); ok && len(ids) < 2 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []string{a.PublicIdentities[0]}
	}
	return ids
}

// registeredIdentity is the identity of registered that uri names. A SIP
// URI with a "+" number and user=phone is compared as the tel URI of
// that number, since a UE may write a registered tel URI in that form
// (5.2.6.3.1 NOTE 1).
func registeredIdentity(uri string, registered []string) (string, bool) {
	candidates := []string{uri}
	if u, err := sip.ParseURI(uri); err == nil && u.Scheme == "sip" && strings.HasPrefix(u.User, "+") {
		if phone, _ := u.Params.Get("user"); strings.EqualFold(phone, "phone") {
			candidates = append(candidates, "tel:"+u.User)
		}
	}
	for _, id := range registered {
		for _, c := range candidates {
			if ims.SamePublicIdentity(id, c) {
				return id, true
			}
		}
	}
	return "", false
}
