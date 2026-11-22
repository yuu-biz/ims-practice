package sdp

import (
	"net/netip"
	"testing"
)

// The tests below pin, byte for byte, the offer that AudioOffer writes
// with the VoLTE codecs and the answers of AudioAnswer to five offers.
// The last offer has EVS in front of AMR-WB (TS 26.114 A.14.1.3): the
// UE, which has no EVS, answers it with AMR-WB.

const evsAndAMROffer = "v=0\r\no=- 1 1 IN IP4 192.0.2.40\r\ns=-\r\nc=IN IP4 192.0.2.40\r\nt=0 0\r\n" +
	"m=audio 49152 RTP/AVP 97 98 99 100 101 102\r\nb=AS:42\r\nb=RS:0\r\nb=RR:2000\r\n" +
	"a=rtpmap:97 EVS/16000/1\r\na=fmtp:97 br=5.9-24.4; bw=nb-swb; max-red=220\r\n" +
	"a=rtpmap:98 AMR-WB/16000/1\r\na=fmtp:98 mode-change-capability=2; max-red=220\r\n" +
	"a=rtpmap:99 AMR-WB/16000/1\r\na=fmtp:99 mode-change-capability=2; max-red=220; octet-align=1\r\n" +
	"a=rtpmap:100 AMR/8000/1\r\na=fmtp:100 mode-change-capability=2; max-red=220\r\n" +
	"a=rtpmap:101 AMR/8000/1\r\na=fmtp:101 mode-change-capability=2; max-red=220; octet-align=1\r\n" +
	"a=rtpmap:102 telephone-event/16000\r\na=fmtp:102 0-15\r\na=ptime:20\r\na=maxptime:240\r\n"

const keepModeSetOffer = "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
	"m=audio 5004 RTP/AVP 110 111 112\r\n" +
	"a=rtpmap:110 AMR/8000\r\na=fmtp:110 mode-set=0,2,4,7; octet-align=1; foo=bar\r\n" +
	"a=rtpmap:111 telephone-event/16000\r\n" +
	"a=rtpmap:112 telephone-event/8000\r\n"

const guardVoLTEOffer = "v=0\r\n" +
	"o=- 7 1 IN IP4 172.28.0.100\r\n" +
	"s=-\r\n" +
	"c=IN IP4 172.28.0.100\r\n" +
	"t=0 0\r\n" +
	"m=audio 40002 RTP/AVP 96 97 98 99 0 100 101\r\n" +
	"b=AS:80\r\n" +
	"b=RS:0\r\n" +
	"b=RR:4000\r\n" +
	"a=rtpmap:96 AMR-WB/16000/1\r\n" +
	"a=fmtp:96 mode-change-capability=2; max-red=0\r\n" +
	"a=rtpmap:97 AMR-WB/16000/1\r\n" +
	"a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1\r\n" +
	"a=rtpmap:98 AMR/8000/1\r\n" +
	"a=fmtp:98 mode-change-capability=2; max-red=0\r\n" +
	"a=rtpmap:99 AMR/8000/1\r\n" +
	"a=fmtp:99 mode-change-capability=2; max-red=0; octet-align=1\r\n" +
	"a=rtpmap:0 PCMU/8000\r\n" +
	"a=rtpmap:100 telephone-event/16000\r\n" +
	"a=fmtp:100 0-15\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=fmtp:101 0-15\r\n" +
	"a=ptime:20\r\n" +
	"a=maxptime:240\r\n" +
	"a=sendrecv\r\n"

// guardAnswers are the answers of AudioAnswer with VoLTECodecs to
// guardOffers, in order.
var guardAnswers = []string{
	"v=0\r\n" +
		"o=- 9 1 IN IP4 172.28.0.101\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.101\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 96 100\r\n" +
		"b=AS:41\r\n" +
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:96 AMR-WB/16000/1\r\n" +
		"a=fmtp:96 mode-change-capability=2; max-red=0\r\n" +
		"a=rtpmap:100 telephone-event/16000\r\n" +
		"a=fmtp:100 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n",
	"v=0\r\n" +
		"o=- 9 1 IN IP4 172.28.0.101\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.101\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 97 101\r\n" +
		"b=AS:41\r\n" +
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:97 AMR-WB/16000/1\r\n" +
		"a=fmtp:97 mode-change-capability=2; max-red=0\r\n" +
		"a=rtpmap:101 telephone-event/16000\r\n" +
		"a=fmtp:101 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n",
	"v=0\r\n" +
		"o=- 9 1 IN IP4 172.28.0.101\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.101\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 0 101\r\n" +
		"b=AS:80\r\n" +
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:101 telephone-event/8000\r\n" +
		"a=fmtp:101 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n" +
		"m=video 0 RTP/AVP 99\r\n",
	"v=0\r\n" +
		"o=- 9 1 IN IP4 172.28.0.101\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.101\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 110 112\r\n" +
		"b=AS:30\r\n" +
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:110 AMR/8000\r\n" +
		"a=fmtp:110 mode-set=0,2,4,7; mode-change-capability=2; max-red=0; octet-align=1\r\n" +
		"a=rtpmap:112 telephone-event/8000\r\n" +
		"a=fmtp:112 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n",
	"v=0\r\n" +
		"o=- 9 1 IN IP4 172.28.0.101\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.101\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 98 102\r\n" +
		"b=AS:41\r\n" +
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:98 AMR-WB/16000/1\r\n" +
		"a=fmtp:98 mode-change-capability=2; max-red=0\r\n" +
		"a=rtpmap:102 telephone-event/16000\r\n" +
		"a=fmtp:102 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n",
}

var guardOffers = []string{guardVoLTEOffer, mtsiOffer, softphoneOffer, keepModeSetOffer, evsAndAMROffer}

func TestVoLTEOfferUnchanged(t *testing.T) {
	o, err := AudioOffer("7", netip.MustParseAddrPort("172.28.0.100:40002"), VoLTECodecs())
	if err != nil || string(o.Bytes()) != guardVoLTEOffer {
		t.Errorf("AudioOffer(VoLTECodecs()) changed, %v:\n%s", err, o.Bytes())
	}
}

func TestVoLTEAnswersUnchanged(t *testing.T) {
	for i, offer := range guardOffers {
		o, err := Parse([]byte(offer))
		if err != nil {
			t.Fatal(err)
		}
		a, err := AudioAnswer(o, "9", ueAddr, VoLTECodecs())
		if err != nil || string(a.Bytes()) != guardAnswers[i] {
			t.Errorf("AudioAnswer to offer %d changed, %v:\n%s", i, err, a.Bytes())
		}
	}
}
