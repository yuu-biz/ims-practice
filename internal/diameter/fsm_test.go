package diameter

import (
	"reflect"
	"testing"
)

const (
	realm = "ims.mnc001.mcc001.3gppnetwork.org"
	hss   = "hss." + realm
	scscf = "scscf." + realm
)

func TestTransition(t *testing.T) {
	req := MessageIDs{HopByHop: 7, EndToEnd: 9}
	cx := []uint32{ApplicationIDCx}
	initiator := func(p Phase, w WatchdogStatus) State {
		return State{Role: Initiator, Phase: p, Watchdog: w, PeerHost: hss, Reconnect: true, Applications: cx}
	}
	responder := func(p Phase, w WatchdogStatus) State {
		return State{Role: Responder, Phase: p, Watchdog: w, PeerHost: hss, Applications: cx}
	}
	stopped := func(s State) State { s.Reconnect = false; return s }
	request := &outgoing{answer: make(chan answerResult, 1)}
	answer := &outgoing{gen: 1}

	tests := []struct {
		name        string
		state       State
		event       Event
		wantState   State
		wantActions []Action
	}{
		// Initiator: connect, CER, CEA (RFC 6733 5.6).
		{"start connects", NewInitiatorState(hss, cx), Start{},
			initiator(WaitConnAck, WatchdogOkay), []Action{Connect{}}},
		{"Tc expiry connects again", initiator(Closed, WatchdogOkay), Timeout{},
			initiator(WaitConnAck, WatchdogOkay), []Action{Connect{}}},
		{"connection up sends CER", initiator(WaitConnAck, WatchdogOkay), RcvConnAck{},
			initiator(WaitICEA, WatchdogOkay), []Action{SendCER{}, SetTimer{TimerAnswer}}},
		{"connection refused waits Tc", initiator(WaitConnAck, WatchdogOkay), RcvConnNack{},
			initiator(Closed, WatchdogOkay), []Action{SetTimer{TimerReconnect}}},
		{"successful CEA opens", initiator(WaitICEA, WatchdogOkay),
			RcvCEA{ResultCode: DiameterSuccess, OriginHost: hss, ApplicationIDs: cx},
			initiator(Open, WatchdogOkay), []Action{SetTimer{TimerWatchdog}}},
		{"Origin-Host compares case-insensitively", initiator(WaitICEA, WatchdogOkay),
			RcvCEA{ResultCode: DiameterSuccess, OriginHost: "HSS.ims.mnc001.mcc001.3gppnetwork.org", ApplicationIDs: cx},
			initiator(Open, WatchdogOkay), []Action{SetTimer{TimerWatchdog}}},
		{"CEA from a relay opens", initiator(WaitICEA, WatchdogOkay),
			RcvCEA{ResultCode: DiameterSuccess, OriginHost: hss, ApplicationIDs: []uint32{applicationIDRelay}},
			initiator(Open, WatchdogOkay), []Action{SetTimer{TimerWatchdog}}},
		{"failed CEA closes and waits Tc", initiator(WaitICEA, WatchdogOkay),
			RcvCEA{ResultCode: DiameterNoCommonApplication, OriginHost: hss, ApplicationIDs: cx},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"CEA from another host closes", initiator(WaitICEA, WatchdogOkay),
			RcvCEA{ResultCode: DiameterSuccess, OriginHost: "other.example", ApplicationIDs: cx},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"CEA without Cx closes", initiator(WaitICEA, WatchdogOkay),
			RcvCEA{ResultCode: DiameterSuccess, OriginHost: hss, ApplicationIDs: []uint32{4}},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"non-CEA while waiting for CEA closes", initiator(WaitICEA, WatchdogOkay), RcvDWR{Request: req},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"no CEA in time closes", initiator(WaitICEA, WatchdogOkay), Timeout{},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},

		// Responder: R-Conn-CER (RFC 6733 5.6, 5.3).
		{"CER with Cx opens", NewResponderState(cx),
			RConnCER{Request: req, OriginHost: hss, ApplicationIDs: cx},
			responder(Open, WatchdogOkay),
			[]Action{SendCEA{Request: req, ResultCode: DiameterSuccess}, SetTimer{TimerWatchdog}}},
		{"CER without a common application is refused", NewResponderState(cx),
			RConnCER{Request: req, OriginHost: hss, ApplicationIDs: []uint32{4}},
			responder(Closed, WatchdogOkay),
			[]Action{SendCEA{Request: req, ResultCode: DiameterNoCommonApplication}, Disconnect{}, Finish{}}},

		// Watchdog in Open (RFC 3539 Appendix A).
		{"idle Tw sends DWR", initiator(Open, WatchdogOkay), Timeout{},
			initiator(Open, WatchdogPending), []Action{SendDWR{}, SetTimer{TimerWatchdog}}},
		{"DWA clears pending", initiator(Open, WatchdogPending), RcvDWA{},
			initiator(Open, WatchdogOkay), []Action{SetTimer{TimerWatchdog}}},
		{"unanswered DWR becomes suspect", initiator(Open, WatchdogPending), Timeout{},
			initiator(Open, WatchdogSuspect), []Action{SetTimer{TimerWatchdog}}},
		{"suspect connection closes on the next Tw", initiator(Open, WatchdogSuspect), Timeout{},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"DWA fails back from suspect", initiator(Open, WatchdogSuspect), RcvDWA{},
			initiator(Open, WatchdogOkay), []Action{SetTimer{TimerWatchdog}}},
		{"other traffic fails back but leaves the DWR pending", initiator(Open, WatchdogSuspect), RcvMessage{},
			initiator(Open, WatchdogPending), []Action{Process{}, SetTimer{TimerWatchdog}}},

		// Application messages (RFC 6733 5.6 Send-Message and Process).
		{"open connection sends a request", initiator(Open, WatchdogPending), SendMessage{Outgoing: request},
			initiator(Open, WatchdogPending), []Action{SndMessage{Outgoing: request}}},
		{"suspect connection refuses a request", initiator(Open, WatchdogSuspect), SendMessage{Outgoing: request},
			initiator(Open, WatchdogSuspect), []Action{RefuseMessage{Outgoing: request}}},
		{"suspect connection still sends an answer", responder(Open, WatchdogSuspect), SendMessage{Outgoing: answer},
			responder(Open, WatchdogSuspect), []Action{SndMessage{Outgoing: answer}}},
		{"closed connection refuses a request", initiator(Closed, WatchdogOkay), SendMessage{Outgoing: request},
			initiator(Closed, WatchdogOkay), []Action{RefuseMessage{Outgoing: request}}},
		{"closing connection refuses an answer", responder(Closing, WatchdogOkay), SendMessage{Outgoing: answer},
			responder(Closing, WatchdogOkay), []Action{RefuseMessage{Outgoing: answer}}},
		{"DWR is answered", responder(Open, WatchdogOkay), RcvDWR{Request: req},
			responder(Open, WatchdogOkay), []Action{SendDWA{Request: req}, SetTimer{TimerWatchdog}}},
		{"peer disconnect closes and waits Tc", initiator(Open, WatchdogOkay), PeerDisc{},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"responder peer disconnect finishes", responder(Open, WatchdogOkay), PeerDisc{},
			responder(Closed, WatchdogOkay), []Action{Disconnect{}, Finish{}}},

		// Disconnect (RFC 6733 5.4).
		{"stop sends DPR REBOOTING", initiator(Open, WatchdogOkay), Stop{},
			stopped(initiator(Closing, WatchdogOkay)),
			[]Action{SendDPR{Cause: Rebooting}, SetTimer{TimerAnswer}}},
		{"DPA after stop finishes", stopped(initiator(Closing, WatchdogOkay)), RcvDPA{},
			stopped(initiator(Closed, WatchdogOkay)), []Action{Disconnect{}, Finish{}}},
		{"no DPA in time finishes", stopped(initiator(Closing, WatchdogOkay)), Timeout{},
			stopped(initiator(Closed, WatchdogOkay)), []Action{Disconnect{}, Finish{}}},
		{"DPR REBOOTING is answered", initiator(Open, WatchdogOkay), RcvDPR{Request: req, Cause: Rebooting},
			initiator(Closing, WatchdogOkay), []Action{SendDPA{Request: req}, SetTimer{TimerAnswer}}},
		{"after DPR REBOOTING the initiator reconnects", initiator(Closing, WatchdogOkay), PeerDisc{},
			initiator(Closed, WatchdogOkay), []Action{Disconnect{}, SetTimer{TimerReconnect}}},
		{"DPR BUSY is answered and not reconnected", initiator(Open, WatchdogOkay), RcvDPR{Request: req, Cause: Busy},
			stopped(initiator(Closing, WatchdogOkay)), []Action{SendDPA{Request: req}, SetTimer{TimerAnswer}}},
		{"stop while waiting for Tc finishes", initiator(Closed, WatchdogOkay), Stop{},
			stopped(initiator(Closed, WatchdogOkay)), []Action{Finish{}}},
		{"stop while connecting finishes", initiator(WaitConnAck, WatchdogOkay), Stop{},
			stopped(initiator(Closed, WatchdogOkay)), []Action{Finish{}}},
		{"stop while closing waits for the close", initiator(Closing, WatchdogOkay), Stop{},
			stopped(initiator(Closing, WatchdogOkay)), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotState, gotActions := Transition(tt.state, tt.event)
			if !reflect.DeepEqual(gotState, tt.wantState) {
				t.Errorf("state = %+v, want %+v", gotState, tt.wantState)
			}
			if !reflect.DeepEqual(gotActions, tt.wantActions) {
				t.Errorf("actions = %#v, want %#v", gotActions, tt.wantActions)
			}
		})
	}
}

// Each role advertises only what it speaks, so the common
// application depends on both ends: the HSS takes Cx and Sh, the
// IP-SM-GW only Sh.
func TestCommonApplicationIsPerNode(t *testing.T) {
	req := MessageIDs{HopByHop: 7, EndToEnd: 9}
	tests := []struct {
		name  string
		local []uint32
		peer  []uint32
		want  uint32
	}{
		{"HSS takes an AS with Sh", []uint32{ApplicationIDCx, ApplicationIDSh}, []uint32{ApplicationIDSh}, DiameterSuccess},
		{"HSS takes a CSCF with Cx", []uint32{ApplicationIDCx, ApplicationIDSh}, []uint32{ApplicationIDCx}, DiameterSuccess},
		{"Sh-only node refuses Cx", []uint32{ApplicationIDSh}, []uint32{ApplicationIDCx}, DiameterNoCommonApplication},
		{"Cx-only node refuses Sh", []uint32{ApplicationIDCx}, []uint32{ApplicationIDSh}, DiameterNoCommonApplication},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, actions := Transition(NewResponderState(tt.local), RConnCER{Request: req, OriginHost: hss, ApplicationIDs: tt.peer})
			cea, ok := actions[0].(SendCEA)
			if !ok || cea.ResultCode != tt.want {
				t.Errorf("actions = %#v, want a CEA with Result-Code %d", actions, tt.want)
			}
		})
	}
}
