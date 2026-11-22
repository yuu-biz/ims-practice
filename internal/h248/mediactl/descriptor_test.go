package mediactl

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

var pcmu = sdp.Codec{PayloadType: 0, Name: sdp.NamePCMU, ClockRate: 8000}

func TestControllerSDP(t *testing.T) {
	amr := sdp.Codec{PayloadType: 97, Name: sdp.NameAMR, ClockRate: 8000, Fmtp: "octet-align=1"}
	tests := []struct {
		name    string
		address netip.AddrPort
		media   agw.Media
		want    string
	}{
		{"CHOOSE without formats", netip.AddrPort{}, agw.Media{}, "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP -\n"},
		{"CHOOSE with formats", netip.AddrPort{}, agw.Media{Codecs: []sdp.Codec{amr, pcmu}, Bandwidth: 29},
			"v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 97 0\nb=AS:29\na=rtpmap:97 AMR/8000\na=fmtp:97 octet-align=1\na=rtpmap:0 PCMU/8000\n"},
		{"an address", netip.MustParseAddrPort("192.0.2.30:49170"), agw.Media{Codecs: []sdp.Codec{pcmu}},
			"v=0\nc=IN IP4 192.0.2.30\nm=audio 49170 RTP/AVP 0\na=rtpmap:0 PCMU/8000\n"},
		{"IPv6", netip.MustParseAddrPort("[2001:db8::30]:49170"), agw.Media{},
			"v=0\nc=IN IP6 2001:db8::30\nm=audio 49170 RTP/AVP -\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := controllerSDP(tt.address, tt.media)
			if got != tt.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tt.want)
			}
			d, err := parseSDP(got)
			if err != nil {
				t.Fatal(err)
			}
			if d.chooseAddress != !tt.address.IsValid() || d.address != tt.address ||
				d.media.Bandwidth != tt.media.Bandwidth || !slices.Equal(d.media.Codecs, tt.media.Codecs) {
				t.Errorf("read back %+v", d)
			}
		})
	}
}

func TestParseSDP(t *testing.T) {
	// A full session description, with CRLF, as a controller of another
	// make might send it (H.248.1 (05/2002) 7.1.8).
	d, err := parseSDP("v=0\r\no=- 1 1 IN IP4 192.0.2.1\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\nm=audio 49170 RTP/AVP 96\r\na=rtpmap:96 AMR-WB/16000/1\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.origin != "- 1 1 IN IP4 192.0.2.1" || d.name != "-" || d.timing != "0 0" ||
		d.address != netip.MustParseAddrPort("192.0.2.30:49170") ||
		!slices.Equal(d.media.Codecs, []sdp.Codec{{PayloadType: 96, Name: sdp.NameAMRWB, ClockRate: 16000, Channels: 1}}) {
		t.Errorf("%+v", d)
	}
	for _, bad := range []string{
		"v=0\nc=IN IP4 $\n",
		"v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\nv=0\nc=IN IP4 $\nm=audio $ RTP/AVP 8\n",
		"v=0\nm=audio $ RTP/AVP 0\nm=video $ RTP/AVP 99\n",
		"v=0\nc=IN IP4 192.0.2.300\nm=audio 49170 RTP/AVP 0\n",
		"v=0\nm=audio 49170 RTP/AVP PCMU\n",
	} {
		if _, err := parseSDP(bad); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
}

func TestGatewayLocalFillsInTheSessionLines(t *testing.T) {
	local, err := parseSDP("v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n")
	if err != nil {
		t.Fatal(err)
	}
	got := gatewayLocal(local, netip.MustParseAddrPort("198.51.100.10:40000"), 7)
	// The static payload type gains its a=rtpmap line.
	want := "v=0\no=- 7 0 IN IP4 198.51.100.10\ns=-\nc=IN IP4 198.51.100.10\nt=0 0\nm=audio 40000 RTP/AVP 0\na=rtpmap:0 PCMU/8000\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestTerminationIDs(t *testing.T) {
	if got := terminationID(1, agw.Core, 7); got != "ip/1/core/7" {
		t.Errorf("terminationID = %s", got)
	}
	tests := []struct {
		id   h248.TerminationID
		want ipTermination
		ok   bool
	}{
		{"ip/1/access/$", ipTermination{group: 1, groupText: "1", side: agw.Access, idText: "$"}, true},
		{"IP/2/Core/17", ipTermination{group: 2, groupText: "2", side: agw.Core, id: 17, idText: "17"}, true},
		{"ip/$/access/$", ipTermination{groupText: "$", side: agw.Access, idText: "$"}, true},
		{"ip/1/access/0", ipTermination{}, false},
		{"ip/65536/access/1", ipTermination{}, false},
		{"ROOT", ipTermination{}, false},
		{"rtp/1/access/1", ipTermination{}, false},
	}
	for _, tt := range tests {
		got, ok := parseTerminationID(tt.id)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseTerminationID(%s) = %+v, %v", tt.id, got, ok)
		}
	}
}

func TestTimeStamp(t *testing.T) {
	got := timeStamp(time.Date(2026, 11, 20, 9, 0, 12, 340_000_000, time.UTC))
	if got != (h248.TimeNotation{Date: "20261120", Time: "09001234"}) {
		t.Errorf("%+v", got)
	}
}
