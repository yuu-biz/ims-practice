package h248

import "testing"

var mediaRoundTrips = []roundTripCase{
	{
		name: "stream with LocalControl and an SDP that leaves choices open",
		message: request(1001, ContextChoose, CommandRequest{
			Command: Add, TerminationID: "ip/1/access/$",
			Media: &MediaDescriptor{MultiStream: []StreamDescriptor{{
				StreamID: 1,
				StreamParms: StreamParms{
					LocalControl: &LocalControlDescriptor{
						StreamMode:   Inactive,
						ReserveValue: new(false),
						PropertyParms: []PropertyParm{
							{Name: "ipdc/realm", Value: Value{Text: "access", Quoted: true}},
							{Name: "gm/saf", Value: Value{Text: "ON"}},
							{Name: "ds/dscp", Value: Value{Text: "2E"}},
						},
					},
					Local: &LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\nb=AS:80\n"},
				},
			}}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1001 {
  Context = $ {
    Add = ip/1/access/$ {
      Media {
        Stream = 1 {
          LocalControl {
            Mode = Inactive,
            ReservedValue = OFF,
            ipdc/realm = "access",
            gm/saf = ON,
            ds/dscp = 2E
          },
          Local {
v=0
c=IN IP4 $
m=audio $ RTP/AVP 0
b=AS:80
}
        }
      }
    }
  }
}
`,
	},
	{
		name: "reply with the address and port the media gateway chose",
		message: reply(1001, 7, CommandReply{
			Command: Add, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{MultiStream: []StreamDescriptor{{
				StreamID: 1,
				StreamParms: StreamParms{
					Local: &LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 192.0.2.20\nm=audio 40000 RTP/AVP 0\nb=AS:80\n"},
				},
			}}},
		}),
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1001 {
  Context = 7 {
    Add = ip/1/access/11 {
      Media {
        Stream = 1 {
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
}
`,
	},
	{
		name: "single stream without a Stream descriptor",
		message: request(1002, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{OneStream: &StreamParms{
				LocalControl: &LocalControlDescriptor{StreamMode: SendRecv},
				Remote:       &LocalRemoteDescriptor{SDP: "v=0\r\nc=IN IP4 198.51.100.7\r\nm=audio 49170 RTP/AVP 0\r\n"},
			}},
		}),
		text: "MEGACO/2 <pcscf1.ims.example.net>\n" +
			"Transaction = 1002 {\n" +
			"  Context = 7 {\n" +
			"    Modify = ip/1/access/11 {\n" +
			"      Media {\n" +
			"        LocalControl {\n" +
			"          Mode = SendReceive\n" +
			"        },\n" +
			"        Remote {\n" +
			"v=0\r\n" +
			"c=IN IP4 198.51.100.7\r\n" +
			"m=audio 49170 RTP/AVP 0\r\n" +
			"}\n" +
			"      }\n" +
			"    }\n" +
			"  }\n" +
			"}\n",
	},
	{
		name: "audio and video streams",
		message: request(1003, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{MultiStream: []StreamDescriptor{
				{StreamID: 1, StreamParms: StreamParms{LocalControl: &LocalControlDescriptor{StreamMode: SendOnly}}},
				{StreamID: 2, StreamParms: StreamParms{LocalControl: &LocalControlDescriptor{StreamMode: RecvOnly, ReserveGroup: new(true)}}},
			}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1003 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Media {
        Stream = 1 {
          LocalControl {
            Mode = SendOnly
          }
        },
        Stream = 2 {
          LocalControl {
            Mode = ReceiveOnly,
            ReservedGroup = ON
          }
        }
      }
    }
  }
}
`,
	},
	{
		name: "TerminationState of ROOT with a property that is a list",
		message: reply(1004, ContextNull, CommandReply{
			Command: AuditValue, TerminationID: TerminationRoot,
			Media: &MediaDescriptor{TerminationState: &TerminationStateDescriptor{
				ServiceState: ServiceStateInService,
				PropertyParms: []PropertyParm{
					{Name: "root/maxNumberOfContexts", Value: Value{Text: "1000"}},
					{Name: "ipra/ar", Sublist: []Value{{Text: "access", Quoted: true}, {Text: "core", Quoted: true}}},
				},
			}},
		}),
		text: `MEGACO/2 <agw1.ims.example.net>
Reply = 1004 {
  Context = - {
    AuditValue = ROOT {
      Media {
        TerminationState {
          ServiceStates = InService,
          root/maxNumberOfContexts = 1000,
          ipra/ar = ["access", "core"]
        }
      }
    }
  }
}
`,
	},
	{
		// RFC 3525 7.1.6: empty Local and Remote descriptors, with
		// the Reserve properties off, delete a stream.
		name: "empty Local and Remote",
		message: request(1005, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{MultiStream: []StreamDescriptor{{
				StreamID: 2,
				StreamParms: StreamParms{
					LocalControl: &LocalControlDescriptor{ReserveValue: new(false), ReserveGroup: new(false)},
					Local:        &LocalRemoteDescriptor{},
					Remote:       &LocalRemoteDescriptor{},
				},
			}}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1005 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Media {
        Stream = 2 {
          LocalControl {
            ReservedValue = OFF,
            ReservedGroup = OFF
          },
          Local {
},
          Remote {
}
        }
      }
    }
  }
}
`,
	},
	{
		name: "SDP with a closing brace",
		message: request(1006, 7, CommandRequest{
			Command: Modify, TerminationID: "ip/1/access/11",
			Media: &MediaDescriptor{OneStream: &StreamParms{
				Remote: &LocalRemoteDescriptor{SDP: "v=0\ns={conference}\nc=IN IP4 198.51.100.7\nm=audio 49170 RTP/AVP 0\n"},
			}},
		}),
		text: `MEGACO/2 <pcscf1.ims.example.net>
Transaction = 1006 {
  Context = 7 {
    Modify = ip/1/access/11 {
      Media {
        Remote {
v=0
s={conference\}
c=IN IP4 198.51.100.7
m=audio 49170 RTP/AVP 0
}
      }
    }
  }
}
`,
	},
}

func TestMediaDescriptor(t *testing.T) {
	roundTrip(t, mediaRoundTrips)
}

func TestParseMediaInOtherSpellings(t *testing.T) {
	message := request(1001, ContextChoose, CommandRequest{
		Command: Add, TerminationID: "ip/1/access/$",
		Media: &MediaDescriptor{
			TerminationState: &TerminationStateDescriptor{ServiceState: ServiceStateOutOfService},
			MultiStream: []StreamDescriptor{{
				StreamID: 1,
				StreamParms: StreamParms{
					LocalControl: &LocalControlDescriptor{
						StreamMode:    Inactive,
						ReserveValue:  new(true),
						PropertyParms: []PropertyParm{{Name: "ipdc/realm", Value: Value{Text: "access", Quoted: true}}},
					},
					Local: &LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n"},
				},
			}},
		},
	})
	parseThenRoundTrip(t, []parseCase{
		{
			name: "short tokens",
			text: "!/2 <pcscf1.ims.example.net>\n" +
				"T=1001{C=${A=ip/1/access/${M{TS{SI=OS},ST=1{O{MO=IN,RV=on,ipdc/realm=\"access\"},L{\n" +
				"v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n" +
				"}}}}}}",
			message: message,
		},
		{
			name: "closing brace of the SDP on an indented line",
			text: "MEGACO/2 <pcscf1.ims.example.net>\n" +
				"Transaction = 1001 { Context = $ { Add = ip/1/access/$ { Media {\n" +
				"  TerminationState { ServiceStates = OutOfService },\n" +
				"  Stream = 1 {\n" +
				"    LocalControl { Mode = Inactive, ReservedValue = ON, ipdc/realm = \"access\" },\n" +
				"    Local {\n" +
				"      v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n" +
				"    }\n" +
				"  }\n" +
				"} } } }\n",
			message: message,
		},
	})
}

func TestParseRefusesBrokenMedia(t *testing.T) {
	media := func(body string) string {
		return "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 1 { Context = 7 { Modify = ip/1/access/11 { Media { " + body + " } } } }\n"
	}
	parseErrors(t, []errorCase{
		{"empty", media(""), "expected a token"},
		{"with and without Stream", media("LocalControl { Mode = Inactive }, Stream = 2 { LocalControl { Mode = Inactive } }"), "not both"},
		{"LocalControl twice", media("LocalControl { Mode = Inactive }, LocalControl { Mode = Inactive }"), "appears twice in a stream"},
		{"TerminationState twice", media("TerminationState { ServiceStates = Test }, TerminationState { ServiceStates = Test }"), "appears twice"},
		{"Mode twice", media("LocalControl { Mode = Inactive, Mode = SendOnly }"), "Mode appears twice"},
		{"unknown mode", media("LocalControl { Mode = Duplex }"), "unknown or unsupported token"},
		{"not a mode", media("LocalControl { Mode = Local }"), "Local is not a stream mode"},
		{"not ON or OFF", media("LocalControl { ReservedValue = true }"), "expected ON or OFF"},
		{"not a service state", media("TerminationState { ServiceStates = Inactive }"), "is not a service state"},
		{"EventBufferControl", media("TerminationState { Buffer = OFF }"), `"Buffer" cannot stand in a TerminationState descriptor`},
		{"stream ID too large", media("Stream = 65536 { LocalControl { Mode = Inactive } }"), "does not fit in 16 bits"},
		{"other descriptor in Media", media("Error = 500 { }"), "Error cannot stand in a Media descriptor"},
		{"property without value", media("LocalControl { ipdc/realm }"), "expected '='"},
		{"alternative values", media("LocalControl { tman/pdr = { 100, 200 } }"), "alternative values is not supported"},
		{"range of values", media("LocalControl { tman/pdr = [100:200] }"), "range of values is not supported"},
		{"relation", media("LocalControl { tman/pdr < 200 }"), "relation '<' is not supported"},
		{"broken package item", media("LocalControl { ipdc/ = 1 }"), "is not the name of a package item"},
		{"open SDP", "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 1 { Context = 7 { Modify = ip/1/access/11 { Media { Local {\nv=0\n", "is not closed"},
		{"Media in a Subtract request", "MEGACO/2 <pcscf1.ims.example.net>\nTransaction = 1 { Context = 7 { Subtract = * { Media { LocalControl { Mode = Inactive } } } } }\n", "a Media descriptor is not legal in this Subtract"},
	})
}

func TestMarshalRefusesBrokenMedia(t *testing.T) {
	modify := func(m *MediaDescriptor) Message {
		return request(1, 7, CommandRequest{Command: Modify, TerminationID: "ip/1/access/11", Media: m})
	}
	inactive := &LocalControlDescriptor{StreamMode: Inactive}
	marshalErrors(t, []marshalErrorCase{
		{"empty Media", modify(&MediaDescriptor{}), "a Media descriptor is empty"},
		{"empty stream", modify(&MediaDescriptor{OneStream: &StreamParms{}}), "a stream has none of"},
		{"with and without Stream", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: inactive}, MultiStream: []StreamDescriptor{{StreamID: 2, StreamParms: StreamParms{LocalControl: inactive}}}}), "not both"},
		{"empty LocalControl", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{}}}), "a LocalControl descriptor is empty"},
		{"unknown mode", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{StreamMode: "Both"}}}), "is not a stream mode"},
		{"unknown service state", modify(&MediaDescriptor{TerminationState: &TerminationStateDescriptor{ServiceState: "Up"}}), "is not a service state"},
		{"string that is not quoted", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{PropertyParms: []PropertyParm{{Name: "ipdc/realm", Value: Value{Text: "access net"}}}}}}), "has to be quoted"},
		{"property without value", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{PropertyParms: []PropertyParm{{Name: "ipdc/realm"}}}}}), "cannot be empty"},
		{"value and sub-list", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{PropertyParms: []PropertyParm{{Name: "ipra/ar", Value: Value{Text: "a"}, Sublist: []Value{{Text: "b"}}}}}}}), "both a value and a sub-list"},
		{"property name without package", modify(&MediaDescriptor{OneStream: &StreamParms{LocalControl: &LocalControlDescriptor{PropertyParms: []PropertyParm{{Name: "realm", Value: Value{Text: "a"}}}}}}), "is not the name of a package item"},
		{"zero octet in the SDP", modify(&MediaDescriptor{OneStream: &StreamParms{Local: &LocalRemoteDescriptor{SDP: "v=0\x00\n"}}}), "zero octet"},
		{"Media in a Notify", request(1, 7, CommandRequest{Command: Notify, TerminationID: "ip/1/access/11", Media: &MediaDescriptor{OneStream: &StreamParms{LocalControl: inactive}}}), "a Media descriptor is not legal in this Notify"},
	})
}
