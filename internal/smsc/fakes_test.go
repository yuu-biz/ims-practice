package smsc

import (
	"context"
	"errors"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// The network of the tests. All identities are fictitious: PLMN 001/01
// and numbers from +1 202 555 0100 to 0199.
const (
	scAddress = "12025550100"
	epcRealm  = "epc.mnc001.mcc001.3gppnetwork.org"
	imsRealm  = "ims.mnc001.mcc001.3gppnetwork.org"
	mmeHost   = "mme." + epcRealm
	mmeNumber = "12025550190"
	smsfHost  = "smsf." + epcRealm
	ipsmgw    = "ipsmgw." + imsRealm
)

// imsiOf is the IMSI the fake HSS gives each user.
var imsiOf = map[string]string{alice: "001010000000001", bob: "001010000000002", carol: "001010000000003"}

// fakeClock is a Clock that moves only when the test says.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeHSS answers SRR from what the test set, and records the requests.
type fakeHSS struct {
	mu sync.Mutex
	// answers gives the SRA for each MSISDN. An MSISDN without one is a
	// user unknown.
	answers map[string]sgd.SendRoutingInfoForSMAnswer
	down    bool
	srr     []sgd.SendRoutingInfoForSMRequest
	rdr     []sgd.ReportSMDeliveryStatusRequest
	// rdaResult is the result of every RDA; zero means success.
	rdaResult diameter.Result
}

func (h *fakeHSS) ReportSMDeliveryStatus(_ context.Context, req sgd.ReportSMDeliveryStatusRequest) (sgd.ReportSMDeliveryStatusAnswer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rdr = append(h.rdr, req)
	if h.down {
		return sgd.ReportSMDeliveryStatusAnswer{}, errors.New("no route to the HSS")
	}
	if h.rdaResult == (diameter.Result{}) {
		return sgd.ReportSMDeliveryStatusAnswer{Result: diameter.Base(diameter.DiameterSuccess)}, nil
	}
	return sgd.ReportSMDeliveryStatusAnswer{Result: h.rdaResult}, nil
}

func (h *fakeHSS) reports() []sgd.ReportSMDeliveryStatusRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]sgd.ReportSMDeliveryStatusRequest(nil), h.rdr...)
}

func (h *fakeHSS) SendRoutingInfoForSM(_ context.Context, req sgd.SendRoutingInfoForSMRequest) (sgd.SendRoutingInfoForSMAnswer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.srr = append(h.srr, req)
	if h.down {
		return sgd.SendRoutingInfoForSMAnswer{}, errors.New("no route to the HSS")
	}
	if a, ok := h.answers[req.MSISDN]; ok {
		return a, nil
	}
	return sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorUserUnknown)}, nil
}

func (h *fakeHSS) set(msisdn string, a sgd.SendRoutingInfoForSMAnswer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answers[msisdn] = a
}

func (h *fakeHSS) srrCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.srr)
}

// servedByMME is the SRA for a user that the MME serves.
func servedByMME(msisdn string) sgd.SendRoutingInfoForSMAnswer {
	return sgd.SendRoutingInfoForSMAnswer{
		Result:      diameter.Base(diameter.DiameterSuccess),
		UserName:    imsiOf[msisdn],
		ServingNode: &sgd.ServingNode{MMEName: mmeHost, MMERealm: epcRealm, MMENumberForMTSMS: mmeNumber},
	}
}

// fakeNodes are the serving nodes behind SGd. Each answers the TFR with
// what its function gives, and the TFRs are recorded.
type fakeNodes struct {
	mu     sync.Mutex
	answer map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error)
	// silent are the nodes that answer no TFR: the SC waits for the TFA
	// until it gives the attempt up.
	silent map[string]bool
	// gone are the nodes that the SC has no connection to. A TFR for one
	// does not go out, so it is not recorded.
	gone map[string]bool
	tfr  []sgd.MTForwardShortMessageRequest
}

func (n *fakeNodes) MTForwardShortMessage(ctx context.Context, req sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
	n.mu.Lock()
	if n.gone[req.DestinationHost] {
		n.mu.Unlock()
		return sgd.MTForwardShortMessageAnswer{}, diameter.ErrPeerUnavailable
	}
	n.tfr = append(n.tfr, req)
	f := n.answer[req.DestinationHost]
	silent := n.silent[req.DestinationHost]
	n.mu.Unlock()
	if silent {
		<-ctx.Done()
		return sgd.MTForwardShortMessageAnswer{}, ctx.Err()
	}
	if f == nil {
		return sgd.MTForwardShortMessageAnswer{}, errors.New("no route to " + req.DestinationHost)
	}
	return f(req)
}

func (n *fakeNodes) set(host string, f func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.answer[host] = f
}

// ServingNodes are the nodes of realm that set gave an answer, less
// those that are gone. A node is named inside its realm.
func (n *fakeNodes) ServingNodes(realm string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var hosts []string
	for host := range n.answer {
		if realmOf(host, "") == realm && !n.gone[host] {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	return hosts
}

// disconnect takes away the connection of the SC to host.
func (n *fakeNodes) disconnect(host string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gone[host] = true
}

// silence has host answer no TFR while on holds, and answer as set says
// once it does not.
func (n *fakeNodes) silence(host string, on bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.silent[host] = on
}

func (n *fakeNodes) sent() []sgd.MTForwardShortMessageRequest {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]sgd.MTForwardShortMessageRequest(nil), n.tfr...)
}

// answerWith is a node that always gives result.
func answerWith(result diameter.Result) func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
	return func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		return sgd.MTForwardShortMessageAnswer{Result: result}, nil
	}
}

var (
	accepts = answerWith(diameter.Base(diameter.DiameterSuccess))
	absent  = answerWith(diameter.Experimental(sgd.DiameterErrorAbsentUser))
)

// network is an SC with a fake HSS and fake serving nodes. Alice, Bob
// and Carol are served by the MME, which accepts every short message.
type network struct {
	t     *testing.T
	clock *fakeClock
	hss   *fakeHSS
	nodes *fakeNodes
	store *MemoryStore
	sc    *SMSC
}

func newNetwork(t *testing.T) *network {
	n := &network{
		t:     t,
		clock: &fakeClock{now: t0},
		hss:   &fakeHSS{answers: map[string]sgd.SendRoutingInfoForSMAnswer{}},
		nodes: &fakeNodes{answer: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){},
			silent: map[string]bool{}, gone: map[string]bool{}},
		store: NewMemoryStore(),
	}
	for _, user := range []string{alice, bob, carol} {
		n.hss.set(user, servedByMME(user))
	}
	n.nodes.set(mmeHost, accepts)
	n.sc = n.newSC()
	return n
}

// newSC is one more SC instance on the same store, HSS and nodes.
func (n *network) newSC() *SMSC {
	return New(Config{Address: scAddress, HSSRealm: epcRealm}, n.store, n.nodes, n.hss, n.clock, quietLog)
}

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// submit sends Alice's OFR with an SMS-SUBMIT of text for to, and
// waits for the deliveries that it starts.
func (n *network) submit(to string, s sms.Submit) sgd.MOForwardShortMessageAnswer {
	n.t.Helper()
	ofa := n.submitTo(n.sc, s)
	n.sc.Wait()
	return ofa
}

// submitTo sends sc Alice's OFR with the SMS-SUBMIT s, and returns its
// answer without waiting for the delivery that it starts.
func (n *network) submitTo(sc *SMSC, s sms.Submit) sgd.MOForwardShortMessageAnswer {
	n.t.Helper()
	tpdu, err := s.Marshal()
	if err != nil {
		n.t.Fatal(err)
	}
	return sc.MOForwardShortMessage(context.Background(), sgd.MOForwardShortMessageRequest{
		Header:         diameter.Header{SessionID: mmeHost + ";1;1", OriginHost: mmeHost, OriginRealm: epcRealm, DestinationRealm: epcRealm},
		SCAddress:      scAddress,
		UserIdentifier: sgd.UserIdentifier{UserName: imsiOf[alice], MSISDN: alice},
		SMRPUI:         tpdu,
	})
}

// cutStore is a Store that fails a call whose context has ended before
// it is made, as a store outside the process does: the client of
// internal/kvstore gives the call up when its context ends.
type cutStore struct{ *MemoryStore }

func (s cutStore) Enqueue(ctx context.Context, m Message, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemoryStore.Enqueue(ctx, m, now)
}

func (s cutStore) ServiceCentreTimeStamp(ctx context.Context, destination string, now time.Time) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	return s.MemoryStore.ServiceCentreTimeStamp(ctx, destination, now)
}

func (s cutStore) ClaimDue(ctx context.Context, now time.Time, lease time.Duration) (Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, false, err
	}
	return s.MemoryStore.ClaimDue(ctx, now, lease)
}

func (s cutStore) Claim(ctx context.Context, destination string, now time.Time, lease time.Duration) (Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return Lease{}, false, err
	}
	return s.MemoryStore.Claim(ctx, destination, now, lease)
}

func (s cutStore) Queue(ctx context.Context, l Lease) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.MemoryStore.Queue(ctx, l)
}

func (s cutStore) Remove(ctx context.Context, l Lease, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemoryStore.Remove(ctx, l, id)
}

func (s cutStore) Release(ctx context.Context, l Lease, next time.Time, attempts int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.MemoryStore.Release(ctx, l, next, attempts)
}

func (s cutStore) Alert(ctx context.Context, destination string, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return s.MemoryStore.Alert(ctx, destination, now)
}

// hello is an SMS-SUBMIT of "hello" to number.
func hello(t *testing.T, number string, mr uint8) sms.Submit {
	t.Helper()
	dcs, ud, err := sms.EncodeText("hello")
	if err != nil {
		t.Fatal(err)
	}
	return sms.Submit{MessageReference: mr, DestinationAddress: sms.InternationalNumber("+" + number), DataCodingScheme: dcs, UserData: ud}
}

// tpdu reads the TPDU of a TFR: an SMS-DELIVER or an SMS-STATUS-REPORT.
func tpdu(t *testing.T, tfr sgd.MTForwardShortMessageRequest) sms.TPDU {
	t.Helper()
	p, err := sms.ParseTPDU(sms.RPDataNetworkToMS{UserData: tfr.SMRPUI})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// alert sends the ALR of the HSS for msisdn, and waits for the
// deliveries that it starts.
func (n *network) alert(scAddress, msisdn string) sgd.AlertServiceCentreAnswer {
	ala := n.sc.AlertServiceCentre(context.Background(), sgd.AlertServiceCentreRequest{
		Header:         diameter.Header{SessionID: "hss." + epcRealm + ";3;1", OriginHost: "hss." + epcRealm, OriginRealm: epcRealm, DestinationRealm: epcRealm},
		SCAddress:      scAddress,
		UserIdentifier: sgd.UserIdentifier{MSISDN: msisdn},
	})
	n.sc.Wait()
	return ala
}

// advance moves the clock and runs what is due.
func (n *network) advance(d time.Duration) {
	n.clock.Advance(d)
	n.sc.ProcessDue(context.Background())
	n.sc.Wait()
}
