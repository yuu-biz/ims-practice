package ims

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

type locatorFunc func(host string, port int, transport sip.Transport) []sip.Destination

func (f locatorFunc) Locate(_ context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error) {
	return f(host, port, transport), nil
}

// Every target that DNS gives goes to the stack, in the order DNS gave
// them, so that the request can go on to the next one (RFC 3263 4.3).
func TestTargetsGivesEveryTargetInOrder(t *testing.T) {
	want := []sip.Destination{
		{Transport: sip.TCP, Addr: netip.MustParseAddrPort("192.0.2.31:5060")},
		{Transport: sip.TCP, Addr: netip.MustParseAddrPort("192.0.2.32:5060")},
	}
	var asked sip.Transport
	loc := locatorFunc(func(host string, _ int, transport sip.Transport) []sip.Destination {
		asked = transport
		if host != "scscf.ims.mnc001.mcc001.3gppnetwork.org" {
			return nil
		}
		return want
	})
	u, err := sip.ParseURI("sip:scscf.ims.mnc001.mcc001.3gppnetwork.org;transport=tcp;lr")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Targets(context.Background(), loc, u)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Targets = %v, want %v", got, want)
	}
	if asked != sip.TCP {
		t.Errorf("asked DNS for transport %q, want TCP from the transport parameter", asked)
	}

	u.Host = "unknown.ims.mnc001.mcc001.3gppnetwork.org"
	if got, err := Targets(context.Background(), loc, u); err == nil {
		t.Errorf("Targets for a name with no address = %v, want an error", got)
	}
}
