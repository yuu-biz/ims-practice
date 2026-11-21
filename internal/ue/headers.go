package ue

import (
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// icsiFeature is ims.ICSIMMTel as a g.3gpp.icsi-ref feature parameter (TS
// 24.229 7.9.2), with the colons of the URN escaped (7.2A.8.2).
const icsiFeature = `+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`

// DialogContactParams are the feature parameters of the Contact of the
// UE's INVITEs and of its responses to them (UAConfig.ContactParams),
// and of its answer to OPTIONS: the ICSI of MMTel (TS 24.229
// 5.1.2A.1.1 item 3, IR.92 2.2.9) and the audio media feature tag (IR.92
// 2.2.4, RFC 3840).
var DialogContactParams = sip.ParseParams(icsiFeature + ";audio")

// SessionInterval is the session interval of a UE's INVITE: IR.92 2.2.8
// allows 1800 s or none, and the UE asks for 1800 s, which RFC 4028 4
// recommends.
const SessionInterval = 1800 * time.Second

// UserAgent names the UE in the User-Agent of its requests and the
// Server of its responses (IR.92 2.6): the profile and its major
// version, the terminal, the kind of device, and that no operator has
// customised it.
//
// SIMPLIFIED: IR.92 2.6. The terminal follows the examples of IR.92;
// the rules of GSMA RCC.07 C.4.1 that it refers to were not read.
const UserAgent = "PRD-IR92/24 term-ims-practice/ue-simulator device-type/smart-phone mno-custom/none"

// ReleaseCause is the protocol of the Reason that a UE puts in its BYE
// and CANCEL (TS 24.229 5.1.3.1, 5.1.5, 7.2A.18.11; IR.92 2.2.4).
const ReleaseCause = "RELEASE_CAUSE"

// The causes of RELEASE_CAUSE that the UE sends (TS 24.229 Table
// 7.2A.18.11-2).
var (
	reasonUserEndsCall     = sip.NewReason(ReleaseCause, 1, "User ends call")
	reasonMediaBearerLoss  = sip.NewReason(ReleaseCause, 3, "Media bearer loss")
	reasonNoACK            = sip.NewReason(ReleaseCause, 4, "SIP timeout - no ACK")
	reasonResponseTimedOut = sip.NewReason(ReleaseCause, 5, "SIP response time-out")
)

func (u *UE) requestHeaders() []sip.HeaderField  { return requestHeaders(u.cfg.AccessNetworkInfo) }
func (u *UE) responseHeaders() []sip.HeaderField { return responseHeaders(u.cfg.AccessNetworkInfo) }

// requestHeaders are the header fields of every request the UE sends
// in a call besides CANCEL: User-Agent (IR.92 2.6), and
// P-Access-Network-Info when the access network tells the UE its cell,
// accessNetworkInfo (TS 24.229 5.1.2A.1.1).
func requestHeaders(accessNetworkInfo string) []sip.HeaderField {
	h := []sip.HeaderField{{Name: "User-Agent", Value: UserAgent}}
	if accessNetworkInfo != "" {
		h = append(h, sip.HeaderField{Name: "P-Access-Network-Info", Value: accessNetworkInfo})
	}
	return h
}

// responseHeaders are those of every response of the UE in a call:
// Server (IR.92 2.6) and P-Access-Network-Info (TS 24.229 5.1.2A.2).
func responseHeaders(accessNetworkInfo string) []sip.HeaderField {
	h := []sip.HeaderField{{Name: "Server", Value: UserAgent}}
	if accessNetworkInfo != "" {
		h = append(h, sip.HeaderField{Name: "P-Access-Network-Info", Value: accessNetworkInfo})
	}
	return h
}

// OwnHeaders is the UAConfig.OwnHeaders of a UE whose access network
// gives it accessNetworkInfo, "" for none: the UA puts into what it
// sends by itself in a call, such as the ACK, a session refresh or the
// 200 to a BYE, the same header fields as the UE puts into the rest.
// A BYE that the UA sends by itself also says why (TS 24.229 5.1.5):
// RELEASE_CAUSE 4 when no ACK came for its 2xx, 5 when the peer did not
// answer a request within the dialog in time. Table 7.2A.18.11-2 has no
// cause for a session that expired (RFC 4028 10), so that BYE has no
// Reason.
func OwnHeaders(accessNetworkInfo string) func(method sip.Method, response bool, reason error) []sip.HeaderField {
	return func(method sip.Method, response bool, reason error) []sip.HeaderField {
		if response {
			return responseHeaders(accessNetworkInfo)
		}
		h := requestHeaders(accessNetworkInfo)
		switch {
		case errors.Is(reason, sip.ErrNoACK):
			h = append(h, reasonNoACK.Header())
		case errors.Is(reason, sip.ErrTimeout):
			h = append(h, reasonResponseTimedOut.Header())
		}
		return h
	}
}

// inviteHeaders are the header fields of the UE's initial INVITE
// besides requestHeaders: the body types it takes (TS 24.229 5.1.3.1),
// the service it asks for (5.1.2A.1.1, RFC 6050) and the ICSI it wants
// the callee's contact to have (RFC 3841).
func (u *UE) inviteHeaders() []sip.HeaderField {
	return append(u.requestHeaders(),
		sip.HeaderField{Name: "Accept", Value: "application/sdp, application/3gpp-ims+xml"},
		sip.HeaderField{Name: "P-Preferred-Service", Value: ims.ICSIMMTel},
		sip.HeaderField{Name: "Accept-Contact", Value: "*;" + icsiFeature},
	)
}

func with(headers []sip.HeaderField, more ...sip.HeaderField) []sip.HeaderField {
	return append(append([]sip.HeaderField(nil), headers...), more...)
}
