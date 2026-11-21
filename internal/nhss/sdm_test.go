package nhss

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

// The example bodies are written from the schemas of the OpenAPI of TS
// 29.562 Annex A.3.

// profileData has one service profile with the MMTel AS on originating
// INVITEs and the IP-SM-GW on third-party registration.
const profileData = `{
  "imsServiceProfiles": [{
    "publicIdentifierList": [
      {"publicIdentity": {"imsPublicId": "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org", "identityType": "DISTINCT_IMPU", "irsIsDefault": true}},
      {"publicIdentity": {"imsPublicId": "tel:+12025550123", "identityType": "DISTINCT_IMPU"}}
    ],
    "ifcs": {"ifcList": [
      {
        "priority": 1,
        "trigger": {"conditionType": "CNF", "sptList": [
          {"conditionNegated": false, "sptGroup": [0], "sipMethod": "INVITE"},
          {"conditionNegated": false, "sptGroup": [1], "sessionCase": "ORIGINATING_REGISTERED", "sipMethod": "INVITE"}
        ]},
        "appServer": {"asUri": "sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org", "sessionContinue": true}
      },
      {
        "priority": 2,
        "trigger": {"conditionType": "DNF", "sptList": [
          {"conditionNegated": false, "sptGroup": [0], "sipMethod": "REGISTER", "regType": ["INITIAL_REGISTRATION", "RE_REGISTRATION"]},
          {"conditionNegated": false, "sptGroup": [1], "sipHeader": {"header": "Accept-Contact", "content": "\\+g\\.3gpp\\.smsip"}}
        ]},
        "appServer": {"asUri": "sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org", "serviceInfoList": ["INCLUDE_REGISTER_REQUEST"]}
      }
    ]}
  }]
}`

const locationData = `{"scscfName": "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"}`

const capabilities = `{"mandatoryCapabilityList": [1, 2], "optionalCapabilityList": [3]}`

const assocIds = `{
  "irsState": "REGISTERED",
  "publicIdentities": {"publicIdentities": [
    {"imsPublicId": "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org", "identityType": "DISTINCT_IMPU", "irsIsDefault": true},
    {"imsPublicId": "tel:+12025550123", "identityType": "DISTINCT_IMPU"}
  ]}
}`

const msisdns = `{"basicMsisdn": "12025550123"}`

const smsRegistration = `{"ipSmGwNumber": "12025550199"}`

func TestSDMRoundTrips(t *testing.T) {
	p := roundTrip[ImsProfileData](t, profileData)
	ifcs := p.ImsServiceProfiles[0].Ifcs.IfcList
	if ifcs[0].Trigger.SptList[1].SessionCase != OriginatingRegistered || !ifcs[0].AppServer.SessionContinue {
		t.Errorf("%+v", ifcs[0])
	}
	if ifcs[1].Trigger.SptList[0].RegType[1] != RegTypeReRegistration || ifcs[1].AppServer.ServiceInfoList[0] != IncludeRegisterRequest {
		t.Errorf("%+v", ifcs[1])
	}
	roundTrip[ImsLocationData](t, locationData)
	roundTrip[ScscfCapabilityList](t, capabilities)
	if a := roundTrip[ImsAssociatedIdentities](t, assocIds); a.PublicIdentities.PublicIdentities[1].ImsPublicId != testTelURI {
		t.Errorf("%+v", a)
	}
	roundTrip[MsisdnList](t, msisdns)
	roundTrip[SmsRegistrationInfo](t, smsRegistration)
	roundTrip[IpSmGwAddress](t, smsRegistration)
}

// stubSDM is an HSS for the tests: the test subscriber, known by its
// SIP URI and its tel URI.
type stubSDM struct {
	mu      sync.Mutex
	ipsmgw  string
	private []string // private-identity parameters seen
}

func known(id ImsUeId) bool {
	impu, ok := id.Impu()
	return ok && (impu == testImpu || impu == testTelURI)
}

var errUnknown = NewProblem(CauseUserNotFound, "")

func (h *stubSDM) GetProfileData(_ context.Context, id ImsUeId) (ImsProfileData, error) {
	if !known(id) {
		return ImsProfileData{}, errUnknown
	}
	return decoded[ImsProfileData](profileData), nil
}

func (h *stubSDM) GetServerName(_ context.Context, id ImsUeId) (ImsLocationData, error) {
	if !known(id) {
		return ImsLocationData{}, errUnknown
	}
	return ImsLocationData{ScscfName: testSCSCF}, nil
}

func (h *stubSDM) GetScscfCapabilities(_ context.Context, id ImsUeId) (ScscfCapabilityList, error) {
	return ScscfCapabilityList{}, NewProblem(CauseDataNotFound, "any S-CSCF")
}

func (h *stubSDM) GetImsAssocIds(_ context.Context, id ImsUeId) (ImsAssociatedIdentities, error) {
	if !known(id) {
		return ImsAssociatedIdentities{}, errUnknown
	}
	return decoded[ImsAssociatedIdentities](assocIds), nil
}

func (h *stubSDM) GetMsisdns(_ context.Context, id ImsUeId) (MsisdnList, error) {
	return MsisdnList{BasicMsisdn: "12025550123"}, nil
}

func (h *stubSDM) GetSmsRegistrationInfo(_ context.Context, id ImsUeId, privateIdentity string) (SmsRegistrationInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.private = append(h.private, privateIdentity)
	if h.ipsmgw == "" {
		return SmsRegistrationInfo{}, NewProblem(CauseDataNotFound, "")
	}
	return SmsRegistrationInfo{IpSmGwNumber: h.ipsmgw}, nil
}

func (h *stubSDM) UpdateSmsRegistrationInfo(_ context.Context, id ImsUeId, privateIdentity string, a IpSmGwAddress) (*SmsRegistrationInfo, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.private = append(h.private, privateIdentity)
	created := h.ipsmgw == ""
	h.ipsmgw = a.IpSmGwNumber
	return &SmsRegistrationInfo{IpSmGwNumber: a.IpSmGwNumber}, created, nil
}

func (h *stubSDM) DeleteSmsRegistrationInfo(_ context.Context, id ImsUeId, privateIdentity string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ipsmgw = ""
	return nil
}

func TestSDMClientAndServer(t *testing.T) {
	hss := &stubSDM{}
	root := serveH2C(t, NewImsSDMHandler(hss))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	scscf := NewClient(root, NFTypeSCSCF)
	as := NewClient(root, NFTypeIMSAS)
	user := ImsUeIdFromImpu(testImpu)

	p, err := scscf.GetProfileData(ctx, user)
	if err != nil || len(p.ImsServiceProfiles[0].Ifcs.IfcList) != 2 {
		t.Fatalf("GetProfileData = %+v, %v", p, err)
	}
	if l, err := as.GetServerName(ctx, user); err != nil || l.ScscfName != testSCSCF {
		t.Errorf("GetServerName = %+v, %v", l, err)
	}
	_, err = NewClient(root, NFTypeICSCF).GetScscfCapabilities(ctx, user)
	var pe *ProblemError
	if !errors.As(err, &pe) || pe.Status != 404 || pe.Problem.Cause != CauseDataNotFound {
		t.Errorf("GetScscfCapabilities: %v", err)
	}
	// The SC asks by the number of the recipient, as a tel URI.
	if a, err := as.GetImsAssocIds(ctx, ImsUeIdFromImpu(testTelURI)); err != nil || len(a.PublicIdentities.PublicIdentities) != 2 {
		t.Errorf("GetImsAssocIds = %+v, %v", a, err)
	}
	if m, err := as.GetMsisdns(ctx, user); err != nil || m.BasicMsisdn != "12025550123" {
		t.Errorf("GetMsisdns = %+v, %v", m, err)
	}
	_, err = as.GetProfileData(ctx, ImsUeIdFromImpu("sip:nobody@ims.example.org"))
	if !errors.As(err, &pe) || pe.Status != 404 || pe.Problem.Cause != CauseUserNotFound {
		t.Errorf("unknown user: %v", err)
	}
}

// TestSMSRegistrationInfo is the IP-SM-GW's registration for a user's
// short messages, and its removal: the PUR of Sh.
func TestSMSRegistrationInfo(t *testing.T) {
	hss := &stubSDM{}
	root := serveH2C(t, NewImsSDMHandler(hss))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	ipsmgw := NewClient(root, NFTypeIMSAS)
	user := ImsUeIdFromImpu(testImpu)

	var pe *ProblemError
	if _, err := ipsmgw.GetSmsRegistrationInfo(ctx, user, ""); !errors.As(err, &pe) || pe.Problem.Cause != CauseDataNotFound {
		t.Errorf("before registration: %v", err)
	}
	s, err := ipsmgw.UpdateSmsRegistrationInfo(ctx, user, testImpi, IpSmGwAddress{IpSmGwNumber: "12025550199"})
	if err != nil || s == nil || s.IpSmGwNumber != "12025550199" {
		t.Fatalf("UpdateSmsRegistrationInfo = %+v, %v", s, err)
	}
	if got, err := ipsmgw.GetSmsRegistrationInfo(ctx, user, testImpi); err != nil || got.IpSmGwNumber != "12025550199" {
		t.Errorf("GetSmsRegistrationInfo = %+v, %v", got, err)
	}
	if err := ipsmgw.DeleteSmsRegistrationInfo(ctx, user, ""); err != nil {
		t.Errorf("DeleteSmsRegistrationInfo: %v", err)
	}
	hss.mu.Lock()
	if len(hss.private) != 3 || hss.private[1] != testImpi || hss.private[2] != testImpi {
		t.Errorf("private-identity parameters %q", hss.private)
	}
	hss.mu.Unlock()

	path := root + SDMRoot + "/impu-" + testImpu + "/service-data/sms-registration-info"
	status, header, _ := request(t, "PUT", path, "application/json", smsRegistration)
	if status != http.StatusCreated || header.Get("Location") != path {
		t.Errorf("first PUT: %d, Location %q", status, header.Get("Location"))
	}
	if status, _, _ := request(t, "PUT", path, "application/json", smsRegistration); status != http.StatusOK {
		t.Errorf("second PUT: %d", status)
	}
	status, header, body := request(t, "PUT", path, "application/json", `{}`)
	if p := problemOf(t, header, body); status != 400 || params(p) != "/ipSmGwNumber" {
		t.Errorf("PUT without ipSmGwNumber: %d %+v", status, p)
	}
}
