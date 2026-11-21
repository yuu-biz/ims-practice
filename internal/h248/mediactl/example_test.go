package mediactl_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// exampleRelay stands for the media relay. It prints what the gateway
// asks of it.
type exampleRelay struct{}

func (exampleRelay) Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error) {
	fmt.Printf("relay: add a context for %s\n", terminations[0].LocalMedia.Codecs[0].Rtpmap())
	return agw.Context{ID: "c1", Terminations: map[agw.TerminationID]*agw.Termination{
		agw.Access: {ID: agw.Access, Local: netip.MustParseAddrPort("192.0.2.20:40000")},
		agw.Core:   {ID: agw.Core, Local: netip.MustParseAddrPort("192.0.2.20:40002")},
	}}, nil
}

func (exampleRelay) Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error {
	fmt.Printf("relay: %s of %s sends to %s\n", termination, id, change.Remote)
	return nil
}

func (exampleRelay) Subtract(ctx context.Context, id string) error {
	fmt.Printf("relay: subtract %s\n", id)
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

	// The P-CSCF uses the Controller as its media relay, with the same
	// operations as the HTTP client of the media node. Until the gateway
	// has registered, there is no gateway to add a context on.
	var relay mediactl.Relay = controller
	ctx := context.Background()
	pcmu := agw.Media{Codecs: []sdp.Codec{{PayloadType: 0, Name: sdp.NamePCMU, ClockRate: 8000}}, Bandwidth: 80}
	var c agw.Context
	for {
		c, err = relay.Add(ctx, agw.TerminationRequest{ID: agw.Access, LocalMedia: pcmu}, agw.TerminationRequest{ID: agw.Core, LocalMedia: pcmu})
		if !errors.Is(err, mediactl.ErrUnavailable) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("P-CSCF: context %s, access %s, core %s\n", c.ID, c.Terminations[agw.Access].Local, c.Terminations[agw.Core].Local)

	ue := agw.TerminationRequest{Remote: netip.MustParseAddrPort("192.0.2.30:49170"), RemoteMedia: pcmu}
	if err := relay.Modify(ctx, c.ID, agw.Access, ue); err != nil {
		log.Fatal(err)
	}
	network := agw.TerminationRequest{Remote: netip.MustParseAddrPort("203.0.113.40:50000"), RemoteMedia: pcmu}
	if err := relay.Modify(ctx, c.ID, agw.Core, network); err != nil {
		log.Fatal(err)
	}
	if err := relay.Subtract(ctx, c.ID); err != nil {
		log.Fatal(err)
	}
	fmt.Println("P-CSCF: context subtracted")
	// Output:
	// relay: add a context for 0 PCMU/8000
	// P-CSCF: context 7@agw1.ims.example.net, access 192.0.2.20:40000, core 192.0.2.20:40002
	// relay: access of c1 sends to 192.0.2.30:49170
	// relay: core of c1 sends to 203.0.113.40:50000
	// relay: subtract c1
	// P-CSCF: context subtracted
}
