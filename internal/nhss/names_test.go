package nhss

import (
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// attributeNames are the attributes of each type, as the schema of the
// same name in the OpenAPI files of TS 29.562 names them; ThreeGAkaAv is
// the schema 3GAkaAv. The names were taken from the files in the
// archive of TS 29.562 V20.1.0 on the 3GPP file server, so a field
// whose tag is spelt otherwise fails TestAttributeNames. The attributes
// of ProblemDetails, from TS 29.571, are checked in package sbi.
var attributeNames = []struct {
	v      any
	file   string
	schema string
	names  []string
}{
	{AdditionalInfo{}, "TS29562_Nhss_imsUECM.yaml", "AdditionalInfo", []string{"scscfServerName"}},
	{ImsProfileData{}, "TS29562_Nhss_imsSDM.yaml", "ImsProfileData", []string{"imsServiceProfiles"}},
	{ImsServiceProfile{}, "TS29562_Nhss_imsSDM.yaml", "ImsServiceProfile", []string{"publicIdentifierList", "ifcs"}},
	{PublicIdentifier{}, "TS29562_Nhss_imsSDM.yaml", "PublicIdentifier", []string{"publicIdentity", "barringIndicator"}},
	{PublicIdentity{}, "TS29562_Nhss_imsSDM.yaml", "PublicIdentity", []string{"imsPublicId", "identityType", "irsIsDefault"}},
	{Ifcs{}, "TS29562_Nhss_imsSDM.yaml", "Ifcs", []string{"ifcList"}},
	{Ifc{}, "TS29562_Nhss_imsSDM.yaml", "Ifc", []string{"priority", "trigger", "appServer"}},
	{TriggerPoint{}, "TS29562_Nhss_imsSDM.yaml", "TriggerPoint", []string{"conditionType", "sptList"}},
	{Spt{}, "TS29562_Nhss_imsSDM.yaml", "Spt", []string{"conditionNegated", "sptGroup", "regType", "requestUri", "sipMethod", "sipHeader", "sessionCase", "sessionDescription"}},
	{HeaderSipRequest{}, "TS29562_Nhss_imsSDM.yaml", "HeaderSipRequest", []string{"header", "content"}},
	{SdpDescription{}, "TS29562_Nhss_imsSDM.yaml", "SdpDescription", []string{"line", "content"}},
	{ApplicationServer{}, "TS29562_Nhss_imsSDM.yaml", "ApplicationServer", []string{"asUri", "sessionContinue", "serviceInfoList"}},
	{ImsLocationData{}, "TS29562_Nhss_imsSDM.yaml", "ImsLocationData", []string{"scscfName"}},
	{ImsAssociatedIdentities{}, "TS29562_Nhss_imsSDM.yaml", "ImsAssociatedIdentities", []string{"irsState", "publicIdentities"}},
	{PublicIdentities{}, "TS29562_Nhss_imsSDM.yaml", "PublicIdentities", []string{"publicIdentities"}},
	{MsisdnList{}, "TS29562_Nhss_imsSDM.yaml", "MsisdnList", []string{"basicMsisdn", "additionalMsisdns"}},
	{SmsRegistrationInfo{}, "TS29562_Nhss_imsSDM.yaml", "SmsRegistrationInfo", []string{"ipSmGwNumber", "ipSmGwAddress", "scAddress"}},
	{IpSmGwAddress{}, "TS29562_Nhss_imsSDM.yaml", "IpSmGwAddress", []string{"ipSmGwNumber"}},
	{SipAuthenticationInfoRequest{}, "TS29562_Nhss_imsUEAU.yaml", "SipAuthenticationInfoRequest", []string{"cscfServerName", "sipAuthenticationScheme", "sipNumberAuthItems", "resynchronizationInfo"}},
	{ResynchronizationInfo{}, "TS29562_Nhss_imsUEAU.yaml", "ResynchronizationInfo", []string{"rand", "auts"}},
	{SipAuthenticationInfoResult{}, "TS29562_Nhss_imsUEAU.yaml", "SipAuthenticationInfoResult", []string{"impi", "3gAkaAvs", "digestAuth"}},
	{ThreeGAkaAv{}, "TS29562_Nhss_imsUEAU.yaml", "3GAkaAv", []string{"rand", "xres", "autn", "ck", "ik"}},
	{DigestAuthentication{}, "TS29562_Nhss_imsUEAU.yaml", "DigestAuthentication", []string{"digestRealm", "digestAlgorithm", "digestQop", "ha1"}},
	{AuthorizationRequest{}, "TS29562_Nhss_imsUECM.yaml", "AuthorizationRequest", []string{"impi", "authorizationType", "visitedNetworkIdentifier"}},
	{AuthorizationResponse{}, "TS29562_Nhss_imsUECM.yaml", "AuthorizationResponse", []string{"authorizationResult", "cscfServerName", "scscfSelectionAssistanceInfo"}},
	{ScscfSelectionAssistanceInformation{}, "TS29562_Nhss_imsSDM.yaml", "ScscfSelectionAssistanceInformation", []string{"scscfCapabilityList", "scscfNames"}},
	{ScscfCapabilityList{}, "TS29562_Nhss_imsSDM.yaml", "ScscfCapabilityList", []string{"mandatoryCapabilityList", "optionalCapabilityList"}},
	{ScscfRegistration{}, "TS29562_Nhss_imsUECM.yaml", "ScscfRegistration", []string{"impi", "imsRegistrationType", "cscfServerName", "scscfInstanceId", "deregCallbackUri", "irsImpus"}},
	{DeregistrationData{}, "TS29562_Nhss_imsUECM.yaml", "DeregistrationData", []string{"deregReason", "impi", "associatedImpis"}},
	{DeregistrationReason{}, "TS29562_Nhss_imsUECM.yaml", "DeregistrationReason", []string{"reasonCode", "reasonText"}},
}

func TestAttributeNames(t *testing.T) {
	for _, a := range attributeNames {
		if got := openapitest.JSONNames(a.v); !slices.Equal(got, a.names) {
			t.Errorf("%T has the attributes %q, the schema %s of %s has %q", a.v, got, a.schema, a.file, a.names)
		}
	}
}
