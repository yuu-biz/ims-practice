package ue_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// TestRegistrationSurvivesTheLossOfAnHSSInstance: the HSS runs as two
// instances on one shared store, and the test stops the one that the
// CSCFs send to. The registration that follows still succeeds, over the
// Cx of the other instance, which reads the same subscriber and writes
// the same registration state (TS 29.228 6.4). This is the failover of
// RFC 6733 5.5.4: the CSCFs keep the instance order of their
// configuration and send the request again to the next one.
func TestRegistrationSurvivesTheLossOfAnHSSInstance(t *testing.T) {
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour, hssInstances: 2})
	if len(n.hss) != 2 {
		t.Fatalf("the network started %d instances of the HSS", len(n.hss))
	}
	ctx := context.Background()

	// The registration before the loss goes to the first instance.
	u := newUE(t, n.pcscf, password)
	if _, err := u.Register(ctx); err != nil {
		t.Fatalf("registration while both instances run: %v", err)
	}
	if err := u.Deregister(ctx); err != nil {
		t.Fatalf("deregistration while both instances run: %v", err)
	}

	// Stop sends a DPR and waits for the DPA, so the CSCFs know the
	// connection is gone before the next REGISTER, as they would once the
	// watchdog of a lost instance expires.
	n.hss[0].server.Stop()

	again := newUE(t, n.pcscf, password)
	reg, err := again.Register(ctx)
	if err != nil {
		t.Fatalf("registration after the first instance stopped: %v", err)
	}
	if len(reg.ServiceRoute) != 1 || !slices.Contains(reg.PublicIdentities, impu) {
		t.Errorf("registration = %+v", reg)
	}

	// The instance that is left holds the registration state the other
	// one would have held: both read and write the one store.
	if state := registrationState(t, n, impi); state != "Registered" {
		t.Errorf("the HSS has the user as %q after the registration", state)
	}
}

// registrationState is what the provisioning API of the HSS says about
// the subscription of privateID.
func registrationState(t *testing.T, n testNetwork, privateID string) string {
	t.Helper()
	w := httptest.NewRecorder()
	n.provisioning.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/subscriptions/"+privateID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET the subscription: %d %s", w.Code, w.Body)
	}
	var got struct {
		RegistrationState string `json:"registration_state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%s: %v", w.Body, err)
	}
	return got.RegistrationState
}
