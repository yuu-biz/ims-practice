package epc_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

var storedAddress = netip.MustParseAddr("10.45.0.2")

func storedConnection() epc.PDNConnection {
	return epc.PDNConnection{IMSI: imsi1, Address: storedAddress, APN: "ims",
		DefaultBearer: epc.Bearer{EPSBearerIdentity: 5, State: epc.BearerActive, QoS: epc.EPSQoS{QCI: 5}},
		AFSessions: []epc.AFSession{{SessionID: "pcscf;1;sig", AFHost: "pcscf", AFRealm: realm,
			Signalling: true, SpecificActions: []rx.SpecificAction{rx.IndicationOfLossOfBearer}}}}
}

func TestMemoryStorePDNConnection(t *testing.T) {
	s := epc.NewMemoryStore()
	c := storedConnection()
	s.PutPDNConnection(c)
	got, ok := s.PDNConnection(storedAddress)
	if want, _ := json.Marshal(c); !ok || string(mustJSON(t, got)) != string(want) {
		t.Fatalf("PDNConnection = %+v, %v; want %+v", got, ok, c)
	}
	got, err := s.UpdatePDNConnection(storedAddress, func(c *epc.PDNConnection) error {
		c.VoiceBearer = &epc.Bearer{EPSBearerIdentity: 6, State: epc.BearerActivePending, QoS: epc.EPSQoS{QCI: 1}}
		return nil
	})
	if err != nil || got.VoiceBearer == nil {
		t.Fatalf("UpdatePDNConnection = %+v, %v", got, err)
	}
	if got, _ := s.PDNConnection(storedAddress); got.VoiceBearer == nil || got.VoiceBearer.EPSBearerIdentity != 6 {
		t.Errorf("the update is not kept: %+v", got)
	}
	if got, ok := s.TakePDNConnection(storedAddress); !ok || got.IMSI != c.IMSI {
		t.Errorf("TakePDNConnection = %+v, %v", got, ok)
	}
	if _, ok := s.PDNConnection(storedAddress); ok {
		t.Error("PDNConnection after TakePDNConnection")
	}
	if _, err := s.UpdatePDNConnection(storedAddress, func(*epc.PDNConnection) error { return nil }); !errors.Is(err, epc.ErrNoPDNConnection) {
		t.Errorf("UpdatePDNConnection of no connection = %v, want ErrNoPDNConnection", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMemoryStoreUpdateThatFailsKeepsNothing(t *testing.T) {
	s := epc.NewMemoryStore()
	s.PutPDNConnection(storedConnection())
	refused := errors.New("refused")
	if _, err := s.UpdatePDNConnection(storedAddress, func(c *epc.PDNConnection) error {
		c.APN = "changed"
		return refused
	}); !errors.Is(err, refused) {
		t.Errorf("UpdatePDNConnection = %v, want the error of change", err)
	}
	if got, _ := s.PDNConnection(storedAddress); got.APN != "ims" {
		t.Errorf("APN = %q after a failed change", got.APN)
	}
}

// Two AARs of one UE bind Rx sessions to its PDN connection at once. No
// session may be lost.
func TestMemoryStoreConcurrentUpdatesOfOneConnection(t *testing.T) {
	s := epc.NewMemoryStore()
	s.PutPDNConnection(storedConnection())
	const n = 20
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			for j := range n {
				s.UpdatePDNConnection(storedAddress, func(c *epc.PDNConnection) error {
					c.AFSessions = append(c.AFSessions, epc.AFSession{SessionID: fmt.Sprintf("pcscf;%d;%d", i, j)})
					return nil
				})
			}
		})
	}
	wg.Wait()
	if got, _ := s.PDNConnection(storedAddress); len(got.AFSessions) != 1+2*n {
		t.Errorf("%d AF sessions, want %d", len(got.AFSessions), 1+2*n)
	}
}

func TestMemoryStoreOnlyOneTakesAConnection(t *testing.T) {
	s := epc.NewMemoryStore()
	for range 10 {
		s.PutPDNConnection(storedConnection())
		var takes atomic.Int32
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				if _, ok := s.TakePDNConnection(storedAddress); ok {
					takes.Add(1)
				}
			})
		}
		wg.Wait()
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d callers took the connection, want 1", got)
		}
	}
}

func TestMemoryStoreBinding(t *testing.T) {
	s := epc.NewMemoryStore()
	s.PutBinding("pcscf;1;media", epc.Binding{Address: storedAddress})
	if got, ok := s.Binding("pcscf;1;media"); !ok || got.Address != storedAddress || got.Aborted {
		t.Fatalf("Binding = %+v, %v", got, ok)
	}
	s.PutBinding("pcscf;1;media", epc.Binding{Address: storedAddress, Aborted: true})
	if got, _ := s.Binding("pcscf;1;media"); !got.Aborted {
		t.Error("Aborted is not kept")
	}
	s.DeleteBinding("pcscf;1;media")
	if _, ok := s.Binding("pcscf;1;media"); ok {
		t.Error("Binding after DeleteBinding")
	}
}
