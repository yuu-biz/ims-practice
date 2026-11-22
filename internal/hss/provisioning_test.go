package hss

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

func do(t *testing.T, api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(method, path, r))
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("%s: %v", w.Body.String(), err)
	}
	return v
}

const newSubscription = `{"private_identity": "001010000000009@` + realm + `",
	"public_identities": ["sip:001010000000009@` + realm + `", "tel:+12025550109"], "password": "test-password-9"}`

func TestProvisioningAddsASubscriptionTheHSSServes(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})

	w := do(t, api, "POST", "/subscriptions", newSubscription)
	if w.Code != http.StatusCreated || w.Header().Get("Location") != "/subscriptions/001010000000009@"+realm {
		t.Fatalf("POST: %d %s, Location %q", w.Code, w.Body, w.Header().Get("Location"))
	}
	if strings.Contains(w.Body.String(), "test-password-9") {
		t.Errorf("the answer gives out the password: %s", w.Body)
	}

	// The new subscriber registers like one from the configuration.
	a := uar(t, h, "001010000000009@"+realm, "tel:+12025550109", cx.Registration)
	if a.Result != cx.Experimental(cx.DiameterFirstRegistration) {
		t.Errorf("UAR for the new subscriber: %+v", a)
	}

	got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/001010000000009@"+realm, ""))
	if got.RegistrationState != "Not Registered" || len(got.PublicIdentities) != 2 {
		t.Errorf("GET one: %+v", got)
	}
	list := decode[[]subscriptionResponse](t, do(t, api, "GET", "/subscriptions", ""))
	if len(list) != 3 {
		t.Errorf("GET all: %+v", list)
	}
}

// The listing comes in pages: "limit" says how many, the Link header of
// rel="next" holds the query of the request for the page that follows,
// and the body is the same list of subscriptions as a listing that does
// not page.
func TestProvisioningPagesTheSubscriptions(t *testing.T) {
	api := newTestHSS().Provisioning(&fakeSCSCFs{})
	// The two test users, and a third whose identity sorts after both.
	if w := do(t, api, "POST", "/subscriptions", newSubscription); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	want := []string{impi, otherImp, "001010000000009@" + realm}

	var got []string
	path := "/subscriptions?limit=2"
	pages := 0
	for path != "" {
		w := do(t, api, "GET", path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
		for _, s := range decode[[]subscriptionResponse](t, w) {
			got = append(got, s.PrivateIdentity)
		}
		pages++
		if pages > len(want)+1 {
			t.Fatalf("still paging after %d pages: %v", pages, got)
		}
		path = nextPage(t, "/subscriptions?limit=2", w.Header().Get("Link"))
	}
	if !slices.Equal(got, want) {
		t.Errorf("the pages held %v, want %v", got, want)
	}

	// A listing that asks for nothing is the whole list, as before, and
	// has no page after it.
	w := do(t, api, "GET", "/subscriptions", "")
	if list := decode[[]subscriptionResponse](t, w); len(list) != len(want) {
		t.Errorf("GET without a limit: %+v", list)
	}
	if link := w.Header().Get("Link"); link != "" {
		t.Errorf("a listing that holds everything names a next page: %q", link)
	}
	if w := do(t, api, "GET", "/subscriptions?limit=none", ""); w.Code != http.StatusBadRequest {
		t.Errorf("GET with a limit that is not a number: %d %s", w.Code, w.Body)
	}
	if w := do(t, api, "GET", "/subscriptions?after="+url.QueryEscape(want[len(want)-1]), ""); len(decode[[]subscriptionResponse](t, w)) != 0 {
		t.Errorf("GET after the last subscription: %s", w.Body)
	}
}

// nextPage is the path of the page that the Link header names, read
// against the path of the request that answered with it (RFC 3986 4.2),
// or "" when there is no such header.
func nextPage(t *testing.T, path, link string) string {
	t.Helper()
	if link == "" {
		return ""
	}
	target, ok := strings.CutSuffix(strings.TrimPrefix(link, "<"), `>; rel="next"`)
	if !ok {
		t.Fatalf("Link header %q is not a target with rel=\"next\"", link)
	}
	base, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	next, err := url.Parse(target)
	if err != nil {
		t.Fatalf("Link target %q: %v", target, err)
	}
	return base.ResolveReference(next).String()
}

func TestProvisioningTakesTheTelURIApart(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "x@` + realm + `", "public_identities": ["sip:x@` + realm + `"], "password": "p", "tel": "tel:+12025550109"}`
	if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/x@"+realm, ""))
	if len(got.PublicIdentities) != 2 || got.PublicIdentities[0] != "sip:x@"+realm || got.PublicIdentities[1] != "tel:+12025550109" {
		t.Errorf("public identities: %v", got.PublicIdentities)
	}
	if a := uar(t, h, "x@"+realm, "tel:+12025550109", cx.Registration); a.Result != cx.Experimental(cx.DiameterFirstRegistration) {
		t.Errorf("UAR for the tel URI: %+v", a)
	}
	bad := `{"private_identity": "y@` + realm + `", "public_identities": ["sip:y@` + realm + `"], "password": "p", "tel": "+12025550110"}`
	if w := do(t, api, "POST", "/subscriptions", bad); w.Code != http.StatusBadRequest {
		t.Errorf("POST with a tel that is not a tel URI: %d %s", w.Code, w.Body)
	}
}

func TestProvisioningShowsTheRegistrationState(t *testing.T) {
	h := newTestHSS()
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	got := decode[subscriptionResponse](t, do(t, h.Provisioning(&fakeSCSCFs{}), "GET", "/subscriptions/"+impi, ""))
	if got.RegistrationState != "Registered" || got.ServerName != scscf {
		t.Errorf("GET: %+v", got)
	}
}

func TestProvisioningRefusesWhatTheConfigurationRefuses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"not JSON", "private_identity=x", http.StatusBadRequest},
		{"unknown field", `{"private_identity": "x@` + realm + `", "public_identities": ["sip:x@` + realm + `"], "password": "p", "imsi": "1"}`, http.StatusBadRequest},
		{"no password", `{"private_identity": "x@` + realm + `", "public_identities": ["sip:x@` + realm + `"]}`, http.StatusBadRequest},
		{"not a SIP or tel URI", `{"private_identity": "x@` + realm + `", "public_identities": ["x@` + realm + `"], "password": "p"}`, http.StatusBadRequest},
		{"short AKA key", `{"private_identity": "x@` + realm + `", "public_identities": ["sip:x@` + realm + `"], "aka": {"k": "465b", "opc": "cd63cb71954a9f4e48a5994e37a02baf"}}`, http.StatusBadRequest},
		{"public identity of another subscription", `{"private_identity": "x@` + realm + `", "public_identities": ["` + impu + `"], "password": "p"}`, http.StatusConflict},
		{"private identity of another subscription", `{"private_identity": "` + impi + `", "public_identities": ["sip:x@` + realm + `"], "password": "p"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newTestHSS().Provisioning(&fakeSCSCFs{})
			if w := do(t, api, "POST", "/subscriptions", tt.body); w.Code != tt.want {
				t.Errorf("POST: %d %s, want %d", w.Code, w.Body, tt.want)
			}
			if w := do(t, api, "GET", "/subscriptions/x@"+realm, ""); w.Code != http.StatusNotFound {
				t.Errorf("GET of the refused subscription: %d %s", w.Code, w.Body)
			}
		})
	}
}

// TestProvisioningAddsAnAKASubscription: a subscription with AKA
// credentials and no password answers IMS-AKA, and the API never shows
// the keys.
func TestProvisioningAddsAnAKASubscription(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "` + akaImpi + `", "public_identities": ["` + akaImpu + `"],
		"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf", "amf": "8000", "sqn": "000000000020"}}`
	w := do(t, api, "POST", "/subscriptions", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "465b5ce8") || strings.Contains(w.Body.String(), "cd63cb71") {
		t.Errorf("the answer gives out the keys: %s", w.Body)
	}
	got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+akaImpi, ""))
	if len(got.AuthenticationSchemes) != 1 || got.AuthenticationSchemes[0] != cx.SchemeDigestAKAv1MD5 {
		t.Errorf("authentication_schemes = %v", got.AuthenticationSchemes)
	}

	// SQN 0x20 is SEQ 1 with IND 0: the first vector has SEQ 2, IND 1.
	usim := aka.NewUSIM(akaSecret, aka.NewSQN(2, 0))
	a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil)
	if a.AuthenticationVector == nil {
		t.Fatalf("MAA = %+v", a)
	}
	if _, err := usim.Authenticate(a.AuthenticationVector.RAND, a.AuthenticationVector.AUTN); err != nil {
		t.Fatalf("USIM: %v", err)
	}
	if sqn := vectorSQN(t, a.AuthenticationVector); sqn != aka.NewSQN(2, 1) {
		t.Errorf("SQN of the vector = %d, want %d", sqn, aka.NewSQN(2, 1))
	}
	// AUTN is SQN ⊕ AK || AMF || MAC: the AMF goes in clear.
	if amf := a.AuthenticationVector.AUTN[6:8]; amf[0] != 0x80 || amf[1] != 0x00 {
		t.Errorf("AMF of the vector = %x, want 8000", amf)
	}
}

// shRepositoryData is what an AS reads with a UDR for the repository
// data of publicID under serviceIndication, or nil when there is none.
func shRepositoryData(t *testing.T, h *HSS, publicID, serviceIndication string) *sh.TransparentData {
	t.Helper()
	a, err := sh.ParseUserDataAnswer(exchange(t, h, sh.UserDataRequest{Header: hdr, UserIdentity: sh.UserIdentity{PublicIdentity: publicID},
		ServiceIndications: []string{serviceIndication}, DataReferences: []sh.DataReference{sh.RepositoryData}}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if a.Result != cx.Base(cx.DiameterSuccess) {
		t.Fatalf("UDA for %s of %s: %v", serviceIndication, publicID, a.Result)
	}
	if len(a.UserData) == 0 {
		return nil
	}
	data, err := sh.ParseShData(a.UserData)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.RepositoryData) != 1 {
		t.Fatalf("UDA for %s of %s: %d RepositoryData", serviceIndication, publicID, len(data.RepositoryData))
	}
	return &data.RepositoryData[0]
}

func TestProvisioningNamesTheDigestScheme(t *testing.T) {
	h := newTestHSS()
	got := decode[subscriptionResponse](t, do(t, h.Provisioning(&fakeSCSCFs{}), "GET", "/subscriptions/"+impi, ""))
	if len(got.AuthenticationSchemes) != 1 || got.AuthenticationSchemes[0] != cx.SchemeSIPDigest {
		t.Errorf("authentication_schemes = %v", got.AuthenticationSchemes)
	}
}

// fakeSCSCFs answers each RTR as an S-CSCF would, through the wire
// format, and keeps the RTRs.
type fakeSCSCFs struct {
	mu     sync.Mutex
	hosts  []string
	rtrs   []cx.RegistrationTerminationRequest
	result cx.Result
}

func (f *fakeSCSCFs) Request(_ context.Context, originHost string, m *diam.Message) (*diam.Message, error) {
	b, err := m.Serialize()
	if err != nil {
		return nil, err
	}
	req, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		return nil, err
	}
	rtr, err := cx.ParseRegistrationTerminationRequest(req)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts, f.rtrs = append(f.hosts, originHost), append(f.rtrs, rtr)
	result := f.result
	if result == (cx.Result{}) {
		result = cx.Base(cx.DiameterSuccess)
	}
	return cx.RegistrationTerminationAnswer{Result: result}.Message(req, diameter.Identity{OriginHost: originHost, OriginRealm: realm}), nil
}

func TestProvisioningRemovesASubscription(t *testing.T) {
	h := newTestHSS()
	scscfs := &fakeSCSCFs{}
	api := h.Provisioning(scscfs)
	if w := do(t, api, "DELETE", "/subscriptions/"+impi, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	// TS 29.228 6.1.3.1: a user who is Not Registered has no S-CSCF to
	// tell.
	if len(scscfs.rtrs) != 0 {
		t.Errorf("RTR for a user who was not registered: %+v", scscfs.rtrs)
	}
	if w := do(t, api, "GET", "/subscriptions/"+impi, ""); w.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE: %d", w.Code)
	}
	if w := do(t, api, "DELETE", "/subscriptions/"+impi, ""); w.Code != http.StatusNotFound {
		t.Errorf("second DELETE: %d", w.Code)
	}
	// TS 29.228 6.1.1.1 step 1: the HSS no longer knows the user.
	if a := uar(t, h, impi, impu, cx.Registration); a.Result != cx.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("UAR after DELETE: %+v", a)
	}
}

// TestRemovingARegisteredSubscriptionSendsRTR is TS 29.228 6.1.3: the
// S-CSCF that registered the user hears with PERMANENT_TERMINATION that
// it no longer serves the private identity and all its public ones.
func TestRemovingARegisteredSubscriptionSendsRTR(t *testing.T) {
	for _, result := range []cx.Result{cx.Base(cx.DiameterSuccess), cx.Experimental(cx.DiameterErrorUserUnknown)} {
		h := newTestHSS()
		mar(t, h, scscf)
		sar(t, h, scscf, cx.AssignRegistration)
		scscfs := &fakeSCSCFs{result: result}
		if w := do(t, h.Provisioning(scscfs), "DELETE", "/subscriptions/"+impi, ""); w.Code != http.StatusNoContent {
			t.Fatalf("DELETE: %d %s", w.Code, w.Body)
		}
		if len(scscfs.rtrs) != 1 {
			t.Fatalf("%d RTRs, want 1", len(scscfs.rtrs))
		}
		rtr := scscfs.rtrs[0]
		if scscfs.hosts[0] != hdr.OriginHost || rtr.DestinationHost != hdr.OriginHost || rtr.DestinationRealm != hdr.OriginRealm {
			t.Errorf("RTR sent to %s with Destination-Host %s and Destination-Realm %s, want the Origin of the SAR (%s)",
				scscfs.hosts[0], rtr.DestinationHost, rtr.DestinationRealm, hdr.OriginHost)
		}
		if rtr.UserName != impi || len(rtr.PublicIdentities) != 0 || rtr.DeregistrationReason.ReasonCode != cx.PermanentTermination {
			t.Errorf("RTR = %+v", rtr)
		}
		if rtr.OriginHost != "hss."+realm || rtr.SessionID == "" {
			t.Errorf("RTR header = %+v", rtr.Header)
		}
		// Whatever the S-CSCF answers, the HSS no longer has the user.
		if a := uar(t, h, impi, impu, cx.Registration); a.Result != cx.Experimental(cx.DiameterErrorUserUnknown) {
			t.Errorf("UAR after DELETE with RTA %s: %+v", result, a)
		}
	}
}

// A subscriber added through the API gets the configured MMTelServices
// and, with a Ut password, the Ut credential, as H(A1) only.
func TestProvisioningAddsTheSupplementaryServiceData(t *testing.T) {
	h := newTestHSS()
	doc := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><complete-communication-hold><operator-communication-hold authorized="true"/></complete-communication-hold></MMTelServices>`
	d, err := MMTelServicesData([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	h.config.MMTelServices = &d
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "001010000000009@` + realm + `", "public_identities": ["sip:001010000000009@` + realm + `"],
		"password": "test-password-9", "ut_password": "test-ut-password-9"}`
	if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	// The MMTel AS and the XCAP server read them over Sh.
	user := "sip:001010000000009@" + realm
	if got := shRepositoryData(t, h, user, "MMTEL-Services"); got == nil || !strings.Contains(string(got.ServiceData.XML), "operator-communication-hold") {
		t.Errorf("MMTEL-Services = %+v", got)
	}
	if cred := shRepositoryData(t, h, user, "Ut-Digest-Credential"); cred == nil || strings.Contains(string(cred.ServiceData.XML), "test-ut-password-9") ||
		!strings.Contains(string(cred.ServiceData.XML), realm) {
		t.Errorf("Ut credential = %+v", cred)
	}

	// A document of its own replaces the configured one; one that is not
	// an MMTelServices document is refused.
	bad := `{"private_identity": "x@` + realm + `", "public_identities": ["sip:x@` + realm + `"], "password": "p", "mmtel_services": "<simservs/>"}`
	if w := do(t, api, "POST", "/subscriptions", bad); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "mmtel_services") {
		t.Errorf("POST with a wrong document: %d %s", w.Code, w.Body)
	}
}

// TestProvisioningSetsTheCustomizedAlertingTone: the operator provisions
// CAT for a user and changes it later (TS 24.182 4.5.2); each change of
// the repository data has the next sequence number (TS 29.328 6.1.2.1).
func TestProvisioningSetsTheCustomizedAlertingTone(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	if w := do(t, api, "POST", "/subscriptions", newSubscription); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	path := "/subscriptions/001010000000009@" + realm + "/customized-alerting-tone"
	for i, body := range []string{`{"tone": "cat1"}`, `{"tone": "cat2", "active": true}`} {
		if w := do(t, api, "PUT", path, body); w.Code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", body, w.Code, w.Body)
		}
		d := shRepositoryData(t, h, "tel:+12025550109", mmtel.CATServiceIndication)
		if d == nil || d.SequenceNumber != uint16(i) {
			t.Fatalf("after PUT %s Sh reads %+v", body, d)
		}
	}
	d := shRepositoryData(t, h, "tel:+12025550109", mmtel.CATServiceIndication)
	if got, err := mmtel.ParseCustomizedAlertingTone(d.ServiceData.XML); err != nil || got.Tone != "cat2" || !got.Active {
		t.Errorf("the HSS keeps %s: %+v, %v", d.ServiceData.XML, got, err)
	}
	// The subscription shows the tone, so that the dashboard can.
	w := do(t, api, "GET", "/subscriptions/001010000000009@"+realm, "")
	if !strings.Contains(w.Body.String(), `"customized_alerting_tone":{"active":true,"tone":"cat2"}`) {
		t.Errorf("GET of the subscription: %s", w.Body)
	}
	if w := do(t, api, "GET", "/subscriptions", ""); !strings.Contains(w.Body.String(), `"tone":"cat2"`) {
		t.Errorf("GET of the list: %s", w.Body)
	}
	for _, bad := range []struct {
		path, body string
		code       int
	}{
		{path, `{"tone": "cat-1"}`, http.StatusBadRequest},
		{path, `{"tone": ""}`, http.StatusBadRequest},
		{"/subscriptions/nobody@" + realm + "/customized-alerting-tone", `{"tone": "cat1"}`, http.StatusNotFound},
	} {
		if w := do(t, api, "PUT", bad.path, bad.body); w.Code != bad.code {
			t.Errorf("PUT %s %s: %d, want %d", bad.path, bad.body, w.Code, bad.code)
		}
	}
}
