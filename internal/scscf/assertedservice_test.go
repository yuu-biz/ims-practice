package scscf

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const otherICSI = "urn:urn-7:3gpp-service.ims.icsi.other"

// withSDP gives m a session description with one stream of media.
func withSDP(m *sip.Message, media string) *sip.Message {
	m.Add("Content-Type", "application/sdp")
	m.Body = []byte("v=0\r\no=- 1 1 IN IP4 192.0.2.10\r\ns=-\r\nc=IN IP4 192.0.2.10\r\nt=0 0\r\nm=" + media + " 4000 RTP/AVP 0\r\n")
	return m
}

// TestOriginatingRequestAssertsItsService is TS 24.229 5.4.3.2 steps 4C
// and 4D: the S-CSCF asserts the service that the request matches, the
// one the UE preferred if it is that one, and the P-Preferred-Service
// goes.
func TestOriginatingRequestAssertsItsService(t *testing.T) {
	for _, tt := range []struct {
		name               string
		method             sip.Method
		media              string // "" for no session description
		preferred, claimed string
		want               string
	}{
		{"preferred MMTel with audio", sip.INVITE, "audio", ims.ICSIMMTel, "", ims.ICSIMMTel},
		{"preferred MMTel without an offer", sip.INVITE, "", ims.ICSIMMTel, "", ims.ICSIMMTel},
		{"preferred service that does not match", sip.INVITE, "audio", otherICSI, "", ""},
		{"no preference", sip.INVITE, "audio", "", "", ims.ICSIMMTel},
		{"no media of MMTel", sip.INVITE, "application", ims.ICSIMMTel, "", ""},
		{"MESSAGE", sip.MESSAGE, "", ims.ICSIMMTel, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, icscf, _ := registeredHarness(t)
			m := newRequest(tt.method, "sip:001010000000002@"+domain, orig, impu)
			if tt.method == sip.INVITE {
				m.Add("Contact", "<sip:001010000000001@192.0.2.10:5060>")
			}
			if tt.media != "" {
				withSDP(m, tt.media)
			}
			if tt.preferred != "" {
				m.Add("P-Preferred-Service", tt.preferred)
			}
			if tt.claimed != "" {
				m.Add("P-Asserted-Service", tt.claimed)
			}
			h.send(m)
			got := received(t, icscf)
			if got.Get("P-Asserted-Service") != tt.want || got.Get("P-Preferred-Service") != "" {
				t.Errorf("P-Asserted-Service %q, P-Preferred-Service %q; want %q and none",
					got.Get("P-Asserted-Service"), got.Get("P-Preferred-Service"), tt.want)
			}
		})
	}
}

// TestTerminatingRequestKeepsASubscribedService is TS 24.229 5.4.3.3
// steps 3C and 3D: a P-Asserted-Service of a service the callee
// subscribes to stays, another goes, and a request without one gets the
// service it matches.
func TestTerminatingRequestKeepsASubscribedService(t *testing.T) {
	for _, tt := range []struct {
		name     string
		method   sip.Method
		asserted string
		want     string
	}{
		{"MMTel", sip.INVITE, ims.ICSIMMTel, ims.ICSIMMTel},
		{"not subscribed", sip.INVITE, otherICSI, ""},
		{"none asserted", sip.INVITE, "", ims.ICSIMMTel},
		{"MESSAGE", sip.MESSAGE, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, _, pcscf := registeredHarness(t)
			m := newRequest(tt.method, impu, "<sip:scscf."+domain+":5060;lr>", "<sip:caller@visited.example>")
			if tt.method == sip.INVITE {
				m.Add("Contact", "<sip:caller@192.0.2.20:5060>")
				withSDP(m, "audio")
			}
			if tt.asserted != "" {
				m.Add("P-Asserted-Service", tt.asserted)
			}
			h.send(m)
			if got := received(t, pcscf).Get("P-Asserted-Service"); got != tt.want {
				t.Errorf("P-Asserted-Service %q, want %q", got, tt.want)
			}
		})
	}
}
