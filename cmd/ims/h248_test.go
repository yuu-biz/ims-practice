package main

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// TestMediaNodeTakesItsCommandsOverH248: a media relay configured with
// an h248 section registers with the controller of a P-CSCF over UDP,
// and the controller then carries out the call related procedures of Iq
// on the relay itself: the context it adds holds the ports the relay
// opened, a Modify reaches the relay, and a Subtract releases them.
// This is the whole way from the configuration of the two nodes to the
// relay, which the tests of internal/h248/mediactl do not cover: they
// run a controller and a gateway against a relay of their own.
//
// The ports come from rtptest.PortRange rather than from the system, so
// that the gateway can be configured with the controller's port before
// the controller listens on it.
func TestMediaNodeTakesItsCommandsOverH248(t *testing.T) {
	minPort, maxPort := rtptest.PortRange(t, 8)
	loopback := netip.MustParseAddr("127.0.0.1")
	controllerAddr := netip.AddrPortFrom(loopback, maxPort-1)
	gatewayAddr := netip.AddrPortFrom(loopback, maxPort)

	controller, stopController, err := startMediaController(
		config.MediaController{Listen: controllerAddr}, "pcscf."+domain, quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stopController()

	// The relay keeps four ports, enough for the two terminations of one
	// context, and the two above them carry the H.248 messages.
	_, stopRelay, err := startMediaNode(config.AGW{
		MediaGateway: &config.MediaGateway{
			Listen:     gatewayAddr,
			Host:       "agw." + domain,
			Controller: controllerAddr.String(),
		},
		RTPAddress: loopback,
		MinPort:    minPort,
		MaxPort:    minPort + 3,
	}, nil, agw.NewMemoryContextStore(), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stopRelay()

	// A gateway waits a random time before it registers (the maximum
	// waiting delay of H.248.1 9.2), so the first Add may come before
	// any gateway has registered. Asking again is what the P-CSCF does
	// for the next call.
	ctx := context.Background()
	var c agw.Context
	deadline := time.Now().Add(30 * time.Second)
	for {
		c, err = controller.Add(ctx, agw.TerminationRequest{ID: agw.Access}, agw.TerminationRequest{ID: agw.Core})
		if !errors.Is(err, mediactl.ErrUnavailable) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no media gateway registered with the controller within 30 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	// The ID of the context names the gateway that holds it, so that
	// every later command for it goes to that gateway.
	if !strings.HasSuffix(c.ID, "@agw."+domain) {
		t.Errorf("the context is %q, want it to name the gateway", c.ID)
	}
	for _, side := range []agw.TerminationID{agw.Access, agw.Core} {
		termination := c.Terminations[side]
		if termination == nil {
			t.Fatalf("the context %q has no %s termination", c.ID, side)
		}
		local := termination.Local
		if local.Addr() != loopback || local.Port() < minPort || local.Port() > minPort+3 {
			t.Errorf("the %s termination receives on %v, want a port of the relay between %d and %d",
				side, local, minPort, minPort+3)
		}
	}

	ue := netip.MustParseAddrPort("192.0.2.30:49170")
	if err := controller.Modify(ctx, c.ID, agw.Access, agw.TerminationRequest{Remote: ue}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Subtract(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	// The relay gave its ports back: another context fits in the same
	// four ports.
	again, err := controller.Add(ctx, agw.TerminationRequest{ID: agw.Access}, agw.TerminationRequest{ID: agw.Core})
	if err != nil {
		t.Fatalf("a context after the Subtract: %v", err)
	}
	if err := controller.Subtract(ctx, again.ID); err != nil {
		t.Error(err)
	}
}
