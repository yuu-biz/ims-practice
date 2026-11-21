package nhss

import (
	"context"
	"net/http"
	"net/url"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// Nhss_imsSubscriberDataManagement (TS 29.562 5.3, 6.2): the data of a
// user that the S-CSCF, the I-CSCF and an IMS AS read, and the service
// data an AS writes.

// ImsProfileData is the user profile (TS 29.562 6.2.6.2.4), what the
// User-Data of a SAA holds on Cx as XML (TS 29.228).
//
// SIMPLIFIED: TS 29.562 6.2.6.2.4. Charging information, service level
// trace, priority levels and the limit of simultaneous registrations
// are left out.
type ImsProfileData struct {
	ImsServiceProfiles []ImsServiceProfile `json:"imsServiceProfiles"`
}

// ImsServiceProfile is one service profile (TS 29.562 6.2.6.2.32): the
// public identities that share it and their filter criteria.
type ImsServiceProfile struct {
	PublicIdentifierList []PublicIdentifier `json:"publicIdentifierList"`
	Ifcs                 *Ifcs              `json:"ifcs,omitempty"`
}

// PublicIdentifier is one public identity of a service profile (TS
// 29.562 6.2.6.2.33).
type PublicIdentifier struct {
	PublicIdentity   PublicIdentity `json:"publicIdentity"`
	BarringIndicator bool           `json:"barringIndicator,omitempty"`
}

// PublicIdentity is an IMS public identity and its kind (TS 29.562
// 6.2.6.2.10). IrsIsDefault marks the default identity of its implicit
// registration set.
type PublicIdentity struct {
	ImsPublicId  string       `json:"imsPublicId"`
	IdentityType IdentityType `json:"identityType"`
	IrsIsDefault bool         `json:"irsIsDefault,omitempty"`
}

// Ifcs are the filter criteria of a service profile (TS 29.562
// 6.2.6.2.14).
//
// SIMPLIFIED: TS 29.562 6.2.6.2.14. cscfFilterSetIdList, the shared iFC
// sets, is left out; the HSS stub sends complete criteria.
type Ifcs struct {
	IfcList []Ifc `json:"ifcList,omitempty"`
}

// Ifc is one initial filter criterion (TS 29.562 6.2.6.2.15; TS 29.228
// B.2.2 explains the terms). Without Trigger the criterion matches
// every request. The ProfilePartIndicator of the XML is gone.
type Ifc struct {
	Priority  int               `json:"priority"`
	Trigger   *TriggerPoint     `json:"trigger,omitempty"`
	AppServer ApplicationServer `json:"appServer"`
}

// TriggerPoint is a boolean expression over SPTs (TS 29.562
// 6.2.6.2.16): CNF is an AND of groups, each the OR of its SPTs; DNF an
// OR of groups, each the AND of its SPTs.
type TriggerPoint struct {
	ConditionType TypeOfCondition `json:"conditionType"`
	SptList       []Spt           `json:"sptList"`
}

// Spt is a Service Point Trigger (TS 29.562 6.2.6.2.17; TS 29.228
// B.2.3 and Annex F): one of RequestUri, SipMethod, SipHeader and
// SessionDescription, and optionally SessionCase. RegType applies only
// with SipMethod REGISTER. The table of 6.2.6.2.17 writes
// "requestURI"; the OpenAPI writes "requestUri", which this type
// follows.
type Spt struct {
	ConditionNegated   bool               `json:"conditionNegated"`
	SptGroup           []int              `json:"sptGroup"`
	RegType            []RegistrationType `json:"regType,omitempty"`
	RequestUri         string             `json:"requestUri,omitempty"`
	SipMethod          string             `json:"sipMethod,omitempty"`
	SipHeader          *HeaderSipRequest  `json:"sipHeader,omitempty"`
	SessionCase        RequestDirection   `json:"sessionCase,omitempty"`
	SessionDescription *SdpDescription    `json:"sessionDescription,omitempty"`
}

// HeaderSipRequest is an SPT on a header field (TS 29.562 6.2.6.2.18).
// Without Content the SPT is about the presence of the header, or,
// negated, its absence.
type HeaderSipRequest struct {
	Header  string `json:"header"`
	Content string `json:"content,omitempty"`
}

// SdpDescription is an SPT on a line of the SDP (TS 29.562 6.2.6.2.19).
type SdpDescription struct {
	Line    string `json:"line"`
	Content string `json:"content,omitempty"`
}

// ApplicationServer is the server a matching criterion sends the
// request to (TS 29.562 6.2.6.2.20).
//
// Two differences from the XML of TS 29.228 matter to a reader of the
// profile. SessionContinue absent or false means the session is
// released when the server cannot be reached, whereas an absent
// DefaultHandling in the XML is read as SESSION_CONTINUED. And
// ServiceInfoList only says whether to include the REGISTER request or
// response; the free text of the XML ServiceInfo has no JSON
// counterpart in Release 20.
type ApplicationServer struct {
	AsUri           string               `json:"asUri"`
	SessionContinue bool                 `json:"sessionContinue,omitempty"`
	ServiceInfoList []ServiceInformation `json:"serviceInfoList,omitempty"`
}

// ImsLocationData is the S-CSCF assigned to the user (TS 29.562
// 6.2.6.2.21): the S-CSCFName data of Sh, and the Server-Name of a LIA.
type ImsLocationData struct {
	ScscfName string `json:"scscfName"`
}

// ImsAssociatedIdentities are the public identities of the implicit
// registration set (TS 29.562 6.2.6.2.45): the IMSPublicIdentity data
// of Sh.
type ImsAssociatedIdentities struct {
	IrsState         ImsRegistrationState `json:"irsState"`
	PublicIdentities PublicIdentities     `json:"publicIdentities"`
}

// PublicIdentities is a list of public identities (TS 29.562
// 6.2.6.2.9).
type PublicIdentities struct {
	PublicIdentities []PublicIdentity `json:"publicIdentities"`
}

// MsisdnList is the MSISDNs of the user (TS 29.562 6.2.6.2.8): the
// MSISDN data of Sh. The numbers are digits only (the Msisdn pattern of
// the OpenAPI).
type MsisdnList struct {
	BasicMsisdn       string   `json:"basicMsisdn"`
	AdditionalMsisdns []string `json:"additionalMsisdns,omitempty"`
}

// SmsRegistrationInfo is the IP-SM-GW registered for the user's short
// messages (TS 29.562 6.2.6.2.43): the SMSRegistrationInfo data of Sh.
type SmsRegistrationInfo struct {
	IpSmGwNumber  string         `json:"ipSmGwNumber"`
	IpSmGwAddress *IpSmGwAddress `json:"ipSmGwAddress,omitempty"`
	ScAddress     string         `json:"scAddress,omitempty"`
}

// IpSmGwAddress is the body of UpdateSmsRegistrationInfo (TS 29.562
// 6.2.6.2.44, 6.2.3.30.3.2): the E.164 number of the IP-SM-GW, digits
// only.
//
// SIMPLIFIED: TS 29.562 6.2.6.2.44. The Diameter identity and realm of
// the IP-SM-GW for S6c, its SBI support indication and its NF instance
// ID are left out; the SMS registration that the IP-SM-GW writes over
// Sh today holds only its number.
type IpSmGwAddress struct {
	IpSmGwNumber string `json:"ipSmGwNumber"`
}

// IdentityType is the kind of a public identity (TS 29.562 6.2.6.3.4).
type IdentityType string

const (
	DistinctImpu   IdentityType = "DISTINCT_IMPU"
	DistinctPsi    IdentityType = "DISTINCT_PSI"
	WildcardedImpu IdentityType = "WILDCARDED_IMPU"
	WildcardedPsi  IdentityType = "WILDCARDED_PSI"
)

// ImsRegistrationState is the registration state of an identity (TS
// 29.562 6.2.6.3.5).
type ImsRegistrationState string

const (
	Registered              ImsRegistrationState = "REGISTERED"
	NotRegistered           ImsRegistrationState = "NOT_REGISTERED"
	AuthenticationPending   ImsRegistrationState = "AUTHENTICATION_PENDING"
	RegisteredUnregServices ImsRegistrationState = "REGISTERED_UNREG_SERVICES"
)

// TypeOfCondition is the normal form of a TriggerPoint (TS 29.562
// 6.2.6.3.6): ConditionTypeCNF true or false in the XML.
type TypeOfCondition string

const (
	CNF TypeOfCondition = "CNF"
	DNF TypeOfCondition = "DNF"
)

// RegistrationType is the kind of REGISTER an SPT matches (TS 29.562
// 6.2.6.3.7).
type RegistrationType string

const (
	RegTypeInitialRegistration RegistrationType = "INITIAL_REGISTRATION"
	RegTypeReRegistration      RegistrationType = "RE_REGISTRATION"
	RegTypeDeRegistration      RegistrationType = "DE_REGISTRATION"
)

// RequestDirection is the session case of an SPT (TS 29.562
// 6.2.6.3.8), with the labels of the SessionCase of the XML.
type RequestDirection string

const (
	OriginatingRegistered   RequestDirection = "ORIGINATING_REGISTERED"
	OriginatingUnregistered RequestDirection = "ORIGINATING_UNREGISTERED"
	OriginatingCdiv         RequestDirection = "ORIGINATING_CDIV"
	TerminatingRegistered   RequestDirection = "TERMINATING_REGISTERED"
	TerminatingUnregistered RequestDirection = "TERMINATING_UNREGISTERED"
)

// ServiceInformation is what the S-CSCF includes in the third-party
// REGISTER to the server (TS 29.562 6.2.6.3.9).
type ServiceInformation string

const (
	IncludeRegisterRequest  ServiceInformation = "INCLUDE_REGISTER_REQUEST"
	IncludeRegisterResponse ServiceInformation = "INCLUDE_REGISTER_RESPONSE"
)

// The resources of the API that the IMS procedures use (TS 29.562
// 6.2.3), after {apiRoot}/nhss-ims-sdm/v1/{imsUeId}.
const (
	profileDataPath         = "/ims-data/profile-data"
	serverNamePath          = "/ims-data/location-data/server-name"
	scscfCapabilitiesPath   = "/ims-data/location-data/scscf-capabilities"
	imsAssocIdsPath         = "/identities/ims-associated-identities"
	msisdnsPath             = "/identities/msisdns"
	smsRegistrationInfoPath = "/service-data/sms-registration-info"
)

func (c *Client) sdmURI(id ImsUeId, resource string) string {
	return c.apiRoot + SDMRoot + "/" + segment(string(id)) + resource
}

// get reads one resource of the user id into v.
func (c *Client) get(ctx context.Context, operation, uri string, v any) error {
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:    http.MethodGet,
		URI:       uri,
		UserAgent: string(c.nfType),
	})
	if err != nil {
		return err
	}
	return decodeAnswer(operation, resp, v)
}

// GetProfileData is the IMS User Profile Retrieval of TS 29.562
// 5.3.2.2.4.1: the S-CSCF reads the profile after it has registered,
// where Cx returns it in the SAA (TS 23.228 Table AA.2.2.2-1,
// Cx-Pull).
//
// SIMPLIFIED: TS 29.562 6.2.3.5.3.1. The dataset-names and
// supported-features query parameters are not sent; the whole profile
// comes back.
func (c *Client) GetProfileData(ctx context.Context, id ImsUeId) (ImsProfileData, error) {
	var p ImsProfileData
	return p, c.get(ctx, "GetProfileData", c.sdmURI(id, profileDataPath), &p)
}

// GetServerName is the Server Name Retrieval of TS 29.562 5.3.2.2.3.2:
// the S-CSCF assigned to the user. DATA_NOT_FOUND means the user has
// not registered yet (6.2.3.13).
func (c *Client) GetServerName(ctx context.Context, id ImsUeId) (ImsLocationData, error) {
	var l ImsLocationData
	return l, c.get(ctx, "GetServerName", c.sdmURI(id, serverNamePath), &l)
}

// GetScscfCapabilities is the S-CSCF Capabilities Retrieval of TS
// 29.562 5.3.2.2.3.1: the I-CSCF reads it after Authorize gave no
// S-CSCF name. DATA_NOT_FOUND means any S-CSCF will do (6.2.3.4).
func (c *Client) GetScscfCapabilities(ctx context.Context, id ImsUeId) (ScscfCapabilityList, error) {
	var l ScscfCapabilityList
	return l, c.get(ctx, "GetScscfCapabilities", c.sdmURI(id, scscfCapabilitiesPath), &l)
}

// GetImsAssocIds is the IMS Identity Data Retrieval of TS 29.562
// 5.3.2.2.2.1: the public identities of the user's implicit
// registration set. Where Sh asks by MSISDN, as the SC of the IP-SM-GW
// does, the caller names the user by the tel URI of the number, since
// {imsUeId} is a public identity here (6.2.3.2.2).
func (c *Client) GetImsAssocIds(ctx context.Context, id ImsUeId) (ImsAssociatedIdentities, error) {
	var a ImsAssociatedIdentities
	return a, c.get(ctx, "GetImsAssocIds", c.sdmURI(id, imsAssocIdsPath), &a)
}

// GetMsisdns is the MSISDN(s) Retrieval of TS 29.562 5.3.2.2.2.2.
//
// SIMPLIFIED: TS 29.562 6.2.3.3.3.1. The private-id query parameter,
// which asks for the C-MSISDN of one of several private identities, is
// not sent.
func (c *Client) GetMsisdns(ctx context.Context, id ImsUeId) (MsisdnList, error) {
	var m MsisdnList
	return m, c.get(ctx, "GetMsisdns", c.sdmURI(id, msisdnsPath), &m)
}

// smsRegistrationInfoURI adds the private-identity query parameter,
// which TS 29.562 6.2.3.30.3 requires when the public identity is
// shared by several private identities.
func (c *Client) smsRegistrationInfoURI(id ImsUeId, privateIdentity string) string {
	uri := c.sdmURI(id, smsRegistrationInfoPath)
	if privateIdentity != "" {
		uri += "?private-identity=" + url.QueryEscape(privateIdentity)
	}
	return uri
}

// GetSmsRegistrationInfo is the SMS Registration Information retrieval
// of TS 29.562 5.3.2.2.7.2. DATA_NOT_FOUND means no IP-SM-GW is
// registered (6.2.3.30.3.1).
func (c *Client) GetSmsRegistrationInfo(ctx context.Context, id ImsUeId, privateIdentity string) (SmsRegistrationInfo, error) {
	var s SmsRegistrationInfo
	return s, c.get(ctx, "GetSmsRegistrationInfo", c.smsRegistrationInfoURI(id, privateIdentity), &s)
}

// UpdateSmsRegistrationInfo is the SMS Registration Information
// Creation or Update of TS 29.562 5.3.2.7.6: the IP-SM-GW registers
// itself for the user's short messages, where Sh has a PUR of
// SMSRegistrationInfo. It returns the registration the HSS answered
// with (201 or 200), or nil for a 204.
func (c *Client) UpdateSmsRegistrationInfo(ctx context.Context, id ImsUeId, privateIdentity string, a IpSmGwAddress) (*SmsRegistrationInfo, error) {
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPut,
		URI:         c.smsRegistrationInfoURI(id, privateIdentity),
		Body:        a,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   string(c.nfType),
	})
	if err != nil || resp.Status == http.StatusNoContent {
		return nil, err
	}
	var s SmsRegistrationInfo
	if err := decodeAnswer("UpdateSmsRegistrationInfo", resp, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSmsRegistrationInfo is the SMS Registration Information
// Deletion of TS 29.562 5.3.2.7.7, where Sh has a PUR that empties
// SMSRegistrationInfo. The HSS answers 204.
func (c *Client) DeleteSmsRegistrationInfo(ctx context.Context, id ImsUeId, privateIdentity string) error {
	_, err := api.Send(ctx, c.http, sbi.Request{
		Method:    http.MethodDelete,
		URI:       c.smsRegistrationInfoURI(id, privateIdentity),
		UserAgent: string(c.nfType),
	})
	return err
}

// ImsSDM is what an HSS implements behind NewImsSDMHandler. id is the
// {imsUeId} of the URI. privateIdentity is the private-identity query
// parameter, empty when absent. A *ProblemError is the answer, such as
// NewProblem(CauseDataNotFound, ""); any other error is answered with
// 500.
type ImsSDM interface {
	GetProfileData(ctx context.Context, id ImsUeId) (ImsProfileData, error)
	GetServerName(ctx context.Context, id ImsUeId) (ImsLocationData, error)
	GetScscfCapabilities(ctx context.Context, id ImsUeId) (ScscfCapabilityList, error)
	GetImsAssocIds(ctx context.Context, id ImsUeId) (ImsAssociatedIdentities, error)
	GetMsisdns(ctx context.Context, id ImsUeId) (MsisdnList, error)
	GetSmsRegistrationInfo(ctx context.Context, id ImsUeId, privateIdentity string) (SmsRegistrationInfo, error)
	// UpdateSmsRegistrationInfo stores a; created makes a 201 with
	// Location, otherwise a non-nil result is a 200 and nil a 204.
	UpdateSmsRegistrationInfo(ctx context.Context, id ImsUeId, privateIdentity string, a IpSmGwAddress) (result *SmsRegistrationInfo, created bool, err error)
	DeleteSmsRegistrationInfo(ctx context.Context, id ImsUeId, privateIdentity string) error
}

// NewImsSDMHandler returns the HTTP handler of the Nhss_imsSDM API, to
// serve with NewServer. It keeps no state.
func NewImsSDMHandler(hss ImsSDM) http.Handler {
	mux := http.NewServeMux()
	user := func(r *http.Request) ImsUeId { return ImsUeId(r.PathValue("imsUeId")) }
	base := SDMRoot + "/{imsUeId}"
	mux.HandleFunc("GET "+base+profileDataPath, handleGet(func(r *http.Request) (ImsProfileData, error) {
		return hss.GetProfileData(r.Context(), user(r))
	}))
	mux.HandleFunc("GET "+base+serverNamePath, handleGet(func(r *http.Request) (ImsLocationData, error) {
		return hss.GetServerName(r.Context(), user(r))
	}))
	mux.HandleFunc("GET "+base+scscfCapabilitiesPath, handleGet(func(r *http.Request) (ScscfCapabilityList, error) {
		return hss.GetScscfCapabilities(r.Context(), user(r))
	}))
	mux.HandleFunc("GET "+base+imsAssocIdsPath, handleGet(func(r *http.Request) (ImsAssociatedIdentities, error) {
		return hss.GetImsAssocIds(r.Context(), user(r))
	}))
	mux.HandleFunc("GET "+base+msisdnsPath, handleGet(func(r *http.Request) (MsisdnList, error) {
		return hss.GetMsisdns(r.Context(), user(r))
	}))
	mux.HandleFunc("GET "+base+smsRegistrationInfoPath, handleGet(func(r *http.Request) (SmsRegistrationInfo, error) {
		return hss.GetSmsRegistrationInfo(r.Context(), user(r), r.URL.Query().Get("private-identity"))
	}))
	mux.HandleFunc("PUT "+base+smsRegistrationInfoPath, func(w http.ResponseWriter, r *http.Request) {
		var a IpSmGwAddress
		if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &a); p != nil {
			api.WriteError(w, p)
			return
		}
		if a.IpSmGwNumber == "" {
			api.WriteError(w, api.Missing("/ipSmGwNumber"))
			return
		}
		result, created, err := hss.UpdateSmsRegistrationInfo(r.Context(), user(r), r.URL.Query().Get("private-identity"), a)
		writeResource(w, r, result, created, err)
	})
	mux.HandleFunc("DELETE "+base+smsRegistrationInfoPath, func(w http.ResponseWriter, r *http.Request) {
		if err := hss.DeleteSmsRegistrationInfo(r.Context(), user(r), r.URL.Query().Get("private-identity")); err != nil {
			api.WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// handleGet serves a GET whose answer is one resource, with 200.
func handleGet[T any](get func(r *http.Request) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := get(r)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, v)
	}
}
