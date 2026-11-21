package scscf

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The S-CSCF tells the IMS communication service of an initial request
// to the nodes after it in P-Asserted-Service (RFC 6050): the terminating
// P-CSCF, for one, names it to the PCRF.
//
// SIMPLIFIED: TS 24.229 5.4.3.2 steps 4C and 4D, 5.4.3.3 steps 3C and 3D.
// The user profile from the HSS lists no subscribed services, so every
// user subscribes to MMTel, which is the only service of this project
// with an ICSI. The S-CSCF takes neither of the operator options to
// answer 403 to a request that matches no subscribed service.

// assertService is steps 4C and 4D of TS 24.229 5.4.3.2 for a request
// of the served user on its first visit. A P-Preferred-Service is taken
// only when the request matches the service it names (4C); without one,
// the S-CSCF picks the service that the request matches (4D). Either
// way the P-Preferred-Service goes.
func assertService(out *sip.Message) {
	preferred := out.Get("P-Preferred-Service")
	out.Remove("P-Preferred-Service")
	service, ok := matchService(out)
	if ok && (preferred == "" || preferred == service) {
		out.Set("P-Asserted-Service", service)
	}
}

// confirmService is steps 3C and 3D of TS 24.229 5.4.3.3 for a request
// to the served user on its first visit: a P-Asserted-Service of a node
// in the trust domain stays when the user subscribes to the service, and
// a request without one gets the service it matches.
func confirmService(out *sip.Message) {
	if asserted := out.Get("P-Asserted-Service"); asserted != "" {
		if asserted != ims.ICSIMMTel {
			out.Remove("P-Asserted-Service")
		}
		return
	}
	if service, ok := matchService(out); ok {
		out.Set("P-Asserted-Service", service)
	}
}

// matchService is the operator-configured data that TS 24.229 5.4.3.2
// and 5.4.3.3 match a request against: an INVITE is MMTel when it has
// no session description, whose offer then comes in a response, or one
// with audio or video in it. Any other request has no ICSI: a MESSAGE
// that carries an SMS, for one (TS 24.341).
func matchService(m *sip.Message) (string, bool) {
	if m.Method != sip.INVITE {
		return "", false
	}
	if len(m.Body) == 0 {
		return ims.ICSIMMTel, true
	}
	if !strings.EqualFold(m.Get("Content-Type"), "application/sdp") {
		return "", false
	}
	session, err := sdp.Parse(m.Body)
	if err != nil {
		return "", false
	}
	for _, media := range session.Media {
		if media.Type == "audio" || media.Type == "video" {
			return ims.ICSIMMTel, true
		}
	}
	return "", false
}
