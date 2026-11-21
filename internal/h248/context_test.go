package h248

import "testing"

var contextRoundTrips = []roundTripCase{
	{
		name: "emergency call with a priority",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionRequest{TransactionID: 1001, Actions: []ActionRequest{{
				ContextID:      ContextChoose,
				ContextRequest: &ContextRequest{Priority: new(uint16(0)), Emergency: true},
				Commands:       []CommandRequest{{Command: Add, TerminationID: "ip/1/access/$"}},
			}}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1001 {
  Context = $ {
    Priority = 0,
    Emergency,
    Add = ip/1/access/$
  }
}
`,
	},
	{
		name: "topology of a conference, without a command",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionRequest{TransactionID: 1002, Actions: []ActionRequest{{
				ContextID: 9,
				ContextRequest: &ContextRequest{TopologyReq: []TopologyRequest{
					{TerminationFrom: "ip/1/mrf/31", TerminationTo: "ip/1/mrf/32", TopologyDirection: Isolate},
					{TerminationFrom: "ip/1/mrf/31", TerminationTo: TerminationAll, TopologyDirection: Oneway},
					{TerminationFrom: "ip/1/mrf/32", TerminationTo: "ip/1/mrf/33", TopologyDirection: Bothway},
				}},
			}}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1002 {
  Context = 9 {
    Topology {
      ip/1/mrf/31, ip/1/mrf/32, Isolate,
      ip/1/mrf/31, *, Oneway,
      ip/1/mrf/32, ip/1/mrf/33, Bothway
    }
  }
}
`,
	},
	{
		name: "Move of a termination into a conference",
		message: request(1003, 9, CommandRequest{
			Command: Move, TerminationID: "ip/1/mrf/34",
			Media: &MediaDescriptor{OneStream: &StreamParms{
				LocalControl: &LocalControlDescriptor{StreamMode: SendRecv},
			}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1003 {
  Context = 9 {
    Move = ip/1/mrf/34 {
      Media {
        LocalControl {
          Mode = SendReceive
        }
      }
    }
  }
}
`,
	},
	{
		name:    "reply to the Move",
		message: reply(1003, 9, CommandReply{Command: Move, TerminationID: "ip/1/mrf/34"}),
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1003 {
  Context = 9 {
    Move = ip/1/mrf/34
  }
}
`,
	},
}

func TestContextRequest(t *testing.T) {
	roundTrip(t, contextRoundTrips)
}

func TestParseContextRequestInOtherSpellings(t *testing.T) {
	parseThenRoundTrip(t, []parseCase{
		{
			name: "short tokens",
			text: "!/2 <pcscf1.ims.example.net> T=1{C=9{TP{ip/1/mrf/31,ip/1/mrf/32,IS,ip/1/mrf/31,*,OW,*,*,BW},PR=15,EG,S=ip/1/mrf/33}}",
			message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
				TransactionRequest{TransactionID: 1, Actions: []ActionRequest{{
					ContextID: 9,
					ContextRequest: &ContextRequest{
						Priority:  new(uint16(15)),
						Emergency: true,
						TopologyReq: []TopologyRequest{
							{TerminationFrom: "ip/1/mrf/31", TerminationTo: "ip/1/mrf/32", TopologyDirection: Isolate},
							{TerminationFrom: "ip/1/mrf/31", TerminationTo: TerminationAll, TopologyDirection: Oneway},
							{TerminationFrom: TerminationAll, TerminationTo: TerminationAll, TopologyDirection: Bothway},
						},
					},
					Commands: []CommandRequest{{Command: Subtract, TerminationID: "ip/1/mrf/33"}},
				}}},
			}},
		},
	})
}

func TestParseRefusesBrokenContextRequest(t *testing.T) {
	action := func(body string) string {
		return "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 1 { Context = 9 { " + body + " } }\n"
	}
	parseErrors(t, []errorCase{
		{"context audit", action("ContextAudit { Topology }"), "ContextAudit is not supported"},
		{"priority twice", action("Priority = 1, Priority = 2"), "Priority appears twice"},
		{"topology twice", action("Topology { *, *, Isolate }, Topology { *, *, Bothway }"), "Topology appears twice"},
		{"priority without value", action("Priority"), "expected '='"},
		{"incomplete triple", action("Topology { ip/1/mrf/31, Isolate }"), "expected ','"},
		{"not a direction", action("Topology { ip/1/mrf/31, ip/1/mrf/32, Inactive }"), "is not a direction"},
		{"optional property", action("O-Emergency"), `"Emergency" is not a command`},
		{"property in a reply", "MEGACO/2 <agw1.ims.example.net>\nReply = 1 { Context = 9 { Priority = 3 } }\n", `"Priority" is not a command`},
	})
}

func TestMarshalRefusesBrokenContextRequest(t *testing.T) {
	action := func(c *ContextRequest) Message {
		return Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionRequest{TransactionID: 1, Actions: []ActionRequest{{ContextID: 9, ContextRequest: c}}},
		}}
	}
	marshalErrors(t, []marshalErrorCase{
		{"nothing set and no command", action(&ContextRequest{}), "has no command"},
		{"bad termination", action(&ContextRequest{TopologyReq: []TopologyRequest{{TerminationFrom: "", TerminationTo: "*", TopologyDirection: Isolate}}}), "are not two termination IDs"},
		{"bad direction", action(&ContextRequest{TopologyReq: []TopologyRequest{{TerminationFrom: "*", TerminationTo: "*", TopologyDirection: "Both"}}}), "is not a direction"},
	})
}
