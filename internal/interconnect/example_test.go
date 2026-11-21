package interconnect_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The INVITE that the S-CSCF of network A sends to its IBCF, for a call
// to a number of network B. The addresses are from the documentation
// ranges and the numbers are fictional.
const fromSCSCF = "INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0\r\n" +
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
	"Contact: <sip:001010000000001@192.0.2.10:5060>\r\n" +
	"P-Asserted-Identity: <sip:+12025550123@ims.mnc001.mcc001.3gppnetwork.org;user=phone>\r\n" +
	"P-Access-Network-Info: 3GPP-E-UTRAN-FDD; utran-cell-id-3gpp=0010100010000001\r\n" +
	"P-Charging-Vector: icid-value=1234bc9876e;orig-ioi=ims.mnc001.mcc001.3gppnetwork.org\r\n" +
	"P-Charging-Function-Addresses: ccf=192.0.2.90\r\n" +
	"Supported: 100rel, precondition\r\n" +
	"Require: precondition\r\n" +
	"Content-Type: application/sdp\r\n" +
	"\r\n" +
	"v=0\r\n" +
	"o=- 1352 1 IN IP4 192.0.2.30\r\n" +
	"s=-\r\n" +
	"c=IN IP4 192.0.2.30\r\n" +
	"t=0 0\r\n" +
	"m=audio 40000 RTP/AVP 96 0 101\r\n" +
	"a=rtpmap:96 AMR-WB/16000/1\r\n" +
	"a=rtpmap:0 PCMU/8000\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=fmtp:101 0-15\r\n" +
	"a=sendrecv\r\n"

// This example shows how the IBCF role of network A uses the package
// for an INVITE that leaves the network: what of it goes into the
// INVITE of the IBCF's dialog with network B.
func Example_outgoingCall() {
	// Configuration: the border with network B, which is not in the
	// trust domain and accepts PCMU and DTMF only.
	internal := sip.URI{Scheme: "sip", Host: "ibcf.ims.mnc001.mcc001.3gppnetwork.org"}
	border := interconnect.Border{
		Peer: interconnect.Profile{
			Name:         "peer-b",
			Domain:       "ims.mnc002.mcc001.3gppnetwork.org",
			Transports:   []sip.Transport{sip.UDP, sip.TCP},
			OptionTags:   map[string]interconnect.OptionTagPolicy{sip.OptionPrecondition: interconnect.OptionPass},
			NumberFormat: interconnect.NumberFormatSIP,
			Codecs:       []string{"PCMU", "telephone-event/8000"},
		},
		HomeDomain: "ims.mnc001.mcc001.3gppnetwork.org",
	}
	if err := border.Validate(); err != nil {
		panic(err)
	}

	// The INVITE comes from the S-CSCF. The IBCF takes its own URI off
	// Route (TS 24.229 5.10.2.2 step 1A) and lets the border prepare a
	// copy of it.
	invite, err := sip.Parse([]byte(fromSCSCF))
	if err != nil {
		panic(err)
	}
	sip.PopOwnRoute(invite, internal)
	if err := border.ExitRequest(invite); err != nil {
		var r *interconnect.Rejection
		if errors.As(err, &r) {
			fmt.Println(r.Response(invite, sip.NewTag()).Reason)
			return
		}
		panic(err)
	}
	// The IBCF's UA towards network B writes its own Via, From and To
	// tags, Call-ID, CSeq and Contact. It copies these header fields from
	// the prepared INVITE, and the body after the TrGW has rewritten its
	// c= and o= lines.
	fmt.Println("Request-URI:", invite.RequestURI)
	for _, h := range interconnect.CrossingFields(invite, "Allow", "Supported", "Session-Expires", "Min-SE") {
		fmt.Println(h.Name + ": " + h.Value)
	}
	fmt.Print(strings.ReplaceAll(string(invite.Body), "\r\n", "\n"))

	// Output:
	// Request-URI: sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone
	// Supported: 100rel, precondition
	// Require: precondition
	// v=0
	// o=- 1352 1 IN IP4 192.0.2.30
	// s=-
	// c=IN IP4 192.0.2.30
	// t=0 0
	// m=audio 40000 RTP/AVP 0 101
	// a=rtpmap:0 PCMU/8000
	// a=rtpmap:101 telephone-event/8000
	// a=fmtp:101 0-15
	// a=sendrecv
}
