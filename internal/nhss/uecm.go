package nhss

import (
	"context"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// Nhss_imsUEContextManagement (TS 29.562 5.2, 6.1): the registration of
// a user's S-CSCF in the HSS, and the authorization of a registration
// or a session.

// AuthorizationRequest is the body of Authorize (TS 29.562 6.1.6.2.2),
// what the UAR carries on Cx. Impi and VisitedNetworkIdentifier are
// present when the authorization is for a registration or
// deregistration.
type AuthorizationRequest struct {
	Impi                     string            `json:"impi,omitempty"`
	AuthorizationType        AuthorizationType `json:"authorizationType"`
	VisitedNetworkIdentifier string            `json:"visitedNetworkIdentifier,omitempty"`
}

// AuthorizationResponse is the answer of Authorize (TS 29.562
// 6.1.6.2.3), what the UAA and the LIA carry on Cx: the S-CSCF that
// serves the user, or, when none does, what the I-CSCF needs to choose
// one.
type AuthorizationResponse struct {
	AuthorizationResult          AuthorizationResult                  `json:"authorizationResult"`
	CscfServerName               string                               `json:"cscfServerName,omitempty"`
	ScscfSelectionAssistanceInfo *ScscfSelectionAssistanceInformation `json:"scscfSelectionAssistanceInfo,omitempty"`
}

// ScscfSelectionAssistanceInformation helps the I-CSCF choose an
// S-CSCF (TS 29.562 6.2.6.2.51; the type belongs to the Nhss_imsSDM
// API and Authorize uses it). It has the capabilities the S-CSCF must
// or may have, or the names of S-CSCFs to choose from.
type ScscfSelectionAssistanceInformation struct {
	ScscfCapabilityList *ScscfCapabilityList `json:"scscfCapabilityList,omitempty"`
	ScscfNames          []string             `json:"scscfNames,omitempty"`
}

// ScscfCapabilityList is the Server-Capabilities AVP of Cx in JSON (TS
// 29.562 6.2.6.2.2): operator-defined numbers that the I-CSCF matches
// against its list of S-CSCFs.
type ScscfCapabilityList struct {
	MandatoryCapabilityList []int `json:"mandatoryCapabilityList,omitempty"`
	OptionalCapabilityList  []int `json:"optionalCapabilityList,omitempty"`
}

// ScscfRegistration is the S-CSCF Registration resource (TS 29.562
// 6.1.6.2.10), what the SAR carries on Cx. ImsRegistrationType and
// CscfServerName are required. DeregCallbackUri is where the HSS sends
// a DeregistrationNotification; it must say which public identity or
// implicit registration set it is about, because the notification
// itself carries only the private identity. IrsImpus comes back in
// the answer with the public identities of the implicit registration
// set.
//
// SIMPLIFIED: TS 29.562 6.1.6.2.10. The multiple registration,
// restoration, reselection, wildcard and loose route attributes are
// left out. The table makes scscfInstanceId mandatory, the OpenAPI
// does not; without an NRF the S-CSCF has no NF instance ID, so it is
// sent only when the caller has one.
type ScscfRegistration struct {
	Impi                string              `json:"impi,omitempty"`
	ImsRegistrationType ImsRegistrationType `json:"imsRegistrationType"`
	CscfServerName      string              `json:"cscfServerName"`
	ScscfInstanceId     string              `json:"scscfInstanceId,omitempty"`
	DeregCallbackUri    string              `json:"deregCallbackUri,omitempty"`
	IrsImpus            []string            `json:"irsImpus,omitempty"`
}

// DeregistrationData is the body of a DeregistrationNotification (TS
// 29.562 6.1.6.2.13), what the RTR carries on Cx, and of the S-CSCF's
// 200 answer to it.
type DeregistrationData struct {
	DeregReason DeregistrationReason `json:"deregReason"`
	Impi        string               `json:"impi"`
	// AssociatedImpis lists, in an answer, the other private identities
	// that were deregistered too.
	AssociatedImpis []string `json:"associatedImpis,omitempty"`
}

// DeregistrationReason is why the HSS deregisters (TS 29.562
// 6.1.6.2.14): the Deregistration-Reason AVP of Cx. The table makes
// reasonText optional; the OpenAPI requires it, so it is always sent.
type DeregistrationReason struct {
	ReasonCode DeregistrationReasonCode `json:"reasonCode"`
	ReasonText string                   `json:"reasonText"`
}

// AuthorizationType is what Authorize is asked for (TS 29.562
// 6.1.6.3.3). Cx has a third value, REGISTRATION_AND_CAPABILITIES; on
// Nhss the I-CSCF reads the capabilities with GetScscfCapabilities
// instead.
type AuthorizationType string

const (
	AuthorizationTypeRegistration   AuthorizationType = "REGISTRATION"
	AuthorizationTypeDeregistration AuthorizationType = "DEREGISTRATION"
)

// AuthorizationResult is the outcome of Authorize (TS 29.562
// 6.1.6.3.4): the DIAMETER_FIRST_REGISTRATION and
// DIAMETER_SUBSEQUENT_REGISTRATION results of a UAA.
type AuthorizationResult string

const (
	FirstRegistration      AuthorizationResult = "FIRST_REGISTRATION"
	SubsequentRegistration AuthorizationResult = "SUBSEQUENT_REGISTRATION"
)

// ImsRegistrationType is the type of an S-CSCF registration or
// deregistration (TS 29.562 6.1.6.3.5): the Server-Assignment-Type of a
// SAR.
type ImsRegistrationType string

const (
	InitialRegistration          ImsRegistrationType = "INITIAL_REGISTRATION"
	ReRegistration               ImsRegistrationType = "RE_REGISTRATION"
	TimeoutDeregistration        ImsRegistrationType = "TIMEOUT_DEREGISTRATION"
	UserDeregistration           ImsRegistrationType = "USER_DEREGISTRATION"
	AdministrativeDeregistration ImsRegistrationType = "ADMINISTRATIVE_DEREGISTRATION"
	AuthenticationFailure        ImsRegistrationType = "AUTHENTICATION_FAILURE"
	AuthenticationTimeout        ImsRegistrationType = "AUTHENTICATION_TIMEOUT"
	UnregisteredUser             ImsRegistrationType = "UNREGISTERED_USER"
)

// DeregistrationReasonCode is the Reason-Code of a deregistration (TS
// 29.562 6.1.6.3.7).
type DeregistrationReasonCode string

const (
	PermanentTermination DeregistrationReasonCode = "PERMANENT_TERMINATION"
	NewServerAssigned    DeregistrationReasonCode = "NEW_SERVER_ASSIGNED"
	RemoveSCSCF          DeregistrationReasonCode = "REMOVE_S-CSCF"
	ServerChange         DeregistrationReasonCode = "SERVER_CHANGE"
)

// Authorize is the Authorize operation (TS 29.562 5.2.2.5.2): a POST of
// the authorize custom operation on the public identity impu, a SIP or
// tel URI. The I-CSCF sends it for a REGISTER, where Cx has the UAR.
//
// TS 23.228 Table AA.2.2.2-1 also maps Cx-Location-Query, the LIR of a
// terminating request, onto Authorize, and TS 29.562 5.2.2.5.1 lets it
// authorize sessions. But Release 20 defines no AuthorizationType for
// a session: the enumeration of 6.1.6.3.3 has only REGISTRATION and
// DEREGISTRATION. Which value to send for a terminating request is
// therefore left to the caller.
func (c *Client) Authorize(ctx context.Context, impu string, req AuthorizationRequest) (AuthorizationResponse, error) {
	uri := c.apiRoot + UECMRoot + "/" + segment(impu) + "/authorize"
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         uri,
		Body:        req,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   string(c.nfType),
	})
	if err != nil {
		return AuthorizationResponse{}, err
	}
	var a AuthorizationResponse
	return a, decodeAnswer("Authorize", resp, &a)
}

// Registration is the S-CSCF registration of TS 29.562 5.2.2.2.2: a PUT
// of the S-CSCF Registration of the public identity id, with an
// ImsRegistrationType of INITIAL_REGISTRATION, RE_REGISTRATION or
// UNREGISTERED_USER. It returns the registration the HSS answered with
// (201 or 200), or nil when it answered 204. Another S-CSCF already
// serving the user is a *ProblemError with
// CauseIdentityAlreadyRegistered, and its name is in
// Problem.ScscfServerName.
func (c *Client) Registration(ctx context.Context, id ImsUeId, reg ScscfRegistration) (*ScscfRegistration, error) {
	return c.putScscfRegistration(ctx, "Registration", id, reg)
}

// Deregistration is the S-CSCF deregistration of TS 29.562 5.2.2.4.2:
// a PUT of the same resource with a deregistration type. id may name a
// public identity, which deregisters its implicit registration set, or
// a private identity, which deregisters all its public identities.
func (c *Client) Deregistration(ctx context.Context, id ImsUeId, reg ScscfRegistration) (*ScscfRegistration, error) {
	return c.putScscfRegistration(ctx, "Deregistration", id, reg)
}

func (c *Client) putScscfRegistration(ctx context.Context, operation string, id ImsUeId, reg ScscfRegistration) (*ScscfRegistration, error) {
	uri := c.apiRoot + UECMRoot + "/" + segment(string(id)) + "/scscf-registration"
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPut,
		URI:         uri,
		Body:        reg,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   string(c.nfType),
	})
	if err != nil || resp.Status == http.StatusNoContent {
		return nil, err
	}
	var r ScscfRegistration
	if err := decodeAnswer(operation, resp, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ImsUECM is what an HSS implements behind NewImsUECMHandler. The
// handler has checked the media type and the mandatory attributes. An
// error that is a *ProblemError is the answer, built with NewProblem;
// for IDENTITY_ALREADY_REGISTERED the implementation also sets
// Problem.ScscfServerName. Any other error is answered with 500.
type ImsUECM interface {
	// Authorize authorizes the public identity impu (TS 29.562
	// 5.2.2.5.2) and answers 200.
	Authorize(ctx context.Context, impu string, req AuthorizationRequest) (AuthorizationResponse, error)
	// PutScscfRegistration serves both Registration (5.2.2.2.2) and
	// Deregistration (5.2.2.4.2); reg.ImsRegistrationType tells them
	// apart. created makes a 201 with Location; otherwise a non-nil
	// result is a 200 and nil a 204.
	PutScscfRegistration(ctx context.Context, id ImsUeId, reg ScscfRegistration) (result *ScscfRegistration, created bool, err error)
}

// NewImsUECMHandler returns the HTTP handler of the Nhss_imsUECM API,
// to serve with NewServer. It keeps no state.
func NewImsUECMHandler(hss ImsUECM) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+UECMRoot+"/{impu}/authorize", func(w http.ResponseWriter, r *http.Request) {
		var req AuthorizationRequest
		if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &req); p != nil {
			api.WriteError(w, p)
			return
		}
		if req.AuthorizationType == "" {
			api.WriteError(w, api.Missing("/authorizationType"))
			return
		}
		a, err := hss.Authorize(r.Context(), r.PathValue("impu"), req)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, a)
	})
	mux.HandleFunc("PUT "+UECMRoot+"/{imsUeId}/scscf-registration", func(w http.ResponseWriter, r *http.Request) {
		var reg ScscfRegistration
		if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &reg); p != nil {
			api.WriteError(w, p)
			return
		}
		var absent []string
		if reg.ImsRegistrationType == "" {
			absent = append(absent, "/imsRegistrationType")
		}
		if reg.CscfServerName == "" {
			absent = append(absent, "/cscfServerName")
		}
		if len(absent) > 0 {
			api.WriteError(w, api.Missing(absent...))
			return
		}
		result, created, err := hss.PutScscfRegistration(r.Context(), ImsUeId(r.PathValue("imsUeId")), reg)
		writeResource(w, r, result, created, err)
	})
	return mux
}

// SCSCF is what an S-CSCF does with the DeregistrationNotification of
// the HSS (TS 29.562 5.2.2.3.2), behind
// NewDeregistrationNotificationHandler. callbackPath is the path of the
// deregCallbackUri the S-CSCF registered, which tells it which public
// identities the notification is about. It returns nil for a 204, or
// the result of the deregistration for a 200; a *ProblemError, such as
// NewProblem(CauseContextNotFound, ""), is sent as it is.
type SCSCF interface {
	DeregistrationNotification(ctx context.Context, callbackPath string, d DeregistrationData) (*DeregistrationData, error)
}

// NewDeregistrationNotificationHandler returns the handler the S-CSCF
// serves at its deregCallbackUri. Every POST that reaches it is a
// DeregistrationNotification; the S-CSCF chooses the paths.
func NewDeregistrationNotificationHandler(s SCSCF) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			api.WriteError(w, &ProblemError{Status: http.StatusMethodNotAllowed})
			return
		}
		var d DeregistrationData
		if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &d); p != nil {
			api.WriteError(w, p)
			return
		}
		var absent []string
		if d.DeregReason.ReasonCode == "" {
			absent = append(absent, "/deregReason/reasonCode")
		}
		if d.Impi == "" {
			absent = append(absent, "/impi")
		}
		if len(absent) > 0 {
			api.WriteError(w, api.Missing(absent...))
			return
		}
		result, err := s.DeregistrationNotification(r.Context(), r.URL.Path, d)
		writeResource(w, r, result, false, err)
	})
}

// Notifier sends the notifications of an HSS. It keeps no state: the
// caller gives the callback URI the S-CSCF registered.
type Notifier struct {
	http *http.Client
}

// callbackDeregistrationNotification is the 3gpp-Sbi-Callback value of
// the notification: the API name of TS 29.562 Annex A.2 and the name
// of the callback in its OpenAPI, as TS 29.500 Annex B builds them.
const callbackDeregistrationNotification = "Nhss_imsUECM_deregistrationNotification"

// NewNotifier returns a notifier that speaks cleartext HTTP/2.
func NewNotifier() *Notifier { return &Notifier{http: sbi.NewHTTPClient()} }

// DeregistrationNotification is the HSS initiated S-CSCF
// deregistration of TS 29.562 5.2.2.3.2: a POST of d to
// deregCallbackUri. It returns nil when the S-CSCF answers 204, or the
// result it answered with 200.
func (n *Notifier) DeregistrationNotification(ctx context.Context, deregCallbackUri string, d DeregistrationData) (*DeregistrationData, error) {
	resp, err := api.Send(ctx, n.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         deregCallbackUri,
		Body:        d,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   string(NFTypeHSS),
		Callback:    callbackDeregistrationNotification,
	})
	if err != nil || resp.Status == http.StatusNoContent || len(resp.Body) == 0 {
		return nil, err
	}
	var result DeregistrationData
	if err := decodeAnswer("DeregistrationNotification", resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
