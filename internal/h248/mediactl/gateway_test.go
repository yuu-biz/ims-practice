package mediactl

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/h248test"
)

// send sends a request to the gateway from the controller's Endpoint,
// past the checks of Controller.
func (b *testbed) send(t *testing.T, actions ...h248.ActionRequest) h248.TransactionReply {
	t.Helper()
	reply, err := b.controller.endpoint.Request(context.Background(), h248test.Addr(gatewayAddr), actions)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func errorCode(reply h248.TransactionReply) int {
	var rejected *CommandRejected
	if errors.As(rejection(reply), &rejected) {
		return rejected.ErrorDescriptor.ErrorCode
	}
	return 0
}

// reserveAction is the action of the Controller's Add for an access
// and a core termination, changed by modify.
func reserveAction(c *Controller, modify func(adds []h248.CommandRequest) []h248.CommandRequest) h248.ActionRequest {
	var adds []h248.CommandRequest
	for _, side := range []agw.TerminationID{agw.Access, agw.Core} {
		add, err := c.iqCommand(h248.Add, chooseTerminationID(side), agw.TerminationRequest{ID: side})
		if err != nil {
			panic(err)
		}
		adds = append(adds, add)
	}
	return h248.ActionRequest{ContextID: h248.ContextChoose, Commands: modify(adds)}
}

func modifyAction(id h248.ContextID, t h248.TerminationID, remote string) h248.ActionRequest {
	return h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{{
		Command:       h248.Modify,
		TerminationID: t,
		Media:         &h248.MediaDescriptor{OneStream: &h248.StreamParms{Remote: &h248.LocalRemoteDescriptor{SDP: remote}}},
	}}}
}

const remoteSDP = "v=0\nc=IN IP4 192.0.2.30\nm=audio 49170 RTP/AVP 0\n"

func TestGatewayRefusesWhatItDoesNotSupport(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	reply := b.send(t, reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest { return adds }))
	if code := errorCode(reply); code != 0 {
		t.Fatalf("reserving failed with %d", code)
	}
	reserved := reply.Actions[0].ContextID
	other := b.send(t, reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest { return adds })).Actions[0].ContextID

	tests := []struct {
		name   string
		action h248.ActionRequest
		want   int
	}{
		{"mode Inactive", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Media.OneStream.LocalControl.StreamMode = h248.Inactive
			return adds
		}), 517},
		{"no LocalControl, so Inactive", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[1].Media.OneStream.LocalControl = nil
			return adds
		}), 517},
		{"a property of a package the gateway lacks", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Media.OneStream.LocalControl.PropertyParms = []h248.PropertyParm{{Name: "ipdc/realm", Value: h248.Value{Text: "access", Quoted: true}}}
			return adds
		}), 440},
		{"a specific termination ID in Add", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].TerminationID = "ip/1/access/5"
			return adds
		}), 501},
		{"an interface the gateway lacks", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].TerminationID = "ip/1/wlan/$"
			return adds
		}), 431},
		{"one Add only", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			return adds[:1]
		}), 501},
		{"two access terminations", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[1].TerminationID = adds[0].TerminationID
			return adds
		}), 434},
		{"latching with a napt that H.248.37 does not define", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Signals.Signals[0].SigParList[0].Value.Text = "SOMETIMES"
			return adds
		}), 449},
		{"latching without napt", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Signals.Signals[0].SigParList = nil
			return adds
		}), 457},
		{"an event of a package the gateway lacks", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Events.EventList = append(adds[0].Events.EventList, h248.RequestedEvent{PkgdName: "adid/ipstop"})
			return adds
		}), 440},
		{"an event that g does not have", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Events.EventList = append(adds[0].Events.EventList, h248.RequestedEvent{PkgdName: "g/sc"})
			return adds
		}), 451},
		{"video", reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest {
			adds[0].Media.OneStream.Local.SDP = "v=0\nc=IN IP4 $\nm=video $ RTP/AVP 99\n"
			return adds
		}), 515},
		{"an unknown context", modifyAction(999, "ip/1/access/999", remoteSDP), 411},
		{"a termination of another context", modifyAction(reserved, terminationID(1, agw.Access, other), remoteSDP), 435},
		{"an unknown termination", modifyAction(reserved, "ip/1/wlan/1", remoteSDP), 430},
		{"a Remote without an address", modifyAction(reserved, terminationID(1, agw.Core, reserved), "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n"), 441},
		{"a Remote that is not RTP/AVP", modifyAction(reserved, terminationID(1, agw.Core, reserved), "v=0\nc=IN IP4 192.0.2.30\nm=audio 49170 RTP/SAVP 0\n"), 449},
		{"a third termination", h248.ActionRequest{ContextID: reserved, Commands: []h248.CommandRequest{{
			Command: h248.Add, TerminationID: chooseTerminationID(agw.Access),
		}}}, 434},
		{"Subtract of one termination", h248.ActionRequest{ContextID: reserved, Commands: []h248.CommandRequest{{
			Command: h248.Subtract, TerminationID: terminationID(1, agw.Access, reserved),
		}}}, 501},
		{"an ephemeral termination in the NULL context", h248.ActionRequest{ContextID: h248.ContextNull, Commands: []h248.CommandRequest{{
			Command: h248.AuditValue, TerminationID: terminationID(1, agw.Access, reserved), Audit: &h248.AuditDescriptor{},
		}}}, 430},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorCode(b.send(t, tt.action)); got != tt.want {
				t.Errorf("error %d, want %d", got, tt.want)
			}
		})
	}
	if reserved, _, open := b.relay.counts(); reserved != 2 || open != 2 {
		t.Errorf("the relay reserved %d contexts and holds %d, want 2 and 2", reserved, open)
	}
}

func TestGatewayAuditsRoot(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	reply := b.send(t, h248.ActionRequest{ContextID: h248.ContextNull, Commands: []h248.CommandRequest{{
		Command:       h248.AuditValue,
		TerminationID: h248.TerminationRoot,
		Audit: &h248.AuditDescriptor{
			AuditToken:         []h248.AuditToken{h248.PackagesToken},
			AuditPropertyToken: []h248.IndAuditParameter{{ServiceStates: true}},
		},
	}}})
	c := reply.Actions[0].Commands[0]
	if c.Error != nil || !slices.Equal(c.Packages, Packages) || c.Media.TerminationState.ServiceState != h248.ServiceStateInService {
		t.Errorf("reply %+v", c)
	}
}

func TestGatewayRefusesCommandsBeforeItHasRegistered(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	// The gateway waits up to MaxWaitingDelay before it registers.
	reply := b.send(t, reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest { return adds }))
	if code := errorCode(reply); code != 505 {
		t.Errorf("error %d, want 505", code)
	}
	if !errors.Is(rejection(reply), ErrUnavailable) {
		t.Errorf("%v is not ErrUnavailable", rejection(reply))
	}
}

func TestRelayErrors(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	b.relay.setFail(agw.ErrNoPort)
	_, err := reserve(ctx, b.controller, agw.Media{})
	var rejected *CommandRejected
	if !errors.Is(err, agw.ErrNoPort) || !errors.As(err, &rejected) || rejected.ErrorDescriptor.ErrorCode != 510 {
		t.Errorf("Add: %v", err)
	}
	b.relay.setFail(errors.New("the relay broke"))
	if _, err := reserve(ctx, b.controller, agw.Media{}); !errors.As(err, &rejected) || rejected.ErrorDescriptor.ErrorCode != 500 {
		t.Errorf("Add: %v", err)
	}
	b.relay.setFail(nil)
	if err := b.controller.Modify(ctx, "4711@agw1.ims.example.net", agw.Access, agw.TerminationRequest{Remote: mustAddrPort("192.0.2.30:49170")}); !errors.Is(err, agw.ErrNoContext) {
		t.Errorf("Modify of an unknown context: %v", err)
	}
	if err := b.controller.Modify(ctx, "4711@agw9.ims.example.net", agw.Access, agw.TerminationRequest{Remote: mustAddrPort("192.0.2.30:49170")}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Modify on an unknown gateway: %v", err)
	}
	if err := b.controller.Modify(ctx, "not a context", agw.Access, agw.TerminationRequest{Remote: mustAddrPort("192.0.2.30:49170")}); err == nil {
		t.Error("Modify of an ID that is not one")
	}
}

func TestGatewayLeavesUndoneWhatComesAfterAReplyThatDoesNotFit(t *testing.T) {
	// The gateway sends messages of up to 600 octets, so that the
	// replies to six audits of ROOT, each with the list of packages, do
	// not fit in one, though the request does.
	b := newTestbed(t, ControllerConfig{}, func(c *GatewayConfig) { c.Endpoint.MaxMessageSize = 600 })
	b.waitRegistered(t)
	audit := h248.CommandRequest{Command: h248.AuditValue, TerminationID: h248.TerminationRoot, Audit: &h248.AuditDescriptor{
		AuditToken: []h248.AuditToken{h248.PackagesToken},
	}}
	root := h248.ActionRequest{ContextID: h248.ContextNull}
	for range 6 {
		root.Commands = append(root.Commands, audit)
	}
	reserve := reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest { return adds })
	reply := b.send(t, root, reserve)

	// H.248.8 (03/2013) 4.2.66: the audit whose reply would overflow
	// fails with 533, and nothing after it is carried out.
	if len(reply.Actions) != 1 {
		t.Fatalf("reply %+v, want the reply to the first action only", reply)
	}
	commands := reply.Actions[0].Commands
	last := commands[len(commands)-1]
	if len(commands) == len(root.Commands) || last.Error == nil || last.Error.ErrorCode != 533 {
		t.Errorf("replies %+v, want error 533 before the last audit", commands)
	}
	if reserved, _, _ := b.relay.counts(); reserved != 0 {
		t.Errorf("the relay reserved %d contexts after the reply that did not fit", reserved)
	}
}

// slowRelay takes until the end of its context to configure a
// termination.
type slowRelay struct{ *fakeRelay }

func (r slowRelay) Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestGatewayAnswers506ForTheCommandThatTookTooLong(t *testing.T) {
	b := newTestbed(t, ControllerConfig{}, func(c *GatewayConfig) {
		c.Relay = slowRelay{c.Relay.(*fakeRelay)}
		c.Endpoint.Timers.PendingLimit = 2
	})
	b.waitRegistered(t)
	reserved := b.send(t, reserveAction(b.controller, func(adds []h248.CommandRequest) []h248.CommandRequest { return adds })).Actions[0].ContextID

	done := make(chan h248.TransactionReply, 1)
	go func() {
		done <- b.send(t, h248.ActionRequest{ContextID: reserved, Commands: []h248.CommandRequest{
			modifyAction(reserved, terminationID(1, agw.Access, reserved), remoteSDP).Commands[0],
			modifyAction(reserved, terminationID(1, agw.Core, reserved), remoteSDP).Commands[0],
		}})
	}()
	var reply h248.TransactionReply
	b.until(t, "the reply", 100*time.Millisecond, func() bool {
		select {
		case reply = <-done:
			return true
		default:
			return false
		}
	})

	// H.248.1 (05/2002) 8.2.3: error 506 stands in the reply to the
	// Modify that took too long, and the Modify after it is not
	// carried out.
	commands := reply.Actions[0].Commands
	if len(reply.Actions) != 1 || len(commands) != 1 || commands[0].TerminationID != terminationID(1, agw.Access, reserved) ||
		commands[0].Error == nil || commands[0].Error.ErrorCode != 506 {
		t.Errorf("reply %+v, want error 506 for the first Modify only", reply)
	}
}
