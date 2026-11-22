// Package nhss is the IMS part of the service based interface of the
// HSS, TS 29.562 (Release 20, V20.1.0): Nhss_imsUEContextManagement
// (UECM), Nhss_imsUEAuthentication (UEAU) and
// Nhss_imsSubscriberDataManagement (SDM). The I-CSCF and the S-CSCF use
// them over N70 instead of Cx, and an AS such as the IP-SM-GW over N71
// instead of Sh (TS 23.228 AA.1.2).
// No node of this project runs it: no binary in cmd builds it in.
//
// TS 23.228 Table AA.2.2.2-1 maps the Cx procedures this project uses
// onto the operations here:
//
//	UAR/UAA  Cx-Query           UECM Authorize, and SDM GetScscfCapabilities
//	MAR/MAA  Cx-AuthDataReq     UEAU GenerateSipAuthData
//	SAR/SAA  Cx-Put, Cx-Pull    UECM Registration and Deregistration, SDM GetProfileData
//	LIR/LIA  Cx-Location-Query  UECM Authorize, and SDM GetScscfCapabilities
//	RTR/RTA  Cx-Deregister      UECM DeregistrationNotification
//
// and Table AA.2.2.3-1 maps Sh-Pull onto SDM Get and Sh-Update onto SDM
// Update: here GetImsAssocIds, GetServerName and GetMsisdns for the
// Sh data references IMSPublicIdentity, S-CSCFName and MSISDN, and
// GetSmsRegistrationInfo, UpdateSmsRegistrationInfo and
// DeleteSmsRegistrationInfo for SMSRegistrationInfo.
//
// The JSON types carry the attribute names of the OpenAPI files of TS
// 29.562 Annex A (TS29562_Nhss_imsUECM.yaml, TS29562_Nhss_imsUEAU.yaml,
// TS29562_Nhss_imsSDM.yaml). A Go field is the attribute name with its
// first letter in upper case.
//
// For each service the package has the client side (methods of Client)
// and the skeleton of the HSS side (NewImsUECMHandler, NewImsUEAUHandler,
// NewImsSDMHandler), which decodes a request, calls an implementation
// and encodes the answer. The HSS's DeregistrationNotification goes out
// through Notifier and arrives at NewDeregistrationNotificationHandler
// on the S-CSCF. None of them keeps state between requests.
//
// SIMPLIFIED: TS 29.562 Annex A. Only the operations above, and of
// their types only the attributes these procedures need, are declared.
// Restoration, IMS AS registration, the subscriptions to data changes
// (the counterpart of PPR/PPA) and the other SDM resources are left
// out. The Cx and Sh procedures that this project uses map onto the
// operations above alone, and decoding ignores attributes it does not
// know, so a fuller peer can still talk to this one.
package nhss

import (
	"net/url"
	"strings"
)

// The API roots of the three services (TS 29.562 6.1.1, 6.2.1,
// 6.3.1): the path after the apiRoot of the HSS.
const (
	UECMRoot = "/nhss-ims-uecm/v1"
	SDMRoot  = "/nhss-ims-sdm/v1"
	UEAURoot = "/nhss-ims-ueau/v1"
)

// NFType is the NF type a client names in User-Agent (TS 29.500 Table
// 5.2.2.2-1). The values are from the NFType enumeration of TS 29.510.
type NFType string

const (
	NFTypeICSCF NFType = "ICSCF"
	NFTypeSCSCF NFType = "SCSCF"
	NFTypeIMSAS NFType = "IMS_AS"
	NFTypeHSS   NFType = "HSS"
)

// ImsUeId is the {imsUeId} of a resource URI: an IMS public identity
// with the prefix "impu-", such as "impu-sip:alice@ims.example.org" or
// "impu-tel:+12025550123", or an IMS private identity with the prefix
// "impi-" (TS 29.562 6.1.3.2.2, 6.2.3.2.2). The pattern of the OpenAPI
// also lets any other text through; such an ImsUeId has neither
// prefix, and the HSS decides what it names.
type ImsUeId string

const (
	impuPrefix = "impu-"
	impiPrefix = "impi-"
)

// ImsUeIdFromImpu names a public user identity or PSI, a SIP or tel URI.
func ImsUeIdFromImpu(impu string) ImsUeId { return ImsUeId(impuPrefix + impu) }

// ImsUeIdFromImpi names a private user identity.
func ImsUeIdFromImpi(impi string) ImsUeId { return ImsUeId(impiPrefix + impi) }

// Impu returns the public identity the ImsUeId names, if it names one.
func (id ImsUeId) Impu() (string, bool) { return strings.CutPrefix(string(id), impuPrefix) }

// Impi returns the private identity the ImsUeId names, if it names one.
func (id ImsUeId) Impi() (string, bool) { return strings.CutPrefix(string(id), impiPrefix) }

// segment writes a path variable. A SIP URI keeps its ":" and "@",
// which a path segment allows (RFC 3986 3.3); "/", "?" and the like are
// percent-encoded, as TS 29.571 asks of reserved characters in a path
// segment.
func segment(s string) string { return url.PathEscape(s) }
