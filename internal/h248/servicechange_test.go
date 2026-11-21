package h248

import (
	"net/netip"
	"testing"
)

// serviceChange wraps a ServiceChange on ROOT into a message from the
// MG.
func serviceChange(id uint32, p *ServiceChangeParm) Message {
	return Message{Version: 2, MId: mg, Transactions: []Transaction{
		TransactionRequest{TransactionID: id, Actions: []ActionRequest{{ContextID: ContextNull, Commands: []CommandRequest{
			{Command: ServiceChange, TerminationID: TerminationRoot, ServiceChange: p},
		}}}},
	}}
}

// serviceChangeReply wraps the reply to a ServiceChange on ROOT into a
// message from the MGC.
func serviceChangeReply(id uint32, p *ServiceChangeResParm) Message {
	return Message{Version: 2, MId: mgc, Transactions: []Transaction{
		TransactionReply{TransactionID: id, Actions: []ActionReply{{ContextID: ContextNull, Commands: []CommandReply{
			{Command: ServiceChange, TerminationID: TerminationRoot, ServiceChangeResult: p},
		}}}},
	}}
}

var serviceChangeRoundTrips = []roundTripCase{
	{
		name: "registration with every parameter",
		message: serviceChange(1, &ServiceChangeParm{
			Method:    Restart,
			Reason:    "901 Cold Boot",
			Profile:   &ServiceChangeProfile{ProfileName: "threegIq", Version: 7},
			Version:   2,
			TimeStamp: TimeNotation{Date: "20261120", Time: "09300000"},
		}),
		text: `MEGACO/2 <agw1.ims.example.net>
Transaction = 1 {
  Context = - {
    ServiceChange = ROOT {
      Services {
        Method = Restart,
        Reason = "901 Cold Boot",
        Profile = threegIq/7,
        Version = 2,
        20261120T09300000
      }
    }
  }
}
`,
	},
	{
		name:    "reply without parameters",
		message: serviceChangeReply(1, nil),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Reply = 1 {
  Context = - {
    ServiceChange = ROOT
  }
}
`,
	},
	{
		name: "reply that lowers the version and names another controller",
		message: serviceChangeReply(1, &ServiceChangeResParm{
			MgcID:     &MId{DomainName: "pcscf2.ims.example.net", PortNumber: 2944},
			Profile:   &ServiceChangeProfile{ProfileName: "threegIq", Version: 7},
			Version:   2,
			TimeStamp: TimeNotation{Date: "20261120", Time: "09300002"},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Reply = 1 {
  Context = - {
    ServiceChange = ROOT {
      Services {
        Profile = threegIq/7,
        Version = 2,
        MgcIdToTry = <pcscf2.ims.example.net>:2944,
        20261120T09300002
      }
    }
  }
}
`,
	},
	{
		name: "hand-over ordered by the controller",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionRequest{TransactionID: 3001, Actions: []ActionRequest{{ContextID: ContextNull, Commands: []CommandRequest{{
				Command: ServiceChange, TerminationID: TerminationRoot,
				ServiceChange: &ServiceChangeParm{
					Method: HandOff,
					Reason: "903 MGC Directed Change",
					MgcID:  &MId{DomainAddress: netip.MustParseAddr("192.0.2.11"), PortNumber: 2944},
				},
			}}}}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 3001 {
  Context = - {
    ServiceChange = ROOT {
      Services {
        Method = HandOff,
        Reason = "903 MGC Directed Change",
        MgcIdToTry = [192.0.2.11]:2944
      }
    }
  }
}
`,
	},
	{
		name: "one termination out of service, reason code alone",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionRequest{TransactionID: 2, Actions: []ActionRequest{{ContextID: 7, Commands: []CommandRequest{{
				Command: ServiceChange, TerminationID: "ip/1/access/11",
				ServiceChange: &ServiceChangeParm{Method: Forced, Reason: "904"},
			}}}}},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Transaction = 2 {
  Context = 7 {
    ServiceChange = ip/1/access/11 {
      Services {
        Method = Forced,
        Reason = "904"
      }
    }
  }
}
`,
	},
	{
		name: "reply with an error",
		message: Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionReply{TransactionID: 1, Actions: []ActionReply{{ContextID: ContextNull, Commands: []CommandReply{{
				Command: ServiceChange, TerminationID: TerminationRoot,
				Error: &ErrorDescriptor{ErrorCode: 406, ErrorText: "Version Not Supported"},
			}}}}},
		}},
		text: `MEGACO/2 <pcscf1.ims.example.net>
Reply = 1 {
  Context = - {
    ServiceChange = ROOT {
      Error = 406 { "Version Not Supported" }
    }
  }
}
`,
	},
}

func TestServiceChangeDescriptor(t *testing.T) {
	roundTrip(t, serviceChangeRoundTrips)
}

func TestParseServiceChangeInOtherSpellings(t *testing.T) {
	methods := []struct {
		short  string
		method ServiceChangeMethod
	}{
		{"GR", Graceful}, {"FO", Forced}, {"RS", Restart}, {"DC", Disconnected}, {"HO", HandOff}, {"FL", Failover},
	}
	var cases []parseCase
	for _, m := range methods {
		cases = append(cases, parseCase{
			name:    "short tokens, method " + string(m.method),
			text:    "!/2 <agw1.ims.example.net> T=1{C=-{SC=ROOT{SV{MT=" + m.short + ",RE=\"900 Service Restored\",PF=threegIq/7,V=2,MG=<pcscf2.ims.example.net>}}}}",
			message: serviceChange(1, &ServiceChangeParm{Method: m.method, Reason: "900 Service Restored", Profile: &ServiceChangeProfile{ProfileName: "threegIq", Version: 7}, Version: 2, MgcID: &MId{DomainName: "pcscf2.ims.example.net"}}),
		})
	}
	cases = append(cases, parseCase{
		// RFC 3525 Annex B.2 says the reason must be quoted. A reason
		// without a text can be read without quotes all the same.
		name:    "reason that is not quoted",
		text:    "MEGACO/2 <agw1.ims.example.net>\nTransaction = 1 { Context = - { ServiceChange = ROOT { Services { Reason = 905, Method = Graceful } } } }\n",
		message: serviceChange(1, &ServiceChangeParm{Method: Graceful, Reason: "905"}),
	})
	parseThenRoundTrip(t, cases)
}

func TestParseRefusesBrokenServiceChange(t *testing.T) {
	req := func(body string) string {
		return "MEGACO/2 <agw1.ims.example.net>\nTransaction = 1 { Context = - { ServiceChange = ROOT " + body + " } }\n"
	}
	rep := func(body string) string {
		return "MEGACO/2 <pcscf1.ims.example.net>\nReply = 1 { Context = - { ServiceChange = ROOT " + body + " } }\n"
	}
	parseErrors(t, []errorCase{
		{"request without descriptor", req(""), "a ServiceChange request needs a Services descriptor"},
		{"empty descriptor", req("{ Services { } }"), "expected a parameter of the Services descriptor"},
		{"unknown method", req(`{ Services { Method = Reboot, Reason = "901" } }`), "unknown or unsupported token"},
		{"not a method", req(`{ Services { Method = Inactive, Reason = "901" } }`), "is not a method"},
		{"reason without code", req(`{ Services { Method = Restart, Reason = "Cold Boot" } }`), "does not start with a reason code"},
		{"method twice", req(`{ Services { Method = Restart, Method = Forced } }`), "Method appears twice"},
		{"time stamp twice", req(`{ Services { 20261120T09300000, 20261120T09300000 } }`), "time stamp appears twice"},
		{"delay", req(`{ Services { Method = Graceful, Reason = "905", Delay = 30 } }`), "Delay in a Services descriptor is not supported"},
		{"address", req(`{ Services { Method = Restart, Reason = "901", ServiceChangeAddress = 2944 } }`), "ServiceChangeAddress in a Services descriptor is not supported"},
		{"extension", req(`{ Services { Method = Restart, Reason = "901", X-vendor = 1 } }`), `"X" in a Services descriptor is not supported`},
		{"profile without version", req(`{ Services { Method = Restart, Reason = "901", Profile = threegIq } }`), "the version of the profile"},
		{"three digit version", req(`{ Services { Method = Restart, Reason = "901", Version = 100 } }`), "more than two digits"},
		{"no reason", req(`{ Services { Method = Restart } }`), "carries both Method and Reason"},
		{"no method", req(`{ Services { Reason = "901 Cold Boot", Version = 2 } }`), "carries both Method and Reason"},
		{"method in a reply", rep(`{ Services { Method = Restart } }`), "carries neither Method nor Reason"},
		{"result and error", rep(`{ Services { Version = 2 }, Error = 406 { } }`), "either a Services descriptor or an Error descriptor"},
		{"Services in an Add", "MEGACO/2 <agw1.ims.example.net>\nTransaction = 1 { Context = 7 { Add = $ { Services { Method = Restart, Reason = \"901\" } } } }\n", "a Services descriptor is not legal in this Add"},
	})
}

func TestMarshalRefusesBrokenServiceChange(t *testing.T) {
	marshalErrors(t, []marshalErrorCase{
		{"request without descriptor", serviceChange(1, nil), "a ServiceChange request needs a Services descriptor"},
		{"no method", serviceChange(1, &ServiceChangeParm{Reason: "901 Cold Boot"}), "is not a method"},
		{"no reason", serviceChange(1, &ServiceChangeParm{Method: Restart}), "does not start with a reason code"},
		{"reason with a quote", serviceChange(1, &ServiceChangeParm{Method: Restart, Reason: `901 "Cold" Boot`}), "cannot be written as a quoted string"},
		{"profile without name", serviceChange(1, &ServiceChangeParm{Method: Restart, Reason: "901", Profile: &ServiceChangeProfile{Version: 7}}), "is not a profile and its version"},
		{"version out of range", serviceChange(1, &ServiceChangeParm{Method: Restart, Reason: "901", Version: 100}), "is not between 1 and 99"},
		{"MgcID without a form", serviceChange(1, &ServiceChangeParm{Method: HandOff, Reason: "903", MgcID: &MId{}}), "an mId is exactly one of"},
		{"empty result", serviceChangeReply(1, &ServiceChangeResParm{}), "a Services descriptor is empty"},
		{"result and error", Message{Version: 2, MId: mgc, Transactions: []Transaction{
			TransactionReply{TransactionID: 1, Actions: []ActionReply{{ContextID: ContextNull, Commands: []CommandReply{{
				Command: ServiceChange, TerminationID: TerminationRoot,
				ServiceChangeResult: &ServiceChangeResParm{Version: 2},
				Error:               &ErrorDescriptor{ErrorCode: 406},
			}}}}},
		}}, "either a Services descriptor or an Error descriptor"},
	})
}
