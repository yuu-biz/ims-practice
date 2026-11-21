package mmtel

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds communication barring (TS 24.611): OCB for the calls
// a user makes, ICB and ACR for the calls to a user. When a rule bars
// the call, the AS answers it as a terminating UA (TS 24.229 5.7.2),
// after an announcement when the configuration names one (reject).
// By default a user has no barring.

// callInfo is what the rules of the served user are evaluated against
// for the INVITE as it goes out to requestURI.
//
// SIMPLIFIED: TS 24.611 4.9.1.4 NOTE 3. Whether the served user roams
// comes from the P-Visited-Network-ID and P-Access-Network-Info of its
// registration, which the AS does not get: its filter criteria send it
// no third-party REGISTER. The served user is taken to be at home, so
// the roaming condition (BIC-Roam) never holds.
func (c *call) callInfo(fields *sip.Message, requestURI string) simservs.CallInfo {
	privacy := sip.ParsePrivacy(fields.Get("Privacy"))
	return simservs.CallInfo{
		CallingIdentity:           callingIdentity(fields),
		CallingIdentityRestricted: privacy.Has(sip.PrivacyID) || privacy.Has(sip.PrivacyHeader) || privacy.Has(sip.PrivacyUser),
		CalledIdentity:            requestURI,
		Media:                     mediaOf(c.in.Request.Body),
		CommunicationDiverted:     diversionsIn(fields) > 0,
		ServedUserNotRegistered:   c.served.RegState == sip.RegStateUnregistered,
		HomeCountryCode:           c.as.cfg.HomeCountryCode,
	}
}

// callingIdentity is the caller's identity in P-Asserted-Identity (TS
// 24.611 4.5.2.6.1): the tel URI when there are two, since rules about
// numbers are written with tel URIs; "" when there is none.
func callingIdentity(fields *sip.Message) string {
	var first string
	for _, v := range fields.GetAll("P-Asserted-Identity") {
		na, err := sip.ParseNameAddr(v)
		if err != nil {
			continue
		}
		if strings.HasPrefix(strings.ToLower(na.URI), "tel:") {
			return na.URI
		}
		if first == "" {
			first = na.URI
		}
	}
	return first
}

// mediaOf is the media of the m= lines of an SDP offer, such as audio
// (RFC 4566 5.14), for the media condition (TS 24.611 4.9.1.4).
func mediaOf(sdp []byte) []string {
	var media []string
	for _, line := range strings.Split(string(sdp), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "m="); ok {
			if m, _, _ := strings.Cut(rest, " "); m != "" {
				media = append(media, m)
			}
		}
	}
	return media
}

// barOutgoing is OCB (TS 24.611 4.5.2.4.1): when the outgoing barring
// rules of the served user bar the call, the AS answers it with 603
// (Decline). It reports whether it did. A call to an emergency service
// is never barred; the clause has the operator write an allow rule for
// the emergency numbers, and the AS does not evaluate the rules for an
// emergency service URN (RFC 5031) at all.
func (c *call) barOutgoing(info simservs.CallInfo) bool {
	cb := c.services.CompleteCommunicationBarring
	if cb == nil || !cb.OperatorOutgoingCommunicationBarring.Authorized ||
		strings.HasPrefix(strings.ToLower(info.CalledIdentity), "urn:service:sos") {
		return false
	}
	r := simservs.EvaluateOutgoingBarring(&cb.OutgoingCommunicationBarring, info)
	if r.Allow {
		return false
	}
	c.log.Info("OCB: the call is barred", "rules", r.MatchedRules, "request_uri", info.CalledIdentity)
	c.reject(603)
	return true
}

// barIncoming is ICB and ACR (TS 24.611 4.5.2.6.1, 4.5.2.6.2): when the
// incoming barring rules of the served user bar the call, the AS
// answers it with 603 (Decline), or with 433 (Anonymity Disallowed)
// when a rule for anonymous callers barred it (RFC 5079). It reports
// whether it did.
func (c *call) barIncoming(info simservs.CallInfo) bool {
	cb := c.services.CompleteCommunicationBarring
	if cb == nil || !cb.OperatorIncomingCommunicationBarring.Authorized {
		return false
	}
	r := simservs.EvaluateIncomingBarring(&cb.IncomingCommunicationBarring, info)
	if r.Allow {
		return false
	}
	if r.AnonymousCommunicationRejection {
		c.log.Info("ACR: the anonymous call is rejected", "rules", r.MatchedRules)
		c.reject(433)
		return true
	}
	c.log.Info("ICB: the call is barred", "rules", r.MatchedRules, "caller", info.CallingIdentity)
	c.reject(603)
	return true
}
