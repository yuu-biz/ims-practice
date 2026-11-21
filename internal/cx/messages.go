package cx

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// UserAuthorizationRequest is the UAR of TS 29.229 6.1.1. The I-CSCF
// sends it for every REGISTER to learn which S-CSCF serves the user
// (TS 29.228 6.1.1).
type UserAuthorizationRequest struct {
	Header
	UserName                 string // the private user identity
	PublicIdentity           string
	VisitedNetworkIdentifier string
	UserAuthorizationType    UserAuthorizationType
}

func (r UserAuthorizationRequest) Message() *diam.Message {
	m := newRequest(CommandUserAuthorization)
	r.begin(m)
	r.destinationHost(m)
	r.destinationRealm(m)
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(r.UserName))
	m.AddAVP(vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(r.PublicIdentity)))
	m.AddAVP(vendorAVP(avpVisitedNetworkIdentifier, mv, datatype.OctetString(r.VisitedNetworkIdentifier)))
	m.AddAVP(vendorAVP(avpUserAuthorizationType, mv, datatype.Enumerated(r.UserAuthorizationType)))
	return m
}

func ParseUserAuthorizationRequest(m *diam.Message) (UserAuthorizationRequest, error) {
	var raw struct {
		requestHeader
		UserName                 string `avp:"User-Name"`
		PublicIdentity           string `avp:"Public-Identity"`
		VisitedNetworkIdentifier string `avp:"Visited-Network-Identifier"`
		UserAuthorizationType    *int32 `avp:"User-Authorization-Type,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return UserAuthorizationRequest{}, fmt.Errorf("UAR: %w", err)
	}
	r := UserAuthorizationRequest{
		Header:                   raw.header(),
		UserName:                 raw.UserName,
		PublicIdentity:           raw.PublicIdentity,
		VisitedNetworkIdentifier: raw.VisitedNetworkIdentifier,
	}
	// TS 29.229 6.3.24: an absent User-Authorization-Type means
	// REGISTRATION.
	if raw.UserAuthorizationType != nil {
		r.UserAuthorizationType = UserAuthorizationType(*raw.UserAuthorizationType)
	}
	return r, nil
}

// ServerCapabilities is the Server-Capabilities AVP, TS 29.229 6.3.4.
// The values are operator-defined numbers that the I-CSCF matches
// against its list of S-CSCFs (TS 29.228 6.7).
type ServerCapabilities struct {
	MandatoryCapabilities []uint32
	OptionalCapabilities  []uint32
	ServerNames           []string
}

func (c ServerCapabilities) avp() *diam.AVP {
	var children []*diam.AVP
	for _, n := range c.MandatoryCapabilities {
		children = append(children, vendorAVP(avpMandatoryCapability, mv, datatype.Unsigned32(n)))
	}
	for _, n := range c.OptionalCapabilities {
		children = append(children, vendorAVP(avpOptionalCapability, mv, datatype.Unsigned32(n)))
	}
	for _, name := range c.ServerNames {
		children = append(children, vendorAVP(avpServerName, mv, datatype.UTF8String(name)))
	}
	return vendorAVP(avpServerCapabilities, mv, &diam.GroupedAVP{AVP: children})
}

// UserAuthorizationAnswer is the UAA of TS 29.229 6.1.2. On success it
// carries either the Server-Name of the S-CSCF already assigned, or the
// capabilities the I-CSCF needs to pick one (TS 29.228 6.1.1.1).
type UserAuthorizationAnswer struct {
	Result             Result
	ServerName         string
	ServerCapabilities *ServerCapabilities
}

// Message builds the UAA that answers request.
func (a UserAuthorizationAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	m := NewAnswer(request, local, a.Result)
	if a.ServerName != "" {
		m.AddAVP(vendorAVP(avpServerName, mv, datatype.UTF8String(a.ServerName)))
	}
	if a.ServerCapabilities != nil {
		m.AddAVP(a.ServerCapabilities.avp())
	}
	return m
}

func ParseUserAuthorizationAnswer(m *diam.Message) (UserAuthorizationAnswer, error) {
	var raw struct {
		answerResult
		ServerName         string `avp:"Server-Name,omitempty"`
		ServerCapabilities *struct {
			Mandatory   []uint32 `avp:"Mandatory-Capability,omitempty"`
			Optional    []uint32 `avp:"Optional-Capability,omitempty"`
			ServerNames []string `avp:"Server-Name,omitempty"`
		} `avp:"Server-Capabilities,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return UserAuthorizationAnswer{}, fmt.Errorf("UAA: %w", err)
	}
	result, err := raw.result()
	if err != nil {
		return UserAuthorizationAnswer{}, fmt.Errorf("UAA: %w", err)
	}
	a := UserAuthorizationAnswer{Result: result, ServerName: raw.ServerName}
	if c := raw.ServerCapabilities; c != nil {
		a.ServerCapabilities = &ServerCapabilities{
			MandatoryCapabilities: c.Mandatory,
			OptionalCapabilities:  c.Optional,
			ServerNames:           c.ServerNames,
		}
	}
	return a, nil
}

// MultimediaAuthRequest is the MAR of TS 29.229 6.1.7. The S-CSCF asks
// the HSS for authentication data (TS 29.228 6.3).
type MultimediaAuthRequest struct {
	Header
	UserName                string
	PublicIdentity          string
	SIPAuthenticationScheme string
	// SIPAuthorization is set only after an IMS-AKA synchronization
	// failure, and only with SchemeDigestAKAv1MD5 (TS 29.228 Table
	// 6.3.2).
	SIPAuthorization   *Resynchronization
	SIPNumberAuthItems uint32
	ServerName         string
}

// Resynchronization is the SIP-Authorization AVP of a MAR, 30 octets:
// the RAND that the S-CSCF sent to the UE and the AUTS that the UE
// answered with (TS 29.229 6.3.11).
type Resynchronization struct {
	RAND aka.RAND
	AUTS aka.AUTS
}

func (r Resynchronization) octets() []byte {
	return append(r.RAND[:], r.AUTS[:]...)
}

func parseResynchronization(b []byte) (*Resynchronization, error) {
	var r Resynchronization
	if len(b) != len(r.RAND)+len(r.AUTS) {
		return nil, fmt.Errorf("SIP-Authorization has %d octets, want RAND and AUTS, %d", len(b), len(r.RAND)+len(r.AUTS))
	}
	copy(r.RAND[:], b)
	copy(r.AUTS[:], b[len(r.RAND):])
	return &r, nil
}

func (r MultimediaAuthRequest) Message() *diam.Message {
	m := newRequest(CommandMultimediaAuth)
	r.begin(m)
	// The MAR format puts Destination-Realm before Destination-Host.
	r.destinationRealm(m)
	r.destinationHost(m)
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(r.UserName))
	m.AddAVP(vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(r.PublicIdentity)))
	item := []*diam.AVP{vendorAVP(avpSIPAuthenticationScheme, mv, datatype.UTF8String(r.SIPAuthenticationScheme))}
	if r.SIPAuthorization != nil {
		item = append(item, vendorAVP(avpSIPAuthorization, mv, datatype.OctetString(r.SIPAuthorization.octets())))
	}
	m.AddAVP(vendorAVP(avpSIPAuthDataItem, mv, &diam.GroupedAVP{AVP: item}))
	m.AddAVP(vendorAVP(avpSIPNumberAuthItems, mv, datatype.Unsigned32(r.SIPNumberAuthItems)))
	m.AddAVP(vendorAVP(avpServerName, mv, datatype.UTF8String(r.ServerName)))
	return m
}

func ParseMultimediaAuthRequest(m *diam.Message) (MultimediaAuthRequest, error) {
	var raw struct {
		requestHeader
		UserName        string `avp:"User-Name"`
		PublicIdentity  string `avp:"Public-Identity"`
		SIPAuthDataItem struct {
			Scheme        string `avp:"SIP-Authentication-Scheme"`
			Authorization []byte `avp:"SIP-Authorization,omitempty"`
		} `avp:"SIP-Auth-Data-Item"`
		SIPNumberAuthItems uint32 `avp:"SIP-Number-Auth-Items"`
		ServerName         string `avp:"Server-Name"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return MultimediaAuthRequest{}, fmt.Errorf("MAR: %w", err)
	}
	r := MultimediaAuthRequest{
		Header:                  raw.header(),
		UserName:                raw.UserName,
		PublicIdentity:          raw.PublicIdentity,
		SIPAuthenticationScheme: raw.SIPAuthDataItem.Scheme,
		SIPNumberAuthItems:      raw.SIPNumberAuthItems,
		ServerName:              raw.ServerName,
	}
	if raw.SIPAuthDataItem.Authorization != nil {
		resync, err := parseResynchronization(raw.SIPAuthDataItem.Authorization)
		if err != nil {
			return MultimediaAuthRequest{}, fmt.Errorf("MAR: %w", err)
		}
		r.SIPAuthorization = resync
	}
	return r, nil
}

// SIPDigestAuthenticate is the SIP-Digest-Authenticate AVP, TS 29.229
// 6.3.36. The HSS hands over H(A1), never the password (TS 29.228
// Table 6.3.7).
type SIPDigestAuthenticate struct {
	DigestRealm     string
	DigestAlgorithm string // empty means MD5
	DigestQoP       string
	DigestHA1       string // hexadecimal, RFC 5090 3.19
}

func (d SIPDigestAuthenticate) avp() *diam.AVP {
	children := []*diam.AVP{diam.NewAVP(avpDigestRealm, avp.Mbit, 0, datatype.UTF8String(d.DigestRealm))}
	if d.DigestAlgorithm != "" {
		children = append(children, diam.NewAVP(avpDigestAlgorithm, avp.Mbit, 0, datatype.UTF8String(d.DigestAlgorithm)))
	}
	children = append(children,
		diam.NewAVP(avpDigestQoP, avp.Mbit, 0, datatype.UTF8String(d.DigestQoP)),
		diam.NewAVP(avpDigestHA1, avp.Mbit, 0, datatype.UTF8String(d.DigestHA1)),
	)
	return vendorAVP(avpSIPDigestAuthenticate, v, &diam.GroupedAVP{AVP: children})
}

// MultimediaAuthAnswer is the MAA of TS 29.229 6.1.8. It holds one
// SIP-Auth-Data-Item: for SIP Digest, SIPDigestAuthenticate (TS 29.228
// Table 6.3.7); for IMS-AKA, AuthenticationVector (Table 6.3.5).
type MultimediaAuthAnswer struct {
	Result                  Result
	UserName                string
	PublicIdentity          string
	SIPAuthenticationScheme string
	SIPDigestAuthenticate   *SIPDigestAuthenticate
	// AuthenticationVector travels as SIP-Authenticate (RAND || AUTN,
	// TS 29.229 6.3.10), SIP-Authorization (XRES, 6.3.11),
	// Confidentiality-Key (CK, 6.3.27) and Integrity-Key (IK, 6.3.28).
	AuthenticationVector *aka.AuthenticationVector
}

// akaItemNumber is the SIP-Item-Number of the only vector in a MAA. The
// item number orders several vectors (TS 29.228 Table 6.3.5); it is
// sent with one too, so that the item reads the same in a capture.
const akaItemNumber = 1

func (a MultimediaAuthAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	m := NewAnswer(request, local, a.Result)
	if !a.Result.Success() {
		return m
	}
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(a.UserName))
	m.AddAVP(vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(a.PublicIdentity)))
	m.AddAVP(vendorAVP(avpSIPNumberAuthItems, mv, datatype.Unsigned32(1)))
	var item []*diam.AVP
	if av := a.AuthenticationVector; av != nil {
		item = append(item,
			vendorAVP(avpSIPItemNumber, mv, datatype.Unsigned32(akaItemNumber)),
			vendorAVP(avpSIPAuthenticationScheme, mv, datatype.UTF8String(a.SIPAuthenticationScheme)),
			vendorAVP(avpSIPAuthenticate, mv, datatype.OctetString(append(av.RAND[:], av.AUTN[:]...))),
			vendorAVP(avpSIPAuthorization, mv, datatype.OctetString(av.XRES)),
			vendorAVP(avpConfidentialityKey, mv, datatype.OctetString(av.CK[:])),
			vendorAVP(avpIntegrityKey, mv, datatype.OctetString(av.IK[:])),
		)
	} else {
		item = append(item, vendorAVP(avpSIPAuthenticationScheme, mv, datatype.UTF8String(a.SIPAuthenticationScheme)))
	}
	if a.SIPDigestAuthenticate != nil {
		item = append(item, a.SIPDigestAuthenticate.avp())
	}
	m.AddAVP(vendorAVP(avpSIPAuthDataItem, mv, &diam.GroupedAVP{AVP: item}))
	return m
}

func ParseMultimediaAuthAnswer(m *diam.Message) (MultimediaAuthAnswer, error) {
	var raw struct {
		answerResult
		UserName        string `avp:"User-Name,omitempty"`
		PublicIdentity  string `avp:"Public-Identity,omitempty"`
		SIPAuthDataItem *struct {
			Scheme             string `avp:"SIP-Authentication-Scheme"`
			Authenticate       []byte `avp:"SIP-Authenticate,omitempty"`
			Authorization      []byte `avp:"SIP-Authorization,omitempty"`
			ConfidentialityKey []byte `avp:"Confidentiality-Key,omitempty"`
			IntegrityKey       []byte `avp:"Integrity-Key,omitempty"`
			Digest             *struct {
				Realm     string `avp:"Digest-Realm"`
				Algorithm string `avp:"Digest-Algorithm,omitempty"`
				QoP       string `avp:"Digest-QoP"`
				HA1       string `avp:"Digest-HA1"`
			} `avp:"SIP-Digest-Authenticate,omitempty"`
		} `avp:"SIP-Auth-Data-Item,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return MultimediaAuthAnswer{}, fmt.Errorf("MAA: %w", err)
	}
	result, err := raw.result()
	if err != nil {
		return MultimediaAuthAnswer{}, fmt.Errorf("MAA: %w", err)
	}
	a := MultimediaAuthAnswer{Result: result, UserName: raw.UserName, PublicIdentity: raw.PublicIdentity}
	if item := raw.SIPAuthDataItem; item != nil {
		a.SIPAuthenticationScheme = item.Scheme
		if d := item.Digest; d != nil {
			a.SIPDigestAuthenticate = &SIPDigestAuthenticate{
				DigestRealm: d.Realm, DigestAlgorithm: d.Algorithm, DigestQoP: d.QoP, DigestHA1: d.HA1,
			}
		}
		if item.Authenticate != nil {
			av, err := parseAuthenticationVector(item.Authenticate, item.Authorization, item.ConfidentialityKey, item.IntegrityKey)
			if err != nil {
				return MultimediaAuthAnswer{}, fmt.Errorf("MAA: %w", err)
			}
			a.AuthenticationVector = av
		}
	}
	return a, nil
}

// parseAuthenticationVector checks the lengths that TS 29.229 6.3.10
// and TS 33.102 6.3.7 give the AVPs of an IMS-AKA item.
func parseAuthenticationVector(authenticate, xres, ck, ik []byte) (*aka.AuthenticationVector, error) {
	var av aka.AuthenticationVector
	switch {
	case len(authenticate) != len(av.RAND)+len(av.AUTN):
		return nil, fmt.Errorf("SIP-Authenticate has %d octets, want RAND and AUTN, %d", len(authenticate), len(av.RAND)+len(av.AUTN))
	case len(xres) < 4 || len(xres) > 16:
		return nil, fmt.Errorf("SIP-Authorization has %d octets, want XRES, 4 to 16", len(xres))
	case len(ck) != len(av.CK):
		return nil, fmt.Errorf("Confidentiality-Key has %d octets, want %d", len(ck), len(av.CK))
	case len(ik) != len(av.IK):
		return nil, fmt.Errorf("Integrity-Key has %d octets, want %d", len(ik), len(av.IK))
	}
	copy(av.RAND[:], authenticate)
	copy(av.AUTN[:], authenticate[len(av.RAND):])
	av.XRES = xres
	copy(av.CK[:], ck)
	copy(av.IK[:], ik)
	return &av, nil
}

// ServerAssignmentRequest is the SAR of TS 29.229 6.1.3. The S-CSCF
// tells the HSS that it now serves the user, or no longer does
// (TS 29.228 6.1.2).
type ServerAssignmentRequest struct {
	Header
	UserName             string
	PublicIdentities     []string
	ServerName           string
	ServerAssignmentType ServerAssignmentType
	// UserDataAlreadyAvailable tells the HSS that the S-CSCF already
	// holds the user profile and needs no User-Data.
	UserDataAlreadyAvailable bool
}

func (r ServerAssignmentRequest) Message() *diam.Message {
	m := newRequest(CommandServerAssignment)
	r.begin(m)
	r.destinationHost(m)
	r.destinationRealm(m)
	if r.UserName != "" {
		m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(r.UserName))
	}
	for _, id := range r.PublicIdentities {
		m.AddAVP(vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(id)))
	}
	m.AddAVP(vendorAVP(avpServerName, mv, datatype.UTF8String(r.ServerName)))
	m.AddAVP(vendorAVP(avpServerAssignmentType, mv, datatype.Enumerated(r.ServerAssignmentType)))
	available := userDataNotAvailable
	if r.UserDataAlreadyAvailable {
		available = userDataAlreadyAvailable
	}
	m.AddAVP(vendorAVP(avpUserDataAlreadyAvailable, mv, datatype.Enumerated(available)))
	return m
}

func ParseServerAssignmentRequest(m *diam.Message) (ServerAssignmentRequest, error) {
	var raw struct {
		requestHeader
		UserName                 string   `avp:"User-Name,omitempty"`
		PublicIdentities         []string `avp:"Public-Identity,omitempty"`
		ServerName               string   `avp:"Server-Name"`
		ServerAssignmentType     int32    `avp:"Server-Assignment-Type"`
		UserDataAlreadyAvailable int32    `avp:"User-Data-Already-Available"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ServerAssignmentRequest{}, fmt.Errorf("SAR: %w", err)
	}
	return ServerAssignmentRequest{
		Header:                   raw.header(),
		UserName:                 raw.UserName,
		PublicIdentities:         raw.PublicIdentities,
		ServerName:               raw.ServerName,
		ServerAssignmentType:     ServerAssignmentType(raw.ServerAssignmentType),
		UserDataAlreadyAvailable: raw.UserDataAlreadyAvailable == userDataAlreadyAvailable,
	}, nil
}

// ServerAssignmentAnswer is the SAA of TS 29.229 6.1.4. User-Data holds
// the user profile, the XML of TS 29.228 Annex E.
type ServerAssignmentAnswer struct {
	Result   Result
	UserName string
	UserData []byte
	// ServerName is the S-CSCF already assigned, returned with
	// DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED (TS 29.228 6.1.2.1).
	ServerName string
}

func (a ServerAssignmentAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	m := NewAnswer(request, local, a.Result)
	if a.UserName != "" {
		m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(a.UserName))
	}
	if len(a.UserData) > 0 {
		m.AddAVP(vendorAVP(avpUserData, mv, datatype.OctetString(a.UserData)))
	}
	if a.ServerName != "" {
		m.AddAVP(vendorAVP(avpServerName, mv, datatype.UTF8String(a.ServerName)))
	}
	return m
}

func ParseServerAssignmentAnswer(m *diam.Message) (ServerAssignmentAnswer, error) {
	var raw struct {
		answerResult
		UserName   string `avp:"User-Name,omitempty"`
		UserData   []byte `avp:"User-Data,omitempty"`
		ServerName string `avp:"Server-Name,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ServerAssignmentAnswer{}, fmt.Errorf("SAA: %w", err)
	}
	result, err := raw.result()
	if err != nil {
		return ServerAssignmentAnswer{}, fmt.Errorf("SAA: %w", err)
	}
	return ServerAssignmentAnswer{Result: result, UserName: raw.UserName, UserData: raw.UserData, ServerName: raw.ServerName}, nil
}

// requestHeader reads the routing AVPs every Cx request starts with.
type requestHeader struct {
	SessionID        string `avp:"Session-Id"`
	OriginHost       string `avp:"Origin-Host"`
	OriginRealm      string `avp:"Origin-Realm"`
	DestinationHost  string `avp:"Destination-Host,omitempty"`
	DestinationRealm string `avp:"Destination-Realm"`
}

func (h requestHeader) header() Header {
	return Header{
		SessionID:        h.SessionID,
		OriginHost:       h.OriginHost,
		OriginRealm:      h.OriginRealm,
		DestinationHost:  h.DestinationHost,
		DestinationRealm: h.DestinationRealm,
	}
}

// LocationInfoRequest is the LIR of TS 29.229 6.1.5. The I-CSCF asks
// the HSS which S-CSCF serves the callee of a terminating request
// (TS 29.228 6.1.4).
type LocationInfoRequest struct {
	Header
	PublicIdentity string
}

func (r LocationInfoRequest) Message() *diam.Message {
	m := newRequest(CommandLocationInfo)
	r.begin(m)
	r.destinationHost(m)
	r.destinationRealm(m)
	m.AddAVP(vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(r.PublicIdentity)))
	return m
}

func ParseLocationInfoRequest(m *diam.Message) (LocationInfoRequest, error) {
	var raw struct {
		requestHeader
		PublicIdentity string `avp:"Public-Identity"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return LocationInfoRequest{}, fmt.Errorf("LIR: %w", err)
	}
	return LocationInfoRequest{Header: raw.header(), PublicIdentity: raw.PublicIdentity}, nil
}

// LocationInfoAnswer is the LIA of TS 29.229 6.1.6: the S-CSCF of a
// registered callee, or capabilities to pick one for unregistered
// services, or an error.
type LocationInfoAnswer struct {
	Result             Result
	ServerName         string
	ServerCapabilities *ServerCapabilities
}

func (a LocationInfoAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	m := NewAnswer(request, local, a.Result)
	if a.ServerName != "" {
		m.AddAVP(vendorAVP(avpServerName, mv, datatype.UTF8String(a.ServerName)))
	}
	if a.ServerCapabilities != nil {
		m.AddAVP(a.ServerCapabilities.avp())
	}
	return m
}

func ParseLocationInfoAnswer(m *diam.Message) (LocationInfoAnswer, error) {
	uaa, err := ParseUserAuthorizationAnswer(m)
	if err != nil {
		return LocationInfoAnswer{}, fmt.Errorf("LIA: %w", err)
	}
	// The LIA carries the same result, Server-Name and
	// Server-Capabilities AVPs as the UAA.
	return LocationInfoAnswer(uaa), nil
}

// ReasonCode is the Reason-Code AVP inside Deregistration-Reason, TS
// 29.229 6.3.17. TS 29.228 6.1.3.1 says what each one asks of the
// S-CSCF.
type ReasonCode uint32

const (
	// PermanentTermination: the S-CSCF no longer serves the identities,
	// and no other S-CSCF can be assigned, for example because the
	// subscription was cancelled.
	PermanentTermination ReasonCode = 0
	NewServerAssigned    ReasonCode = 1
	// ServerChange: the user is to register again, so that the I-CSCF
	// picks a new S-CSCF.
	ServerChange ReasonCode = 2
	RemoveSCSCF  ReasonCode = 3
)

func (c ReasonCode) String() string {
	switch c {
	case PermanentTermination:
		return "PERMANENT_TERMINATION"
	case NewServerAssigned:
		return "NEW_SERVER_ASSIGNED"
	case ServerChange:
		return "SERVER_CHANGE"
	case RemoveSCSCF:
		return "REMOVE_S-CSCF"
	}
	return fmt.Sprintf("Reason-Code %d", uint32(c))
}

// DeregistrationReason is the Deregistration-Reason AVP, TS 29.229
// 6.3.16: the code that tells the S-CSCF what to do, and an optional
// text meant for the user (Reason-Info, 6.3.18).
type DeregistrationReason struct {
	ReasonCode ReasonCode
	ReasonInfo string
}

// RegistrationTerminationRequest is the RTR of TS 29.229 6.1.9. The HSS
// sends it to the S-CSCF to deregister the identities it names (TS
// 29.228 6.1.3). DestinationHost is required: it is the Origin-Host of
// the S-CSCF's earlier requests for the user (TS 29.229 5.5, TS 29.228
// Table 6.1.3.1, Routing Information).
type RegistrationTerminationRequest struct {
	Header
	UserName string // the private user identity
	// PublicIdentities are the identities to deregister. None means all
	// of those of UserName (TS 29.228 6.1.3.1).
	PublicIdentities     []string
	DeregistrationReason DeregistrationReason
}

func (r RegistrationTerminationRequest) Message() *diam.Message {
	m := newRequest(CommandRegistrationTermination)
	r.begin(m)
	r.destinationHost(m)
	r.destinationRealm(m)
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(r.UserName))
	for _, id := range r.PublicIdentities {
		m.AddAVP(vendorAVP(avpPublicIdentity, mv, datatype.UTF8String(id)))
	}
	reason := []*diam.AVP{vendorAVP(avpReasonCode, mv, datatype.Enumerated(r.DeregistrationReason.ReasonCode))}
	if r.DeregistrationReason.ReasonInfo != "" {
		reason = append(reason, vendorAVP(avpReasonInfo, mv, datatype.UTF8String(r.DeregistrationReason.ReasonInfo)))
	}
	m.AddAVP(vendorAVP(avpDeregistrationReason, mv, &diam.GroupedAVP{AVP: reason}))
	return m
}

func ParseRegistrationTerminationRequest(m *diam.Message) (RegistrationTerminationRequest, error) {
	var raw struct {
		requestHeader
		UserName             string   `avp:"User-Name"`
		PublicIdentities     []string `avp:"Public-Identity,omitempty"`
		DeregistrationReason *struct {
			ReasonCode int32  `avp:"Reason-Code"`
			ReasonInfo string `avp:"Reason-Info,omitempty"`
		} `avp:"Deregistration-Reason,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return RegistrationTerminationRequest{}, fmt.Errorf("RTR: %w", err)
	}
	if raw.DeregistrationReason == nil {
		return RegistrationTerminationRequest{}, fmt.Errorf("RTR: no Deregistration-Reason")
	}
	return RegistrationTerminationRequest{
		Header:           raw.header(),
		UserName:         raw.UserName,
		PublicIdentities: raw.PublicIdentities,
		DeregistrationReason: DeregistrationReason{
			ReasonCode: ReasonCode(raw.DeregistrationReason.ReasonCode),
			ReasonInfo: raw.DeregistrationReason.ReasonInfo,
		},
	}, nil
}

// RegistrationTerminationAnswer is the RTA of TS 29.229 6.1.10. It has
// no Associated-Identities and no Identity-with-Emergency-Registration:
// every subscription of this project has one private identity, and none
// registers for emergency.
type RegistrationTerminationAnswer struct {
	Result Result
}

func (a RegistrationTerminationAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return NewAnswer(request, local, a.Result)
}

func ParseRegistrationTerminationAnswer(m *diam.Message) (RegistrationTerminationAnswer, error) {
	var raw struct{ answerResult }
	if err := m.Unmarshal(&raw); err != nil {
		return RegistrationTerminationAnswer{}, fmt.Errorf("RTA: %w", err)
	}
	result, err := raw.result()
	if err != nil {
		return RegistrationTerminationAnswer{}, fmt.Errorf("RTA: %w", err)
	}
	return RegistrationTerminationAnswer{Result: result}, nil
}
