package mediactl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

var (
	mrfcMId = h248.MId{DomainName: "mrfc1.ims.example.net"}
	mrfpMId = h248.MId{DomainName: "mrfp1.ims.example.net"}
)

// fakeProcessor is a Processor that keeps its contexts in a map. Its
// local addresses count up from 198.51.100.20:42000. It knows the
// announcements "notice" and "welcome".
type fakeProcessor struct {
	mu sync.Mutex
	// made counts the contexts made, and opened the terminations.
	made, opened int
	contexts     map[string]*fakeProcessorContext
	// fail, when set, is returned by every call.
	fail error
}

type fakeProcessorContext struct {
	chosen int
	terms  map[agw.TerminationID]*fakeProcessorTermination
}

// fakeProcessorTermination is what the processor was asked to do with
// a termination.
type fakeProcessorTermination struct {
	local       netip.AddrPort
	mode        agw.Mode
	media       agw.Media
	remote      netip.AddrPort
	remoteMedia agw.Media
	signals     *agw.Signals
	events      *agw.Events
}

func newFakeProcessor() *fakeProcessor {
	return &fakeProcessor{contexts: map[string]*fakeProcessorContext{}}
}

func (p *fakeProcessor) check(t agw.TerminationRequest, add bool) error {
	if add && t.Mode == "" {
		// The media node would take it as sendrecv.
		return errors.New("fake processor: an Add without a mode")
	}
	if t.Signals != nil && t.Signals.Play != nil && t.Signals.Play.Announcement != "notice" && t.Signals.Play.Announcement != "welcome" {
		return agw.ErrUnknownAnnouncement
	}
	return nil
}

// open is a new termination of c, with the ID that t asks for or else
// one that it chooses. p.mu is held.
func (p *fakeProcessor) open(c *fakeProcessorContext, t agw.TerminationRequest) agw.Termination {
	id := t.ID
	if id == "" {
		c.chosen++
		id = agw.TerminationID(fmt.Sprintf("t%d", c.chosen))
	}
	p.opened++
	local := netip.AddrPortFrom(netip.MustParseAddr("198.51.100.20"), uint16(42000+2*(p.opened-1)))
	c.terms[id] = &fakeProcessorTermination{local: local, mode: t.Mode, media: t.LocalMedia, remote: t.Remote, remoteMedia: t.RemoteMedia, signals: t.Signals, events: t.Events}
	return agw.Termination{ID: id, Mode: t.Mode, Local: local, Remote: t.Remote}
}

func (p *fakeProcessor) Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return agw.Context{}, p.fail
	}
	for _, t := range terminations {
		if err := p.check(t, true); err != nil {
			return agw.Context{}, err
		}
	}
	p.made++
	id := fmt.Sprintf("c%d", p.made)
	c := &fakeProcessorContext{terms: map[agw.TerminationID]*fakeProcessorTermination{}}
	out := agw.Context{ID: id, Terminations: map[agw.TerminationID]*agw.Termination{}}
	for _, t := range terminations {
		opened := p.open(c, t)
		out.Terminations[opened.ID] = &opened
	}
	p.contexts[id] = c
	return out, nil
}

func (p *fakeProcessor) AddTermination(ctx context.Context, contextID string, t agw.TerminationRequest) (agw.Termination, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return agw.Termination{}, p.fail
	}
	c, ok := p.contexts[contextID]
	if !ok {
		return agw.Termination{}, agw.ErrNoContext
	}
	if err := p.check(t, true); err != nil {
		return agw.Termination{}, err
	}
	return p.open(c, t), nil
}

func (p *fakeProcessor) Modify(ctx context.Context, contextID string, id agw.TerminationID, change agw.TerminationRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	c, ok := p.contexts[contextID]
	if !ok {
		return agw.ErrNoContext
	}
	t, ok := c.terms[id]
	if !ok {
		return agw.ErrNoTermination
	}
	if err := p.check(change, false); err != nil {
		return err
	}
	if change.Mode != "" {
		t.mode = change.Mode
	}
	if change.Remote.IsValid() {
		t.remote, t.remoteMedia = change.Remote, change.RemoteMedia
	}
	if change.Signals != nil {
		t.signals = change.Signals
	}
	if change.Events != nil {
		t.events = change.Events
	}
	return nil
}

// SubtractTermination reports made up counts: 250 packets each way.
func (p *fakeProcessor) SubtractTermination(ctx context.Context, contextID string, id agw.TerminationID) (agw.Termination, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return agw.Termination{}, false, p.fail
	}
	c, ok := p.contexts[contextID]
	if !ok {
		return agw.Termination{}, false, agw.ErrNoContext
	}
	if _, ok := c.terms[id]; !ok {
		return agw.Termination{}, false, agw.ErrNoTermination
	}
	delete(c.terms, id)
	deleted := len(c.terms) == 0
	if deleted {
		delete(p.contexts, contextID)
	}
	return agw.Termination{ID: id, Packets: 250, PacketsSent: 250}, deleted, nil
}

// termination returns what the processor holds for a termination.
func (p *fakeProcessor) termination(contextID string, id agw.TerminationID) (fakeProcessorTermination, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.contexts[contextID]
	if !ok {
		return fakeProcessorTermination{}, false
	}
	t, ok := c.terms[id]
	if !ok {
		return fakeProcessorTermination{}, false
	}
	return *t, true
}

// only returns the one context of the processor, and its terminations.
func (p *fakeProcessor) only(t *testing.T) (string, map[agw.TerminationID]fakeProcessorTermination) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.contexts) != 1 {
		t.Fatalf("the processor has %d contexts, want 1", len(p.contexts))
	}
	for id, c := range p.contexts {
		terms := map[agw.TerminationID]fakeProcessorTermination{}
		for tid, term := range c.terms {
			terms[tid] = *term
		}
		return id, terms
	}
	return "", nil
}

func (p *fakeProcessor) contextCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.contexts)
}

// notifications collects what ControllerConfig.Notify receives.
type notifications struct {
	mu   sync.Mutex
	list []Notification
}

func (n *notifications) add(x Notification) {
	n.mu.Lock()
	n.list = append(n.list, x)
	n.mu.Unlock()
}

func (n *notifications) all() []Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Notification(nil), n.list...)
}

// mpTestbed is a testbed with an MRFC and an MRFP.
type mpTestbed struct {
	*testbed
	processor *fakeProcessor
	notified  *notifications
}

func newMpTestbed(t *testing.T, config ControllerConfig) *mpTestbed {
	t.Helper()
	clock := newFakeClock()
	b := &mpTestbed{
		testbed:   &testbed{clock: clock, net: newMemNetwork(clock)},
		processor: newFakeProcessor(),
		notified:  &notifications{},
	}
	config.Profile = MpProfile
	config.Notify = b.notified.add
	config.Endpoint.MId = mrfcMId
	config.Endpoint.Clock = b.clock
	config.Endpoint.FirstTransactionID = 1
	config.Endpoint.Timers = testTimers
	c, err := NewController(b.net.listen(controllerAddr), config)
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve()
	t.Cleanup(func() { c.Close() })
	b.controller = c

	g, err := NewGateway(b.net.listen(gatewayAddr), GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: mrfpMId, Clock: b.clock, FirstTransactionID: 5001, Timers: testTimers},
		LocateController: func() (net.Addr, error) { return memAddr(controllerAddr), nil },
		Processor:        b.processor,
		MaxWaitingDelay:  10 * time.Millisecond,
		FirstContextID:   200,
	})
	if err != nil {
		t.Fatal(err)
	}
	go g.Serve()
	t.Cleanup(func() { g.Close() })
	b.gateway = g
	return b
}

func (p *fakeProcessor) setFail(err error) {
	p.mu.Lock()
	p.fail = err
	p.mu.Unlock()
}
