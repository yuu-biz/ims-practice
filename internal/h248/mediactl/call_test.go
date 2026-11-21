package mediactl

import (
	"context"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

func TestRegistration(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	eventually(t, "the reply to the Modify", func() bool { return len(summaries(t, b.net.datagrams())) >= 4 })

	got := summaries(t, b.net.datagrams())
	want := []string{
		"mg MEGACO/1 ServiceChange=ROOT",
		"mgc MEGACO/1 Reply",
		"mgc MEGACO/2 Modify=ROOT",
		"mg MEGACO/2 Reply",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	register := firstRequest(t, b.net.datagrams(), h248.ServiceChange)
	sc := register.Actions[0].Commands[0].ServiceChange
	if sc.Method != h248.Restart || sc.Reason != "901 Cold Boot" || sc.Version != 2 ||
		sc.Profile == nil || *sc.Profile != Profile || sc.TimeStamp.Date != "20261120" {
		t.Errorf("registration %+v", sc)
	}
	reply := firstReply(t, b.net.datagrams(), register.TransactionID)
	result := reply.Actions[0].Commands[0].ServiceChangeResult
	if result == nil || result.TimeStamp.Date == "" || result.Profile != nil || result.Version != 0 {
		t.Errorf("reply to the registration %+v", result)
	}
	events := firstRequest(t, b.net.datagrams(), h248.Modify).Actions[0].Commands[0].Events
	if events.EventList[0].PkgdName != "it/ito" || events.EventList[0].EvParList[0].Value.Text != "2750" {
		t.Errorf("inactivity timer %+v", events)
	}
	b.gateway.mu.Lock()
	mit := b.gateway.inactivity.mit
	b.gateway.mu.Unlock()
	if mit != 27500*time.Millisecond {
		t.Errorf("mit = %v", mit)
	}
}

// firstRequest finds the first request whose first command is c.
func firstRequest(t *testing.T, ds []datagram, c h248.Command) h248.TransactionRequest {
	t.Helper()
	for _, d := range ds {
		m, _ := h248.Parse(d.payload)
		for _, tr := range m.Transactions {
			if r, ok := tr.(h248.TransactionRequest); ok && r.Actions[0].Commands[0].Command == c {
				return r
			}
		}
	}
	t.Fatalf("no %s", c)
	return h248.TransactionRequest{}
}

// firstReply finds the reply to transaction id.
func firstReply(t *testing.T, ds []datagram, id uint32) h248.TransactionReply {
	t.Helper()
	for _, d := range ds {
		m, _ := h248.Parse(d.payload)
		for _, tr := range m.Transactions {
			if r, ok := tr.(h248.TransactionReply); ok && r.TransactionID == id {
				return r
			}
		}
	}
	t.Fatalf("no reply to %d", id)
	return h248.TransactionReply{}
}

func TestFullCall(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.datagrams())
	ctx := context.Background()

	amrwb := sdp.Codec{PayloadType: 96, Name: sdp.NameAMRWB, ClockRate: 16000, Fmtp: "mode-change-capability=2; max-red=0"}
	r, err := reserve(ctx, b.controller, agw.Media{Codecs: []sdp.Codec{amrwb, pcmu}, Bandwidth: 41})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "100@agw1.ims.example.net" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Terminations[agw.Access].Local != netip.MustParseAddrPort("198.51.100.10:40000") || r.Terminations[agw.Core].Local != netip.MustParseAddrPort("198.51.100.10:40002") {
		t.Errorf("terminations %v", r.Terminations)
	}
	relayContext, ok := b.relay.context("c1")
	for _, side := range []agw.TerminationID{agw.Access, agw.Core} {
		if media := relayContext.media[side]; !ok || len(media.Codecs) != 2 || media.Codecs[0] != amrwb || media.Bandwidth != 41 {
			t.Errorf("the relay was asked for %+v on %s", media, side)
		}
	}

	ue := agw.TerminationRequest{Remote: netip.MustParseAddrPort("192.0.2.30:49170"), RemoteMedia: agw.Media{Codecs: []sdp.Codec{amrwb}, Bandwidth: 41}}
	network := agw.TerminationRequest{Remote: netip.MustParseAddrPort("203.0.113.40:50000"), RemoteMedia: agw.Media{Codecs: []sdp.Codec{amrwb}, Bandwidth: 41}}
	if err := b.controller.Modify(ctx, r.ID, agw.Access, ue); err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Modify(ctx, r.ID, agw.Core, network); err != nil {
		t.Fatal(err)
	}
	relayContext, _ = b.relay.context("c1")
	if got := relayContext.remote[agw.Access]; got.Remote != ue.Remote || len(got.RemoteMedia.Codecs) != 1 || got.RemoteMedia.Codecs[0] != amrwb || got.RemoteMedia.Bandwidth != 41 {
		t.Errorf("access remote %+v", got)
	}
	if got := relayContext.remote[agw.Core]; got.Remote != network.Remote {
		t.Errorf("core remote %+v", got)
	}

	if err := b.controller.Subtract(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, released, open := b.relay.counts(); released != 1 || open != 0 {
		t.Errorf("the relay released %d and has %d open", released, open)
	}

	got := summaries(t, b.net.datagrams()[before:])
	wantMessages := []string{
		"mgc MEGACO/2 Add=ip/1/access/$",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Modify=ip/1/access/100",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Modify=ip/1/core/100",
		"mg MEGACO/2 Reply",
		"mgc MEGACO/2 Subtract=*",
		"mg MEGACO/2 Reply",
	}
	if !slices.Equal(got, wantMessages) {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantMessages, "\n"))
	}
	if err := b.controller.Subtract(ctx, r.ID); !errorIs(err, agw.ErrNoContext) {
		t.Errorf("a second Subtract: %v", err)
	}
}

// Reserve and Configure AGW Connection Point (TS 29.334 5.17.2.4): the
// Add of a termination whose request has a remote address carries the
// Remote descriptor too, and the relay learns where that termination
// sends.
func TestAddWithARemoteAddress(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.datagrams())
	ctx := context.Background()

	ue := mustAddrPort("192.0.2.30:49170")
	pcmuOnly := agw.Media{Codecs: []sdp.Codec{pcmu}}
	r, err := b.controller.Add(ctx,
		agw.TerminationRequest{ID: agw.Access, LocalMedia: pcmuOnly, Remote: ue, RemoteMedia: pcmuOnly},
		agw.TerminationRequest{ID: agw.Core, LocalMedia: pcmuOnly})
	if err != nil {
		t.Fatal(err)
	}
	if access, core := r.Terminations[agw.Access], r.Terminations[agw.Core]; access == nil || core == nil || access.Remote != ue || core.Remote.IsValid() {
		t.Errorf("terminations %v", r.Terminations)
	}
	add := firstRequest(t, b.net.datagrams()[before:], h248.Add)
	for _, cmd := range add.Actions[0].Commands {
		hasRemote := cmd.Media != nil && cmd.Media.OneStream != nil && cmd.Media.OneStream.Remote != nil
		if want := strings.Contains(string(cmd.TerminationID), "/access/"); hasRemote != want {
			t.Errorf("the Add of %s has a Remote descriptor: %v, want %v", cmd.TerminationID, hasRemote, want)
		}
	}
	relayContext, _ := b.relay.context("c1")
	if got, ok := relayContext.remote[agw.Access]; !ok || got.Remote != ue || len(got.RemoteMedia.Codecs) != 1 || got.RemoteMedia.Codecs[0] != pcmu {
		t.Errorf("the relay sends from the access termination to %+v", got)
	}
	if got, ok := relayContext.remote[agw.Core]; ok {
		t.Errorf("the relay sends from the core termination to %+v", got)
	}
}

// A Modify without a remote address keeps where the termination sends:
// Configure AGW Connection Point (TS 29.334 5.17.2.3) then carries no
// Remote descriptor, and the relay is not asked to change anything.
func TestModifyWithoutARemoteAddress(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()

	r, err := reserve(ctx, b.controller, agw.Media{Codecs: []sdp.Codec{pcmu}})
	if err != nil {
		t.Fatal(err)
	}
	ue := mustAddrPort("192.0.2.30:49170")
	if err := b.controller.Modify(ctx, r.ID, agw.Access, agw.TerminationRequest{Remote: ue}); err != nil {
		t.Fatal(err)
	}
	before := len(b.net.datagrams())
	if err := b.controller.Modify(ctx, r.ID, agw.Access, agw.TerminationRequest{}); err != nil {
		t.Fatal(err)
	}
	modify := firstRequest(t, b.net.datagrams()[before:], h248.Modify).Actions[0].Commands[0]
	if modify.Media == nil || modify.Media.OneStream == nil || modify.Media.OneStream.Remote != nil {
		t.Errorf("the Modify carries %+v, want no Remote descriptor", modify.Media)
	}
	if relayContext, _ := b.relay.context("c1"); relayContext.remote[agw.Access].Remote != ue {
		t.Errorf("the relay sends from the access termination to %+v", relayContext.remote[agw.Access])
	}
}

// Iq reports no statistics on Subtract (TS 29.334 5.7.7, Table 5.7.7.2),
// and the reply to a Subtract has no descriptors (Table 5.8.3.2): the
// Subtract that the Controller sends has no Audit descriptor, and its
// reply still names the two terminations and nothing else.
func TestSubtractReplyCarriesNoStatistics(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.datagrams())
	ctx := context.Background()

	r, err := reserve(ctx, b.controller, agw.Media{Codecs: []sdp.Codec{pcmu}, Bandwidth: 80})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Subtract(ctx, r.ID); err != nil {
		t.Fatal(err)
	}

	datagrams := b.net.datagrams()[before:]
	subtract := firstRequest(t, datagrams, h248.Subtract)
	if subtract.Actions[0].Commands[0].Audit != nil {
		t.Fatalf("the Subtract asks for an audit: %+v", subtract.Actions[0].Commands[0].Audit)
	}
	reply := firstReply(t, datagrams, subtract.TransactionID)
	var terminations []h248.TerminationID
	for _, action := range reply.Actions {
		for _, cmd := range action.Commands {
			if want := (h248.CommandReply{Command: h248.Subtract, TerminationID: cmd.TerminationID}); !reflect.DeepEqual(cmd, want) {
				t.Errorf("the reply for %s is %+v, want a Subtract with no descriptors", cmd.TerminationID, cmd)
			}
			terminations = append(terminations, cmd.TerminationID)
		}
	}
	if want := []h248.TerminationID{"ip/1/access/100", "ip/1/core/100"}; !slices.Equal(terminations, want) {
		t.Errorf("the reply names %v, want %v", terminations, want)
	}
	// Whatever the parsed form says, no Statistics descriptor is on the wire.
	for _, d := range datagrams {
		if strings.Contains(string(d.payload), "Statistics") {
			t.Errorf("a Statistics descriptor on the wire:\n%s", d.payload)
		}
	}
}
