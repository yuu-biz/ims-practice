package h248

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// The names and addresses of the nodes in the tests.
var (
	mgc = MId{DomainName: "pcscf1.ims.example.net"}
	mg  = MId{DomainName: "agw1.ims.example.net"}
)

// roundTripCase is a message and the text that Marshal writes for it.
type roundTripCase struct {
	name    string
	message Message
	text    string
}

// roundTrip checks both directions: the message is written as the
// text, and the text is read back as the message.
func roundTrip(t *testing.T, cases []roundTripCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, err := c.message.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(text) != c.text {
				t.Errorf("Marshal wrote\n%s\nwant\n%s", text, c.text)
			}
			message, err := Parse([]byte(c.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(message, c.message) {
				t.Errorf("Parse read\n%+v\nwant\n%+v", message, c.message)
			}
		})
	}
}

// parseCase is a text as another implementation might write it, and
// the message it stands for.
type parseCase struct {
	name    string
	text    string
	message Message
}

// parseThenRoundTrip checks that the text is read as the message, and
// that writing the message and reading it again gives the same.
func parseThenRoundTrip(t *testing.T, cases []parseCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message, err := Parse([]byte(c.text))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(message, c.message) {
				t.Fatalf("Parse read\n%+v\nwant\n%+v", message, c.message)
			}
			text, err := message.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			again, err := Parse(text)
			if err != nil {
				t.Fatalf("Parse of what Marshal wrote: %v\n%s", err, text)
			}
			if !reflect.DeepEqual(again, c.message) {
				t.Errorf("after Marshal, Parse read\n%+v\nwant\n%+v", again, c.message)
			}
		})
	}
}

// errorCase is an input that must be refused, and a part of the reason.
type errorCase struct {
	name string
	text string
	want string
}

func parseErrors(t *testing.T, cases []errorCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message, err := Parse([]byte(c.text))
			if err == nil {
				t.Fatalf("Parse read %+v, want an error with %q", message, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse failed with %q, want an error with %q", err, c.want)
			}
		})
	}
}

// marshalErrorCase is a message that cannot be written, and a part of
// the reason.
type marshalErrorCase struct {
	name    string
	message Message
	want    string
}

func marshalErrors(t *testing.T, cases []marshalErrorCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, err := c.message.Marshal()
			if err == nil {
				t.Fatalf("Marshal wrote\n%s\nwant an error with %q", text, c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Marshal failed with %q, want an error with %q", err, c.want)
			}
		})
	}
}

// request wraps commands on one context into a message from the MGC.
func request(id uint32, context ContextID, commands ...CommandRequest) Message {
	return Message{Version: 2, MId: mgc, Transactions: []Transaction{
		TransactionRequest{TransactionID: id, Actions: []ActionRequest{{ContextID: context, Commands: commands}}},
	}}
}

// reply wraps command replies on one context into a message from the
// MG.
func reply(id uint32, context ContextID, commands ...CommandReply) Message {
	return Message{Version: 2, MId: mg, Transactions: []Transaction{
		TransactionReply{TransactionID: id, Actions: []ActionReply{{ContextID: context, Commands: commands}}},
	}}
}

var messageRoundTrips = []roundTripCase{
	{
		name: "request with two actions",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionRequest{TransactionID: 1002, Actions: []ActionRequest{
				{ContextID: 7, Commands: []CommandRequest{
					{Optional: true, WildcardResponse: true, Command: Subtract, TerminationID: TerminationAll},
				}},
				{ContextID: 8, Commands: []CommandRequest{
					{Command: Subtract, TerminationID: "ip/1/access/21"},
					{Command: Subtract, TerminationID: "ip/1/core/22"},
				}},
			}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1002 {
  Context = 7 {
    O-W-Subtract = *
  },
  Context = 8 {
    Subtract = ip/1/access/21,
    Subtract = ip/1/core/22
  }
}
`,
	},
	{
		name: "reply after a pending, with a failed command",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionReply{TransactionID: 1002, ImmAckRequired: true, Actions: []ActionReply{
				{ContextID: 7, Commands: []CommandReply{
					{Command: Subtract, TerminationID: TerminationAll},
				}},
				{ContextID: 8, Commands: []CommandReply{
					{Command: Subtract, TerminationID: "ip/1/access/21"},
					{Command: Subtract, TerminationID: "ip/1/core/22", Error: &ErrorDescriptor{ErrorCode: 430, ErrorText: "Unknown TerminationID"}},
				}},
			}},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1002 {
  ImmAckRequired,
  Context = 7 {
    Subtract = *
  },
  Context = 8 {
    Subtract = ip/1/access/21,
    Subtract = ip/1/core/22 {
      Error = 430 { "Unknown TerminationID" }
    }
  }
}
`,
	},
	{
		name: "reply with an error for the action",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionReply{TransactionID: 1003, Actions: []ActionReply{
				{ContextID: 8, Commands: []CommandReply{
					{Command: Subtract, TerminationID: "ip/1/access/21"},
				}, Error: &ErrorDescriptor{ErrorCode: 500}},
			}},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1003 {
  Context = 8 {
    Subtract = ip/1/access/21,
    Error = 500 { }
  }
}
`,
	},
	{
		name: "reply with an error for the transaction",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionReply{TransactionID: 1004, Error: &ErrorDescriptor{ErrorCode: 411, ErrorText: "The transaction refers to an unknown ContextID"}},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1004 {
  Error = 411 { "The transaction refers to an unknown ContextID" }
}
`,
	},
	{
		name:    "error for the message",
		message: Message{Version: 1, MId: mgc, MessageError: &ErrorDescriptor{ErrorCode: 406, ErrorText: "Version Not Supported"}},
		text: `MEGACO/1 <pcscf1.ims.example.net>
Error = 406 { "Version Not Supported" }
`,
	},
	{
		name: "pending",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionPending{TransactionID: 1002},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Pending = 1002 { }
`,
	},
	{
		name: "reply and response ack in one message",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionReply{TransactionID: 2001, Actions: []ActionReply{
				{ContextID: 7, Commands: []CommandReply{{Command: Notify, TerminationID: "ip/1/access/11"}}},
			}},
			TransactionResponseAck{Acks: []TransactionAck{{FirstAck: 1001}, {FirstAck: 1003, LastAck: 1005}}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Reply = 2001 {
  Context = 7 {
    Notify = ip/1/access/11
  }
}
TransactionResponseAck { 1001, 1003-1005 }
`,
	},
	{
		name: "context wildcards and ROOT",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionRequest{TransactionID: 1, Actions: []ActionRequest{
				{ContextID: ContextChoose, Commands: []CommandRequest{{Command: Add, TerminationID: TerminationChoose}}},
				{ContextID: ContextAll, Commands: []CommandRequest{{Command: Subtract, TerminationID: "ip/1/*"}}},
				{ContextID: ContextNull, Commands: []CommandRequest{{Command: Modify, TerminationID: TerminationRoot}}},
			}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1 {
  Context = $ {
    Add = $
  },
  Context = * {
    Subtract = ip/1/*
  },
  Context = - {
    Modify = ROOT
  }
}
`,
	},
}

func TestMessagesAndTransactions(t *testing.T) {
	roundTrip(t, messageRoundTrips)
}

var pendingOnly = []Transaction{TransactionPending{TransactionID: 1}}

var mIDRoundTrips = []roundTripCase{
	{
		name:    "IPv4 address and port",
		message: Message{Version: 2, MId: MId{DomainAddress: netip.MustParseAddr("192.0.2.10"), PortNumber: 2944}, Transactions: pendingOnly},
		text:    "MEGACO/2 [192.0.2.10]:2944\nPending = 1 { }\n",
	},
	{
		name:    "IPv6 address",
		message: Message{Version: 2, MId: MId{DomainAddress: netip.MustParseAddr("2001:db8::10")}, Transactions: pendingOnly},
		text:    "MEGACO/2 [2001:db8::10]\nPending = 1 { }\n",
	},
	{
		name:    "domain name and port",
		message: Message{Version: 2, MId: MId{DomainName: "agw1.ims.example.net", PortNumber: 2944}, Transactions: pendingOnly},
		text:    "MEGACO/2 <agw1.ims.example.net>:2944\nPending = 1 { }\n",
	},
	{
		name:    "device name",
		message: Message{Version: 2, MId: MId{DeviceName: "agw1"}, Transactions: pendingOnly},
		text:    "MEGACO/2 agw1\nPending = 1 { }\n",
	},
}

func TestMId(t *testing.T) {
	roundTrip(t, mIDRoundTrips)
}

// The receiver takes the short tokens, any case, comments and any
// white space (TS 29.334 5.9, RFC 3525 Annex B.2).
func TestParseOtherSpellings(t *testing.T) {
	message := Message{Version: 2, MId: mgc, Transactions: []Transaction{
		TransactionRequest{TransactionID: 1002, Actions: []ActionRequest{
			{ContextID: 7, Commands: []CommandRequest{
				{Optional: true, WildcardResponse: true, Command: Subtract, TerminationID: TerminationAll},
			}},
			{ContextID: ContextNull, Commands: []CommandRequest{
				{Command: Modify, TerminationID: TerminationRoot},
			}},
		}},
		TransactionPending{TransactionID: 2001},
		TransactionResponseAck{Acks: []TransactionAck{{FirstAck: 2000}}},
	}}
	parseThenRoundTrip(t, []parseCase{
		{
			name:    "short tokens without white space",
			text:    "!/2 <pcscf1.ims.example.net> T=1002{C=7{O-W-S=*},C=-{MF=ROOT}}PN=2001{}K{2000}",
			message: message,
		},
		{
			name: "lower case, CRLF and comments",
			text: "megaco/2 <pcscf1.ims.example.net> ; from the MGC\r\n" +
				"transaction = 1002 { ; release\r\n" +
				"\tcontext = 7 { o-w-subtract = * },\r\n" +
				"\tcontext = - { modify = root }\r\n" +
				"}\r\n" +
				"pending = 2001 { }\r\n" +
				"transactionresponseack { 2000 }\r\n",
			message: message,
		},
	})
}

func TestParseRefusesBrokenFraming(t *testing.T) {
	const header = "MEGACO/2 <agw1.ims.example.net>\n"
	parseErrors(t, []errorCase{
		{"empty", "", "expected a token"},
		{"other protocol", "SIP/2.0 200 OK\n", `unknown or unsupported token "SIP"`},
		{"authentication header", "Authentication = 0x00000001:0x00000002:0x000000000000000000000000\n" + header, `unknown or unsupported token "Authentication"`},
		{"no version", "MEGACO <agw1.ims.example.net>\n", "expected the version"},
		{"three digit version", "MEGACO/100 <agw1.ims.example.net>\n", "more than two digits"},
		{"no mId", "MEGACO/2 {\n", "expected an mId"},
		{"MTP address", "MEGACO/2 MTP{0E01}\nPending = 1 { }\n", "MTP address is not supported"},
		{"bad address", "MEGACO/2 [192.0.2.300]\nPending = 1 { }\n", "is not an IP address"},
		{"no body", header, "either one error descriptor or a list of transactions"},
		{"error and transaction", header + "Error = 400 { }\nPending = 1 { }\n", "either one error descriptor or a list of transactions"},
		{"line number", header + "Pending = 1 { }\nPending = x { }\n", "line 3: expected a number"},
		{"transaction ID too large", header + "Pending = 4294967296 { }\n", "does not fit in 32 bits"},
		{"reserved context ID", header + "Transaction = 1 { Context = 0 { Subtract = * } }\n", "context ID 0 is reserved"},
		{"action without command", header + "Transaction = 1 { Context = 1 { } }\n", "expected a token"},
		{"not a command", header + "Transaction = 1 { Context = 1 { Pending = * } }\n", `"Pending" is not a command`},
		{"unclosed", header + "Transaction = 1 { Context = 1 { Subtract = * }\n", `expected '}', found the end of the message`},
		{"no termination ID", header + "Transaction = 1 { Context = 1 { Subtract = } }\n", "expected a termination ID"},
		{"descriptor not legal", header + "Transaction = 1 { Context = 1 { Subtract = * { Error = 500 { } } } }\n", "a Error descriptor is not legal in this Subtract"},
		{"descriptor twice", header + "Reply = 1 { Context = 1 { Subtract = * { Error = 500 { }, Error = 501 { } } } }\n", "appears twice"},
		{"error and actions", header + "Reply = 1 { Context = 1 { Subtract = * }, Error = 500 { } }\n", "either one error descriptor or action replies"},
		{"command after the error", header + "Reply = 1 { Context = 1 { Error = 500 { }, Subtract = * } }\n", "comes last"},
		{"late ImmAckRequired", header + "Reply = 1 { Context = 1 { Subtract = * }, ImmAckRequired }\n", "comes first"},
		{"five digit error code", header + "Error = 10000 { }\n", "more than four digits"},
		{"open quoted string", header + "Error = 400 { \"oops }\n", "not closed"},
		{"backward ack range", header + "TransactionResponseAck { 5-3 }\n", "runs backwards"},
	})
}

func TestMarshalRefusesBrokenFraming(t *testing.T) {
	subtract := []ActionRequest{{ContextID: 1, Commands: []CommandRequest{{Command: Subtract, TerminationID: TerminationAll}}}}
	marshalErrors(t, []marshalErrorCase{
		{"no version", Message{MId: mg, Transactions: []Transaction{TransactionPending{TransactionID: 1}}}, "version 0"},
		{"no mId", Message{Version: 2, Transactions: []Transaction{TransactionPending{TransactionID: 1}}}, "an mId is exactly one of"},
		{"two forms of mId", Message{Version: 2, MId: MId{DomainName: "a.example.net", DeviceName: "a"}, Transactions: []Transaction{TransactionPending{}}}, "an mId is exactly one of"},
		{"bad domain name", Message{Version: 2, MId: MId{DomainName: "a b"}, Transactions: []Transaction{TransactionPending{}}}, "is not a domain name"},
		{"no body", Message{Version: 2, MId: mg}, "either one error descriptor or a list of transactions"},
		{"request without action", Message{Version: 2, MId: mg, Transactions: []Transaction{TransactionRequest{TransactionID: 1}}}, "has no action"},
		{"action without command", Message{Version: 2, MId: mg, Transactions: []Transaction{TransactionRequest{TransactionID: 1, Actions: []ActionRequest{{ContextID: 1}}}}}, "has no command"},
		{"empty reply", Message{Version: 2, MId: mg, Transactions: []Transaction{TransactionReply{TransactionID: 1}}}, "either one error descriptor or action replies"},
		{"empty ack", Message{Version: 2, MId: mg, Transactions: []Transaction{TransactionResponseAck{}}}, "confirms no transaction"},
		{"unknown command", Message{Version: 2, MId: mg, Transactions: []Transaction{TransactionRequest{TransactionID: 1, Actions: []ActionRequest{{ContextID: 1, Commands: []CommandRequest{{Command: "Delete", TerminationID: "*"}}}}}}}, `"Delete" is not a command`},
		{"bad termination ID", Message{Version: 2, MId: mg, Transactions: []Transaction{TransactionRequest{TransactionID: 1, Actions: []ActionRequest{{ContextID: 1, Commands: []CommandRequest{{Command: Subtract, TerminationID: "ip 1"}}}}}}}, "is not a termination ID"},
		{"quote in an error text", Message{Version: 2, MId: mg, MessageError: &ErrorDescriptor{ErrorCode: 400, ErrorText: `say "no"`}}, "cannot be written as a quoted string"},
		{"error code out of range", Message{Version: 2, MId: mg, MessageError: &ErrorDescriptor{ErrorCode: 10000}}, "up to four digits"},
		{"error and transactions", Message{Version: 2, MId: mg, MessageError: &ErrorDescriptor{ErrorCode: 400}, Transactions: []Transaction{TransactionRequest{TransactionID: 1, Actions: subtract}}}, "either one error descriptor or a list of transactions"},
	})
}
