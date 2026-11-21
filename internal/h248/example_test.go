package h248_test

import (
	"fmt"
	"log"

	"github.com/yuu-biz/ims-practice/internal/h248"
)

// The controller asks the media gateway for a new context with one
// termination on the access side, leaving the context ID, the
// termination ID, the address and the port to the media gateway
// (Reserve AGW Connection Point, TS 29.334 5.17.2.2). It then reads
// what the media gateway chose from the reply.
func Example() {
	request := h248.Message{
		Version: 2,
		MId:     h248.MId{DomainName: "pcscf1.ims.example.net"},
		Transactions: []h248.Transaction{
			h248.TransactionRequest{TransactionID: 1001, Actions: []h248.ActionRequest{{
				ContextID: h248.ContextChoose,
				Commands: []h248.CommandRequest{{
					Command:       h248.Add,
					TerminationID: "ip/1/access/$",
					Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
						LocalControl: &h248.LocalControlDescriptor{StreamMode: h248.SendRecv},
						Local:        &h248.LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\nb=AS:80\n"},
					}},
				}},
			}}},
		},
	}
	text, err := request.Marshal()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", text)

	answer, err := h248.Parse([]byte(`MEGACO/2 <agw1.ims.example.net>
Reply = 1001 {
  Context = 7 {
    Add = ip/1/access/11 {
      Media {
        Local {
v=0
c=IN IP4 192.0.2.20
m=audio 40000 RTP/AVP 0
b=AS:80
}
      }
    }
  }
}
`))
	if err != nil {
		log.Fatal(err)
	}
	reply, ok := answer.Transactions[0].(h248.TransactionReply)
	if !ok || reply.Error != nil {
		log.Fatal("the transaction failed")
	}
	action := reply.Actions[0]
	add := action.Commands[0]
	if add.Error != nil {
		log.Fatalf("Add failed with %d", add.Error.ErrorCode)
	}
	fmt.Println("transaction:", reply.TransactionID)
	fmt.Println("context:", action.ContextID)
	fmt.Println("termination:", add.TerminationID)
	fmt.Printf("local SDP:\n%s", add.Media.OneStream.Local.SDP)
	// Output:
	// MEGACO/2 <pcscf1.ims.example.net>
	// Transaction = 1001 {
	//   Context = $ {
	//     Add = ip/1/access/$ {
	//       Media {
	//         LocalControl {
	//           Mode = SendReceive
	//         },
	//         Local {
	// v=0
	// c=IN IP4 $
	// m=audio $ RTP/AVP 0
	// b=AS:80
	// }
	//       }
	//     }
	//   }
	// }
	//
	// transaction: 1001
	// context: 7
	// termination: ip/1/access/11
	// local SDP:
	// v=0
	// c=IN IP4 192.0.2.20
	// m=audio 40000 RTP/AVP 0
	// b=AS:80
}
