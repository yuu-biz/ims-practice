package h248

import "testing"

var auditRoundTrips = []roundTripCase{
	{
		name: "empty audit to see whether the media gateway answers",
		message: request(1001, ContextNull, CommandRequest{
			Command: AuditValue, TerminationID: TerminationRoot,
			Audit: &AuditDescriptor{},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1001 {
  Context = - {
    AuditValue = ROOT {
      Audit { }
    }
  }
}
`,
	},
	{
		name: "audit of whole descriptors and of single items",
		message: request(1002, ContextNull, CommandRequest{
			Command: AuditValue, TerminationID: TerminationRoot,
			Audit: &AuditDescriptor{
				AuditToken: []AuditToken{MediaToken, EventsToken, SignalsToken, ObservedEventsToken, StatsToken, PackagesToken},
				AuditPropertyToken: []IndAuditParameter{
					{ServiceStates: true},
					{PkgdName: "root/maxNumberOfContexts"},
					{PkgdName: "ipra/*"},
				},
			},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1002 {
  Context = - {
    AuditValue = ROOT {
      Audit {
        Media,
        Events,
        Signals,
        ObservedEvents,
        Statistics,
        Packages,
        Media { TerminationState { ServiceStates } },
        Media { TerminationState { root/maxNumberOfContexts } },
        Media { TerminationState { ipra/* } }
      }
    }
  }
}
`,
	},
	{
		name: "reply with the service state and the packages",
		message: reply(1002, ContextNull, CommandReply{
			Command: AuditValue, TerminationID: TerminationRoot,
			Media: &MediaDescriptor{TerminationState: &TerminationStateDescriptor{ServiceState: ServiceStateInService}},
			Packages: PackagesDescriptor{
				{PackageName: "g", PackageVersion: 1},
				{PackageName: "root", PackageVersion: 2},
				{PackageName: "ipnapt", PackageVersion: 1},
				{PackageName: "hangterm", PackageVersion: 1},
			},
		}),
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1002 {
  Context = - {
    AuditValue = ROOT {
      Media {
        TerminationState {
          ServiceStates = InService
        }
      },
      Packages { g-1, root-2, ipnapt-1, hangterm-1 }
    }
  }
}
`,
	},
	{
		name: "Subtract that asks for no statistics, with one reply for all",
		message: request(1003, 7, CommandRequest{
			WildcardResponse: true, Command: Subtract, TerminationID: TerminationAll,
			Audit: &AuditDescriptor{},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1003 {
  Context = 7 {
    W-Subtract = * {
      Audit { }
    }
  }
}
`,
	},
	{
		name: "Subtract reply with statistics",
		message: reply(1004, 7, CommandReply{
			Command: Subtract, TerminationID: "ip/1/access/11",
			Statistics: StatisticsDescriptor{
				{StatName: "nt/os", StatValue: Value{Text: "240000"}},
				{StatName: "nt/or", StatValue: Value{Text: "239840"}},
				{StatName: "rtp/ps", StatValue: Value{Text: "1500"}},
				{StatName: "rtp/pr"},
			},
		}),
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1004 {
  Context = 7 {
    Subtract = ip/1/access/11 {
      Statistics {
        nt/os = 240000,
        nt/or = 239840,
        rtp/ps = 1500,
        rtp/pr
      }
    }
  }
}
`,
	},
	{
		name: "audit of the capabilities of a termination",
		message: request(1005, 7, CommandRequest{
			Command: AuditCapability, TerminationID: "ip/1/access/11",
			Audit: &AuditDescriptor{AuditToken: []AuditToken{MediaToken}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1005 {
  Context = 7 {
    AuditCapability = ip/1/access/11 {
      Audit {
        Media
      }
    }
  }
}
`,
	},
	{
		name: "reply with the capabilities of a termination",
		message: reply(1005, 7, CommandReply{
			Command: AuditCapability, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{OneStream: &StreamParms{
				LocalControl: &LocalControlDescriptor{PropertyParms: []PropertyParm{
					{Name: "ds/dscp", Value: Value{Text: "2E"}},
				}},
			}},
		}),
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1005 {
  Context = 7 {
    AuditCapability = ip/1/access/11 {
      Media {
        LocalControl {
          ds/dscp = 2E
        }
      }
    }
  }
}
`,
	},
	{
		name: "Modify that asks for the Media descriptor back",
		message: request(1006, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{StreamMode: SendRecv}}},
			Audit: &AuditDescriptor{AuditToken: []AuditToken{MediaToken}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1006 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Media {
        LocalControl {
          Mode = SendReceive
        }
      },
      Audit {
        Media
      }
    }
  }
}
`,
	},
}

func TestAuditPackagesAndStatisticsDescriptors(t *testing.T) {
	roundTrip(t, auditRoundTrips)
}

func TestParseAuditInOtherSpellings(t *testing.T) {
	parseThenRoundTrip(t, []parseCase{
		{
			name: "short tokens in a request",
			text: "!/2 <pcscf1.ims.example.net> T=1{C=-{AV=ROOT{AT{M,E,SG,OE,SA,PG,M{TS{SI}},M{TS{root/*}}}}}}",
			message: request(1, ContextNull, CommandRequest{
				Command: AuditValue, TerminationID: TerminationRoot,
				Audit: &AuditDescriptor{
					AuditToken:         []AuditToken{MediaToken, EventsToken, SignalsToken, ObservedEventsToken, StatsToken, PackagesToken},
					AuditPropertyToken: []IndAuditParameter{{ServiceStates: true}, {PkgdName: "root/*"}},
				},
			}),
		},
		{
			name: "short tokens in a reply",
			text: "!/2 <agw1.ims.example.net> P=1{C=7{S=ip/1/access/11{SA{rtp/ps=1500,rtp/pr},PG{g-1}}}}",
			message: reply(1, 7, CommandReply{
				Command: Subtract, TerminationID: "ip/1/access/11",
				Statistics: StatisticsDescriptor{{StatName: "rtp/ps", StatValue: Value{Text: "1500"}}, {StatName: "rtp/pr"}},
				Packages:   PackagesDescriptor{{PackageName: "g", PackageVersion: 1}},
			}),
		},
		{
			name:    "empty audit without white space",
			text:    "MEGACO/2 <pcscf1.ims.example.net>\nTransaction=1{Context=7{Subtract=*{Audit{}}}}",
			message: request(1, 7, CommandRequest{Command: Subtract, TerminationID: TerminationAll, Audit: &AuditDescriptor{}}),
		},
	})
}

func TestParseRefusesBrokenAudit(t *testing.T) {
	audit := func(body string) string {
		return "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 1 { Context = - { AuditValue = ROOT " + body + " } }\n"
	}
	auditReply := func(body string) string {
		return "MEGACO/2 <agw1.ims.example.net>\nReply = 1 { Context = - { AuditValue = ROOT { " + body + " } } }\n"
	}
	parseErrors(t, []errorCase{
		{"AuditValue without Audit", audit(""), "a AuditValue request needs a Audit descriptor"},
		{"token twice", audit("{ Audit { Media, Media } }"), "Media appears twice"},
		{"descriptor that is not supported", audit("{ Audit { DigitMap } }"), "auditing DigitMap is not supported"},
		{"unknown token", audit("{ Audit { Mux } }"), "unknown or unsupported token"},
		{"single event", audit("{ Audit { Events = 1 { g/cause } } }"), "auditing single items of the Events descriptor is not supported"},
		{"single statistic", audit("{ Audit { Statistics { rtp/ps } } }"), "auditing single items of the Statistics descriptor is not supported"},
		{"single item of LocalControl", audit("{ Audit { Media { LocalControl { Mode } } } }"), "auditing single items of the LocalControl descriptor is not supported"},
		{"EventBufferControl", audit("{ Audit { Media { TerminationState { Buffer } } } }"), `"Buffer" cannot be audited`},
		{"two items in one TerminationState", audit("{ Audit { Media { TerminationState { ServiceStates, root/* } } } }"), "expected '}'"},
		{"list of terminations in the reply", "MEGACO/2 <agw1.ims.example.net>\nReply = 1 { Context = 7 { AuditValue = Context { ip/1/access/11, ip/1/core/12 } } }\n", "unknown or unsupported token"},
		{"Audit in a reply", auditReply("Audit { Media }"), "a Audit descriptor is not legal in this AuditValue"},
		{"package without version", auditReply("Packages { g }"), "the version of the package"},
		{"empty Packages", auditReply("Packages { }"), "expected the name of a package"},
		{"empty Statistics", auditReply("Statistics { }"), "expected the name of a package item"},
		{"Statistics in a request", audit("{ Audit { }, Statistics { rtp/ps } }"), "a Statistics descriptor is not legal in this AuditValue"},
	})
}

func TestMarshalRefusesBrokenAudit(t *testing.T) {
	audit := func(a *AuditDescriptor) Message {
		return request(1, ContextNull, CommandRequest{Command: AuditValue, TerminationID: TerminationRoot, Audit: a})
	}
	marshalErrors(t, []marshalErrorCase{
		{"AuditValue without Audit", audit(nil), "a AuditValue request needs a Audit descriptor"},
		{"unknown token", audit(&AuditDescriptor{AuditToken: []AuditToken{"DigitMap"}}), "cannot be audited"},
		{"token twice", audit(&AuditDescriptor{AuditToken: []AuditToken{MediaToken, MediaToken}}), "Media appears twice"},
		{"item that is both", audit(&AuditDescriptor{AuditPropertyToken: []IndAuditParameter{{ServiceStates: true, PkgdName: "root/*"}}}), "either the service state or a property"},
		{"item that is neither", audit(&AuditDescriptor{AuditPropertyToken: []IndAuditParameter{{}}}), "is not the name of a package item"},
		{"empty Packages", reply(1, ContextNull, CommandReply{Command: AuditValue, TerminationID: TerminationRoot, Packages: PackagesDescriptor{}}), "a Packages descriptor is empty"},
		{"package without name", reply(1, ContextNull, CommandReply{Command: AuditValue, TerminationID: TerminationRoot, Packages: PackagesDescriptor{{PackageVersion: 1}}}), "is not the name of a package"},
		{"empty Statistics", reply(1, 7, CommandReply{Command: Subtract, TerminationID: "ip/1/access/11", Statistics: StatisticsDescriptor{}}), "a Statistics descriptor is empty"},
		{"statistic without package", reply(1, 7, CommandReply{Command: Subtract, TerminationID: "ip/1/access/11", Statistics: StatisticsDescriptor{{StatName: "ps"}}}), "is not the name of a package item"},
		{"Statistics in a Notify reply", reply(1, 7, CommandReply{Command: Notify, TerminationID: "ip/1/access/11", Statistics: StatisticsDescriptor{{StatName: "rtp/ps"}}}), "a Statistics descriptor is not legal in this Notify"},
	})
}
