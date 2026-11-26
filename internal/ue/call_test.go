package ue_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// party is a UE with a UA of its own, registered through pcscfAddr.
type party struct {
	ue      *ue.UE
	ua      *sip.UA
	contact ue.Contact
	media   *ue.Media
	reg     ue.Registration
	calls   chan *sip.IncomingCall
	// wire is every SIP message the UE sent and received.
	wire *wireLog
}

func newParty(t *testing.T, pcscfAddr netip.AddrPort, private, public, pass string) *party {
	return newPartyWith(t, pcscfAddr, private, public, pass, ue.CallConfig{})
}

// newPartyWith is newParty with calls as the CallConfig of the UE.
func newPartyWith(t *testing.T, pcscfAddr netip.AddrPort, private, public, pass string, calls ue.CallConfig) *party {
	return newPartyTweaked(t, pcscfAddr, private, public, pass, calls, func(*sip.UAConfig) {})
}

// newPartyTweaked is newPartyWith with tweak changing the UAConfig.
func newPartyTweaked(t *testing.T, pcscfAddr netip.AddrPort, private, public, pass string, calls ue.CallConfig, tweak func(*sip.UAConfig)) *party {
	t.Helper()
	p := &party{calls: make(chan *sip.IncomingCall, 1), wire: &wireLog{}}
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), p.wire.observe)
	if err != nil {
		t.Fatal(err)
	}
	addr := tr.LocalAddr()
	ueConfig := ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscfAddr, PCSCFURI: "sip:pcscf." + domain + ":5060", Calls: calls}
	p.contact = ue.NewContact(ueConfig, addr)
	cfg := ue.UAConfig(sip.UAConfig{
		SentBy:  addr.String(),
		Contact: p.contact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { p.calls <- ic },
		// Every request of the UE goes to its P-CSCF.
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: pcscfAddr}}, nil
		},
	}, calls)
	tweak(&cfg)
	p.ua, err = sip.NewUA(tr, cfg, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.ua.Close)
	p.ue = ue.New(ueConfig, p.ua.Stack(), p.contact, quiet())
	if p.media, err = ue.ListenMedia("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.media.Close() })
	if p.reg, err = p.ue.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCallThroughTheWholeIMS(t *testing.T) {
	addr := network(t, time.Second)
	caller := newParty(t, addr, impi, impu, password)
	callee := newParty(t, addr, impi2, impu2, password2)

	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		answered <- callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{PCM: ue.Tone(880, time.Second)}, ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	if err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.Speech{PCM: ue.Tone(440, 500*time.Millisecond)}, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-answered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callee never saw the call end")
	}
	got, packets := callee.media.Received()
	if packets != 25 || !bytes.Equal(got, caller.media.Sent()) {
		t.Errorf("callee got %d packets, %d bytes; want the 25 packets the caller sent", packets, len(got))
	}
	if back, _ := caller.media.Received(); len(back) == 0 {
		t.Error("the caller heard nothing from the callee")
	}
}

func TestBusyCallee(t *testing.T) {
	addr := network(t, time.Second)
	caller := newParty(t, addr, impi, impu, password)
	callee := newParty(t, addr, impi2, impu2, password2)
	go func() {
		ic := <-callee.calls
		callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{}, ue.AnswerOptions{Reject: 486})
	}()
	err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.Speech{}, ue.CallOptions{})
	var failed *ue.CallFailed
	if !errors.As(err, &failed) || failed.StatusCode != 486 {
		t.Fatalf("err = %v, want a 486", err)
	}
}

func TestCallToAnUnregisteredCallee(t *testing.T) {
	addr := network(t, time.Second)
	caller := newParty(t, addr, impi, impu, password)
	err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.Speech{}, ue.CallOptions{})
	var failed *ue.CallFailed
	if !errors.As(err, &failed) || failed.StatusCode != 480 {
		t.Fatalf("err = %v, want a 480", err)
	}
}

func TestCallerGivesUpOnACalleeThatDoesNotAnswer(t *testing.T) {
	addr := network(t, time.Second)
	caller := newParty(t, addr, impi, impu, password)
	callee := newParty(t, addr, impi2, impu2, password2)
	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		// Rings far longer than the caller waits.
		answered <- callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{}, ue.AnswerOptions{PickUp: time.After(time.Minute)})
	}()
	err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.Speech{}, ue.CallOptions{GiveUpAfter: 500 * time.Millisecond})
	var failed *ue.CallFailed
	if !errors.As(err, &failed) || failed.StatusCode != 487 {
		t.Fatalf("err = %v, want a 487 after the CANCEL", err)
	}
	select {
	case err := <-answered:
		if !errors.Is(err, sip.ErrCancelled) {
			t.Errorf("callee ended with %v, want ErrCancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callee never saw the CANCEL")
	}
}

// directCaller is a UE whose requests go straight to a bare UA that
// onCall answers, with no IMS in between. It returns the UE, its UA and
// media, the callee's URI, and the callee's media.
func directCaller(t *testing.T, onCall func(ic *sip.IncomingCall, media *ue.Media)) (*ue.UE, *sip.UA, *ue.Media, string, *ue.Media) {
	t.Helper()
	listen := func() *sip.TransportLayer {
		tr, err := sip.Listen("127.0.0.1:0", quiet())
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	media := func() *ue.Media {
		m, err := ue.ListenMedia("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Close() })
		return m
	}
	calleeMedia := media()
	receiveOnly(t, calleeMedia)
	calleeTransport := listen()
	calleeAddr := calleeTransport.LocalAddr()
	callee, err := sip.NewUA(calleeTransport, sip.UAConfig{
		SentBy:  calleeAddr.String(),
		Contact: "sip:callee@" + calleeAddr.String(),
		OnCall:  func(ic *sip.IncomingCall) { onCall(ic, calleeMedia) },
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callee.Close)

	callerTransport := listen()
	callerAddr := callerTransport.LocalAddr()
	callerConfig := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain,
		PCSCF: calleeAddr, PCSCFURI: "sip:pcscf." + domain + ":5060"}
	callerContact := ue.NewContact(callerConfig, callerAddr)
	caller, err := sip.NewUA(callerTransport, ue.UAConfig(sip.UAConfig{
		SentBy:  callerAddr.String(),
		Contact: callerContact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { ic.Reject(486) },
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: calleeAddr}}, nil
		},
	}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(caller.Close)
	u := ue.New(callerConfig, caller.Stack(), callerContact, quiet())
	return u, caller, media(), "sip:callee@" + calleeAddr.String(), calleeMedia
}

var directRegistration = ue.Registration{PublicIdentities: []string{impu}}

// receiveOnly has m keep the PCMU it receives, for a peer of the UE that
// is not a UE simulator.
func receiveOnly(t *testing.T, m *ue.Media) {
	t.Helper()
	if err := m.SetStream(ue.Stream{Remote: netip.MustParseAddrPort("127.0.0.1:9"), Codec: pcmu}); err != nil {
		t.Fatal(err)
	}
	m.SetFlow(ue.Flow{Receive: true})
}

// talk has m, the media of a peer that is not a UE simulator, send a
// PCMU tone to the address to, as a peer that answers sendrecv does.
// Without it the UE would end the call with ErrNoMedia.
func talk(m *ue.Media, to netip.AddrPort) {
	m.SetStream(ue.Stream{Remote: to, Codec: pcmu})
	m.SetFlow(ue.Flow{Send: true, Receive: true})
	m.Start(ue.Speech{PCM: ue.Tone(880, 10*time.Second)})
}

// TestCalleeSendsToTheStreamItAccepted is RFC 3264 6: of two audio
// streams, the callee accepts the one with PCMU, and sends its audio
// there rather than to the first.
func TestCalleeSendsToTheStreamItAccepted(t *testing.T) {
	calleeTransport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	calleeAddr := calleeTransport.LocalAddr()
	calleeConfig := ue.Config{PrivateIdentity: impi2, PublicIdentity: impu2, Password: password2, HomeDomain: domain,
		PCSCF: calleeAddr, PCSCFURI: "sip:pcscf." + domain + ":5060"}
	calleeContact := ue.NewContact(calleeConfig, calleeAddr)
	calls := make(chan *sip.IncomingCall, 1)
	calleeUA, err := sip.NewUA(calleeTransport, ue.UAConfig(sip.UAConfig{
		SentBy:  calleeAddr.String(),
		Contact: calleeContact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { calls <- ic },
	}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(calleeUA.Close)
	calleeUE := ue.New(calleeConfig, calleeUA.Stack(), calleeContact, quiet())
	listen := func() *ue.Media {
		m, err := ue.ListenMedia("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.Close() })
		return m
	}
	calleeMedia, pcma, pcmu := listen(), listen(), listen()
	for _, m := range []*ue.Media{pcma, pcmu} {
		receiveOnly(t, m)
	}

	callerTransport := listenTransport(t)
	callerUA, err := sip.NewUA(callerTransport, sip.UAConfig{
		SentBy:  callerTransport.LocalAddr().String(),
		Contact: "sip:caller@" + callerTransport.LocalAddr().String(),
		OnCall:  func(ic *sip.IncomingCall) { ic.Reject(486) },
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: calleeAddr}}, nil
		},
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callerUA.Close)
	offer := sdp.Offer("1", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), pcmu.Port()))
	pcmaOnly := sdp.Media{Type: "audio", Port: int(pcma.Port()), Proto: "RTP/AVP", Formats: []int{8}}
	offer.Media = append([]sdp.Media{pcmaOnly}, offer.Media...)
	call, err := callerUA.Invite(sip.Invitation{
		RequestURI: calleeContact.URI(),
		From:       impu,
		Body:       sip.Body{Type: "application/sdp", Content: offer.Bytes()},
	})
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan error, 1)
	go func() {
		ic := <-calls
		answered <- calleeUE.Answer(context.Background(), ic, calleeMedia, ue.Speech{PCM: ue.Tone(880, time.Second)}, ue.AnswerOptions{PickUp: time.After(0)})
	}()
	for range call.Responses() {
	}
	talk(pcmu, loopback(calleeMedia.Port()))
	// Once the accepted stream has its packets, the rejected one would
	// have had its own, sent at the same pace.
	waitFor(t, "packets on the PCMU stream that the callee accepted", func() bool {
		_, n := pcmu.Received()
		return n > 2
	})
	if err := call.Bye(); err != nil {
		t.Fatal(err)
	}
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	if _, n := pcma.Received(); n != 0 {
		t.Errorf("%d packets reached the PCMA stream that the callee rejected", n)
	}
}

func listenTransport(t *testing.T) *sip.TransportLayer {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestCallStoppedWhileRingingIsCancelled is RFC 3261 9.1: a caller that
// stops before the answer hangs up with CANCEL, and the INVITE ends with
// 487.
func TestCallStoppedWhileRingingIsCancelled(t *testing.T) {
	calleeEnded := make(chan error, 1)
	u, ua, media, callee, _ := directCaller(t, func(ic *sip.IncomingCall, _ *ue.Media) {
		ic.Progress(180, sip.Body{})
		<-ic.Done()
		calleeEnded <- ic.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- u.Call(ctx, ua, directRegistration, callee, media, ue.Speech{}, ue.CallOptions{}) }()
	select {
	case err := <-ended:
		var failed *ue.CallFailed
		if !errors.As(err, &failed) || failed.StatusCode != 487 {
			t.Errorf("err = %v, want a 487 after the CANCEL", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call still waits for the answer after its context ended")
	}
	select {
	case err := <-calleeEnded:
		if !errors.Is(err, sip.ErrCancelled) {
			t.Errorf("callee ended with %v, want ErrCancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callee never saw the CANCEL")
	}
}

// TestAnswerInTheReliable183 is RFC 3262 5 and RFC 3261 13.2.1: the
// callee may answer in a reliable 183 and send the 200 without SDP; the
// first session description is the answer.
func TestAnswerInTheReliable183(t *testing.T) {
	u, ua, media, callee, calleeMedia := directCaller(t, func(ic *sip.IncomingCall, media *ue.Media) {
		offer, err := sdp.Parse(ic.Request.Body)
		if err != nil {
			ic.Reject(488)
			return
		}
		answer, err := sdp.Answer(offer, "1", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), media.Port()))
		if err != nil {
			ic.Reject(488)
			return
		}
		if err := ic.Progress(183, sip.Body{Type: "application/sdp", Content: answer.Bytes()}); err != nil {
			return
		}
		if _, err := ic.Accept(sip.Body{}); err == nil {
			talk(media, offer.Address(*offer.AudioMedia()))
		}
	})
	if err := u.Call(context.Background(), ua, directRegistration, callee, media, ue.Speech{PCM: ue.Tone(440, 200*time.Millisecond)}, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, packets := calleeMedia.Received(); packets == 0 {
		t.Error("the callee got no audio")
	}
}

// acceptAndHangUp answers a call with 200 and no 183, and the callee
// hangs up after hangUpAfter.
func acceptAndHangUp(hangUpAfter time.Duration) func(*sip.IncomingCall, *ue.Media) {
	return func(ic *sip.IncomingCall, media *ue.Media) {
		offer, err := sdp.Parse(ic.Request.Body)
		if err != nil {
			ic.Reject(488)
			return
		}
		answer, err := sdp.Answer(offer, "1", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), media.Port()))
		if err != nil {
			ic.Reject(488)
			return
		}
		if _, err := ic.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()}); err != nil {
			return
		}
		talk(media, offer.Address(*offer.AudioMedia()))
		time.Sleep(hangUpAfter)
		ic.Bye()
	}
}

// TestCalleeHangsUpFirst is RFC 3261 15: when the callee sends the BYE,
// the caller stops its audio at once instead of playing it to the end.
func TestCalleeHangsUpFirst(t *testing.T) {
	u, ua, media, callee, _ := directCaller(t, acceptAndHangUp(200*time.Millisecond))
	ended := make(chan error, 1)
	go func() {
		ended <- u.Call(context.Background(), ua, directRegistration, callee, media, ue.Speech{PCM: ue.Tone(440, 10*time.Second)}, ue.CallOptions{})
	}()
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("Call ended with %v, want nil after the callee's BYE", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the caller still plays its audio after the callee hung up")
	}
}

// progressLog records what a Progress hears.
type progressLog struct {
	mu      sync.Mutex
	states  []ue.CallState
	callIDs map[string]bool
}

func (l *progressLog) progress(state ue.CallState, callID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.callIDs == nil {
		l.callIDs = map[string]bool{}
	}
	l.states = append(l.states, state)
	l.callIDs[callID] = true
}

func (l *progressLog) get() ([]ue.CallState, map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.states), maps.Clone(l.callIDs)
}

// TestProgressOfACall is what "ue serve" shows of a call: the caller
// calls, hears it ring and gets the answer; the callee rings and
// answers. Both hear the one Call-ID of the INVITE, and the caller that
// stays up keeps the call after its audio, until it hangs up.
func TestProgressOfACall(t *testing.T) {
	addr := network(t, time.Second)
	caller := newParty(t, addr, impi, impu, password)
	callee := newParty(t, addr, impi2, impu2, password2)

	var callerLog, calleeLog progressLog
	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		answered <- callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{PCM: ue.Tone(880, time.Second)},
			ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond), Progress: calleeLog.progress})
	}()
	ctx, hangUp := context.WithCancel(context.Background())
	called := make(chan error, 1)
	go func() {
		called <- caller.ue.Call(ctx, caller.ua, caller.reg, impu2, caller.media, ue.Speech{PCM: ue.Tone(440, 100*time.Millisecond)},
			ue.CallOptions{StayUp: true, Progress: callerLog.progress})
	}()
	select {
	case err := <-called:
		t.Fatalf("the call ended with %v before the caller hung up", err)
	case <-time.After(time.Second):
	}
	hangUp()
	for _, ended := range []chan error{called, answered} {
		select {
		case err := <-ended:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("call ended with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the call did not end after the caller hung up")
		}
	}
	states, callerIDs := callerLog.get()
	if want := []ue.CallState{ue.CallCalling, ue.CallRinging, ue.CallActive}; !slices.Equal(slices.Compact(states), want) {
		t.Errorf("caller heard %v, want %v", states, want)
	}
	states, calleeIDs := calleeLog.get()
	if want := []ue.CallState{ue.CallRinging, ue.CallActive}; !slices.Equal(states, want) {
		t.Errorf("callee heard %v, want %v", states, want)
	}
	if len(callerIDs) != 1 || !maps.Equal(callerIDs, calleeIDs) {
		t.Errorf("caller heard Call-IDs %v, callee %v; want the same one", callerIDs, calleeIDs)
	}
}

// ringingCallee is a UE with no IMS in front of it, called from a bare
// UA. The callee answers the call with Answer and opts; the caller's
// responses come on the returned call.
func ringingCallee(t *testing.T, ctx context.Context, opts ue.AnswerOptions) (*sip.OutgoingCall, <-chan error) {
	t.Helper()
	calleeTransport := listenTransport(t)
	calleeAddr := calleeTransport.LocalAddr()
	calleeConfig := ue.Config{PrivateIdentity: impi2, PublicIdentity: impu2, Password: password2, HomeDomain: domain,
		PCSCF: calleeAddr, PCSCFURI: "sip:pcscf." + domain + ":5060"}
	calleeContact := ue.NewContact(calleeConfig, calleeAddr)
	calls := make(chan *sip.IncomingCall, 1)
	calleeUA, err := sip.NewUA(calleeTransport, ue.UAConfig(sip.UAConfig{
		SentBy:  calleeAddr.String(),
		Contact: calleeContact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { calls <- ic },
	}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(calleeUA.Close)
	calleeUE := ue.New(calleeConfig, calleeUA.Stack(), calleeContact, quiet())
	calleeMedia, err := ue.ListenMedia("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { calleeMedia.Close() })

	callerUA := newTestUA(t, "caller", func(ic *sip.IncomingCall) { ic.Reject(486) })
	offer := sdp.Offer("1", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 4000))
	call, err := callerUA.Invite(sip.Invitation{
		RequestURI: calleeContact.URI(),
		From:       impu,
		Body:       sip.Body{Type: "application/sdp", Content: offer.Bytes()},
	})
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan error, 1)
	go func() { answered <- calleeUE.Answer(ctx, <-calls, calleeMedia, ue.Speech{}, opts) }()
	return call, answered
}

// TestCalleeThatKeepsRingingSendsA180EveryMinute is RFC 3261 13.3.1.1,
// with the minute shortened.
func TestCalleeThatKeepsRingingSendsA180EveryMinute(t *testing.T) {
	ue.SetRingingRefresh(t, 100*time.Millisecond)
	call, _ := ringingCallee(t, context.Background(), ue.AnswerOptions{PickUp: time.After(450 * time.Millisecond)})
	ringing := 0
	for resp := range call.Responses() {
		if resp.StatusCode == 180 {
			ringing++
		}
	}
	call.Bye()
	if ringing < 3 {
		t.Errorf("%d 180s in 450 ms of ringing, want one every 100 ms", ringing)
	}
}

// TestCalleeThatStopsWhileRingingTurnsTheCallDown: the callee answers 480
// rather than leave the call ringing.
func TestCalleeThatStopsWhileRingingTurnsTheCallDown(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	call, answered := ringingCallee(t, ctx, ue.AnswerOptions{PickUp: make(chan time.Time)})
	final := 0
	for resp := range call.Responses() {
		final = resp.StatusCode
	}
	if final != 480 {
		t.Errorf("final response %d, want 480", final)
	}
	if err := <-answered; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Answer ended with %v, want the end of its context", err)
	}
}

// TestOneWayAudioFailsTheCall: the callee answers sendrecv but sends no
// RTP, so the caller's call ends with ErrNoMedia, while the callee,
// which heard the caller, ends without an error.
func TestOneWayAudioFailsTheCall(t *testing.T) {
	addr := network(t, time.Second)
	caller := newParty(t, addr, impi, impu, password)
	callee := newParty(t, addr, impi2, impu2, password2)
	answered := answerIn(t, callee, ue.Speech{}, ue.AnswerOptions{PickUp: time.After(0)})
	err := caller.ue.Call(context.Background(), caller.ua, caller.reg, impu2, caller.media, ue.ToneSpeech(300*time.Millisecond), ue.CallOptions{})
	if !errors.Is(err, ue.ErrNoMedia) {
		t.Errorf("the caller's call ended with %v, want ErrNoMedia", err)
	}
	if err := wait(t, "the answer", answered); err != nil {
		t.Errorf("the callee, which heard the caller, ended with %v", err)
	}
	if !slices.Contains(caller.wire.labels(), "> BYE") {
		t.Error("the caller did not hang up with a BYE")
	}
}

// TestNoMediaFromAPeerThatDoesNotSend: a callee whose answer says
// recvonly sends nothing (RFC 3264 6.1), so a call without RTP from it
// is no failure.
func TestNoMediaFromAPeerThatDoesNotSend(t *testing.T) {
	u, ua, media, callee, calleeMedia := directCaller(t, func(ic *sip.IncomingCall, media *ue.Media) {
		offer, err := sdp.Parse(ic.Request.Body)
		if err != nil {
			ic.Reject(488)
			return
		}
		answer, err := sdp.Answer(offer, "1", loopback(media.Port()))
		if err != nil {
			ic.Reject(488)
			return
		}
		answer.AudioMedia().SetDirection("recvonly")
		ic.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()})
	})
	if err := u.Call(context.Background(), ua, directRegistration, callee, media, ue.ToneSpeech(200*time.Millisecond), ue.CallOptions{}); err != nil {
		t.Errorf("call ended with %v, want nil: the callee said it would not send", err)
	}
	if _, n := calleeMedia.Received(); n == 0 {
		t.Error("the callee got no audio")
	}
}
