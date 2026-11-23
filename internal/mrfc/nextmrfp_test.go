package mrfc_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// The MRFC asks the next MRFP of its list when the one its hash picks
// gives no context: it does not answer, it has no port left, or an
// operator has taken it out of service (TS 23.333 6.1.2).
//
// Which MRFP the hash picks depends on the Call-ID or the conference
// ID, so these tests put the refusal where the walk is rather than on a
// named MRFP: every MRFP of counted refuses the first Add of the test,
// whichever one the hash asked first, and serves the next.

// counted wraps the Mp operations of an MRFP and counts the Adds that
// reach it. Every MRFP of a test shares one asked, so that the count is
// the length of the walk over the list, and refuseFirst makes the MRFP
// that the walk asks first give no context.
type counted struct {
	mrfc.MRFP
	asked        *atomic.Int32
	refuseFirst  bool
	refuseAlways bool
}

func (c counted) Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error) {
	n := c.asked.Add(1)
	if c.refuseAlways || (c.refuseFirst && n == 1) {
		return agw.Context{}, agw.ErrNoPort
	}
	return c.MRFP.Add(ctx, terminations...)
}

// countedMRFPs are len(media) MRFPs whose Mp operations are counted, in
// the order an MRFC lists them.
func countedMRFPs(t *testing.T, how counted, media ...testMRFP) ([]mrfc.Processor, *atomic.Int32) {
	t.Helper()
	asked := &atomic.Int32{}
	var processors []mrfc.Processor
	for i, m := range media {
		p := m.processor(string(rune('a' + i)))
		p.Control = counted{MRFP: p.Control, asked: asked, refuseFirst: how.refuseFirst, refuseAlways: how.refuseAlways}
		processors = append(processors, p)
	}
	return processors, asked
}

// TestAnnouncementTakesTheNextMRFP: the MRFP that the Call-ID picks
// gives no context, and the announcement plays from the next MRFP of
// the list.
func TestAnnouncementTakesTheNextMRFP(t *testing.T) {
	first, second := startMRFP(t), startMRFP(t)
	processors, asked := countedMRFPs(t, counted{refuseFirst: true}, first, second)
	m := startMRFC(t, mrfc.NewMemoryStore(), processors...)
	as := newUA(t, "mmtel", nil)
	caller := party(t)

	out := invite(t, as, "sip:annc@"+m.host+";play=/provisioned/notice", offerFrom(t, addrOf(caller), "sendrecv"))
	ok := final(t, out)
	if ok.StatusCode != 200 {
		t.Fatalf("got %d, want 200 from the next MRFP", ok.StatusCode)
	}
	if n := asked.Load(); n != 2 {
		t.Errorf("the MRFC asked %d MRFPs for a context, want both of them", n)
	}
	if len(first.relay.Contexts())+len(second.relay.Contexts()) != 1 {
		t.Errorf("the MRFPs hold %v and %v, want the announcement in one of them",
			first.relay.Contexts(), second.relay.Contexts())
	}
	out.Bye()
}

// TestConferenceTakesTheNextMRFP: the MRFP that the conference ID picks
// gives no context, and the mix is made on the next MRFP of the list.
func TestConferenceTakesTheNextMRFP(t *testing.T) {
	first, second := startMRFP(t), startMRFP(t)
	processors, asked := countedMRFPs(t, counted{refuseFirst: true}, first, second)
	store := mrfc.NewMemoryStore()
	m := startMRFC(t, store, processors...)
	as := newUA(t, "mmtel", nil)

	out := invite(t, as, "sip:conf=x@"+m.host, offerFrom(t, addrOf(party(t)), "sendrecv"))
	ok := final(t, out)
	if ok.StatusCode != 200 {
		t.Fatalf("got %d, want 200 from the next MRFP", ok.StatusCode)
	}
	if n := asked.Load(); n != 2 {
		t.Errorf("the MRFC asked %d MRFPs for a context, want both of them", n)
	}
	media, held, err := store.Conference(context.Background(), "x")
	if err != nil || !held {
		t.Fatalf("the Store holds no conference: %v", err)
	}
	holder := first
	if len(second.relay.Contexts()) == 1 {
		holder = second
	}
	if cs := holder.relay.Contexts(); len(cs) != 1 || cs[0].ID != media.Context {
		t.Errorf("the MRFPs hold %v and %v, want the context %s that the Store names",
			first.relay.Contexts(), second.relay.Contexts(), media.Context)
	}
	out.Bye()
}

// TestTheMRFCRefusesWhenNoMRFPGivesAContext: the MRFC asks every MRFP
// of its list and answers 503 (Service Unavailable) once none is left,
// as it did with one MRFP that gave no context.
func TestTheMRFCRefusesWhenNoMRFPGivesAContext(t *testing.T) {
	processors, asked := countedMRFPs(t, counted{refuseAlways: true}, startMRFP(t), startMRFP(t), startMRFP(t))
	m := startMRFC(t, mrfc.NewMemoryStore(), processors...)
	as := newUA(t, "mmtel", nil)

	for _, requestURI := range []string{
		"sip:annc@" + m.host + ";play=/provisioned/notice",
		"sip:conf=x@" + m.host,
	} {
		asked.Store(0)
		resp := final(t, invite(t, as, requestURI, offerFrom(t, addrOf(party(t)), "sendrecv")))
		if resp.StatusCode != 503 {
			t.Errorf("%s got %d, want 503", requestURI, resp.StatusCode)
		}
		if n := asked.Load(); n != 3 {
			t.Errorf("%s: the MRFC asked %d MRFPs for a context, want all three", requestURI, n)
		}
	}
}
