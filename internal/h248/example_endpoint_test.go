package h248_test

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/yuu-biz/ims-practice/internal/h248"
)

// A media gateway registers with its controller (IMS-AGW Register, TS
// 29.334 5.17.3.5), and the controller then reserves a termination on
// it. Each side has one Endpoint, which sends its own requests and
// answers those of the other side. In a deployment both would listen on
// port 2944 (h248.DefaultPort); here the system picks free ports on the
// loopback interface.
func ExampleEndpoint() {
	controllerConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	controller, err := h248.NewEndpoint(controllerConn, h248.EndpointConfig{
		MId: h248.MId{DomainName: "pcscf1.ims.example.net"},
		Handler: func(ctx context.Context, r *h248.IncomingRequest) h248.TransactionReply {
			command := r.Actions[0].Commands[0]
			fmt.Printf("controller: %s from %s with %s\n", command.Command, r.From.DomainName, command.ServiceChange.Method)
			return h248.TransactionReply{Actions: []h248.ActionReply{{
				ContextID: h248.ContextNull,
				Commands:  []h248.CommandReply{{Command: h248.ServiceChange, TerminationID: h248.TerminationRoot}},
			}}}
		},
		// The first transaction IDs are fixed only so that the output
		// can be checked. Left at zero, they are picked at random.
		FirstTransactionID: 1,
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
	gateway, err := h248.NewEndpoint(gatewayConn, h248.EndpointConfig{
		MId: h248.MId{DomainName: "agw1.ims.example.net"},
		Handler: func(ctx context.Context, r *h248.IncomingRequest) h248.TransactionReply {
			fmt.Printf("gateway: transaction %d with %s on %s\n", r.TransactionID, r.Actions[0].Commands[0].Command, r.Actions[0].Commands[0].TerminationID)
			return h248.TransactionReply{Actions: []h248.ActionReply{{
				ContextID: 7,
				Commands: []h248.CommandReply{{
					Command:       h248.Add,
					TerminationID: "ip/1/access/11",
					Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
						Local: &h248.LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 192.0.2.20\nm=audio 40000 RTP/AVP 0\nb=AS:80\n"},
					}},
				}},
			}}}
		},
		FirstTransactionID: 5001,
	})
	if err != nil {
		log.Fatal(err)
	}
	go gateway.Serve()
	defer gateway.Close()

	ctx := context.Background()
	registered, err := gateway.Request(ctx, controllerConn.LocalAddr(), []h248.ActionRequest{{
		ContextID: h248.ContextNull,
		Commands: []h248.CommandRequest{{
			Command:       h248.ServiceChange,
			TerminationID: h248.TerminationRoot,
			ServiceChange: &h248.ServiceChangeParm{
				Method:  h248.Restart,
				Reason:  "901 Cold Boot",
				Profile: &h248.ServiceChangeProfile{ProfileName: "threegIq", Version: 7},
				Version: 2,
			},
		}},
	}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("gateway: registered in transaction", registered.TransactionID)

	reply, err := controller.Request(ctx, gatewayConn.LocalAddr(), []h248.ActionRequest{{
		ContextID: h248.ContextChoose,
		Commands: []h248.CommandRequest{{
			Command:       h248.Add,
			TerminationID: "ip/1/access/$",
			Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
				Local: &h248.LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\nb=AS:80\n"},
			}},
		}},
	}})
	if err != nil {
		log.Fatal(err)
	}
	if reply.Error != nil {
		log.Fatalf("the gateway refused with %d", reply.Error.ErrorCode)
	}
	fmt.Println("controller: context", reply.Actions[0].ContextID, "termination", reply.Actions[0].Commands[0].TerminationID)
	// Output:
	// controller: ServiceChange from agw1.ims.example.net with Restart
	// gateway: registered in transaction 5001
	// gateway: transaction 1 with Add on ip/1/access/$
	// controller: context 7 termination ip/1/access/11
}
