package mediactl

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

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
	r, err := b.controller.Reserve(ctx, Media{Codecs: []sdp.Codec{amrwb, pcmu}, Bandwidth: 41})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "100@agw1.ims.example.net" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Local[Access] != netip.MustParseAddrPort("198.51.100.10:40000") || r.Local[Core] != netip.MustParseAddrPort("198.51.100.10:40002") {
		t.Errorf("local addresses %v", r.Local)
	}
	relayContext, ok := b.relay.context("c1")
	if !ok || len(relayContext.media.Codecs) != 2 || relayContext.media.Codecs[0] != amrwb || relayContext.media.Bandwidth != 41 {
		t.Errorf("the relay was asked for %+v", relayContext.media)
	}

	ue := Media{Address: netip.MustParseAddrPort("192.0.2.30:49170"), Codecs: []sdp.Codec{amrwb}, Bandwidth: 41}
	network := Media{Address: netip.MustParseAddrPort("203.0.113.40:50000"), Codecs: []sdp.Codec{amrwb}, Bandwidth: 41}
	if err := b.controller.Configure(ctx, r.ID, Access, ue); err != nil {
		t.Fatal(err)
	}
	if err := b.controller.Configure(ctx, r.ID, Core, network); err != nil {
		t.Fatal(err)
	}
	relayContext, _ = b.relay.context("c1")
	if got := relayContext.remote[Access]; got.Address != ue.Address || len(got.Codecs) != 1 || got.Codecs[0] != amrwb || got.Bandwidth != 41 {
		t.Errorf("access remote %+v", got)
	}
	if got := relayContext.remote[Core]; got.Address != network.Address {
		t.Errorf("core remote %+v", got)
	}

	b.clock.Advance(3 * time.Second)
	stats, err := b.controller.Release(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := Statistics{Duration: 3 * time.Second, PacketsSent: 50, PacketsReceived: 50, OctetsSent: 8600, OctetsReceived: 8600}
	if stats[Access] != want || stats[Core] != want {
		t.Errorf("statistics %+v, want %+v for each side", stats, want)
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
	if _, err := b.controller.Release(ctx, r.ID); !errorIs(err, ErrNoContext) {
		t.Errorf("a second Release: %v", err)
	}
}
