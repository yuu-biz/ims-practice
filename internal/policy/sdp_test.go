package policy

import (
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// The expected values below come from TS 29.213 Tables 6.2.1 and 6.2.2,
// worked out by hand for these SDPs.

const (
	ue    = "172.28.0.110"
	relay = "172.28.0.50"
)

func sdpOf(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n") }

// The UE calls out: its offer is the uplink SDP; the answer, rewritten
// by the P-CSCF to point at the media relay, is the downlink SDP.
var (
	ueOffer = sdpOf("v=0", "o=- 1 1 IN IP4 "+ue, "s=-", "c=IN IP4 "+ue, "t=0 0",
		"m=audio 49000 RTP/AVP 0 101", "b=AS:80",
		"a=rtpmap:0 PCMU/8000", "a=rtpmap:101 telephone-event/8000", "a=sendrecv")
	relayAnswer = sdpOf("v=0", "o=- 2 1 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0",
		"m=audio 40000 RTP/AVP 0", "b=AS:64", "b=RS:600", "b=RR:2000", "a=rtpmap:0 PCMU/8000", "a=sendrecv")
)

func u32(v uint32) *uint32 { return &v }

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestMediaComponentsOfACallFromTheUE(t *testing.T) {
	got, err := MediaComponents(OfferAnswer{Offer: ueOffer, Answer: relayAnswer, UEOffered: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []MediaComponent{{
		Number:     1,
		Type:       Audio,
		FlowStatus: Enabled,
		// UL from the b=AS of the downlink SDP, DL from the uplink SDP.
		MaxRequestedBandwidthUL: u32(64000),
		MaxRequestedBandwidthDL: u32(80000),
		RRBandwidth:             u32(2000),
		RSBandwidth:             u32(600),
		Codecs: []CodecData{
			{Direction: Uplink, Lines: []string{"m=audio 49000 RTP/AVP 0 101", "a=rtpmap:0 PCMU/8000", "a=rtpmap:101 telephone-event/8000"}},
			{Direction: Downlink, Answer: true, Lines: []string{"m=audio 40000 RTP/AVP 0", "a=rtpmap:0 PCMU/8000"}},
		},
		SubComponents: []MediaSubComponent{
			{FlowNumber: 1, Flows: []FlowDescription{
				{Direction: Uplink, Protocol: 17, Source: addr(ue), Destination: addr(relay), DestinationPort: 40000},
				{Direction: Downlink, Protocol: 17, Source: addr(relay), Destination: addr(ue), DestinationPort: 49000},
			}},
			{FlowNumber: 2, Usage: RTCP, Flows: []FlowDescription{
				{Direction: Uplink, Protocol: 17, Source: addr(ue), Destination: addr(relay), DestinationPort: 40001},
				{Direction: Downlink, Protocol: 17, Source: addr(relay), Destination: addr(ue), DestinationPort: 49001},
			}},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("components\n got %+v\nwant %+v", got, want)
	}
}

func TestRTCPMuxSharesOneFlowAndAddsRTCPBandwidth(t *testing.T) {
	offer := sdpOf("v=0", "o=- 1 1 IN IP4 "+ue, "s=-", "c=IN IP4 "+ue, "t=0 0",
		"m=audio 49000 RTP/AVP 0", "b=AS:80", "a=rtcp-mux", "a=recvonly")
	withRTCP := sdpOf("v=0", "o=- 2 1 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0",
		"m=audio 40000 RTP/AVP 0", "b=AS:64", "b=RS:600", "b=RR:2000", "a=rtcp-mux", "a=sendonly")
	without := sdpOf("v=0", "o=- 2 1 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0",
		"m=audio 40000 RTP/AVP 0", "b=AS:64", "a=rtcp-mux", "a=sendonly")
	for _, tc := range []struct {
		answer []byte
		ul, dl uint32
	}{
		{withRTCP, 64000 + 2000 + 600, 80 * 1050},
		{without, 64 * 1050, 80 * 1050},
	} {
		got, err := MediaComponents(OfferAnswer{Offer: offer, Answer: tc.answer, UEOffered: true})
		if err != nil {
			t.Fatal(err)
		}
		c := got[0]
		// rtcp-mux: always ENABLED so that RTCP passes (Table 6.2.1 NOTE 12),
		// one sub-component with both flows (Table 6.2.2).
		if c.FlowStatus != Enabled || *c.MaxRequestedBandwidthUL != tc.ul || *c.MaxRequestedBandwidthDL != tc.dl {
			t.Errorf("status %v, UL %d, DL %d; want Enabled, %d, %d", c.FlowStatus, *c.MaxRequestedBandwidthUL, *c.MaxRequestedBandwidthDL, tc.ul, tc.dl)
		}
		if len(c.SubComponents) != 1 || c.SubComponents[0].FlowNumber != 1 || c.SubComponents[0].Usage != NoInformation || len(c.SubComponents[0].Flows) != 2 {
			t.Errorf("sub-components = %+v", c.SubComponents)
		}
	}
}

// The network offers a held call (sendonly) and the UE answers recvonly.
// The answer is the uplink SDP and decides.
func TestHeldCallToTheUE(t *testing.T) {
	offer := sdpOf("v=0", "o=- 2 2 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0",
		"m=audio 40000 RTP/AVP 0", "a=sendonly")
	answer := sdpOf("v=0", "o=- 1 2 IN IP4 "+ue, "s=-", "c=IN IP4 "+ue, "t=0 0",
		"m=audio 49000 RTP/AVP 0", "a=recvonly")
	got, err := MediaComponents(OfferAnswer{Offer: offer, Answer: answer})
	if err != nil {
		t.Fatal(err)
	}
	c := got[0]
	if c.FlowStatus != EnabledDownlink {
		t.Errorf("Flow-Status = %v, want EnabledDownlink", c.FlowStatus)
	}
	if c.MaxRequestedBandwidthUL != nil || c.MaxRequestedBandwidthDL != nil {
		t.Errorf("bandwidths without b=AS: UL %v, DL %v; want none", c.MaxRequestedBandwidthUL, c.MaxRequestedBandwidthDL)
	}
	rtp, rtcp := c.SubComponents[0], c.SubComponents[1]
	wantRTP := []FlowDescription{{Direction: Downlink, Protocol: 17, Source: addr(relay), Destination: addr(ue), DestinationPort: 49000}}
	if !reflect.DeepEqual(rtp.Flows, wantRTP) {
		t.Errorf("RTP flows = %+v, want only the downlink one %+v", rtp.Flows, wantRTP)
	}
	if rtcp.Usage != RTCP || len(rtcp.Flows) != 2 {
		t.Errorf("RTCP = %+v, want both directions", rtcp)
	}
	if c.Codecs[0].Direction != Downlink || c.Codecs[0].Answer || c.Codecs[1].Direction != Uplink || !c.Codecs[1].Answer {
		t.Errorf("codecs = %+v", c.Codecs)
	}
}

func TestInactiveInTheOfferDisablesTheFlows(t *testing.T) {
	offer := sdpOf("v=0", "o=- 1 1 IN IP4 "+ue, "s=-", "c=IN IP4 "+ue, "t=0 0", "m=audio 49000 RTP/AVP 0", "a=inactive")
	got, err := MediaComponents(OfferAnswer{Offer: offer, Answer: relayAnswer, UEOffered: true})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FlowStatus != Disabled {
		t.Errorf("Flow-Status = %v, want Disabled (Table 6.2.1 NOTE 5)", got[0].FlowStatus)
	}
}

func TestPortZeroRemovesTheComponent(t *testing.T) {
	rejected := sdpOf("v=0", "o=- 2 1 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0", "m=audio 0 RTP/AVP 0")
	got, err := MediaComponents(OfferAnswer{Offer: ueOffer, Answer: rejected, UEOffered: true})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FlowStatus != Removed || got[0].SubComponents != nil {
		t.Errorf("component = %+v, want Removed without flows", got[0])
	}
}

// An offer from the network before the UE answers: what the uplink SDP
// would give is a wildcard (Table 6.2.2 NOTE 6), and the flows are
// numbered by the uplink destination ports.
func TestOfferToTheUEWithoutAnswer(t *testing.T) {
	offer := sdpOf("v=0", "o=- 2 1 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0",
		"m=audio 40000 RTP/AVP 0", "b=AS:64", "a=rtcp:39999")
	got, err := MediaComponents(OfferAnswer{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	c := got[0]
	if *c.MaxRequestedBandwidthUL != 64000 || c.MaxRequestedBandwidthDL != nil || len(c.Codecs) != 1 {
		t.Errorf("UL %v, DL %v, codecs %d; want 64000, none, 1", c.MaxRequestedBandwidthUL, c.MaxRequestedBandwidthDL, len(c.Codecs))
	}
	// a=rtcp:39999 puts RTCP below RTP, so RTCP is flow 1.
	rtcp, rtp := c.SubComponents[0], c.SubComponents[1]
	if rtcp.Usage != RTCP || rtcp.FlowNumber != 1 || rtp.FlowNumber != 2 {
		t.Fatalf("sub-components = %+v", c.SubComponents)
	}
	want := []FlowDescription{
		{Direction: Uplink, Protocol: 17, Destination: addr(relay), DestinationPort: 40000},
		{Direction: Downlink, Protocol: 17, Source: addr(relay)},
	}
	if !reflect.DeepEqual(rtp.Flows, want) {
		t.Errorf("RTP flows\n got %+v\nwant %+v", rtp.Flows, want)
	}
}

func TestMediaComponentsRefusesWhatItCannotDerive(t *testing.T) {
	msrp := sdpOf("v=0", "o=- 1 1 IN IP4 "+ue, "s=-", "c=IN IP4 "+ue, "t=0 0", "m=message 7394 TCP/MSRP 0")
	if _, err := MediaComponents(OfferAnswer{Offer: msrp, UEOffered: true}); !errors.Is(err, ErrUnsupportedTransport) {
		t.Errorf("TCP media: %v, want ErrUnsupportedTransport", err)
	}
	twoLines := sdpOf("v=0", "o=- 2 1 IN IP4 "+relay, "s=-", "c=IN IP4 "+relay, "t=0 0",
		"m=audio 40000 RTP/AVP 0", "m=video 40002 RTP/AVP 96")
	if _, err := MediaComponents(OfferAnswer{Offer: ueOffer, Answer: twoLines, UEOffered: true}); err == nil {
		t.Error("an answer with more m= lines than the offer was accepted")
	}
}

func TestFlowDescriptionIsAnIPFilterRule(t *testing.T) {
	for _, tc := range []struct {
		flow FlowDescription
		rule string
	}{
		{FlowDescription{Direction: Uplink, Protocol: 17, Source: addr(ue), Destination: addr(relay), DestinationPort: 40000},
			"permit in 17 from 172.28.0.110 to 172.28.0.50 40000"},
		{FlowDescription{Direction: Downlink, Protocol: 17, Source: addr(relay)},
			"permit out 17 from 172.28.0.50 to any"},
	} {
		if got := tc.flow.String(); got != tc.rule {
			t.Errorf("String() = %q, want %q", got, tc.rule)
		}
		back, err := ParseFlowDescription(tc.rule)
		if err != nil || back != tc.flow {
			t.Errorf("ParseFlowDescription(%q) = %+v, %v; want %+v", tc.rule, back, err, tc.flow)
		}
	}
	// TS 29.214 5.3.8 restrictions this project relies on.
	for _, rule := range []string{
		"deny in 17 from any to any",
		"permit in 17 from 172.28.0.110 5000 to any",
		"permit in 17 from any to 172.28.0.50/32 40000",
		"permit in 17 from any to any 40000-40001",
		"permit in 17 from any to any 40000 frag",
	} {
		if _, err := ParseFlowDescription(rule); err == nil {
			t.Errorf("ParseFlowDescription(%q) succeeded", rule)
		}
	}
}

func TestCodecDataStartsWithDirectionAndKind(t *testing.T) {
	c := CodecData{Direction: Uplink, Lines: []string{"m=audio 49000 RTP/AVP 0", "a=rtpmap:0 PCMU/8000"}}
	if want := "uplink\noffer\nm=audio 49000 RTP/AVP 0\na=rtpmap:0 PCMU/8000"; c.String() != want {
		t.Errorf("String() = %q, want %q", c.String(), want)
	}
	back, err := ParseCodecData(c.String())
	if err != nil || !reflect.DeepEqual(back, c) {
		t.Errorf("ParseCodecData = %+v, %v; want %+v", back, err, c)
	}
	if _, err := ParseCodecData("sideways\noffer\nm=audio 0 RTP/AVP 0"); err == nil {
		t.Error("a Codec-Data without uplink or downlink was accepted")
	}
}
