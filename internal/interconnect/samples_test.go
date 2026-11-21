package interconnect

import (
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Sample messages of a call from network A, the home network, to
// network B, the peer. Network A uses the test PLMN 001/01 and network
// B 001/02 (TS 23.003); the numbers are fictional NANP numbers, and the
// addresses come from the documentation ranges: 192.0.2.0/24 inside
// network A, 198.51.100.0/24 between the two IBCFs, and 203.0.113.0/24
// inside network B.

const (
	homeDomain = "ims.mnc001.mcc001.3gppnetwork.org"
	peerDomain = "ims.mnc002.mcc001.3gppnetwork.org"
)

// exitInvite is the INVITE that the S-CSCF of network A sends to its
// IBCF after ENUM turned the callee's number into a SIP URI of network
// B (TS 24.229 5.4.3.2 steps 10 to 12).
const exitInvite = "INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP scscf.ims.mnc001.mcc001.3gppnetwork.org:6060;branch=z9hG4bKs1c2\r\n" +
	"Via: SIP/2.0/UDP pcscf.ims.mnc001.mcc001.3gppnetwork.org;branch=z9hG4bKp1c2\r\n" +
	"Via: SIP/2.0/UDP 192.0.2.10:5060;received=192.0.2.10;rport=5060;branch=z9hG4bKu1c2\r\n" +
	"Max-Forwards: 67\r\n" +
	"Route: <sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org;lr>\r\n" +
	"Record-Route: <sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:6060;lr>\r\n" +
	"Record-Route: <sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org;lr>\r\n" +
	"From: <sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>;tag=a73kszlfl\r\n" +
	"To: <tel:+12025550151>\r\n" +
	"Call-ID: 4f6e1a2b3c4d5e6f\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"Contact: <sip:001010000000001@192.0.2.10:5060>;+g.3gpp.icsi-ref=\"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel\"\r\n" +
	"P-Asserted-Identity: <sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>\r\n" +
	"P-Asserted-Identity: <tel:+12025550123>\r\n" +
	"P-Access-Network-Info: 3GPP-E-UTRAN-FDD; utran-cell-id-3gpp=0010100010000001\r\n" +
	"P-Charging-Vector: icid-value=1234bc9876e;icid-generated-at=192.0.2.20;orig-ioi=ims.mnc001.mcc001.3gppnetwork.org\r\n" +
	"P-Charging-Function-Addresses: ccf=192.0.2.90\r\n" +
	"P-Early-Media: supported\r\n" +
	"Supported: 100rel, precondition, timer\r\n" +
	"Require: precondition\r\n" +
	"Session-Expires: 1800\r\n" +
	"Allow: INVITE, ACK, CANCEL, BYE, PRACK, UPDATE, OPTIONS, MESSAGE\r\n" +
	"User-Agent: ims-practice-ue\r\n" +
	"Content-Type: application/sdp\r\n" +
	"\r\n" +
	voLTEOffer

// voLTEOffer is the offer of the VoLTE UE in network A, as the P-CSCF
// rewrote it to the address of the media relay.
const voLTEOffer = "v=0\r\n" +
	"o=- 1352 1 IN IP4 192.0.2.30\r\n" +
	"s=-\r\n" +
	"c=IN IP4 192.0.2.30\r\n" +
	"t=0 0\r\n" +
	"m=audio 40000 RTP/AVP 96 97 98 99 0 100 101\r\n" +
	"b=AS:80\r\n" +
	"a=rtpmap:96 AMR-WB/16000/1\r\n" +
	"a=fmtp:96 mode-change-capability=2;max-red=0\r\n" +
	"a=rtpmap:97 AMR-WB/16000/1\r\n" +
	"a=fmtp:97 octet-align=1;mode-change-capability=2;max-red=0\r\n" +
	"a=rtpmap:98 AMR/8000/1\r\n" +
	"a=fmtp:98 mode-change-capability=2;max-red=0\r\n" +
	"a=rtpmap:99 AMR/8000/1\r\n" +
	"a=fmtp:99 octet-align=1;mode-change-capability=2;max-red=0\r\n" +
	"a=rtpmap:0 PCMU/8000\r\n" +
	"a=rtpmap:100 telephone-event/16000\r\n" +
	"a=fmtp:100 0-15\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=fmtp:101 0-15\r\n" +
	"a=ptime:20\r\n" +
	"a=maxptime:240\r\n" +
	"a=sendrecv\r\n"

// entryOK is the 200 OK that the IBCF of network B returns to the
// INVITE once it left network A. Its first Via is the one the IBCF of
// network A added, which the sip stack takes off before the IBCF sees
// the response; the samples leave it out for that reason. Record-Route
// and Via carry what network A's IBCF put there; tests fill them in.
const entryOK = "SIP/2.0 200 OK\r\n" +
	"Record-Route: <sip:ibcf.nni.mnc002.example.net;lr>\r\n" +
	"From: <sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>;tag=a73kszlfl\r\n" +
	"To: <tel:+12025550151>;tag=b91q0zt\r\n" +
	"Call-ID: 4f6e1a2b3c4d5e6f\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"Contact: <sip:ibcf.nni.mnc002.example.net;transport=udp>\r\n" +
	"P-Asserted-Identity: <sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone>\r\n" +
	"P-Charging-Vector: icid-value=1234bc9876e;orig-ioi=ims.mnc001.mcc001.3gppnetwork.org;term-ioi=ims.mnc002.mcc001.3gppnetwork.org\r\n" +
	"P-Charging-Function-Addresses: ccf=203.0.113.90\r\n" +
	"Require: precondition\r\n" +
	"Session-Expires: 1800;refresher=uac\r\n" +
	"Server: peer-b-ue\r\n" +
	"Content-Type: application/sdp\r\n" +
	"\r\n" +
	"v=0\r\n" +
	"o=- 7788 1 IN IP4 198.51.100.40\r\n" +
	"s=-\r\n" +
	"c=IN IP4 198.51.100.40\r\n" +
	"t=0 0\r\n" +
	"m=audio 50000 RTP/AVP 0 101\r\n" +
	"a=rtpmap:0 PCMU/8000\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=fmtp:101 0-15\r\n" +
	"a=ptime:20\r\n" +
	"a=sendrecv\r\n"

// entryBye is the BYE that the callee in network B sends within the
// dialog. Its Route and Request-URI are what network A's IBCF put in
// the INVITE; tests fill them in.
const entryBye = "BYE sip:placeholder@ibcf.nni.mnc001.example.net SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP ibcf.nni.mnc002.example.net;branch=z9hG4bKb2e1\r\n" +
	"Max-Forwards: 69\r\n" +
	"From: <tel:+12025550151>;tag=b91q0zt\r\n" +
	"To: <sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>;tag=a73kszlfl\r\n" +
	"Call-ID: 4f6e1a2b3c4d5e6f\r\n" +
	"CSeq: 2 BYE\r\n" +
	"Reason: SIP;cause=200;text=\"Call completed\"\r\n" +
	"\r\n"

// terminatingOK is the 200 OK that network A's callee sends to the
// INVITE of network A's IBCF, for a call that came from network B.
const terminatingOK = "SIP/2.0 200 OK\r\n" +
	"Via: SIP/2.0/UDP ibcf.ims.mnc001.mcc001.3gppnetwork.org;branch=z9hG4bKt1;received=192.0.2.40\r\n" +
	"Record-Route: <sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org;lr>\r\n" +
	"Record-Route: <sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:6060;lr>\r\n" +
	"From: <sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone>;tag=c11\r\n" +
	"To: <sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>;tag=d22\r\n" +
	"Call-ID: ibcf-a-leg-1\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"Contact: <sip:001010000000001@192.0.2.10:5060>\r\n" +
	"\r\n"

func parse(t *testing.T, s string) *sip.Message {
	t.Helper()
	m, err := sip.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// peerProfile is the agreement with network B in most tests.
func peerProfile() Profile {
	return Profile{
		Name:       "peer-b",
		Transports: []sip.Transport{sip.UDP, sip.TCP},
		Methods:    []sip.Method{sip.MESSAGE},
	}
}

// headerNames lists the header field names of m in order, each once.
func headerNames(m *sip.Message) string {
	var names []string
	for _, h := range m.Headers {
		if len(names) == 0 || names[len(names)-1] != h.Name {
			names = append(names, h.Name)
		}
	}
	return strings.Join(names, ",")
}
