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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// do sends a request to the provisioning API, checked against
// openapi.yaml.
func do(t *testing.T, api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	w := httptest.NewRecorder()
	provisioningDocument(t).Handler(t, api).ServeHTTP(w, httptest.NewRequest(method, path, r))
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
	if a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
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

// TestProvisioningTakesTheMarkAndTheProfileOfASubscription: a POST
// marks a subscription as a test user and names its service profile,
// and a GET shows both. A name that no profile of the configuration
// answers to is refused, and nothing is stored for it: such a
// subscription would reach the SAA with no criteria at all.
func TestProvisioningTakesTheMarkAndTheProfileOfASubscription(t *testing.T) {
	h := newTestHSS()
	withProfiles(h, map[string]ServiceProfile{"default": {}, "trial": {}}, "default", "trial")
	api := h.Provisioning(&fakeSCSCFs{})

	marked := `{"private_identity": "001010000000009@` + realm + `",
		"public_identities": ["sip:001010000000009@` + realm + `"], "password": "test-password-9",
		"test_user": true, "service_profile": "trial"}`
	w := do(t, api, "POST", "/subscriptions", marked)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	if got := decode[subscriptionResponse](t, w); !got.TestUser || got.ServiceProfile != "trial" {
		t.Errorf("the answer to the POST: %+v", got)
	}
	if got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/001010000000009@"+realm, "")); !got.TestUser || got.ServiceProfile != "trial" {
		t.Errorf("GET one: %+v", got)
	}
	// A subscription of the configuration file, which names neither.
	if got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+impi, "")); got.TestUser || got.ServiceProfile != "" {
		t.Errorf("GET a subscription that is neither marked nor named: %+v", got)
	}
	// The profile each of the three kinds of subscription gets. The
	// screen that lists them shows it, since the settings that name the
	// profile of a marked subscription and of any other change while the
	// HSS runs.
	effective := func(t *testing.T, privateID string) string {
		t.Helper()
		return decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+privateID, "")).EffectiveServiceProfile
	}
	if got := effective(t, "001010000000009@"+realm); got != "trial" {
		t.Errorf("the profile of a subscription that names trial: %q", got)
	}
	if got := effective(t, impi); got != "default" {
		t.Errorf("the profile of a subscription that names none: %q", got)
	}
	if w := do(t, api, "PATCH", "/subscriptions/"+impi, `{"test_user": true}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body)
	}
	if got := effective(t, impi); got != "trial" {
		t.Errorf("the profile of a marked subscription that names none: %q", got)
	}

	unknown := strings.NewReplacer("000009", "000008", `"trial"`, `"gold"`).Replace(marked)
	if w := do(t, api, "POST", "/subscriptions", unknown); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), `"gold" is not a profile of service_profiles`) {
		t.Errorf("POST with a profile that the configuration does not define: %d %s", w.Code, w.Body)
	}
	if w := do(t, api, "GET", "/subscriptions/001010000000008@"+realm, ""); w.Code != http.StatusNotFound {
		t.Errorf("the refused subscription was stored: %d %s", w.Code, w.Body)
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
	if a := uar(t, h, "x@"+realm, "tel:+12025550109", cx.Registration); a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
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
	if a.Result != diameter.Base(diameter.DiameterSuccess) {
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

// fakeSCSCFs answers the requests of the HSS as an S-CSCF would,
// through the wire format, and keeps them.
type fakeSCSCFs struct {
	mu     sync.Mutex
	hosts  []string
	rtrs   []cx.RegistrationTerminationRequest
	pprs   []cx.PushProfileRequest
	result diameter.Result
	// beforePPA, when set, runs before each PPA is sent, with the number
	// of PPRs taken so far, so that a test can hold an answer back.
	beforePPA func(n int)
}

func (f *fakeSCSCFs) Request(ctx context.Context, originHost string, m *diam.Message) (*diam.Message, error) {
	// A peer sends nothing for a context that has ended.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := m.Serialize()
	if err != nil {
		return nil, err
	}
	req, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		return nil, err
	}
	result := f.result
	if result == (diameter.Result{}) {
		result = diameter.Base(diameter.DiameterSuccess)
	}
	if req.Header.CommandCode == cx.CommandPushProfile {
		ppr, err := cx.ParsePushProfileRequest(req)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		f.hosts, f.pprs = append(f.hosts, originHost), append(f.pprs, ppr)
		n := len(f.pprs)
		f.mu.Unlock()
		if f.beforePPA != nil {
			f.beforePPA(n)
		}
		return cx.PushProfileAnswer{Result: result}.Message(req, diameter.Identity{OriginHost: originHost, OriginRealm: realm}), nil
	}
	rtr, err := cx.ParseRegistrationTerminationRequest(req)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts, f.rtrs = append(f.hosts, originHost), append(f.rtrs, rtr)
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
	if a := uar(t, h, impi, impu, cx.Registration); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("UAR after DELETE: %+v", a)
	}
}

// TestRemovingARegisteredSubscriptionSendsRTR is TS 29.228 6.1.3: the
// S-CSCF that registered the user hears with PERMANENT_TERMINATION that
// it no longer serves the private identity and all its public ones.
func TestRemovingARegisteredSubscriptionSendsRTR(t *testing.T) {
	for _, result := range []diameter.Result{diameter.Base(diameter.DiameterSuccess), diameter.Experimental(cx.DiameterErrorUserUnknown)} {
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
		if a := uar(t, h, impi, impu, cx.Registration); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
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
	// The subscription shows the tone, so that the Lab can.
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

// putSubscription is a PUT of the subscription of impi, with the answer
// decoded. A request that failed has no answer to decode.
func putSubscription(t *testing.T, api http.Handler, impi, body string) (*httptest.ResponseRecorder, putAnswer) {
	t.Helper()
	w := do(t, api, "PUT", "/subscriptions/"+impi, body)
	if w.Code >= http.StatusBadRequest {
		return w, putAnswer{}
	}
	return w, decode[putAnswer](t, w)
}

// TestProvisioningPutCreatesASubscription: a PUT of a subscription that
// the HSS does not have adds it, and the same body sent again leaves it
// as it is, so that an operator can send a file of subscriptions as
// often as they like. A dry run says what it would do and does nothing.
func TestProvisioningPutCreatesASubscription(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	newIMPI := "001010000000009@" + realm

	w, got := putSubscription(t, api, newIMPI, newSubscription)
	if w.Code != http.StatusCreated || got.Result != resultCreated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if got.Subscription.RegistrationState != "Not Registered" || len(got.Subscription.PublicIdentities) != 2 {
		t.Errorf("the answer: %+v", got.Subscription)
	}
	if strings.Contains(w.Body.String(), "test-password-9") {
		t.Errorf("the answer gives out the password: %s", w.Body)
	}
	// The new subscriber registers like one of the configuration file.
	if a := uar(t, h, newIMPI, "tel:+12025550109", cx.Registration); a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
		t.Errorf("UAR for the new subscriber: %+v", a)
	}

	// The same body again: nothing to do, and the answer says so.
	w, got = putSubscription(t, api, newIMPI, newSubscription)
	if w.Code != http.StatusOK || got.Result != resultUnchanged {
		t.Errorf("the same PUT again: %d %s", w.Code, w.Body)
	}

	// A dry run of a subscription that is not there answers what it would
	// do, with 200 rather than the 201 of a creation it did not make.
	dryIMPI := "001010000000008@" + realm
	dry := strings.NewReplacer("000009", "000008", "+12025550109", "+12025550108").Replace(newSubscription)
	w, got = putSubscription(t, api, dryIMPI+"?dry_run=true", dry)
	if w.Code != http.StatusOK || got.Result != resultCreated {
		t.Fatalf("the dry run: %d %s", w.Code, w.Body)
	}
	if w := do(t, api, "GET", "/subscriptions/"+dryIMPI, ""); w.Code != http.StatusNotFound {
		t.Errorf("the dry run stored the subscription: %d %s", w.Code, w.Body)
	}
}

// TestProvisioningPutReplacesASubscription: a PUT over a subscription
// that is there replaces the whole of it, credentials included, and
// leaves what the running subscriber has: its registration, the S-CSCF
// serving it and SQN_HE. The public identities it no longer names stop
// finding it.
func TestProvisioningPutReplacesASubscription(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "` + akaImpi + `", "public_identities": ["` + akaImpu + `", "tel:+12025550110"],
		"password": "test-password-10",
		"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf", "amf": "8000", "sqn": "000000000020"}}`
	if w, _ := putSubscription(t, api, akaImpi, body); w.Code != http.StatusCreated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	// The user takes a vector, which moves SQN_HE on, and registers.
	if a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil); a.AuthenticationVector == nil {
		t.Fatalf("MAA = %+v", a)
	}
	registered := subscriberOrFail(t, h, akaImpi)
	registered.State, registered.ServerName = Registered, scscf
	if err := h.store.Put(context.Background(), registered); err != nil {
		t.Fatal(err)
	}

	// The operator takes the telephone number away, marks the
	// subscription and drops the password. The sqn of the body is older
	// than SQN_HE and is not read.
	replaced := `{"private_identity": "` + akaImpi + `", "public_identities": ["` + akaImpu + `"], "test_user": true,
		"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf", "amf": "8000", "sqn": "000000000000"}}`
	w, got := putSubscription(t, api, akaImpi, replaced)
	if w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the replacing PUT: %d %s", w.Code, w.Body)
	}
	if !got.Subscription.TestUser || got.Subscription.RegistrationState != "Registered" || got.Subscription.ServerName != scscf {
		t.Errorf("the answer: %+v", got.Subscription)
	}
	if len(got.Subscription.AuthenticationSchemes) != 1 || got.Subscription.AuthenticationSchemes[0] != cx.SchemeDigestAKAv1MD5 {
		t.Errorf("authentication_schemes = %v, want IMS-AKA alone", got.Subscription.AuthenticationSchemes)
	}

	stored := subscriberOrFail(t, h, akaImpi)
	if stored.State != Registered || stored.ServerName != scscf {
		t.Errorf("the running subscriber: %v %q", stored.State, stored.ServerName)
	}
	// SQN 0x20 is SEQ 1 with IND 0, so the vector that went out had SEQ 2
	// with IND 1 (TestProvisioningAddsAnAKASubscription).
	if want := aka.NewSQN(2, 1); stored.AKA.SQN != want {
		t.Errorf("SQN_HE = %d, want the %d of the vector that went out", stored.AKA.SQN, want)
	}
	if stored.Password != "" || !stored.TestUser {
		t.Errorf("the stored subscription: %+v", stored.Subscription)
	}
	// The number it let go reaches nobody; the identity it kept still
	// reaches the subscription.
	if a := lir(t, h, "tel:+12025550110"); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("LIR for the number the subscription let go: %+v", a)
	}
	if a := lir(t, h, akaImpu); !a.Result.Success() {
		t.Errorf("LIR for the identity it kept: %+v", a)
	}
}

// subscriberOrFail is the stored subscriber of privateID, for a test
// that reads what the API does not hand out.
func subscriberOrFail(t *testing.T, h *HSS, privateID string) Subscriber {
	t.Helper()
	s, ok, err := h.store.Subscriber(context.Background(), privateID)
	if err != nil || !ok {
		t.Fatalf("Subscriber(%s) = %v, %v", privateID, ok, err)
	}
	return s
}

// TestProvisioningPutRefusesWhatItCannotStore: the body says which
// subscription it is for, the profile is one of the configuration, and
// a public identity of another subscription is refused with nothing
// written, on a dry run as on a real request.
func TestProvisioningPutRefusesWhatItCannotStore(t *testing.T) {
	h := newTestHSS()
	withProfiles(h, map[string]ServiceProfile{"default": {}}, "default", "")
	api := h.Provisioning(&fakeSCSCFs{})
	newIMPI := "001010000000009@" + realm
	for _, c := range []struct {
		name, impi, body string
		status           int
	}{
		{"another private identity than the path", otherImp, newSubscription, http.StatusBadRequest},
		{"no private identity", newIMPI,
			`{"public_identities": ["sip:001010000000009@` + realm + `"], "password": "p"}`, http.StatusBadRequest},
		{"a profile that the configuration does not define", newIMPI,
			`{"private_identity": "` + newIMPI + `", "public_identities": ["sip:001010000000009@` + realm + `"],
				"password": "p", "service_profile": "gold"}`, http.StatusBadRequest},
		{"repository data beside a document of its own", newIMPI,
			`{"private_identity": "` + newIMPI + `", "public_identities": ["sip:001010000000009@` + realm + `"],
				"password": "p", "mmtel_services": "<MMTelServices/>",
				"repository_data": [{"service_indication": "MMTEL-Services", "service_data": "<MMTelServices/>"}]}`, http.StatusBadRequest},
		{"a dry run that is neither true nor false", newIMPI + "?dry_run=maybe", newSubscription, http.StatusBadRequest},
		{"a public identity of another subscription", newIMPI,
			`{"private_identity": "` + newIMPI + `", "public_identities": ["` + impu + `"], "password": "p"}`, http.StatusConflict},
		{"a public identity of another subscription, on a dry run", newIMPI + "?dry_run=true",
			`{"private_identity": "` + newIMPI + `", "public_identities": ["` + impu + `"], "password": "p"}`, http.StatusConflict},
	} {
		t.Run(c.name, func(t *testing.T) {
			if w, _ := putSubscription(t, api, c.impi, c.body); w.Code != c.status {
				t.Errorf("PUT: %d %s, want %d", w.Code, w.Body, c.status)
			}
			if w := do(t, api, "GET", "/subscriptions/"+newIMPI, ""); w.Code != http.StatusNotFound {
				t.Errorf("the refused subscription was stored: %d %s", w.Code, w.Body)
			}
			if owner := subscriberOf(t, h, impu); owner.PrivateIdentity != impi {
				t.Errorf("%s now belongs to %q", impu, owner.PrivateIdentity)
			}
		})
	}
}

// TestProvisioningPutReplacesTheRepositoryData: a PUT carries the
// repository data of a subscription, which is how an archive of one
// goes back in, and what it leaves out goes. A PUT that names no data
// at all leaves the subscription with the MMTelServices document of the
// configuration, so what the user set over Ut is back to the configured
// default. Each piece that changes gets the next sequence number, as an
// update over Sh would.
func TestProvisioningPutReplacesTheRepositoryData(t *testing.T) {
	h := newTestHSS()
	doc := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><complete-communication-hold><operator-communication-hold authorized="true"/></complete-communication-hold></MMTelServices>`
	configured, err := MMTelServicesData([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	h.config.MMTelServices = &configured
	api := h.Provisioning(&fakeSCSCFs{})
	newIMPI := "001010000000009@" + realm
	identities := `"private_identity": "` + newIMPI + `", "public_identities": ["sip:001010000000009@` + realm + `"], "password": "p"`

	// A subscription with a Ut password and a tone of its own gets the
	// configured document, the credential as H(A1) and the tone.
	body := `{` + identities + `, "ut_password": "test-ut-password-9",
		"customized_alerting_tone": {"tone": "cat1", "active": true}}`
	w, got := putSubscription(t, api, newIMPI, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if got.Subscription.CustomizedAlertingTone == nil || got.Subscription.CustomizedAlertingTone.Tone != "cat1" {
		t.Errorf("the tone of the answer: %+v", got.Subscription.CustomizedAlertingTone)
	}
	if d := storedData(t, h, newIMPI, "MMTEL-Services"); !strings.Contains(string(d.ServiceData.XML), "operator-communication-hold") {
		t.Errorf("MMTEL-Services = %s", d.ServiceData.XML)
	}
	if d := storedData(t, h, newIMPI, "Ut-Digest-Credential"); strings.Contains(string(d.ServiceData.XML), "test-ut-password-9") {
		t.Errorf("the Ut credential holds the password: %s", d.ServiceData.XML)
	}
	storedData(t, h, newIMPI, mmtel.CATServiceIndication)

	// The same body again leaves the data as it is: no piece is written
	// again, so none has a new sequence number.
	if w, got := putSubscription(t, api, newIMPI, body); w.Code != http.StatusOK || got.Result != resultUnchanged {
		t.Errorf("the same PUT again: %d %s", w.Code, w.Body)
	}
	if d := storedData(t, h, newIMPI, "MMTEL-Services"); d.SequenceNumber != 0 {
		t.Errorf("sequence number after an unchanged PUT = %d, want the 0 of new data", d.SequenceNumber)
	}

	// A PUT that changes the password alone leaves the data where it is,
	// so an AS that read a sequence number over Sh can still update from
	// it.
	if w, got := putSubscription(t, api, newIMPI, strings.Replace(body, `"password": "p"`, `"password": "q"`, 1)); w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the PUT of a new password: %d %s", w.Code, w.Body)
	}
	if d := storedData(t, h, newIMPI, "MMTEL-Services"); d.SequenceNumber != 0 {
		t.Errorf("sequence number after a PUT that only changed the password = %d, want 0", d.SequenceNumber)
	}

	// Repository data of its own replaces all of it: the credential and
	// the tone go with the document they came with.
	own := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-waiting active="true"/></MMTelServices>`
	if w, got := putSubscription(t, api, newIMPI, `{`+identities+`, "repository_data": [{"service_indication": "MMTEL-Services",
		"service_data": `+strconv.Quote(own)+`}]}`); w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the PUT of repository data: %d %s", w.Code, w.Body)
	}
	if d := storedData(t, h, newIMPI, "MMTEL-Services"); string(d.ServiceData.XML) != own || d.SequenceNumber != 1 {
		t.Errorf("MMTEL-Services = %s with sequence number %d", d.ServiceData.XML, d.SequenceNumber)
	}
	for _, gone := range []string{"Ut-Digest-Credential", mmtel.CATServiceIndication} {
		if _, ok, err := h.store.RepositoryData(context.Background(), newIMPI, gone); ok || err != nil {
			t.Errorf("%s is still there (%v)", gone, err)
		}
	}

	// A PUT that names no data puts the configured document back.
	if w, got := putSubscription(t, api, newIMPI, `{`+identities+`}`); w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the PUT without any data: %d %s", w.Code, w.Body)
	}
	if d := storedData(t, h, newIMPI, "MMTEL-Services"); !strings.Contains(string(d.ServiceData.XML), "operator-communication-hold") || d.SequenceNumber != 2 {
		t.Errorf("MMTEL-Services = %s with sequence number %d", d.ServiceData.XML, d.SequenceNumber)
	}
	// An empty array is not the same as leaving the field out: it asks
	// for a subscription with no repository data at all.
	if w, got := putSubscription(t, api, newIMPI, `{`+identities+`, "repository_data": []}`); w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the PUT with an empty array: %d %s", w.Code, w.Body)
	}
	if d, err := h.store.AllRepositoryData(context.Background(), newIMPI); len(d) != 0 || err != nil {
		t.Errorf("the repository data is %+v (%v)", d, err)
	}
}

// TestProvisioningPutReadsAnEmptyBarringAsNone: barred_identities with
// an empty array bars nothing, which is what leaving the field out
// does, so the two bodies describe one subscription and a PUT of either
// over the other answers unchanged. An empty repository_data is the
// other way round: it is not the same as leaving that field out.
func TestProvisioningPutReadsAnEmptyBarringAsNone(t *testing.T) {
	api := newTestHSS().Provisioning(&fakeSCSCFs{})
	newIMPI := "001010000000009@" + realm
	identities := `"private_identity": "` + newIMPI + `", "public_identities": ["sip:001010000000009@` + realm + `"], "password": "p"`
	if w, _ := putSubscription(t, api, newIMPI, `{`+identities+`, "barred_identities": []}`); w.Code != http.StatusCreated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if w, got := putSubscription(t, api, newIMPI, `{`+identities+`}`); w.Code != http.StatusOK || got.Result != resultUnchanged {
		t.Errorf("the same subscription without the field: %d %s", w.Code, w.Body)
	}
}

// updatedBeforeTheSwap makes an update of the repository data land
// between the read of a PUT and its swap, which an Sh PUR at another
// instance or a PUT of the tone can: both compare and swap the data
// without the lock the provisioning request holds.
type updatedBeforeTheSwap struct {
	Store
	update sh.TransparentData
	done   bool
}

func (s *updatedBeforeTheSwap) CompareAndSwapRepositoryData(ctx context.Context, privateID string, old *sh.TransparentData, data sh.TransparentData) (bool, error) {
	if !s.done && data.ServiceIndication == s.update.ServiceIndication {
		s.done = true
		if swapped, err := s.Store.CompareAndSwapRepositoryData(ctx, privateID, old, s.update); err != nil || !swapped {
			return false, err
		}
	}
	return s.Store.CompareAndSwapRepositoryData(ctx, privateID, old, data)
}

// TestProvisioningPutWithAnUpdateInBetween: a PUT whose repository data
// another update changes between the read and the swap loses the swap,
// reads again and writes over what that update left, rather than taking
// the data back to the version it had read. The sequence number counts
// both writes (TS 29.328 6.1.2.1).
func TestProvisioningPutWithAnUpdateInBetween(t *testing.T) {
	const (
		v0 = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"/>`
		v1 = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-waiting active="true"/></MMTelServices>`
		v2 = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-waiting active="false"/></MMTelServices>`
	)
	memory, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1",
		RepositoryData: []sh.TransparentData{{ServiceIndication: "MMTEL-Services", ServiceData: &sh.ServiceData{XML: []byte(v0)}}}})
	if err != nil {
		t.Fatal(err)
	}
	store := &updatedBeforeTheSwap{Store: memory,
		update: sh.TransparentData{ServiceIndication: "MMTEL-Services", SequenceNumber: 1, ServiceData: &sh.ServiceData{XML: []byte(v1)}}}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h := mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, node)
	api := h.Provisioning(&fakeSCSCFs{})

	body := `{"private_identity": "` + impi + `", "public_identities": ["` + impu + `"], "password": "test-password-1",
		"repository_data": [{"service_indication": "MMTEL-Services", "service_data": ` + strconv.Quote(v2) + `}]}`
	w, got := putSubscription(t, api, impi, body)
	if w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if !store.done {
		t.Fatal("no update landed between the read and the swap")
	}
	d := storedData(t, h, impi, "MMTEL-Services")
	if string(d.ServiceData.XML) != v2 {
		t.Errorf("MMTEL-Services = %s, want the data of the PUT", d.ServiceData.XML)
	}
	if d.SequenceNumber != 2 {
		t.Errorf("sequence number = %d, want the 2 that follows the update in between", d.SequenceNumber)
	}
}

// TestProvisioningPutDryRunChangesNothing: a dry run of a replacement
// answers what the request would do and leaves the subscription exactly
// as it stands, down to the sequence numbers of its repository data,
// and claims none of the public identities it names.
func TestProvisioningPutDryRunChangesNothing(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	newIMPI := "001010000000009@" + realm
	user := "sip:001010000000009@" + realm
	if w, _ := putSubscription(t, api, newIMPI, `{"private_identity": "`+newIMPI+`", "public_identities": ["`+user+`"],
		"password": "test-password-9", "ut_password": "test-ut-password-9"}`); w.Code != http.StatusCreated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	before := storedState(t, h, newIMPI)

	changed := `{"private_identity": "` + newIMPI + `", "public_identities": ["` + user + `", "tel:+12025550109"],
		"password": "test-password-8", "test_user": true,
		"repository_data": [{"service_indication": "MMTEL-Services", "service_data": "<MMTelServices/>"}]}`
	w, got := putSubscription(t, api, newIMPI+"?dry_run=true", changed)
	if w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the dry run: %d %s", w.Code, w.Body)
	}
	if !got.Subscription.TestUser || len(got.Subscription.PublicIdentities) != 2 {
		t.Errorf("the answer does not show what the request would leave: %+v", got.Subscription)
	}
	if after := storedState(t, h, newIMPI); !bytes.Equal(before, after) {
		t.Errorf("the dry run changed the subscription:\nbefore %s\nafter  %s", before, after)
	}
	if _, ok, err := h.store.SubscriberOf(context.Background(), "tel:+12025550109"); ok || err != nil {
		t.Errorf("the dry run gave the telephone number to the subscription: %v, %v", ok, err)
	}

	// The same body without the dry run does change it, so the comparison
	// above is of a request that had something to do.
	if w, got := putSubscription(t, api, newIMPI, changed); w.Code != http.StatusOK || got.Result != resultUpdated {
		t.Fatalf("the request that is no dry run: %d %s", w.Code, w.Body)
	}
	if after := storedState(t, h, newIMPI); bytes.Equal(before, after) {
		t.Errorf("the request that is no dry run left the subscription at %s", before)
	}
}

// storedState is everything the HSS keeps for a subscription, as JSON,
// so that a test can tell that a request left all of it alone.
func storedState(t *testing.T, h *HSS, privateID string) []byte {
	t.Helper()
	data, err := h.store.AllRepositoryData(context.Background(), privateID)
	if err != nil {
		t.Fatal(err)
	}
	state := struct {
		Subscriber Subscriber
		Repository []sh.TransparentData
	}{subscriberOrFail(t, h, privateID), data}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// losesEverySwap lets no swap of the repository data through, as an
// endless run of updates from elsewhere would.
type losesEverySwap struct{ Store }

func (losesEverySwap) CompareAndSwapRepositoryData(context.Context, string, *sh.TransparentData, sh.TransparentData) (bool, error) {
	return false, nil
}

// TestProvisioningPutThatKeepsLosingTheSwap: a PUT whose repository
// data others keep changing gives up and answers, rather than holding
// the subscriber's lock and trying for good.
func TestProvisioningPutThatKeepsLosingTheSwap(t *testing.T) {
	memory, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"})
	if err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h := mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm}}, losesEverySwap{Store: memory}, node)
	api := h.Provisioning(&fakeSCSCFs{})

	body := `{"private_identity": "` + impi + `", "public_identities": ["` + impu + `"], "password": "test-password-1",
		"repository_data": [{"service_indication": "MMTEL-Services", "service_data": "<MMTelServices/>"}]}`
	answered := make(chan int, 1)
	go func() { answered <- do(t, api, "PUT", "/subscriptions/"+impi, body).Code }()
	select {
	case code := <-answered:
		if code != http.StatusInternalServerError {
			t.Errorf("PUT whose swaps all lose: %d, want 500", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer within 5 s to a PUT whose swaps all lose")
	}
}

// TestProvisioningPatchesTheMarkAndTheProfile: a PATCH changes the mark
// and the service profile name of a subscription and leaves the rest of
// it alone. A field the body leaves out stays as it is, so each one
// changes on its own and a body of {} changes nothing. The criteria that
// the SAA carries follow the name the PATCH leaves behind.
func TestProvisioningPatchesTheMarkAndTheProfile(t *testing.T) {
	// One criterion per profile, each naming an application server of its
	// own, so that the SAA says which profile the subscription has.
	profile := func(server string) []ifc.InitialFilterCriteria {
		return []ifc.InitialFilterCriteria{{
			TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "INVITE"}}},
			ApplicationServer: ifc.ApplicationServer{ServerName: "sip:" + server + "." + realm + ":5060"},
		}}
	}
	h := newTestHSS()
	withProfiles(h, map[string]ServiceProfile{
		"default": {Criteria: profile("mmtel")}, "trial": {Criteria: profile("trial")}, "cdiv": {Criteria: profile("cdiv")}},
		"default", "trial")
	api := h.Provisioning(&fakeSCSCFs{})

	for _, c := range []struct {
		name, body  string
		testUser    bool
		profileName string
		// server names the profile the SAA then carries: the one of the
		// subscription, else the one of its kind of user.
		server string
	}{
		{"both fields", `{"test_user": true, "service_profile": "cdiv"}`, true, "cdiv", "cdiv"},
		{"an empty body, which changes nothing", `{}`, true, "cdiv", "cdiv"},
		{"the profile name alone, back to the one of a test user", `{"service_profile": ""}`, true, "", "trial"},
		{"the mark alone", `{"test_user": false}`, false, "", "mmtel"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := do(t, api, "PATCH", "/subscriptions/"+impi, c.body)
			if w.Code != http.StatusOK {
				t.Fatalf("PATCH: %d %s", w.Code, w.Body)
			}
			if got := decode[subscriptionResponse](t, w); got.TestUser != c.testUser || got.ServiceProfile != c.profileName {
				t.Errorf("the answer: %+v", got)
			}
			got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+impi, ""))
			if got.TestUser != c.testUser || got.ServiceProfile != c.profileName {
				t.Errorf("GET after the PATCH: %+v", got)
			}
			if len(got.PublicIdentities) != 1 || got.PublicIdentities[0] != impu {
				t.Errorf("the public identities after the PATCH: %v", got.PublicIdentities)
			}
			server, want := saaServer(t, h), "sip:"+c.server+"."+realm+":5060"
			if server != want {
				t.Errorf("the criterion of the SAA names %q, want the %q of the %s profile", server, want, c.server)
			}
		})
	}
}

// saaServer is the application server of the one criterion that the
// SAA of a registration carries for the subscription of impi, which
// says which service profile the HSS chose for it.
func saaServer(t *testing.T, h *HSS) string {
	t.Helper()
	a := sar(t, h, scscf, cx.AssignRegistration)
	if !a.Result.Success() {
		t.Fatalf("SAA: %+v", a)
	}
	got, err := cx.ParseUserProfile(a.UserData)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.InitialFilterCriteria) != 1 {
		t.Fatalf("the SAA carries %d criteria, want 1", len(got.InitialFilterCriteria))
	}
	return got.InitialFilterCriteria[0].ApplicationServer.ServerName
}

// TestProvisioningPatchLeavesTheCredentials: a PATCH carries no
// credentials, and the subscription answers afterwards with the ones it
// had. That is why the API has a PATCH at all: the operator cannot read
// the password or the AKA keys back to send them in a PUT (TS 29.228
// Table 6.3.5 and Table 6.3.7), and SQN_HE is not theirs to send either.
func TestProvisioningPatchLeavesTheCredentials(t *testing.T) {
	h := newTestHSS()
	withProfiles(h, map[string]ServiceProfile{"default": {}, "trial": {}}, "default", "trial")
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "` + akaImpi + `", "public_identities": ["` + akaImpu + `"],
		"password": "test-password-10",
		"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf", "amf": "8000", "sqn": "000000000020"}}`
	if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	// The user takes one vector, which moves SQN_HE on from the sqn that
	// was provisioned.
	before := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil)
	used := vectorSQN(t, before.AuthenticationVector)

	w := do(t, api, "PATCH", "/subscriptions/"+akaImpi, `{"test_user": true, "service_profile": "trial"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body)
	}
	if got := decode[subscriptionResponse](t, w); !got.TestUser || got.ServiceProfile != "trial" {
		t.Errorf("the answer: %+v", got)
	}
	if strings.Contains(w.Body.String(), "test-password-10") || strings.Contains(w.Body.String(), "465b5ce8") {
		t.Errorf("the answer gives out a credential: %s", w.Body)
	}

	// The password is the one of the POST: the S-CSCF gets its H(A1).
	digest := akaMAR(t, h, cx.SchemeSIPDigest, scscf, nil)
	if d := digest.SIPDigestAuthenticate; d == nil || d.DigestHA1 != HA1(akaImpi, realm, "test-password-10") {
		t.Errorf("the H(A1) after the PATCH: %+v", d)
	}
	// The keys are the ones of the POST, and SQN_HE is where the
	// authentication left it: a USIM that has taken the vector from
	// before the PATCH takes this one as well.
	usim := aka.NewUSIM(akaSecret, used)
	next := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil)
	if next.AuthenticationVector == nil {
		t.Fatalf("MAA after the PATCH: %+v", next)
	}
	if _, err := usim.Authenticate(next.AuthenticationVector.RAND, next.AuthenticationVector.AUTN); err != nil {
		t.Errorf("the USIM of the subscriber refuses the vector after the PATCH: %v", err)
	}
	if sqn := vectorSQN(t, next.AuthenticationVector); sqn <= used {
		t.Errorf("the SQN of the vector after the PATCH = %d, want one above the %d already used", sqn, used)
	}
}

// TestProvisioningPatchRefusesWhatItCannotStore: the subscription is
// one the HSS has, the profile is one of the configuration, and a field
// the API does not have is a mistake rather than a change that goes
// quietly nowhere. Nothing is written for a request that is refused.
func TestProvisioningPatchRefusesWhatItCannotStore(t *testing.T) {
	h := newTestHSS()
	withProfiles(h, map[string]ServiceProfile{"default": {}, "trial": {}}, "default", "")
	api := h.Provisioning(&fakeSCSCFs{})
	if w := do(t, api, "PATCH", "/subscriptions/"+impi, `{"test_user": true}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		name, impi, body string
		status           int
	}{
		{"a subscription the HSS does not have", "001010000000009@" + realm, `{"test_user": false}`, http.StatusNotFound},
		{"a profile that the configuration does not define", impi, `{"service_profile": "gold"}`, http.StatusBadRequest},
		{"a field the API does not have", impi, `{"password": "p"}`, http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			if w := do(t, api, "PATCH", "/subscriptions/"+c.impi, c.body); w.Code != c.status {
				t.Errorf("PATCH: %d %s, want %d", w.Code, w.Body, c.status)
			}
			if got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+impi, "")); !got.TestUser || got.ServiceProfile != "" {
				t.Errorf("the refused PATCH changed the subscription: %+v", got)
			}
		})
	}
	// A method that the path has no route for, which http.ServeMux
	// answers with the methods it does have, the PATCH among them.
	w := do(t, api, "POST", "/subscriptions/"+impi, "")
	if w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Header().Get("Allow"), "PATCH") {
		t.Errorf("POST of one subscription: %d, Allow %q", w.Code, w.Header().Get("Allow"))
	}
}

// storedData is the repository data stored under serviceIndication,
// which has to be there.
func storedData(t *testing.T, h *HSS, privateID, serviceIndication string) sh.TransparentData {
	t.Helper()
	d, ok, err := h.store.RepositoryData(context.Background(), privateID, serviceIndication)
	if err != nil || !ok || d.ServiceData == nil {
		t.Fatalf("RepositoryData(%s, %s) = %+v, %v, %v", privateID, serviceIndication, d, ok, err)
	}
	return d
}

// subscriberOf is the stored subscriber that has the public identity.
func subscriberOf(t *testing.T, h *HSS, publicID string) Subscriber {
	t.Helper()
	s, ok, err := h.store.SubscriberOf(context.Background(), publicID)
	if err != nil || !ok {
		t.Fatalf("SubscriberOf(%s) = %v, %v", publicID, ok, err)
	}
	return s
}
