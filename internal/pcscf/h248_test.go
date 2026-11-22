package pcscf

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// iqOverUDP is a media relay that the P-CSCF drives over H.248 instead
// of over the HTTP control API: a controller and a media gateway that
// send real datagrams to each other on loopback, with the relay itself
// behind the gateway. It returns the relay, so that a test can read the
// contexts the call left in it, and the controller, which the P-CSCF
// takes as its Relay.
//
// The two endpoints bind port 0, so the system picks the ports; a
// deployment uses the standard port 2944 on each node (h248.DefaultPort).
func iqOverUDP(t *testing.T) (*agw.Relay, *mediactl.Controller) {
	t.Helper()
	minPort, maxPort := rtptest.PortRange(t, 4)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range relay.Contexts() {
			relay.Subtract(c.ID)
		}
		relay.Close()
	})

	controllerConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	controller, err := mediactl.NewController(controllerConn, mediactl.ControllerConfig{
		Endpoint: h248.EndpointConfig{MId: h248.MId{DomainName: "pcscf." + domain}},
	})
	if err != nil {
		controllerConn.Close()
		t.Fatal(err)
	}
	go controller.Serve()
	t.Cleanup(func() { controller.Close() })

	gatewayConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := mediactl.NewGateway(gatewayConn, mediactl.GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: h248.MId{DomainName: "agw." + domain}},
		LocateController: func() (net.Addr, error) { return controllerConn.LocalAddr(), nil },
		Relay:            agw.NewDirect(relay),
		// A deployment leaves this at the five seconds of H.248.1 9.2,
		// which spreads the registrations of many gateways.
		MaxWaitingDelay: time.Millisecond,
	})
	if err != nil {
		gatewayConn.Close()
		t.Fatal(err)
	}
	go gateway.Serve()
	t.Cleanup(func() { gateway.Close() })

	// Until the gateway has registered, the controller has no gateway to
	// add a context on, and the P-CSCF would answer the first call 500.
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := controller.Add(context.Background(), agw.TerminationRequest{ID: agw.Access}, agw.TerminationRequest{ID: agw.Core})
		if err == nil {
			if err := controller.Subtract(context.Background(), c.ID); err != nil {
				t.Fatal(err)
			}
			return relay, controller
		}
		if !errors.Is(err, mediactl.ErrUnavailable) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the media gateway did not register with the controller within 10 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestOriginatingCallGoesThroughTheRelayOverH248 is
// TestOriginatingCallGoesThroughTheRelay with the P-CSCF driving the
// relay over H.248: the call related procedures of Iq (TS 29.334
// 5.17.2.2 to 5.17.2.5) run as real datagrams between the controller
// and the media gateway. The SDP of the call names the ports the relay
// opened, and the end of the call releases the context, as they do when
// the P-CSCF drives the relay over the HTTP control API.
func TestOriginatingCallGoesThroughTheRelayOverH248(t *testing.T) {
	relay, controller := iqOverUDP(t)
	h := newSessionHarnessWith(t, []MediaRelay{{Name: "iq", Control: controller}})
	h.relay = relay

	responses := h.send(h.ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>"))
	got := arrives(t, h.scscf, responses, "S-CSCF")

	contexts := relay.Contexts()
	if len(contexts) != 1 {
		t.Fatalf("%d relay contexts, want 1", len(contexts))
	}
	c := contexts[0]
	if offer := audio(t, got.Body); offer != c.Terminations[agw.Core].Local {
		t.Errorf("offer towards the network names %v, want the core termination %v", offer, c.Terminations[agw.Core].Local)
	}
	if remote := c.Terminations[agw.Access].Remote; remote != netip.MustParseAddrPort("192.0.2.10:4000") {
		t.Errorf("access termination sends to %v, want the UE's SDP address", remote)
	}
	ringing := next(t, responses)
	if ringing.StatusCode != 183 {
		t.Fatalf("UE got %d, want 183", ringing.StatusCode)
	}
	if answer := audio(t, ringing.Body); answer != c.Terminations[agw.Access].Local {
		t.Errorf("answer to the UE names %v, want the access termination %v", answer, c.Terminations[agw.Access].Local)
	}
	if remote := relay.Contexts()[0].Terminations[agw.Core].Remote; remote != netip.MustParseAddrPort("192.0.2.99:6000") {
		t.Errorf("core termination sends to %v, want the callee's SDP address", remote)
	}

	// The 486 ends the call, and the Subtract that follows it crosses
	// the two endpoints, so the context goes a moment after the response.
	close(h.release)
	if r := next(t, responses); r.StatusCode != 486 {
		t.Fatalf("UE got %d, want 486", r.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(relay.Contexts()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d relay contexts left 10 s after the 486", len(relay.Contexts()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
