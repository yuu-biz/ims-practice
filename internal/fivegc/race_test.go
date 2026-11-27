package fivegc_test

import (
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/fivegc"
)

// pausingStore holds the PDU session write of a PDU session
// establishment until the test lets it go, so that a test chooses what
// happens between the check of the registration and the write.
type pausingStore struct {
	fivegc.Store
	reached chan struct{}
	proceed chan struct{}
}

func (s *pausingStore) PutPDUSessionIfRegistered(p fivegc.PDUSession) error {
	close(s.reached)
	<-s.proceed
	return s.Store.PutPDUSessionIfRegistered(p)
}

// A deregistration that completes after the PDU session establishment
// has checked the registration, and before it keeps the session, must
// not leave a PDU session that belongs to the removed registration.
func TestDeregistrationDuringEstablishmentLeavesNoPDUSession(t *testing.T) {
	inner := fivegc.NewMemoryStore()
	store := &pausingStore{Store: inner, reached: make(chan struct{}), proceed: make(chan struct{})}
	stub := fivegc.New(stubConf, store, quiet())
	if _, err := stub.Register(t.Context(), imsi1, fivegc.RegistrationRequest{FiveGSRegistrationType: fivegc.InitialRegistration}); err != nil {
		t.Fatal(err)
	}

	established := make(chan error, 1)
	go func() {
		_, err := stub.EstablishPDUSession(t.Context(), imsi1, establishment(fivegc.IPv4))
		established <- err
	}()
	select {
	case <-store.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the establishment did not reach the write of the PDU session")
	}

	if _, err := stub.Deregister(t.Context(), imsi1, fivegc.DeregistrationRequest{
		DeregistrationType: fivegc.DeregistrationType{AccessType: fivegc.Access3GPP}}); err != nil {
		t.Fatal(err)
	}
	close(store.proceed)

	select {
	case err := <-established:
		if !errors.Is(err, fivegc.ErrNotRegistered) {
			t.Errorf("establishment after the deregistration: %v, want %v", err, fivegc.ErrNotRegistered)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the establishment did not finish")
	}
	if c, ok := inner.UEContext(imsi1); ok {
		t.Errorf("UE context after the deregistration = %+v", c)
	}
	if s, ok := inner.PDUSession(ue1Addr); ok {
		t.Errorf("PDU session left without a registration = %+v", s)
	}
}
