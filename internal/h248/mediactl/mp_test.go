package mediactl

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/h248test"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// waitMessages waits until the datagrams from before on hold every line
// of want, as summaries writes them, and returns the summaries. The
// goroutines of the gateway and the controller write some messages in
// an order that the code does not fix, so the wait is for all of them.
func waitMessages(t *testing.T, b *mpTestbed, before int, want ...string) []string {
	t.Helper()
	var got []string
	h248test.Eventually(t, "the messages "+strings.Join(want, "; "), func() bool {
		got = summaries(t, b.net.Datagrams()[before:])
		for _, w := range want {
			n := 0
			for _, line := range want {
				if line == w {
					n++
				}
			}
			if count := countOf(got, w); count < n {
				return false
			}
		}
		return true
	})
	return got
}

// pass moves the clock on by d in steps of a second, as until does.
// A single step of more than the keep-alive interval would run the
// timers of the Endpoints ahead of the goroutines that answer them.
func (b *mpTestbed) pass(t *testing.T, d time.Duration) {
	t.Helper()
	end := b.clock.Now().Add(d)
	b.until(t, "the clock to pass "+d.String(), time.Second, func() bool { return !b.clock.Now().Before(end) })
}

func countOf(lines []string, line string) int {
	n := 0
	for _, l := range lines {
		if l == line {
			n++
		}
	}
	return n
}

// payloadOf returns the text of the first datagram from from that holds
// every one of parts.
func payloadOf(t *testing.T, ds []h248test.Datagram, from string, parts ...string) string {
	t.Helper()
	for _, d := range ds {
		if d.From != from {
			continue
		}
		text := string(d.Payload)
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(text, p)
		}
		if ok {
			return text
		}
	}
	t.Fatalf("no message from %s with %q", from, parts)
	return ""
}

func TestMpRegistration(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	register := firstRequest(t, b.net.Datagrams(), h248.ServiceChange)
	if p := register.Actions[0].Commands[0].ServiceChange.Profile; p == nil || *p != MpProfile {
		t.Errorf("profile %+v, want %+v", p, MpProfile)
	}
	reply := b.send(t, h248.ActionRequest{ContextID: h248.ContextNull, Commands: []h248.CommandRequest{{
		Command:       h248.AuditValue,
		TerminationID: h248.TerminationRoot,
		Audit:         &h248.AuditDescriptor{AuditToken: []h248.AuditToken{h248.PackagesToken}},
	}}})
	if c := reply.Actions[0].Commands[0]; c.Error != nil || !slices.Equal(c.Packages, MpPackages) {
		t.Errorf("packages %+v", c)
	}
}

func TestIqControllerRefusesAnMRFP(t *testing.T) {
	clock := h248test.NewClock()
	b := &testbed{clock: clock, net: h248test.NewNetwork(clock)}
	b.controller = b.startController(t, ControllerConfig{}, 1)
	g, err := NewGateway(b.net.Listen(gatewayAddr), GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: mrfpMId, Clock: clock, FirstTransactionID: 5001, Timers: testTimers},
		LocateController: func() (net.Addr, error) { return h248test.Addr(controllerAddr), nil },
		Processor:        newFakeProcessor(),
		MaxWaitingDelay:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	go g.Serve()
	t.Cleanup(func() { g.Close() })
	b.gateway = g
	// H.248.1 (05/2002) 7.2.8: the reply names the profile that the
	// controller supports instead.
	b.until(t, "the refusal", 50*time.Millisecond, func() bool { return sent(b.net.Datagrams(), "imsPracticeIq/1") })
	g.mu.Lock()
	registered := g.registered
	g.mu.Unlock()
	if registered || len(*b.controller.available.Load()) != 0 {
		t.Error("an Iq controller took an MRFP")
	}
}

// onlyTermination is the one termination of a context that Add made.
func onlyTermination(t *testing.T, cx agw.Context) *agw.Termination {
	t.Helper()
	if len(cx.Terminations) != 1 {
		t.Fatalf("context %+v, want one termination", cx)
	}
	for _, only := range cx.Terminations {
		return only
	}
	return nil
}

func TestMpAnnouncement(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	ctx := context.Background()

	// The AS asks the MRFC for an announcement; the INVITE offers the
	// UE's PCMU stream, which only listens.
	ue := mustAddrPort("192.0.2.30:49170")
	pcmuOnly := agw.Media{Codecs: []sdp.Codec{pcmu}}
	cx, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly, LocalMedia: pcmuOnly, Remote: ue, RemoteMedia: pcmuOnly})
	if err != nil {
		t.Fatal(err)
	}
	if cx.ID != "200@mrfp1.ims.example.net" || len(cx.Terminations) != 1 || cx.Terminations["ip/200/1"] == nil ||
		*cx.Terminations["ip/200/1"] != (agw.Termination{ID: "ip/200/1", Mode: agw.SendOnly, Local: mustAddrPort("198.51.100.20:42000"), Remote: ue}) {
		t.Fatalf("context %+v", cx)
	}
	tid := agw.TerminationID("ip/200/1")
	processorContext, terms := b.processor.only(t)
	if got := terms["t1"]; got.mode != agw.SendOnly || got.remote != ue || len(got.media.Codecs) != 1 || got.media.Codecs[0] != pcmu {
		t.Errorf("the processor was asked for %+v", got)
	}

	// The ACK has come: play the announcement three times, and report
	// its end.
	err = b.controller.Modify(ctx, cx.ID, tid, agw.TerminationRequest{
		Mode:    agw.SendOnly,
		Signals: &agw.Signals{Play: &agw.Play{Announcement: "notice", Repeat: 3}},
		Events:  &agw.Events{RequestID: 7, Detect: []agw.EventName{agw.SignalCompleted}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := b.processor.termination(processorContext, "t1")
	if got.signals == nil || got.signals.Play == nil || *got.signals.Play != (agw.Play{Announcement: "notice", Repeat: 3}) {
		t.Errorf("the processor plays %+v", got.signals)
	}
	if got.events == nil || got.events.RequestID != 7 || !slices.Equal(got.events.Detect, []agw.EventName{agw.SignalCompleted}) || got.mode != agw.SendOnly {
		t.Errorf("the processor reports %+v in mode %s", got.events, got.mode)
	}

	// The relay has played the third cycle.
	b.pass(t, 3*time.Second)
	detected := b.clock.Now()
	b.gateway.SignalCompleted(processorContext, "t1")
	messages := waitMessages(t, b, before, "mg MEGACO/2 Notify=ip/200/1", "mgc MEGACO/2 Reply")
	h248test.Eventually(t, "the notification", func() bool { return len(b.notified.all()) == 1 })
	n := b.notified.all()[0]
	want := Notification{
		Notify: agw.Notify{Context: cx.ID, Termination: tid, RequestID: 7, Event: agw.SignalCompleted},
		Signal: "an/apf", Method: TimedOut, Detected: detected.Truncate(10 * time.Millisecond),
	}
	if n != want {
		t.Errorf("notification %+v\nwant %+v", n, want)
	}

	// The MRFC ends the dialog with BYE and releases the termination.
	gone, deleted, err := b.controller.SubtractTermination(ctx, cx.ID, tid)
	if err != nil {
		t.Fatal(err)
	}
	if gone != (agw.Termination{ID: tid, Packets: 250, PacketsSent: 250}) || deleted {
		t.Errorf("subtracted %+v, deleted %v: Mp reports the packets only, and not whether the context went", gone, deleted)
	}
	if b.processor.contextCount() != 0 {
		t.Error("the processor still holds the context")
	}
	if err := b.controller.Modify(ctx, cx.ID, tid, agw.TerminationRequest{Mode: agw.SendOnly}); !errors.Is(err, agw.ErrNoContext) {
		t.Errorf("Modify after the last Subtract: %v", err)
	}

	messages = waitMessages(t, b, before, "mgc MEGACO/2 Subtract=ip/200/1", "mg MEGACO/2 Reply", "mg MEGACO/2 Reply", "mg MEGACO/2 Reply")
	wantMessages := []string{
		"mgc MEGACO/2 Add=$",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Modify=ip/200/1",
		"mg MEGACO/2 Reply",
		"mg MEGACO/2 Notify=ip/200/1",
		"mgc MEGACO/2 Reply",
		"mgc MEGACO/2 Subtract=ip/200/1",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Modify=ip/200/1",
		"mg MEGACO/2 Reply error 411",
	}
	if !slices.Equal(messages, wantMessages) {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(messages, "\n"), strings.Join(wantMessages, "\n"))
	}

	ds := b.net.Datagrams()[before:]
	add := payloadOf(t, ds, controllerAddr, "Add = $")
	for _, part := range []string{"Context = $", "Mode = SendOnly", "c=IN IP4 $\nm=audio $ RTP/AVP 0\n", "c=IN IP4 192.0.2.30\nm=audio 49170 RTP/AVP 0\n",
		"Events = 1 {", "hangterm/thb { KeepActive, timerx = 1800 }"} {
		if !strings.Contains(add, part) {
			t.Errorf("the Add has no %q:\n%s", part, add)
		}
	}
	modify := payloadOf(t, ds, controllerAddr, "Modify = ip/200/1")
	for _, part := range []string{"Mode = SendOnly", "Events = 7 {", "hangterm/thb { KeepActive, timerx = 1800 }", "g/sc",
		"an/apf { Duration = 0, NotifyCompletion = { TimeOut }, an = notice, noc = 3 }"} {
		if !strings.Contains(modify, part) {
			t.Errorf("the Modify has no %q:\n%s", part, modify)
		}
	}
	notify := payloadOf(t, ds, gatewayAddr, "Notify = ip/200/1")
	if !strings.Contains(notify, "ObservedEvents = 7 {") || !strings.Contains(notify, `g/sc { SigID = "an/apf", Meth = TO }`) ||
		!strings.Contains(notify, detected.Format("20060102T150405")) {
		t.Errorf("the Notify:\n%s", notify)
	}
	subtracted := payloadOf(t, ds, gatewayAddr, "Subtract = ip/200/1")
	if !strings.Contains(subtracted, "rtp/ps = 250") || !strings.Contains(subtracted, "rtp/pr = 250") || strings.Contains(subtracted, "nt/") {
		t.Errorf("the reply to the Subtract:\n%s", subtracted)
	}
	if modes := modeOnTheWire(t, ds); !slices.Equal(modes, []h248.StreamMode{h248.SendOnly, h248.SendOnly, h248.SendOnly}) {
		t.Errorf("modes %v", modes)
	}
}

func TestMpAnnouncementWithDelayAndDuration(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	ctx := context.Background()

	// Start Announcement in the Add that reserves the termination, as
	// TS 29.333 5.17.2.1 lets procedures share one command.
	play := agw.Play{Announcement: "welcome", Repeat: agw.RepeatForever, DelayMS: 500, DurationMS: 120000}
	cx, err := b.controller.Add(ctx, agw.TerminationRequest{
		Mode:    agw.SendOnly,
		Remote:  mustAddrPort("192.0.2.31:49180"),
		Signals: &agw.Signals{Play: &play},
		Events:  &agw.Events{RequestID: 9, Detect: []agw.EventName{agw.SignalCompleted}},
	})
	if err != nil {
		t.Fatal(err)
	}
	processorContext, terms := b.processor.only(t)
	if got := terms["t1"].signals; got == nil || got.Play == nil || *got.Play != play {
		t.Errorf("the processor plays %+v, want %+v", got, play)
	}
	add := payloadOf(t, b.net.Datagrams()[before:], controllerAddr, "Add = $")
	if want := `aasb/play { SignalType = TimeOut, Duration = 12000, NotifyCompletion = { TimeOut }, an = "sid=<file://welcome>", it = 0, iv = 50 }`; !strings.Contains(add, want) {
		t.Errorf("the Add has no %s:\n%s", want, add)
	}

	b.pass(t, 2*time.Minute)
	b.gateway.SignalCompleted(processorContext, "t1")
	h248test.Eventually(t, "the notification", func() bool { return len(b.notified.all()) == 1 })
	if n := b.notified.all()[0]; n.Signal != "aasb/play" || n.RequestID != 9 || n.Context != cx.ID {
		t.Errorf("notification %+v", n)
	}
	waitMessages(t, b, before, "mg MEGACO/2 Notify=ip/200/1", "mgc MEGACO/2 Reply")
	if _, _, err := b.controller.SubtractTermination(ctx, cx.ID, "ip/200/1"); err != nil {
		t.Fatal(err)
	}
}

func TestMpConference(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	ctx := context.Background()

	alice, bob, carol := mustAddrPort("192.0.2.41:49170"), mustAddrPort("192.0.2.42:49170"), mustAddrPort("192.0.2.43:49170")
	party := func(remote netip.AddrPort) agw.TerminationRequest {
		media := agw.Media{Codecs: []sdp.Codec{pcmu}}
		return agw.TerminationRequest{Mode: agw.SendReceive, LocalMedia: media, Remote: remote, RemoteMedia: media}
	}

	// The first two parties come in one INVITE each; the MRFC reserves
	// both in one transaction here, as the conference of TS 29.333
	// 5.17.2.30 is one context with a termination for each party.
	cx, err := b.controller.Add(ctx, party(alice), party(bob))
	if err != nil {
		t.Fatal(err)
	}
	first, second := cx.Terminations["ip/200/1"], cx.Terminations["ip/200/2"]
	if len(cx.Terminations) != 2 || first == nil || second == nil || first.Remote != alice || second.Remote != bob || first.Local == second.Local {
		t.Fatalf("context %+v", cx)
	}
	third, err := b.controller.AddTermination(ctx, cx.ID, party(carol))
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != "ip/200/3" {
		t.Errorf("third termination %+v", third)
	}
	processorContext, terms := b.processor.only(t)
	if len(terms) != 3 || terms["t3"].remote != carol {
		t.Errorf("the processor holds %+v", terms)
	}

	// Bob leaves.
	if _, _, err := b.controller.SubtractTermination(ctx, cx.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, terms := b.processor.only(t); len(terms) != 2 {
		t.Errorf("the processor holds %d terminations after one left", len(terms))
	}
	err = b.controller.Modify(ctx, cx.ID, second.ID, agw.TerminationRequest{Mode: agw.SendReceive})
	var rejected *CommandRejected
	if !errors.Is(err, agw.ErrNoTermination) || !errors.As(err, &rejected) || rejected.ErrorDescriptor.ErrorCode != 430 {
		t.Errorf("Modify of the termination that left: %v", err)
	}

	// The last two leave: the context goes with the last.
	for _, id := range []agw.TerminationID{first.ID, third.ID} {
		if _, _, err := b.controller.SubtractTermination(ctx, cx.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if n := b.processor.contextCount(); n != 0 {
		t.Errorf("the processor holds %d contexts", n)
	}
	if _, ok := b.processor.termination(processorContext, "t1"); ok {
		t.Error("the processor still holds t1")
	}
	if _, err := b.controller.AddTermination(ctx, cx.ID, agw.TerminationRequest{Mode: agw.SendReceive, Remote: alice, RemoteMedia: agw.Media{Codecs: []sdp.Codec{pcmu}}}); !errors.Is(err, agw.ErrNoContext) {
		t.Errorf("AddTermination to the deleted context: %v", err)
	}

	got := waitMessages(t, b, before, "mg MEGACO/2 Reply error 411")
	want := []string{
		"mgc MEGACO/2 Add=$",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Add=$",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Subtract=ip/200/2",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Modify=ip/200/2",
		"mg MEGACO/2 Reply error 430",
		"mgc MEGACO/2 Subtract=ip/200/1",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Subtract=ip/200/3",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Add=$",
		"mg MEGACO/2 Reply error 411",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	ds := b.net.Datagrams()[before:]
	if add := payloadOf(t, ds, controllerAddr, "Add = $", "192.0.2.42"); strings.Count(add, "Add = $") != 2 || !strings.Contains(add, "192.0.2.41") {
		t.Errorf("the first two parties are not in one Add transaction:\n%s", add)
	}
	if modes := modeOnTheWire(t, ds); len(modes) != 5 || slices.ContainsFunc(modes, func(m h248.StreamMode) bool { return m != h248.SendRecv }) {
		t.Errorf("modes %v, want SendReceive for three Adds, the Modify and the last Add", modes)
	}
}

// An Add without a mode makes the termination SendReceive, as the HTTP
// API of the media node does: H.248 would leave it Inactive (H.248.1
// (05/2002) 7.1.7), so the Controller writes the mode.
func TestMpAddWithoutAModeSendsAndReceives(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	cx, err := b.controller.Add(context.Background(), agw.TerminationRequest{Remote: mustAddrPort("192.0.2.30:49170")})
	if err != nil {
		t.Fatal(err)
	}
	if got := onlyTermination(t, cx).Mode; got != agw.SendReceive {
		t.Errorf("Add returned mode %q", got)
	}
	if _, terms := b.processor.only(t); terms["t1"].mode != agw.SendReceive {
		t.Errorf("the processor holds mode %q", terms["t1"].mode)
	}
}

// A Modify without a mode keeps the mode of the termination, as the
// HTTP API of the media node does: it carries no LocalControl
// descriptor (H.248.1 (05/2002) 7.1.7). The MRFC starts an announcement
// so once the ACK has come.
func TestMpModifyWithoutAModeKeepsIt(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	ctx := context.Background()
	cx, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly, Remote: mustAddrPort("192.0.2.30:49170")})
	if err != nil {
		t.Fatal(err)
	}
	err = b.controller.Modify(ctx, cx.ID, onlyTermination(t, cx).ID, agw.TerminationRequest{
		Signals: &agw.Signals{Play: &agw.Play{Announcement: "notice"}},
		Events:  &agw.Events{RequestID: 7, Detect: []agw.EventName{agw.SignalCompleted}},
	})
	if err != nil {
		t.Fatal(err)
	}
	processorContext, _ := b.processor.only(t)
	if got, _ := b.processor.termination(processorContext, "t1"); got.mode != agw.SendOnly || got.signals == nil {
		t.Errorf("the processor holds %+v", got)
	}
	modify := payloadOf(t, b.net.Datagrams()[before:], controllerAddr, "Modify = ip/200/1")
	if strings.Contains(modify, "Media") || strings.Contains(modify, "Mode") {
		t.Errorf("the Modify sets the mode:\n%s", modify)
	}
}

func TestMpStopAnnouncement(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	ctx := context.Background()

	cx, err := b.controller.Add(ctx, agw.TerminationRequest{
		Mode:    agw.SendOnly,
		Remote:  mustAddrPort("192.0.2.30:49170"),
		Signals: &agw.Signals{Play: &agw.Play{Announcement: "notice", Repeat: agw.RepeatForever}},
		Events:  &agw.Events{RequestID: 11, Detect: []agw.EventName{agw.SignalCompleted}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tid := onlyTermination(t, cx).ID
	processorContext, _ := b.processor.only(t)

	// Stop Announcement: a Signals descriptor without the signal.
	if err := b.controller.Modify(ctx, cx.ID, tid, agw.TerminationRequest{Mode: agw.SendOnly, Signals: &agw.Signals{}}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.processor.termination(processorContext, "t1")
	if got.signals == nil || got.signals.Play != nil {
		t.Errorf("the processor was asked to play %+v", got.signals)
	}
	modify := payloadOf(t, b.net.Datagrams()[before:], controllerAddr, "Modify = "+string(tid))
	if !strings.Contains(modify, "Signals { }") || strings.Contains(modify, "Events") {
		t.Errorf("the Modify:\n%s", modify)
	}

	// The relay reports an end that crossed the Modify: no signal plays,
	// so there is nothing to report. signalCompleted is what
	// SignalCompleted runs, called here directly so that the test can
	// tell that it has done.
	b.gateway.signalCompleted(processorContext, "t1", b.clock.Now())

	// An announcement whose end the MRFC did not ask for.
	if err := b.controller.Modify(ctx, cx.ID, tid, agw.TerminationRequest{Mode: agw.SendOnly, Signals: &agw.Signals{Play: &agw.Play{Announcement: "notice"}}, Events: &agw.Events{}}); err != nil {
		t.Fatal(err)
	}
	b.gateway.signalCompleted(processorContext, "t1", b.clock.Now())
	if _, _, err := b.controller.SubtractTermination(ctx, cx.ID, tid); err != nil {
		t.Fatal(err)
	}
	b.net.Settle(t)
	if sent(b.net.Datagrams()[before:], "Notify = ") || len(b.notified.all()) != 0 {
		t.Errorf("a Notify was sent: %v", b.notified.all())
	}
}

func TestMpGatewayTakesAnAddWithoutModeAsInactive(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	reply := b.send(t, h248.ActionRequest{ContextID: h248.ContextChoose, Commands: []h248.CommandRequest{{
		Command:       h248.Add,
		TerminationID: h248.TerminationChoose,
		Media:         &h248.MediaDescriptor{OneStream: &h248.StreamParms{Local: &h248.LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n"}}},
	}}})
	if code := errorCode(reply); code != 0 {
		t.Fatalf("error %d", code)
	}
	if _, terms := b.processor.only(t); terms["t1"].mode != agw.Inactive {
		t.Errorf("mode %q, want inactive (H.248.1 7.1.7)", terms["t1"].mode)
	}
}

func TestMpErrors(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	cx, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly})
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly})
	if err != nil {
		t.Fatal(err)
	}
	a, id, err := b.controller.lookup(cx.ID)
	if err != nil || a == nil {
		t.Fatal(err)
	}
	tid := h248.TerminationID(onlyTermination(t, cx).ID)
	withSignals := func(signals ...h248.Signal) h248.ActionRequest {
		return h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{{
			Command: h248.Modify, TerminationID: tid, Signals: &h248.SignalsDescriptor{Signals: signals},
		}}}
	}
	apf := func(parms ...h248.SigParameter) h248.Signal {
		return h248.Signal{SignalName: "an/apf", SigParList: parms}
	}
	parm := func(name, value string) h248.SigParameter {
		return h248.SigParameter{Name: name, Value: h248.Value{Text: value}}
	}
	modify := func(t h248.TerminationID, c func(*h248.CommandRequest)) h248.ActionRequest {
		cmd := h248.CommandRequest{Command: h248.Modify, TerminationID: t}
		if c != nil {
			c(&cmd)
		}
		return h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{cmd}}
	}

	tests := []struct {
		name   string
		action h248.ActionRequest
		want   int
	}{
		{"an unknown context", h248.ActionRequest{ContextID: 999, Commands: []h248.CommandRequest{{Command: h248.Modify, TerminationID: "ip/999/1"}}}, 411},
		{"an unknown termination", modify("ip/200/9", nil), 430},
		{"a termination of another context", modify(h248.TerminationID(onlyTermination(t, other).ID), nil), 435},
		{"a signal of a package the MRFP lacks", withSignals(h248.Signal{SignalName: "cg/rt"}), 440},
		{"a signal that an does not have", withSignals(h248.Signal{SignalName: "an/apv", SigParList: []h248.SigParameter{parm("an", "notice")}}), 452},
		{"an unknown announcement", withSignals(apf(parm("an", "goodbye"))), 449},
		{"no announcement name", withSignals(apf(parm("noc", "2"))), 457},
		{"an unknown parameter", withSignals(apf(parm("an", "notice"), parm("xyz", "1"))), 446},
		{"a variant", withSignals(apf(parm("an", "notice"), parm("av", "female"))), 449},
		{"played into the context", withSignals(apf(parm("an", "notice"), parm("di", "int"))), 449},
		{"a voice variable", withSignals(h248.Signal{SignalName: "aasb/play", SigParList: []h248.SigParameter{{Name: "an", Value: h248.Value{Text: "var=<t=dig,v=5>", Quoted: true}}}}), 449},
		{"two signals at once", withSignals(apf(parm("an", "notice")), apf(parm("an", "welcome"))), 501},
		{"KeepActive on a signal", withSignals(h248.Signal{SignalName: "an/apf", KeepActive: true, SigParList: []h248.SigParameter{parm("an", "notice")}}), 501},
		{"a signal list", h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{{
			Command: h248.Modify, TerminationID: tid,
			Signals: &h248.SignalsDescriptor{SeqSigLists: []h248.SeqSigList{{ID: 1, SignalList: []h248.Signal{apf(parm("an", "notice"))}}}},
		}}}, 501},
		{"an event of a package the MRFP lacks", modify(tid, func(c *h248.CommandRequest) {
			c.Events = &h248.EventsDescriptor{RequestID: 3, EventList: []h248.RequestedEvent{{PkgdName: "dd/d0"}}}
		}), 440},
		{"an event that g does not have", modify(tid, func(c *h248.CommandRequest) {
			c.Events = &h248.EventsDescriptor{RequestID: 3, EventList: []h248.RequestedEvent{{PkgdName: "g/xyz"}}}
		}), 451},
		{"mode Loopback", modify(tid, func(c *h248.CommandRequest) {
			c.Media = &h248.MediaDescriptor{OneStream: &h248.StreamParms{LocalControl: &h248.LocalControlDescriptor{StreamMode: h248.LoopBack}}}
		}), 517},
		{"a Local descriptor in Modify", modify(tid, func(c *h248.CommandRequest) {
			c.Media = &h248.MediaDescriptor{OneStream: &h248.StreamParms{Local: &h248.LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n"}}}
		}), 501},
		{"Topology", h248.ActionRequest{ContextID: id, ContextRequest: &h248.ContextRequest{TopologyReq: []h248.TopologyRequest{{TerminationFrom: tid, TerminationTo: "*", TopologyDirection: h248.Isolate}}}}, 522},
		{"Subtract of every termination", h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{{Command: h248.Subtract, TerminationID: h248.TerminationAll}}}, 501},
		{"an Add of a named termination", h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{{Command: h248.Add, TerminationID: "ip/200/7"}}}, 501},
		{"Move", h248.ActionRequest{ContextID: id, Commands: []h248.CommandRequest{{Command: h248.Move, TerminationID: tid}}}, 501},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorCode(b.send(t, tt.action)); got != tt.want {
				t.Errorf("error %d, want %d", got, tt.want)
			}
		})
	}

	// What the controller refuses before it sends anything.
	before := len(b.net.Datagrams())
	if _, err := b.controller.Add(ctx, agw.TerminationRequest{ID: "t1", Mode: agw.SendOnly}); err == nil {
		t.Error("an Add that names its termination")
	}
	if _, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly, Events: &agw.Events{Detect: []agw.EventName{"dd/d0"}}}); err == nil {
		t.Error("an Add with an event that the MRFP does not report")
	}
	if _, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly, Signals: &agw.Signals{Play: &agw.Play{Announcement: "notice", DurationMS: 660000}}}); err == nil {
		t.Error("an announcement longer than a signal can last")
	}
	if err := b.controller.Subtract(ctx, cx.ID); err != errNotIq {
		t.Errorf("Subtract of a whole context on a controller of Mp: %v", err)
	}
	if got := len(b.net.Datagrams()); got != before {
		t.Errorf("%d datagrams were sent", got-before)
	}

	// What the processor refuses.
	b.processor.setFail(agw.ErrNoPort)
	_, err = b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly})
	var rejected *CommandRejected
	if !errors.Is(err, agw.ErrNoPort) || !errors.As(err, &rejected) || rejected.ErrorDescriptor.ErrorCode != 510 {
		t.Errorf("Add: %v", err)
	}
	b.processor.setFail(nil)
	err = b.controller.Modify(ctx, cx.ID, onlyTermination(t, cx).ID, agw.TerminationRequest{Mode: agw.SendOnly, Signals: &agw.Signals{Play: &agw.Play{Announcement: "goodbye"}}})
	if !errors.Is(err, agw.ErrUnknownAnnouncement) || !errors.As(err, &rejected) || rejected.ErrorDescriptor.ErrorCode != 449 {
		t.Errorf("an unknown announcement: %v", err)
	}
}

func TestIqControllerSpeaksNoMp(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	ctx := context.Background()
	if _, err := b.controller.AddTermination(ctx, "100@agw1.ims.example.net", agw.TerminationRequest{Mode: agw.SendOnly}); err != errNotMp {
		t.Errorf("AddTermination on a controller of Iq: %v", err)
	}
	if _, _, err := b.controller.SubtractTermination(ctx, "100@agw1.ims.example.net", "ip/1/access/100"); err != errNotMp {
		t.Errorf("SubtractTermination on a controller of Iq: %v", err)
	}
	if _, err := b.controller.Add(ctx, agw.TerminationRequest{Mode: agw.SendOnly}); err == nil {
		t.Error("an Add on Iq without the access and the core termination")
	}
	if _, err := NewController(h248test.NewNetwork(b.clock).Listen("192.0.2.9:2944"), ControllerConfig{Profile: h248.ServiceChangeProfile{ProfileName: "MRF", Version: 8}}); err == nil {
		t.Error("a controller of a profile that the package does not speak")
	}
}

func TestMpTerminationHeartbeat(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{
		TimerX:               time.Minute,
		TerminationHeartbeat: func(ctx context.Context, id string) bool { return false },
	})
	b.waitRegistered(t)
	before := len(b.net.Datagrams())
	cx, err := b.controller.Add(context.Background(), agw.TerminationRequest{Mode: agw.SendOnly})
	if err != nil {
		t.Fatal(err)
	}
	// After timer X the termination reports its heartbeat. The MRFC
	// has no use for it, answers with error 411 and subtracts it. The
	// reply and the Subtract leave from different goroutines, and the
	// reply to the Subtract comes after both, so the wait is for all.
	tid := string(onlyTermination(t, cx).ID)
	want := []string{"mg MEGACO/2 Notify=" + tid, "mgc MEGACO/2 Reply error 411", "mgc MEGACO/2 Subtract=" + tid}
	b.until(t, "the heartbeat and the Subtract", time.Second, func() bool {
		got := summaries(t, b.net.Datagrams()[before:])
		for _, w := range want {
			if !slices.Contains(got, w) {
				return false
			}
		}
		return b.processor.contextCount() == 0
	})
	if !strings.Contains(payloadOf(t, b.net.Datagrams()[before:], gatewayAddr, "Notify = "+tid), "hangterm/thb") {
		t.Error("the Notify does not report hangterm/thb")
	}
}

func TestMpHeartbeatThatIsNotKeepActiveStopsTheAnnouncement(t *testing.T) {
	b := newMpTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	// An MRFC other than Controller asks for the heartbeat without
	// KeepActive, with an announcement whose end it wants to hear of,
	// also when an event interrupts it.
	reply := b.send(t, h248.ActionRequest{ContextID: h248.ContextChoose, Commands: []h248.CommandRequest{{
		Command:       h248.Add,
		TerminationID: h248.TerminationChoose,
		Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
			LocalControl: &h248.LocalControlDescriptor{StreamMode: h248.SendOnly},
			Local:        &h248.LocalRemoteDescriptor{SDP: "v=0\nc=IN IP4 $\nm=audio $ RTP/AVP 0\n"},
		}},
		Events: &h248.EventsDescriptor{RequestID: 12, EventList: []h248.RequestedEvent{
			{PkgdName: eventHeartbeat, EvParList: []h248.EventParameter{{Name: paramTimerX, Value: h248.Value{Text: "1"}}}},
			{PkgdName: eventSignalCompletion},
		}},
		Signals: &h248.SignalsDescriptor{Signals: []h248.Signal{{
			SignalName:       signalAnnouncementPlay,
			SigParList:       []h248.SigParameter{{Name: "an", Value: h248.Value{Text: "notice"}}},
			NotifyCompletion: []h248.NotificationReason{h248.OnTimeOut, h248.OnInterruptByEvent},
		}}},
	}}})
	if code := errorCode(reply); code != 0 {
		t.Fatalf("error %d", code)
	}

	// H.248.1 (05/2002) 7.1.9: detecting the heartbeat stops the
	// announcement, and g/sc reports it with the method EV (E.1.2).
	b.until(t, "the end of the announcement", 500*time.Millisecond, func() bool { return len(b.notified.all()) > 0 })
	if n := b.notified.all()[0]; n.Event != agw.SignalCompleted || n.Method != InterruptedByEvent || n.Signal != string(signalAnnouncementPlay) || n.RequestID != 12 {
		t.Errorf("notification %+v, want g/sc for an/apf with the method EV", n)
	}
	if _, terms := b.processor.only(t); terms["t1"].signals == nil || terms["t1"].signals.Play != nil {
		t.Errorf("the processor still plays %+v", terms["t1"].signals)
	}
}
