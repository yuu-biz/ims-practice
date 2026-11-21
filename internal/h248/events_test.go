package h248

import "testing"

var eventsAndSignalsRoundTrips = []roundTripCase{
	{
		name: "events and a signal on a new termination",
		message: request(1001, ContextChoose, CommandRequest{
			Command: Add, TerminationID: "ip/1/access/$",
			Events: &EventsDescriptor{RequestID: 1, EventList: []RequestedEvent{
				{PkgdName: "hangterm/thb", EvParList: []EventParameter{{Name: "timerx", Value: Value{Text: "3600"}}}},
				{PkgdName: "g/cause"},
			}},
			Signals: &SignalsDescriptor{Signals: []Signal{
				{SignalName: "ipnapt/latch", SigParList: []SigParameter{{Name: "napt", Value: Value{Text: "LATCH"}}}},
			}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1001 {
  Context = $ {
    Add = ip/1/access/$ {
      Events = 1 {
        hangterm/thb { timerx = 3600 },
        g/cause
      },
      Signals {
        ipnapt/latch { napt = LATCH }
      }
    }
  }
}
`,
	},
	{
		name: "event on one stream that keeps the signals playing",
		message: request(1002, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Events: &EventsDescriptor{RequestID: 2, EventList: []RequestedEvent{
				{PkgdName: "dd/*", StreamID: 1, KeepActive: true},
				{PkgdName: "g/sc"},
			}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1002 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Events = 2 {
        dd/* { Stream = 1, KeepActive },
        g/sc
      }
    }
  }
}
`,
	},
	{
		name: "announcement with every parameter of a signal, and a list",
		message: request(1003, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Signals: &SignalsDescriptor{
				Signals: []Signal{{
					SignalName:       "an/apf",
					StreamID:         1,
					SigType:          TimeOut,
					Duration:         new(uint16(500)),
					NotifyCompletion: []NotificationReason{OnTimeOut, OnInterruptByEvent, OnInterruptByNewSignalDescr, OtherReason},
					KeepActive:       true,
					SigParList: []SigParameter{
						{Name: "an", Value: Value{Text: "5"}},
						{Name: "noc", Value: Value{Text: "2"}},
					},
				}},
				SeqSigLists: []SeqSigList{{ID: 2, SignalList: []Signal{
					{SignalName: "cg/rt", SigType: Brief},
					{SignalName: "cg/bt", SigType: OnOff},
				}}},
			},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1003 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Signals {
        an/apf { Stream = 1, SignalType = TimeOut, Duration = 500, NotifyCompletion = { TimeOut, IntByEvent, IntBySigDescr, OtherReason }, KeepActive, an = 5, noc = 2 },
        SignalList = 2 {
          cg/rt { SignalType = Brief },
          cg/bt { SignalType = OnOff }
        }
      }
    }
  }
}
`,
	},
	{
		// RFC 3525 7.1.9 and 7.1.11: a descriptor without items
		// replaces the one before and so stops events and signals.
		name: "stop every event and signal",
		message: request(1004, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Events:  &EventsDescriptor{},
			Signals: &SignalsDescriptor{},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1004 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Events,
      Signals { }
    }
  }
}
`,
	},
	{
		name: "Notify with an event and its cause",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionRequest{TransactionID: 2001, Actions: []ActionRequest{{ContextID: 7, Commands: []CommandRequest{{
				Command: Notify, TerminationID: "ip/1/access/11",
				ObservedEvents: &ObservedEventsDescriptor{RequestID: 1, ObservedEventList: []ObservedEvent{
					{EventName: "g/cause", EventParList: []EventParameter{{Name: "Generalcause", Value: Value{Text: "NR"}}}},
				}},
			}}}}},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Transaction = 2001 {
  Context = 7 {
    Notify = ip/1/access/11 {
      ObservedEvents = 1 {
        g/cause { Generalcause = NR }
      }
    }
  }
}
`,
	},
	{
		name: "Notify with time stamps, a stream and a list",
		message: Message{Version: 2, MId: mg, Transactions: []Transaction{
			TransactionRequest{TransactionID: 2002, Actions: []ActionRequest{{ContextID: 7, Commands: []CommandRequest{{
				Command: Notify, TerminationID: "ip/1/access/11",
				ObservedEvents: &ObservedEventsDescriptor{RequestID: 2, ObservedEventList: []ObservedEvent{
					{EventName: "dd/d5", StreamID: 1, TimeNotation: TimeNotation{Date: "20261120", Time: "09301500"}},
					{EventName: "g/sc", TimeNotation: TimeNotation{Date: "20261120", Time: "09301725"}, EventParList: []EventParameter{
						{Name: "SigID", Value: Value{Text: "an/apf"}},
						{Name: "Meth", Value: Value{Text: "TO"}},
					}},
					{EventName: "ipra/arc", EventParList: []EventParameter{
						{Name: "nar", Sublist: []Value{{Text: "access", Quoted: true}}},
					}},
				}},
			}}}}},
		}},
		text: `MEGACO/2 <agw1.ims.example.net>
Transaction = 2002 {
  Context = 7 {
    Notify = ip/1/access/11 {
      ObservedEvents = 2 {
        20261120T09301500: dd/d5 { Stream = 1 },
        20261120T09301725: g/sc { SigID = an/apf, Meth = TO },
        ipra/arc { nar = ["access"] }
      }
    }
  }
}
`,
	},
}

func TestEventsAndSignalsDescriptors(t *testing.T) {
	roundTrip(t, eventsAndSignalsRoundTrips)
}

func TestParseEventsAndSignalsInOtherSpellings(t *testing.T) {
	parseThenRoundTrip(t, []parseCase{
		{
			name: "short tokens",
			text: "!/2 <pcscf1.ims.example.net>\n" +
				"T=1{C=7{MF=ip/1/access/11{E=2{dd/*{ST=1,KA},g/sc},SG{an/apf{ST=1,SY=TO,DR=500,NC={TO,IBE,IBS,OR},KA,an=5},SL=2{cg/rt{SY=BR},cg/bt{SY=OO}}}}}}",
			message: request(1, 7, CommandRequest{
				Command: Modify, TerminationID: "ip/1/access/11",
				Events: &EventsDescriptor{RequestID: 2, EventList: []RequestedEvent{
					{PkgdName: "dd/*", StreamID: 1, KeepActive: true},
					{PkgdName: "g/sc"},
				}},
				Signals: &SignalsDescriptor{
					Signals: []Signal{{
						SignalName: "an/apf", StreamID: 1, SigType: TimeOut, Duration: new(uint16(500)),
						NotifyCompletion: []NotificationReason{OnTimeOut, OnInterruptByEvent, OnInterruptByNewSignalDescr, OtherReason},
						KeepActive:       true,
						SigParList:       []SigParameter{{Name: "an", Value: Value{Text: "5"}}},
					}},
					SeqSigLists: []SeqSigList{{ID: 2, SignalList: []Signal{
						{SignalName: "cg/rt", SigType: Brief},
						{SignalName: "cg/bt", SigType: OnOff},
					}}},
				},
			}),
		},
		{
			// The names of parameters are not tokens: "T" and "C" here
			// are parameters, not Transaction and Context.
			name: "parameters that are spelled like tokens of other places",
			text: "MEGACO/2 <agw1.ims.example.net>\n" +
				"Transaction = 2 { Context = 7 { Notify = ip/1/access/11 {\n" +
				"  ObservedEvents = 1 { 20261120t09301500 : xpkg/ev { T = 1, C = \"two words\" } }\n" +
				"} } }\n",
			message: Message{Version: 2, MId: mg, Transactions: []Transaction{
				TransactionRequest{TransactionID: 2, Actions: []ActionRequest{{ContextID: 7, Commands: []CommandRequest{{
					Command: Notify, TerminationID: "ip/1/access/11",
					ObservedEvents: &ObservedEventsDescriptor{RequestID: 1, ObservedEventList: []ObservedEvent{{
						EventName:    "xpkg/ev",
						TimeNotation: TimeNotation{Date: "20261120", Time: "09301500"},
						EventParList: []EventParameter{
							{Name: "T", Value: Value{Text: "1"}},
							{Name: "C", Value: Value{Text: "two words", Quoted: true}},
						},
					}}},
				}}}}},
			}},
		},
		{
			// "Stream = 0" says the same as leaving the stream out.
			name: "stream zero",
			text: "MEGACO/2 <pcscf1.ims.example.net>\n" +
				"Transaction = 1 { Context = 7 { Modify = ip/1/access/11 { Events = 1 { g/cause { Stream = 0 } }, Signals { ipnapt/latch { Stream = 0 } } } } }\n",
			message: request(1, 7, CommandRequest{
				Command: Modify, TerminationID: "ip/1/access/11",
				Events:  &EventsDescriptor{RequestID: 1, EventList: []RequestedEvent{{PkgdName: "g/cause"}}},
				Signals: &SignalsDescriptor{Signals: []Signal{{SignalName: "ipnapt/latch"}}},
			}),
		},
		{
			// H.248.1 version 2 Corrigendum 1, Annex B.2: the braces of
			// signalsDescriptor are optional. A bare Signals token stops
			// every signal, like "Signals { }".
			name: "Signals without braces",
			text: "MEGACO/2 <pcscf1.ims.example.net>\n" +
				"Transaction = 1 { Context = 7 { Modify = ip/1/access/11 { Signals, Events } } }\n",
			message: request(1, 7, CommandRequest{
				Command: Modify, TerminationID: "ip/1/access/11",
				Events:  &EventsDescriptor{},
				Signals: &SignalsDescriptor{},
			}),
		},
	})
}

func TestParseRefusesBrokenEventsAndSignals(t *testing.T) {
	modify := func(body string) string {
		return "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 1 { Context = 7 { Modify = ip/1/access/11 { " + body + " } } }\n"
	}
	notify := func(body string) string {
		return "MEGACO/2 <agw1.ims.example.net>\nTransaction = 1 { Context = 7 { Notify = ip/1/access/11 " + body + " } }\n"
	}
	parseErrors(t, []errorCase{
		{"request ID without events", modify("Events = 1 { }"), "expected the name of a package item"},
		{"event without package", modify("Events = 1 { cause }"), "expected the name of a package item"},
		{"stream twice", modify("Events = 1 { g/cause { Stream = 1, Stream = 2 } }"), "Stream appears twice"},
		{"embedded descriptor", modify("Events = 1 { al/of { Embed { Signals { cg/dt } } } }"), "Embed in an event is not supported"},
		{"digit map", modify("Events = 1 { dd/ce { DigitMap = dialplan } }"), "DigitMap in an event is not supported"},
		{"event parameter without value", modify("Events = 1 { hangterm/thb { timerx } }"), "expected '='"},
		{"wildcard signal", modify("Signals { cg/* }"), "is a wildcard"},
		{"unknown item in Signals", modify("Signals { Stream = 1 }"), `"Stream" cannot stand in a Signals descriptor`},
		{"not a signal type", modify("Signals { cg/rt { SignalType = Inactive } }"), "is not a signal type"},
		{"duration too long", modify("Signals { cg/rt { Duration = 70000 } }"), "does not fit in 16 bits"},
		{"duration twice", modify("Signals { cg/rt { Duration = 1, Duration = 2 } }"), "Duration appears twice"},
		{"not a reason", modify("Signals { cg/rt { NotifyCompletion = { Brief } } }"), "is not a reason"},
		{"empty signal list", modify("Signals { SignalList = 1 { } }"), "expected the name of a package item"},
		{"ObservedEvents in a Modify", modify("ObservedEvents = 1 { g/cause }"), "a ObservedEvents descriptor is not legal in this Modify"},
		{"Notify without ObservedEvents", notify(""), "a Notify request needs a ObservedEvents descriptor"},
		{"Notify with Events", notify("{ Events = 1 { g/cause } }"), "a Events descriptor is not legal in this Notify"},
		{"ObservedEvents without events", notify("{ ObservedEvents = 1 { } }"), "expected the name of a package item"},
		{"ObservedEvents without request ID", notify("{ ObservedEvents { g/cause } }"), "expected '='"},
		{"short time stamp", notify("{ ObservedEvents = 1 { 20261120T0930: g/cause } }"), "a time stamp is"},
		{"time stamp without colon", notify("{ ObservedEvents = 1 { 20261120T09301500 g/cause } }"), "expected ':'"},
	})
}

func TestMarshalRefusesBrokenEventsAndSignals(t *testing.T) {
	modify := func(c CommandRequest) Message {
		c.Command, c.TerminationID = Modify, "ip/1/access/11"
		return request(1, 7, c)
	}
	notify := func(o *ObservedEventsDescriptor) Message {
		return request(1, 7, CommandRequest{Command: Notify, TerminationID: "ip/1/access/11", ObservedEvents: o})
	}
	marshalErrors(t, []marshalErrorCase{
		{"request ID without events", modify(CommandRequest{Events: &EventsDescriptor{RequestID: 1}}), "without events has no request ID"},
		{"event without package", modify(CommandRequest{Events: &EventsDescriptor{RequestID: 1, EventList: []RequestedEvent{{PkgdName: "cause"}}}}), "is not the name of a package item"},
		{"event parameter with a bad name", modify(CommandRequest{Events: &EventsDescriptor{RequestID: 1, EventList: []RequestedEvent{{PkgdName: "g/cause", EvParList: []EventParameter{{Name: "a b", Value: Value{Text: "1"}}}}}}}), "is not the name of a parameter"},
		{"wildcard signal", modify(CommandRequest{Signals: &SignalsDescriptor{Signals: []Signal{{SignalName: "cg/*"}}}}), "is a wildcard"},
		{"unknown signal type", modify(CommandRequest{Signals: &SignalsDescriptor{Signals: []Signal{{SignalName: "cg/rt", SigType: "Long"}}}}), "is not a signal type"},
		{"unknown reason", modify(CommandRequest{Signals: &SignalsDescriptor{Signals: []Signal{{SignalName: "cg/rt", NotifyCompletion: []NotificationReason{"Done"}}}}}), "is not a reason"},
		{"no reason", modify(CommandRequest{Signals: &SignalsDescriptor{Signals: []Signal{{SignalName: "cg/rt", NotifyCompletion: []NotificationReason{}}}}}), "lists no reason"},
		{"empty signal list", modify(CommandRequest{Signals: &SignalsDescriptor{SeqSigLists: []SeqSigList{{ID: 1}}}}), "signal list 1 is empty"},
		{"signal parameter with a bad name", modify(CommandRequest{Signals: &SignalsDescriptor{Signals: []Signal{{SignalName: "cg/rt", SigParList: []SigParameter{{Name: "1st", Value: Value{Text: "1"}}}}}}}), "is not the name of a parameter"},
		{"Notify without ObservedEvents", notify(nil), "a Notify request needs a ObservedEvents descriptor"},
		{"ObservedEvents without events", notify(&ObservedEventsDescriptor{RequestID: 1}), "reports no event"},
		{"broken time stamp", notify(&ObservedEventsDescriptor{RequestID: 1, ObservedEventList: []ObservedEvent{{EventName: "g/cause", TimeNotation: TimeNotation{Date: "2026-11-20", Time: "09301500"}}}}), "is not yyyymmdd"},
	})
}
