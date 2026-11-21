package nhss

import (
	"context"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// Nhss_imsUEAuthentication (TS 29.562 5.4, 6.3): the S-CSCF asks the
// HSS for the data to authenticate a user, where Cx has the MAR/MAA.

// SipAuthenticationInfoRequest is the body of GenerateSipAuthData (TS
// 29.562 6.3.6.2.2). SipNumberAuthItems and ResynchronizationInfo apply
// to IMS-AKA only; without SipNumberAuthItems the HSS returns one
// vector.
type SipAuthenticationInfoRequest struct {
	CscfServerName          string                  `json:"cscfServerName"`
	SipAuthenticationScheme SipAuthenticationScheme `json:"sipAuthenticationScheme"`
	SipNumberAuthItems      int                     `json:"sipNumberAuthItems,omitempty"`
	ResynchronizationInfo   *ResynchronizationInfo  `json:"resynchronizationInfo,omitempty"`
}

// ResynchronizationInfo is the RAND and AUTS of a failed sequence
// number check (TS 29.562 6.3.6.2.4, TS 33.203), each in hexadecimal
// as the Rand and Auts types of TS 29.503 write them: 32 and 28
// characters. On Cx the two travel together in SIP-Authorization.
type ResynchronizationInfo struct {
	Rand string `json:"rand"`
	Auts string `json:"auts"`
}

// SipAuthenticationInfoResult is the answer of GenerateSipAuthData (TS
// 29.562 6.3.6.2.3): IMS-AKA vectors, or the data of SIP Digest.
//
// The table of 6.3.6.2.3 also lists a mandatory sipAuthenticationScheme
// and no impi; the OpenAPI, which is what goes on the wire, requires
// impi and has no sipAuthenticationScheme. This type follows the
// OpenAPI: the scheme is the one of the request, and which of
// ThreeGAkaAvs and DigestAuth is present shows it.
//
// SIMPLIFIED: TS 29.562 6.3.6.2.3. lineIdentifierList and ipAddress,
// for NBA and GIBA, are left out; the project authenticates with
// IMS-AKA and SIP Digest only.
type SipAuthenticationInfoResult struct {
	Impi string `json:"impi"`
	// ThreeGAkaAvs is "3gAkaAvs"; a Go name cannot start with a digit.
	ThreeGAkaAvs []ThreeGAkaAv         `json:"3gAkaAvs,omitempty"`
	DigestAuth   *DigestAuthentication `json:"digestAuth,omitempty"`
}

// ThreeGAkaAv is one IMS-AKA authentication vector, the 3GAkaAv type
// of TS 29.562 6.3.6.2.5. Each part is in hexadecimal, as TS 29.503
// writes its Rand, Xres, Autn, ConfidentialityKey and IntegrityKey: 32
// characters, except Xres with 8 to 32. Cx carries the same parts as
// octets in SIP-Authenticate (RAND and AUTN together),
// SIP-Authorization, Confidentiality-Key and Integrity-Key.
type ThreeGAkaAv struct {
	Rand string `json:"rand"`
	Xres string `json:"xres"`
	Autn string `json:"autn"`
	Ck   string `json:"ck"`
	Ik   string `json:"ik"`
}

// DigestAuthentication is the data of SIP Digest (TS 29.562
// 6.3.6.2.6, RFC 4740): the realm, the algorithm, the quality of
// protection and H(A1), what SIP-Digest-Authenticate holds on Cx.
type DigestAuthentication struct {
	DigestRealm     string             `json:"digestRealm"`
	DigestAlgorithm SipDigestAlgorithm `json:"digestAlgorithm"`
	DigestQop       SipDigestQop       `json:"digestQop"`
	Ha1             string             `json:"ha1"`
}

// SipAuthenticationScheme is an authentication scheme (TS 29.562
// 6.3.6.3.3). The values are those of SIP-Authentication-Scheme on Cx;
// SIP Digest is DIGEST-HTTP.
type SipAuthenticationScheme string

const (
	DigestAKAv1MD5 SipAuthenticationScheme = "DIGEST-AKAV1-MD5"
	DigestHTTP     SipAuthenticationScheme = "DIGEST-HTTP"
	// UnknownScheme lets the HSS choose (TS 29.562 6.3.6.3.3).
	UnknownScheme SipAuthenticationScheme = "UNKNOWN"
)

// SipDigestAlgorithm is the algorithm of SIP Digest (TS 29.562
// 6.3.6.3.4).
type SipDigestAlgorithm string

const (
	MD5     SipDigestAlgorithm = "MD5"
	MD5Sess SipDigestAlgorithm = "MD5_SESS"
)

// SipDigestQop is the quality of protection of SIP Digest (TS 29.562
// 6.3.6.3.5).
type SipDigestQop string

const (
	QopAuth    SipDigestQop = "AUTH"
	QopAuthInt SipDigestQop = "AUTH_INT"
)

// GenerateSipAuthData is the Authentication Information Retrieval of
// TS 29.562 5.4.2.2.2: a POST of the generate-sip-auth-data custom
// operation on the security information of the private identity impi.
// The HSS answers 200 with the result; a scheme it does not support is
// a *ProblemError with CauseUnsupportedSIPAuthenticationScheme.
func (c *Client) GenerateSipAuthData(ctx context.Context, impi string, req SipAuthenticationInfoRequest) (SipAuthenticationInfoResult, error) {
	uri := c.apiRoot + UEAURoot + "/" + segment(impi) + "/security-information/generate-sip-auth-data"
	resp, err := api.Send(ctx, c.http, sbi.Request{
		Method:      http.MethodPost,
		URI:         uri,
		Body:        req,
		ContentType: sbi.ContentTypeJSON,
		UserAgent:   string(c.nfType),
	})
	if err != nil {
		return SipAuthenticationInfoResult{}, err
	}
	var r SipAuthenticationInfoResult
	return r, decodeAnswer("GenerateSipAuthData", resp, &r)
}

// ImsUEAU is what an HSS implements behind NewImsUEAUHandler. impi is
// the {impi} of the URI, which the OpenAPI also allows with the prefix
// "impi-" (TS 29.562 6.3.3.2.2); the handler passes it as it came. A
// *ProblemError is the answer; any other error is answered with 500.
type ImsUEAU interface {
	GenerateSipAuthData(ctx context.Context, impi string, req SipAuthenticationInfoRequest) (SipAuthenticationInfoResult, error)
}

// NewImsUEAUHandler returns the HTTP handler of the Nhss_imsUEAU API,
// to serve with NewServer. It keeps no state.
func NewImsUEAUHandler(hss ImsUEAU) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+UEAURoot+"/{impi}/security-information/generate-sip-auth-data", func(w http.ResponseWriter, r *http.Request) {
		var req SipAuthenticationInfoRequest
		if p := api.ReadJSON(w, r, sbi.ContentTypeJSON, &req); p != nil {
			api.WriteError(w, p)
			return
		}
		var absent []string
		if req.CscfServerName == "" {
			absent = append(absent, "/cscfServerName")
		}
		if req.SipAuthenticationScheme == "" {
			absent = append(absent, "/sipAuthenticationScheme")
		}
		if len(absent) > 0 {
			api.WriteError(w, api.Missing(absent...))
			return
		}
		result, err := hss.GenerateSipAuthData(r.Context(), r.PathValue("impi"), req)
		if err != nil {
			api.WriteError(w, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, result)
	})
	return mux
}
