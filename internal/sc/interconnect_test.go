package sc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// enum answers ENUM from a table of numbers, as the DNS of the home
// network does for the numbers of a peer network.
type enum map[string]string

func (e enum) ENUM(_ context.Context, number, suffix string) (string, error) {
	if suffix != "e164.arpa" {
		return "", errors.New("unexpected suffix " + suffix)
	}
	if uri, ok := e[number]; ok {
		return uri, nil
	}
	return "", errors.New("no NAPTR")
}

const peerUser = "sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone"

var peerNumbers = enum{
	"+12025550151": peerUser,
	// A number of the home network that no subscriber has: ENUM points
	// into the home domain, so the stub still refuses it.
	"+12025550188": "sip:001010000000088@" + domain,
}

// TestSubmitForAUserOfAnotherNetwork: a number that no user of the home
// network has, but ENUM finds in another IMS network, is accepted and
// delivered to the SIP URI that ENUM gives. A number that ENUM keeps in
// the home domain, or does not know, is refused as before.
func TestSubmitForAUserOfAnotherNetwork(t *testing.T) {
	c, g := newSC(everyone, sc.NewMemoryStore())
	c.SetPeerNetworks(peerNumbers, domain)
	submit := hello
	submit.DestinationAddress = sms.InternationalNumber("+12025550151")
	if _, err := c.Submit(context.Background(), alice, submit); err != nil {
		t.Fatal(err)
	}
	if d := g.next(t); d.Recipient != peerUser || d.TPDU.(sms.Deliver).OriginatingAddress != alice {
		t.Errorf("delivery = %+v, want an SMS-DELIVER from Alice to %s", d, peerUser)
	}
	for _, number := range []string{"+12025550188", "+12025550187"} {
		submit.DestinationAddress = sms.InternationalNumber(number)
		var refusal *ipsmgw.Refusal
		if _, err := c.Submit(context.Background(), alice, submit); !errors.As(err, &refusal) || refusal.Cause != sms.RPCauseUnassignedNumber {
			t.Errorf("%s: err = %v, want RP-Cause 1", number, err)
		}
	}
}

// TestSubmitWithoutPeerNetworks: without SetPeerNetworks the stub knows
// the users of the home network only.
func TestSubmitWithoutPeerNetworks(t *testing.T) {
	c, _ := newSC(everyone, sc.NewMemoryStore())
	submit := hello
	submit.DestinationAddress = sms.InternationalNumber("+12025550151")
	var refusal *ipsmgw.Refusal
	if _, err := c.Submit(context.Background(), alice, submit); !errors.As(err, &refusal) || refusal.Cause != sms.RPCauseUnassignedNumber {
		t.Errorf("err = %v, want RP-Cause 1", err)
	}
}
