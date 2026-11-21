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
)

// exampleProcessor stands for the media node in the role of an MRFP. It
// prints what the gateway asks of it, and reports the end of each
// announcement at once, as if it had played in no time.
type exampleProcessor struct {
	gateway *mediactl.Gateway
}

func (*exampleProcessor) Add(ctx context.Context, terminations ...mediactl.TerminationRequest) (mediactl.Context, error) {
	t := terminations[0]
	fmt.Printf("MRFP: new context, %s to %s\n", t.Mode, t.Remote.Address)
	return mediactl.Context{ID: "c1", Terminations: []mediactl.Termination{
		{ID: "t1", Local: netip.MustParseAddrPort("192.0.2.21:42000")},
	}}, nil
}

func (*exampleProcessor) AddTermination(ctx context.Context, contextID string, t mediactl.TerminationRequest) (mediactl.Termination, error) {
	return mediactl.Termination{}, mediactl.ErrInsufficientResources
}

func (p *exampleProcessor) Modify(ctx context.Context, contextID string, id mediactl.TerminationID, change mediactl.TerminationRequest) error {
	if change.Signals != nil && change.Signals.Play != nil {
		fmt.Printf("MRFP: %s of %s plays %s %d times, %s\n", id, contextID, change.Signals.Play.Announcement, change.Signals.Play.Repeat, change.Mode)
		p.gateway.SignalCompleted(contextID, id)
	}
	return nil
}

func (*exampleProcessor) SubtractTermination(ctx context.Context, contextID string, id mediactl.TerminationID) (mediactl.Statistics, error) {
	fmt.Printf("MRFP: subtract %s of %s\n", id, contextID)
	return mediactl.Statistics{PacketsSent: 150}, nil
}

// The media node, as an MRFP, runs a Gateway with a Processor, which
// registers with the Controller of the MRFC, set up with MpProfile. The
// MRFC then plays an announcement to a UE: it reserves a termination
// that sends to the UE, starts the announcement and asks to hear of its
// end, waits for the Notify, and releases the termination.
func Example_announcement() {
	controllerConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	completed := make(chan mediactl.Notification, 1)
	controller, err := mediactl.NewController(controllerConn, mediactl.ControllerConfig{
		Profile:  mediactl.MpProfile,
		Endpoint: h248.EndpointConfig{MId: h248.MId{DomainName: "mrfc1.ims.example.net"}},
		// The MRFC hands each end of an announcement to the dialog that
		// waits for it, by the RequestID that the dialog chose.
		Notify: func(n mediactl.Notification) { completed <- n },
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
	processor := &exampleProcessor{}
	gateway, err := mediactl.NewGateway(gatewayConn, mediactl.GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: h248.MId{DomainName: "mrfp1.ims.example.net"}},
		LocateController: func() (net.Addr, error) { return controllerConn.LocalAddr(), nil },
		Processor:        processor,
		MaxWaitingDelay:  time.Millisecond,
		FirstContextID:   7,
	})
	if err != nil {
		log.Fatal(err)
	}
	processor.gateway = gateway
	go gateway.Serve()
	defer gateway.Close()

	// The MRFC uses the Controller as a mediactl.Processor. Until the
	// MRFP has registered, there is none to reserve on.
	var mrfp mediactl.Processor = controller
	ctx := context.Background()
	ue := mediactl.Media{Address: netip.MustParseAddrPort("192.0.2.30:49170")}
	var cx mediactl.Context
	for {
		cx, err = mrfp.Add(ctx, mediactl.TerminationRequest{Mode: mediactl.SendOnly, Remote: ue})
		if !errors.Is(err, mediactl.ErrUnavailable) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		log.Fatal(err)
	}
	t := cx.Terminations[0]
	fmt.Printf("MRFC: context %s, termination %s receives on %s\n", cx.ID, t.ID, t.Local)

	err = mrfp.Modify(ctx, cx.ID, t.ID, mediactl.TerminationRequest{
		Mode:    mediactl.SendOnly,
		Signals: &mediactl.Signals{Play: &mediactl.Play{Announcement: "notice", Repeat: 2}},
		Events:  &mediactl.Events{RequestID: 42, Detect: []mediactl.EventName{mediactl.SignalCompleted}},
	})
	if err != nil {
		log.Fatal(err)
	}
	n := <-completed
	fmt.Printf("MRFC: %s of %s ended (%s, %s), request %d\n", n.Signal, n.Termination, n.Event, n.Method, n.RequestID)

	stats, err := mrfp.SubtractTermination(ctx, cx.ID, t.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MRFC: the termination sent %d RTP packets\n", stats.PacketsSent)
	// Output:
	// MRFP: new context, sendonly to 192.0.2.30:49170
	// MRFC: context 7@mrfp1.ims.example.net, termination ip/7/1 receives on 192.0.2.21:42000
	// MRFP: t1 of c1 plays notice 2 times, sendonly
	// MRFC: an/apf of ip/7/1 ended (g/sc, TO), request 42
	// MRFP: subtract t1 of c1
	// MRFC: the termination sent 150 RTP packets
}
