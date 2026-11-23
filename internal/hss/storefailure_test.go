package hss_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/hssstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// This file tries the HSS on a store outside the process that fails.
// The HSS answers a Cx or Sh request whose subscriber it cannot read or
// write with DIAMETER_UNABLE_TO_COMPLY (RFC 6733 7.1.5), and the
// provisioning API answers 500 (Internal Server Error). Neither takes
// the failure for a user that does not exist: the CSCFs and the
// application servers would then act on DIAMETER_ERROR_USER_UNKNOWN,
// and the operator on a 404, for a user who is there.

const (
	realm   = "ims.mnc001.mcc001.3gppnetwork.org"
	impi    = "001010000000001@" + realm
	impu    = "sip:001010000000001@" + realm
	akaImpi = "001010000000010@" + realm
	akaImpu = "sip:001010000000010@" + realm
	scscf   = "sip:scscf." + realm + ":5060"
)

var (
	header = diameter.Header{SessionID: "s;1;1", OriginHost: "scscf." + realm, OriginRealm: realm, DestinationRealm: realm}

	unableToComply = diameter.Base(diameter.DiameterUnableToComply)
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// unreachable is a store that the HSS cannot reach: every operation
// fails.
type unreachable struct{ kvstore.Store }

var errUnreachable = errors.New("the store cannot be reached")

func (unreachable) Get(context.Context, string) (kvstore.Item, error) {
	return kvstore.Item{}, errUnreachable
}

func (unreachable) Put(context.Context, string, []byte, time.Duration) (string, error) {
	return "", errUnreachable
}

func (unreachable) CompareAndSwap(context.Context, string, string, []byte, time.Duration) (string, error) {
	return "", errUnreachable
}

func (unreachable) Delete(context.Context, string) error { return errUnreachable }

func (unreachable) CompareAndDelete(context.Context, string, string) error { return errUnreachable }

func (unreachable) AddToIndex(context.Context, string, string) error { return errUnreachable }

func (unreachable) RemoveFromIndex(context.Context, string, string) error { return errUnreachable }

func (unreachable) MembersAfter(context.Context, string, string, int) ([]string, error) {
	return nil, errUnreachable
}

func newHSS(t *testing.T, kv kvstore.Store) *hss.HSS {
	t.Helper()
	return newHSSWithLocks(t, kv, nil)
}

// newHSSWithLocks is newHSS with the lock per subscriber that locks
// names; nil gives the HSS the lock in its own process.
func newHSSWithLocks(t *testing.T, kv kvstore.Store, locks ims.UserLocker) *hss.HSS {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, diameter.Timers{}, quiet())
	h, err := hss.New(hss.Config{Realm: realm, VisitedNetworks: []string{realm}, UserLocks: locks},
		hssstore.New(kv, kvjson.Config{}), node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// failingLocks is a UserLocker that hands out no lock, as the lock in
// the shared store does once the HSS cannot reach it.
type failingLocks struct{}

func (failingLocks) Lock(context.Context, string) (func(), error) { return nil, errUnreachable }

// provisioned is a store that holds a subscriber with a password and
// one with AKA credentials, added through the provisioning API.
func provisioned(t *testing.T) kvstore.Store {
	t.Helper()
	kv := kvstore.NewMemoryStore(nil)
	api := newHSS(t, kv).Provisioning(noSCSCFs{})
	for _, body := range []string{
		`{"private_identity": "` + impi + `", "public_identities": ["` + impu + `", "tel:+12025550101"], "password": "test-password-1"}`,
		`{"private_identity": "` + akaImpi + `", "public_identities": ["` + akaImpu + `"],
			"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf"}}`,
	} {
		if w := do(api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
			t.Fatalf("POST: %d %s", w.Code, w.Body)
		}
	}
	return kv
}

// noSCSCFs is an S-CSCF that no test reaches: no user of the tests
// registers before its subscription goes.
type noSCSCFs struct{}

func (noSCSCFs) Request(context.Context, string, *diam.Message) (*diam.Message, error) {
	return nil, errors.New("no S-CSCF")
}

func do(api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

// exchange sends a request through the wire format and the handler, as
// the Diameter peer would.
func exchange(t *testing.T, h *hss.HSS, m *diam.Message) *diam.Message {
	t.Helper()
	roundTrip := func(m *diam.Message) *diam.Message {
		b, err := m.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		got, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	return roundTrip(h.Handle(roundTrip(m)))
}

// request is one Cx or Sh request and how to read the result of its
// answer.
type request struct {
	name    string
	message func() *diam.Message
	result  func(*diam.Message) (diameter.Result, error)
}

func uaaResult(m *diam.Message) (diameter.Result, error) {
	a, err := cx.ParseUserAuthorizationAnswer(m)
	return a.Result, err
}

func maaResult(m *diam.Message) (diameter.Result, error) {
	a, err := cx.ParseMultimediaAuthAnswer(m)
	return a.Result, err
}

func saaResult(m *diam.Message) (diameter.Result, error) {
	a, err := cx.ParseServerAssignmentAnswer(m)
	return a.Result, err
}

func liaResult(m *diam.Message) (diameter.Result, error) {
	a, err := cx.ParseLocationInfoAnswer(m)
	return a.Result, err
}

func udaResult(m *diam.Message) (diameter.Result, error) {
	a, err := sh.ParseUserDataAnswer(m)
	return a.Result, err
}

func puaResult(m *diam.Message) (diameter.Result, error) {
	a, err := sh.ParseProfileUpdateAnswer(m)
	return a.Result, err
}

func digestMAR() *diam.Message {
	return cx.MultimediaAuthRequest{Header: header, UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: cx.SchemeSIPDigest, SIPNumberAuthItems: 1, ServerName: scscf}.Message()
}

func akaMAR() *diam.Message {
	return cx.MultimediaAuthRequest{Header: header, UserName: akaImpi, PublicIdentity: akaImpu,
		SIPAuthenticationScheme: cx.SchemeDigestAKAv1MD5, SIPNumberAuthItems: 1, ServerName: scscf}.Message()
}

func registrationSAR() *diam.Message {
	return cx.ServerAssignmentRequest{Header: header, UserName: impi, PublicIdentities: []string{impu},
		ServerName: scscf, ServerAssignmentType: cx.AssignRegistration}.Message()
}

func smsRegistrationPUR() *diam.Message {
	return sh.ProfileUpdateRequest{Header: header, UserIdentity: sh.UserIdentity{PublicIdentity: impu},
		DataReferences: []sh.DataReference{sh.SMSRegistrationInfo},
		UserData:       sh.ShData{ShIMSData: &sh.ShIMSData{SMSRegistrationInfo: &sh.SMSRegistrationInfoData{IPSMGWNumber: "12025550199"}}}.Marshal()}.Message()
}

func repositoryDataPUR() *diam.Message {
	data := sh.TransparentData{ServiceIndication: "MMTEL-Services", ServiceData: &sh.ServiceData{XML: []byte("<a/>")}}
	return sh.ProfileUpdateRequest{Header: header, UserIdentity: sh.UserIdentity{PublicIdentity: impu},
		DataReferences: []sh.DataReference{sh.RepositoryData}, UserData: sh.ShData{RepositoryData: []sh.TransparentData{data}}.Marshal()}.Message()
}

func check(t *testing.T, h *hss.HSS, r request) {
	t.Helper()
	got, err := r.result(exchange(t, h, r.message()))
	if err != nil {
		t.Fatalf("%s: %v", r.name, err)
	}
	if got != unableToComply {
		t.Errorf("%s answered %v, want DIAMETER_UNABLE_TO_COMPLY", r.name, got)
	}
}

// TestRequestsWhoseSubscriberCannotBeRead: the HSS cannot reach its
// store, so it cannot tell whether the user of a request exists.
func TestRequestsWhoseSubscriberCannotBeRead(t *testing.T) {
	h := newHSS(t, unreachable{})
	for _, r := range []request{
		{"UAR", func() *diam.Message {
			return cx.UserAuthorizationRequest{Header: header, UserName: impi, PublicIdentity: impu,
				VisitedNetworkIdentifier: realm, UserAuthorizationType: cx.Registration}.Message()
		}, uaaResult},
		{"MAR", digestMAR, maaResult},
		{"SAR", registrationSAR, saaResult},
		{"LIR", func() *diam.Message { return cx.LocationInfoRequest{Header: header, PublicIdentity: impu}.Message() }, liaResult},
		{"UDR", func() *diam.Message {
			return sh.UserDataRequest{Header: header, UserIdentity: sh.UserIdentity{PublicIdentity: impu},
				DataReferences: []sh.DataReference{sh.SCSCFName}}.Message()
		}, udaResult},
		{"PUR", smsRegistrationPUR, puaResult},
	} {
		check(t, h, r)
	}
}

// TestRequestsWhoseSubscriberCannotBeWritten: the HSS reads the
// subscriber of each request, and then its store stops. The S-CSCF and
// the IP-SM-GW must not take the state for stored: a vector or an H(A1)
// whose authentication other HSS instances do not know of, an
// assignment that a later UAR does not find, an IP-SM-GW that no short
// message is sent to.
func TestRequestsWhoseSubscriberCannotBeWritten(t *testing.T) {
	kv := provisioned(t)
	for _, r := range []request{
		{"MAR for SIP Digest", digestMAR, maaResult},
		{"MAR for IMS-AKA", akaMAR, maaResult},
		{"SAR", registrationSAR, saaResult},
		{"PUR of SMSRegistrationInfo", smsRegistrationPUR, puaResult},
		{"PUR of repository data", repositoryDataPUR, puaResult},
	} {
		check(t, newHSS(t, storetest.StopAfter(kv, 0)), r)
	}
}

// TestProvisioningWhenTheStoreFails: every request of the provisioning
// API gets 500 when the store cannot be reached, and those that write
// get it when the store stops before their write.
func TestProvisioningWhenTheStoreFails(t *testing.T) {
	cat := "/subscriptions/" + impi + "/customized-alerting-tone"
	newSubscription := `{"private_identity": "001010000000009@` + realm + `", "public_identities": ["sip:001010000000009@` + realm + `"], "password": "p"}`
	requests := []struct{ method, path, body string }{
		{"GET", "/subscriptions", ""},
		{"GET", "/subscriptions/" + impi, ""},
		{"POST", "/subscriptions", newSubscription},
		{"PATCH", "/subscriptions/" + impi, `{"test_user": true}`},
		{"DELETE", "/subscriptions/" + impi, ""},
		{"PUT", cat, `{"tone": "cat1"}`},
	}
	api := newHSS(t, unreachable{}).Provisioning(noSCSCFs{})
	for _, r := range requests {
		if w := do(api, r.method, r.path, r.body); w.Code != http.StatusInternalServerError {
			t.Errorf("%s %s on a store that cannot be reached: %d %s, want 500", r.method, r.path, w.Code, w.Body)
		}
	}
	kv := provisioned(t)
	for _, r := range requests {
		if r.method == "GET" {
			continue
		}
		api := newHSS(t, storetest.StopAfter(kv, 0)).Provisioning(noSCSCFs{})
		if w := do(api, r.method, r.path, r.body); w.Code != http.StatusInternalServerError {
			t.Errorf("%s %s on a store that stops before the write: %d %s, want 500", r.method, r.path, w.Code, w.Body)
		}
	}
	// What failed changed nothing.
	api = newHSS(t, kv).Provisioning(noSCSCFs{})
	if w := do(api, "GET", "/subscriptions/"+impi, ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "customized_alerting_tone") {
		t.Errorf("GET after the failures: %d %s", w.Code, w.Body)
	}
	if w := do(api, "GET", "/subscriptions/001010000000009@"+realm, ""); w.Code != http.StatusNotFound {
		t.Errorf("GET of the subscription whose POST failed: %d %s", w.Code, w.Body)
	}
}

// TestRequestsWhoseSubscriberCannotBeLocked: the HSS gets no lock for
// the subscriber of a request that would change it. Another instance
// may be changing that subscriber, so this one answers
// DIAMETER_UNABLE_TO_COMPLY rather than read and write without the
// lock, and the provisioning API answers 500 and leaves the
// subscription where it is.
func TestRequestsWhoseSubscriberCannotBeLocked(t *testing.T) {
	kv := provisioned(t)
	h := newHSSWithLocks(t, kv, failingLocks{})
	for _, r := range []request{
		{"MAR", digestMAR, maaResult},
		{"SAR", registrationSAR, saaResult},
		{"PUR of SMSRegistrationInfo", smsRegistrationPUR, puaResult},
		{"PUR of repository data", repositoryDataPUR, puaResult},
	} {
		check(t, h, r)
	}
	api := h.Provisioning(noSCSCFs{})
	for _, r := range []struct{ method, body string }{
		{"PATCH", `{"test_user": true}`},
		{"DELETE", ""},
	} {
		if w := do(api, r.method, "/subscriptions/"+impi, r.body); w.Code != http.StatusInternalServerError {
			t.Errorf("%s of a subscription that cannot be locked: %d %s, want 500", r.method, w.Code, w.Body)
		}
	}
	s, ok, err := hssstore.New(kv, kvjson.Config{}).Subscriber(context.Background(), impi)
	if err != nil || !ok {
		t.Fatalf("the subscription after the requests that got no lock: %v, %v", ok, err)
	}
	if s.TestUser {
		t.Error("the PATCH that got no lock marked the subscription")
	}
}
