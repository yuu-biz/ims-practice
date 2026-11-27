package ibcf

import (
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestOptionsLeftOutOfARequestToAnAddressOfThePeer: a request within a
// dialog with the peer goes to its Contact, which may be an address that
// no host of the agreement names. The tags that the agreement removes
// stay off it all the same, as the address is one of the peer's IBCFs.
func TestOptionsLeftOutOfARequestToAnAddressOfThePeer(t *testing.T) {
	peers, err := NewPeerTable(Peer{
		Profile: interconnect.Profile{Name: "peer-b", Domain: "ims.mnc002.mcc001.3gppnetwork.org", Transports: []sip.Transport{sip.UDP},
			OptionTags: map[string]interconnect.OptionTagPolicy{sip.OptionTimer: interconnect.OptionRemove}},
		Sources: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/28")},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &IBCF{peers: peers}
	bye := &sip.Message{Method: sip.BYE, RequestURI: "sip:198.51.100.5:5060"}
	to := sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("198.51.100.5:5060")}
	if got := b.optionsLeftOut(bye, to, true); len(got) != 1 || got[0] != sip.OptionTimer {
		t.Errorf("a BYE to an address of the peer leaves out %v, want [%s]", got, sip.OptionTimer)
	}
	elsewhere := sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("203.0.113.9:5060")}
	if got := b.optionsLeftOut(&sip.Message{Method: sip.BYE, RequestURI: "sip:203.0.113.9:5060"}, elsewhere, true); got != nil {
		t.Errorf("a BYE to no peer leaves out %v, want nothing", got)
	}
}
