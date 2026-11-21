package mediactl_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"

	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// exampleRelay stands for the media relay. It prints what the gateway
// asks of it.
type exampleRelay struct{}

func (exampleRelay) Reserve(ctx context.Context, media mediactl.Media) (mediactl.Reservation, error) {
	fmt.Printf("relay: reserve a context for %s\n", media.Codecs[0].Rtpmap())
	return mediactl.Reservation{ID: "c1", Local: map[mediactl.Side]netip.AddrPort{
		mediactl.Access: netip.MustParseAddrPort("192.0.2.20:40000"),
		mediactl.Core:   netip.MustParseAddrPort("192.0.2.20:40002"),
	}}, nil
}

func (exampleRelay) Configure(ctx context.Context, id string, side mediactl.Side, remote mediactl.Media) error {
	fmt.Printf("relay: %s of %s sends to %s\n", side, id, remote.Address)
	return nil
}

func (exampleRelay) Release(ctx context.Context, id string) error {
	fmt.Printf("relay: release %s\n", id)
	return nil
}

// The media relay runs a Gateway, which registers with the Controller
// of the P-CSCF. The P-CSCF then takes the media of a call through the
// relay: it reserves a context, tells each termination where to send,
// and releases the context. In a deployment both would listen on port
// 2944 (h248.DefaultPort); here the system picks free ports on the
// loopback interface.
func Example() {
	controllerConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	controller, err := mediactl.NewController(controllerConn, mediactl.ControllerConfig{
		Endpoint: h248.EndpointConfig{MId: h248.MId{DomainName: "pcscf1.ims.example.net"}},
	})
	if err != nil {
		log.Fatal(err)
	}
	go controller.Serve()
	defer controller.Close()

	gatewayConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	gateway, err := mediactl.NewGateway(gatewayConn, mediactl.GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: h248.MId{DomainName: "agw1.ims.example.net"}},
		LocateController: func() (net.Addr, error) { return controllerConn.LocalAddr(), nil },
		Relay:            exampleRelay{},
		MaxWaitingDelay:  time.Millisecond,
		// Fixed only so that the output can be checked. Left at zero,
		// the first context ID is picked at random.
		FirstContextID: 7,
	})
	if err != nil {
		log.Fatal(err)
	}
	go gateway.Serve()
	defer gateway.Close()

	// The P-CSCF uses the Controller as a mediactl.Relay. Until the
	// gateway has registered, there is no gateway to reserve on.
	var relay mediactl.Relay = controller
	ctx := context.Background()
	pcmu := sdp.Codec{PayloadType: 0, Name: sdp.NamePCMU, ClockRate: 8000}
	var r mediactl.Reservation
	for {
		r, err = relay.Reserve(ctx, mediactl.Media{Codecs: []sdp.Codec{pcmu}, Bandwidth: 80})
		if !errors.Is(err, mediactl.ErrUnavailable) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("P-CSCF: context %s, access %s, core %s\n", r.ID, r.Local[mediactl.Access], r.Local[mediactl.Core])

	ue := mediactl.Media{Address: netip.MustParseAddrPort("192.0.2.30:49170"), Codecs: []sdp.Codec{pcmu}, Bandwidth: 80}
	if err := relay.Configure(ctx, r.ID, mediactl.Access, ue); err != nil {
		log.Fatal(err)
	}
	network := mediactl.Media{Address: netip.MustParseAddrPort("203.0.113.40:50000"), Codecs: []sdp.Codec{pcmu}, Bandwidth: 80}
	if err := relay.Configure(ctx, r.ID, mediactl.Core, network); err != nil {
		log.Fatal(err)
	}
	if err := relay.Release(ctx, r.ID); err != nil {
		log.Fatal(err)
	}
	fmt.Println("P-CSCF: context released")
	// Output:
	// relay: reserve a context for 0 PCMU/8000
	// P-CSCF: context 7@agw1.ims.example.net, access 192.0.2.20:40000, core 192.0.2.20:40002
	// relay: access of c1 sends to 192.0.2.30:49170
	// relay: core of c1 sends to 203.0.113.40:50000
	// relay: release c1
	// P-CSCF: context released
}
