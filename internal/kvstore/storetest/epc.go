package storetest

import (
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

// EPC runs the suite of epc.Store.
func EPC(t *testing.T, newPair func(t *testing.T) (epc.Store, epc.Store)) {
	run(t, newPair, []testCase[epc.Store]{
		{"PDNConnection", epcPDNConnection},
		{"UpdateThatFailsKeepsNothing", epcUpdateFails},
		{"ConcurrentUpdatesOfOneConnection", epcConcurrentUpdates},
		{"OnlyOneTakesAConnection", epcOnlyOneTakes},
		{"Binding", epcBinding},
	})
}

var ueAddress = netip.MustParseAddr("10.45.0.2")

func pdnConnection() epc.PDNConnection {
	return epc.PDNConnection{IMSI: "001010000000001", Address: ueAddress, APN: "ims",
		DefaultBearer: epc.Bearer{EPSBearerIdentity: 5, State: epc.BearerActive, QoS: epc.EPSQoS{QCI: 5}},
		AFSessions: []epc.AFSession{{SessionID: "pcscf;1;sig", AFHost: "pcscf", AFRealm: "ims.mnc001.mcc001.3gppnetwork.org",
			Signalling: true, SpecificActions: []rx.SpecificAction{rx.IndicationOfLossOfBearer}}}}
}

func epcPDNConnection(t *testing.T, a, b epc.Store) {
	c := pdnConnection()
	a.PutPDNConnection(c)
	if got, ok := b.PDNConnection(ueAddress); !ok || !sameJSON(t, got, c) {
		t.Fatalf("PDNConnection = %+v, %v; want %+v", got, ok, c)
	}
	got, err := b.UpdatePDNConnection(ueAddress, func(c *epc.PDNConnection) error {
		c.VoiceBearer = &epc.Bearer{EPSBearerIdentity: 6, State: epc.BearerActivePending, QoS: epc.EPSQoS{QCI: 1}}
		return nil
	})
	if err != nil || got.VoiceBearer == nil {
		t.Fatalf("UpdatePDNConnection = %+v, %v", got, err)
	}
	if got, _ := a.PDNConnection(ueAddress); got.VoiceBearer == nil || got.VoiceBearer.EPSBearerIdentity != 6 {
		t.Errorf("the update is not kept: %+v", got)
	}
	if got, ok := a.TakePDNConnection(ueAddress); !ok || got.IMSI != c.IMSI {
		t.Errorf("TakePDNConnection = %+v, %v", got, ok)
	}
	if _, ok := b.PDNConnection(ueAddress); ok {
		t.Error("PDNConnection after TakePDNConnection")
	}
	if _, err := b.UpdatePDNConnection(ueAddress, func(*epc.PDNConnection) error { return nil }); !errors.Is(err, epc.ErrNoPDNConnection) {
		t.Errorf("UpdatePDNConnection of no connection = %v, want ErrNoPDNConnection", err)
	}
}

func epcUpdateFails(t *testing.T, a, b epc.Store) {
	a.PutPDNConnection(pdnConnection())
	refused := errors.New("refused")
	if _, err := b.UpdatePDNConnection(ueAddress, func(c *epc.PDNConnection) error {
		c.APN = "changed"
		return refused
	}); !errors.Is(err, refused) {
		t.Errorf("UpdatePDNConnection = %v, want the error of change", err)
	}
	if got, _ := a.PDNConnection(ueAddress); got.APN != "ims" {
		t.Errorf("APN = %q after a failed change", got.APN)
	}
}

// epcConcurrentUpdates: two instances bind Rx sessions to one PDN
// connection at once, as two AARs of one UE may reach two PCRF
// instances. No session may be lost.
func epcConcurrentUpdates(t *testing.T, a, b epc.Store) {
	a.PutPDNConnection(pdnConnection())
	const n = 20
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		for j := range n {
			s.UpdatePDNConnection(ueAddress, func(c *epc.PDNConnection) error {
				c.AFSessions = append(c.AFSessions, epc.AFSession{SessionID: fmt.Sprintf("pcscf;%d;%d", i, j)})
				return nil
			})
		}
	})
	if got, _ := a.PDNConnection(ueAddress); len(got.AFSessions) != 1+2*n {
		t.Errorf("%d AF sessions, want %d", len(got.AFSessions), 1+2*n)
	}
}

func epcOnlyOneTakes(t *testing.T, a, b epc.Store) {
	for range 10 {
		a.PutPDNConnection(pdnConnection())
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := s.TakePDNConnection(ueAddress); ok {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the connection, want 1", got)
		}
	}
}

func epcBinding(t *testing.T, a, b epc.Store) {
	a.PutBinding("pcscf;1;media", epc.Binding{Address: ueAddress})
	if got, ok := b.Binding("pcscf;1;media"); !ok || got.Address != ueAddress || got.Aborted {
		t.Fatalf("Binding = %+v, %v", got, ok)
	}
	b.PutBinding("pcscf;1;media", epc.Binding{Address: ueAddress, Aborted: true})
	if got, _ := a.Binding("pcscf;1;media"); !got.Aborted {
		t.Error("Aborted is not kept")
	}
	a.DeleteBinding("pcscf;1;media")
	if _, ok := b.Binding("pcscf;1;media"); ok {
		t.Error("Binding after DeleteBinding")
	}
}
