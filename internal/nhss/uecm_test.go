package nhss

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

// The example bodies are written from the schemas of the OpenAPI of TS
// 29.562 Annex A.2 for the registration of a test subscriber (PLMN
// 001/01).
const (
	testImpi   = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	testImpu   = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	testTelURI = "tel:+12025550123"
	testSCSCF  = "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"
)

const authorizationRequest = `{
  "impi": "001010000000001@ims.mnc001.mcc001.3gppnetwork.org",
  "authorizationType": "REGISTRATION",
  "visitedNetworkIdentifier": "ims.mnc001.mcc001.3gppnetwork.org"
}`

// firstRegistration is the answer when no S-CSCF serves the user yet:
// the I-CSCF chooses one by its capabilities.
const firstRegistration = `{
  "authorizationResult": "FIRST_REGISTRATION",
  "scscfSelectionAssistanceInfo": {
    "scscfCapabilityList": {"mandatoryCapabilityList": [1], "optionalCapabilityList": [2, 3]}
  }
}`

const subsequentRegistration = `{
  "authorizationResult": "SUBSEQUENT_REGISTRATION",
  "cscfServerName": "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"
}`

const scscfRegistration = `{
  "impi": "001010000000001@ims.mnc001.mcc001.3gppnetwork.org",
  "imsRegistrationType": "INITIAL_REGISTRATION",
  "cscfServerName": "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org",
  "deregCallbackUri": "http://scscf.ims.mnc001.mcc001.3gppnetwork.org/nhss-callbacks/irs-1"
}`

const scscfRegistrationAnswer = `{
  "impi": "001010000000001@ims.mnc001.mcc001.3gppnetwork.org",
  "imsRegistrationType": "INITIAL_REGISTRATION",
  "cscfServerName": "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org",
  "irsImpus": ["sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org", "tel:+12025550123"]
}`

const identityAlreadyRegistered = `{
  "status": 403,
  "cause": "IDENTITY_ALREADY_REGISTERED",
  "scscfServerName": "sip:scscf2.ims.mnc001.mcc001.3gppnetwork.org"
}`

const deregistrationData = `{
  "deregReason": {"reasonCode": "PERMANENT_TERMINATION", "reasonText": "subscription ended"},
  "impi": "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
}`

func TestUECMRoundTrips(t *testing.T) {
	if r := roundTrip[AuthorizationRequest](t, authorizationRequest); r.AuthorizationType != AuthorizationTypeRegistration {
		t.Errorf("%+v", r)
	}
	if a := roundTrip[AuthorizationResponse](t, firstRegistration); a.ScscfSelectionAssistanceInfo.ScscfCapabilityList.MandatoryCapabilityList[0] != 1 {
		t.Errorf("%+v", a)
	}
	if a := roundTrip[AuthorizationResponse](t, subsequentRegistration); a.CscfServerName != testSCSCF {
		t.Errorf("%+v", a)
	}
	roundTrip[ScscfRegistration](t, scscfRegistration)
	if r := roundTrip[ScscfRegistration](t, scscfRegistrationAnswer); len(r.IrsImpus) != 2 {
		t.Errorf("%+v", r)
	}
	if p := roundTrip[ExtendedProblemDetails](t, identityAlreadyRegistered); p.ScscfServerName == "" || p.Cause != CauseIdentityAlreadyRegistered {
		t.Errorf("%+v", p)
	}
	if d := roundTrip[DeregistrationData](t, deregistrationData); d.DeregReason.ReasonCode != PermanentTermination {
		t.Errorf("%+v", d)
	}
}

// stubUECM is an HSS for the tests: one subscriber, whose S-CSCF it
// remembers.
type stubUECM struct {
	mu     sync.Mutex
	server string
}

func (h *stubUECM) Authorize(_ context.Context, impu string, req AuthorizationRequest) (AuthorizationResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if impu != testImpu {
		return AuthorizationResponse{}, NewProblem(CauseUserNotFound, "")
	}
	if req.Impi != "" && req.Impi != testImpi {
		return AuthorizationResponse{}, NewProblem(CauseIdentitiesDoNotMatch, "")
	}
	if h.server != "" {
		return AuthorizationResponse{AuthorizationResult: SubsequentRegistration, CscfServerName: h.server}, nil
	}
	return AuthorizationResponse{AuthorizationResult: FirstRegistration, ScscfSelectionAssistanceInfo: &ScscfSelectionAssistanceInformation{
		ScscfCapabilityList: &ScscfCapabilityList{MandatoryCapabilityList: []int{1}},
	}}, nil
}

func (h *stubUECM) PutScscfRegistration(_ context.Context, id ImsUeId, reg ScscfRegistration) (*ScscfRegistration, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if impu, _ := id.Impu(); impu != testImpu {
		if impi, _ := id.Impi(); impi != testImpi {
			return nil, false, NewProblem(CauseUserNotFound, "")
		}
	}
	switch reg.ImsRegistrationType {
	case InitialRegistration, ReRegistration:
		if h.server != "" && h.server != reg.CscfServerName {
			p := NewProblem(CauseIdentityAlreadyRegistered, "")
			p.Problem.ScscfServerName = h.server
			return nil, false, p
		}
		created := h.server == ""
		h.server = reg.CscfServerName
		reg.IrsImpus = []string{testImpu, testTelURI}
		return &reg, created, nil
	case UserDeregistration, TimeoutDeregistration, AdministrativeDeregistration:
		h.server = ""
		return nil, false, nil
	}
	return nil, false, NewProblem(CauseErrorInRegistrationType, "")
}

func TestUECMClientAndServer(t *testing.T) {
	root := serveH2C(t, NewImsUECMHandler(&stubUECM{}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	icscf := NewClient(root, NFTypeICSCF)
	scscf := NewClient(root, NFTypeSCSCF)

	req := roundTrip[AuthorizationRequest](t, authorizationRequest)
	a, err := icscf.Authorize(ctx, testImpu, req)
	if err != nil || a.AuthorizationResult != FirstRegistration || a.ScscfSelectionAssistanceInfo == nil {
		t.Fatalf("first Authorize = %+v, %v", a, err)
	}

	reg := roundTrip[ScscfRegistration](t, scscfRegistration)
	got, err := scscf.Registration(ctx, ImsUeIdFromImpu(testImpu), reg)
	if err != nil || got == nil || len(got.IrsImpus) != 2 {
		t.Fatalf("Registration = %+v, %v", got, err)
	}

	a, err = icscf.Authorize(ctx, testImpu, req)
	if err != nil || a.AuthorizationResult != SubsequentRegistration || a.CscfServerName != testSCSCF {
		t.Errorf("second Authorize = %+v, %v", a, err)
	}

	// A re-registration replaces the resource: 200 with it.
	reg.ImsRegistrationType = ReRegistration
	if got, err := scscf.Registration(ctx, ImsUeIdFromImpu(testImpu), reg); err != nil || got == nil {
		t.Errorf("re-registration = %+v, %v", got, err)
	}

	// Another S-CSCF may not take the user over.
	other := reg
	other.CscfServerName = "sip:scscf2.ims.mnc001.mcc001.3gppnetwork.org"
	_, err = NewClient(root, NFTypeSCSCF).Registration(ctx, ImsUeIdFromImpu(testImpu), other)
	var p *ProblemError
	if !errors.As(err, &p) || p.Status != 403 || p.Problem.Cause != CauseIdentityAlreadyRegistered || p.Problem.ScscfServerName != testSCSCF {
		t.Errorf("Registration by another S-CSCF: %v %+v", err, p)
	}

	// Deregistration by the private identity: 204.
	dereg := ScscfRegistration{ImsRegistrationType: UserDeregistration, CscfServerName: testSCSCF}
	if got, err := scscf.Deregistration(ctx, ImsUeIdFromImpi(testImpi), dereg); err != nil || got != nil {
		t.Errorf("Deregistration = %+v, %v", got, err)
	}
}

func TestUECMCreatedHasLocation(t *testing.T) {
	root := serveH2C(t, NewImsUECMHandler(&stubUECM{}))
	path := UECMRoot + "/impu-" + testImpu + "/scscf-registration"
	status, header, _ := request(t, "PUT", root+path, "application/json", scscfRegistration)
	if status != http.StatusCreated {
		t.Fatalf("status %d", status)
	}
	if got := header.Get("Location"); got != root+path {
		t.Errorf("Location %q, want %q", got, root+path)
	}
}

func TestUECMErrors(t *testing.T) {
	root := serveH2C(t, NewImsUECMHandler(&stubUECM{}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	c := NewClient(root, NFTypeICSCF)

	_, err := c.Authorize(ctx, "sip:nobody@ims.example.org", AuthorizationRequest{AuthorizationType: AuthorizationTypeRegistration})
	var p *ProblemError
	if !errors.As(err, &p) || p.Status != 404 || p.Problem.Cause != CauseUserNotFound {
		t.Errorf("unknown user: %v", err)
	}
	_, err = c.Authorize(ctx, testImpu, AuthorizationRequest{Impi: "other@ims.example.org", AuthorizationType: AuthorizationTypeRegistration})
	if !errors.As(err, &p) || p.Status != 403 || p.Problem.Cause != CauseIdentitiesDoNotMatch {
		t.Errorf("identities do not match: %v", err)
	}

	cases := []struct {
		name, method, path, contentType, body string
		status                                int
		cause, params                         string
	}{
		{"no authorizationType", "POST", "/" + testImpu + "/authorize", "application/json", `{}`, 400, CauseMandatoryIEMissing, "/authorizationType"},
		{"registration without its required attributes", "PUT", "/impu-" + testImpu + "/scscf-registration", "application/json", `{}`,
			400, CauseMandatoryIEMissing, "/imsRegistrationType /cscfServerName"},
		{"not JSON", "PUT", "/impu-" + testImpu + "/scscf-registration", "application/json", `[`, 400, CauseInvalidMsgFormat, ""},
		{"wrong media type", "POST", "/" + testImpu + "/authorize", "text/plain", authorizationRequest, 415, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, header, body := request(t, c.method, root+UECMRoot+c.path, c.contentType, c.body)
			p := problemOf(t, header, body)
			if status != c.status || p.Status != c.status || p.Cause != c.cause || params(p) != c.params {
				t.Errorf("got %d %+v", status, p)
			}
		})
	}
}

// recordingSCSCF keeps the notifications it gets.
type recordingSCSCF struct {
	mu     sync.Mutex
	paths  []string
	data   []DeregistrationData
	answer *DeregistrationData
}

func (s *recordingSCSCF) DeregistrationNotification(_ context.Context, path string, d DeregistrationData) (*DeregistrationData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
	s.data = append(s.data, d)
	return s.answer, nil
}

// TestDeregistrationNotification: the HSS ends a registration (the RTR
// of Cx) by posting to the callback URI the S-CSCF registered.
func TestDeregistrationNotification(t *testing.T) {
	scscf := &recordingSCSCF{}
	var mu sync.Mutex
	var headers []string
	root := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("User-Agent")+" "+r.Header.Get("3gpp-Sbi-Callback"))
		mu.Unlock()
		NewDeregistrationNotificationHandler(scscf).ServeHTTP(w, r)
	}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	n := NewNotifier()
	d := roundTrip[DeregistrationData](t, deregistrationData)

	result, err := n.DeregistrationNotification(ctx, root+"/nhss-callbacks/irs-1", d)
	if err != nil || result != nil {
		t.Fatalf("204 answer: %+v, %v", result, err)
	}
	scscf.mu.Lock()
	scscf.answer = &DeregistrationData{DeregReason: d.DeregReason, Impi: testImpi, AssociatedImpis: []string{"second@ims.example.org"}}
	scscf.mu.Unlock()
	result, err = n.DeregistrationNotification(ctx, root+"/nhss-callbacks/irs-1", d)
	if err != nil || result == nil || len(result.AssociatedImpis) != 1 {
		t.Fatalf("200 answer: %+v, %v", result, err)
	}

	scscf.mu.Lock()
	if len(scscf.paths) != 2 || scscf.paths[0] != "/nhss-callbacks/irs-1" || scscf.data[0].Impi != testImpi {
		t.Errorf("got %v %+v", scscf.paths, scscf.data)
	}
	scscf.mu.Unlock()
	mu.Lock()
	if headers[0] != "HSS Nhss_imsUECM_deregistrationNotification" {
		t.Errorf("headers %q", headers[0])
	}
	mu.Unlock()

	status, header, body := request(t, "POST", root+"/nhss-callbacks/irs-1", "application/json", `{"deregReason":{}}`)
	if p := problemOf(t, header, body); status != 400 || p.Cause != CauseMandatoryIEMissing || params(p) != "/deregReason/reasonCode /impi" {
		t.Errorf("got %d %+v", status, p)
	}
	if status, _, _ := request(t, "GET", root+"/nhss-callbacks/irs-1", "", ""); status != http.StatusMethodNotAllowed {
		t.Errorf("GET answered %d", status)
	}
}
