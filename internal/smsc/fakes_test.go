package smsc

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	rdaResult sgd.Result
}

func (h *fakeHSS) ReportSMDeliveryStatus(_ context.Context, req sgd.ReportSMDeliveryStatusRequest) (sgd.ReportSMDeliveryStatusAnswer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rdr = append(h.rdr, req)
	if h.down {
		return sgd.ReportSMDeliveryStatusAnswer{}, errors.New("no route to the HSS")
	}
	if h.rdaResult == (sgd.Result{}) {
		return sgd.ReportSMDeliveryStatusAnswer{Result: sgd.Base(sgd.DiameterSuccess)}, nil
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
	return sgd.SendRoutingInfoForSMAnswer{Result: sgd.Experimental(sgd.DiameterErrorUserUnknown)}, nil
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
		Result:      sgd.Base(sgd.DiameterSuccess),
		UserName:    imsiOf[msisdn],
		ServingNode: &sgd.ServingNode{MMEName: mmeHost, MMERealm: epcRealm, MMENumberForMTSMS: mmeNumber},
	}
}

// fakeNodes are the serving nodes behind SGd. Each answers the TFR with
// what its function gives, and the TFRs are recorded.
type fakeNodes struct {
	mu     sync.Mutex
	answer map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error)
	tfr    []sgd.MTForwardShortMessageRequest
}

func (n *fakeNodes) MTForwardShortMessage(_ context.Context, req sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
	n.mu.Lock()
	n.tfr = append(n.tfr, req)
	f := n.answer[req.DestinationHost]
	n.mu.Unlock()
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

func (n *fakeNodes) sent() []sgd.MTForwardShortMessageRequest {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]sgd.MTForwardShortMessageRequest(nil), n.tfr...)
}

// answerWith is a node that always gives result.
func answerWith(result sgd.Result) func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
	return func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		return sgd.MTForwardShortMessageAnswer{Result: result}, nil
	}
}

var (
	accepts = answerWith(sgd.Base(sgd.DiameterSuccess))
	absent  = answerWith(sgd.Experimental(sgd.DiameterErrorAbsentUser))
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
		nodes: &fakeNodes{answer: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){}},
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
	tpdu, err := s.Marshal()
	if err != nil {
		n.t.Fatal(err)
	}
	ofa := n.sc.MOForwardShortMessage(context.Background(), sgd.MOForwardShortMessageRequest{
		Header:         sgd.Header{SessionID: mmeHost + ";1;1", OriginHost: mmeHost, OriginRealm: epcRealm, DestinationRealm: epcRealm},
		SCAddress:      scAddress,
		UserIdentifier: sgd.UserIdentifier{UserName: imsiOf[alice], MSISDN: alice},
		SMRPUI:         tpdu,
	})
	n.sc.Wait()
	return ofa
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
		Header:         sgd.Header{SessionID: "hss." + epcRealm + ";3;1", OriginHost: "hss." + epcRealm, OriginRealm: epcRealm, DestinationRealm: epcRealm},
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
