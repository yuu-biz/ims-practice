package fivegc

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// This file is the API between a UE and the 5GC stub: JSON messages
// named after the 5GS NAS messages of TS 24.501 8.2 (5GMM) and 8.3
// (5GSM), with their information elements as fields. The UE posts its
// messages to /ues/{imsi}/{message}, and receives the messages of the
// network on the event stream /ues/{imsi}/events, as it does with the
// EPC stub.
//
// A 5GSM message travels inside UL NAS TRANSPORT or DL NAS TRANSPORT
// (TS 24.501 8.2.10, 8.2.11), whose payload container holds it. The
// path and the event name are those of the 5GSM message; the body is
// the NAS transport message around it, so that the DNN and the request
// type sit where TS 24.501 puts them.
//
// SIMPLIFIED: TS 24.501 and TS 38.413. There is no NAS encoding, no
// NGAP and no radio: the messages travel as HTTP and JSON over the one
// Docker network, and the IMSI in the URL stands for the N1 connection
// that tells the AMF which UE a message is from, and for the SUCI the
// UE would send in REGISTRATION REQUEST. Security (5G-AKA, security
// mode control), the 5G-GUTI, network slicing (NSSAI and S-NSSAI),
// mobility and periodic registration updating, and the timers of the
// network side (T3522, T3591, T3592) are left out; a learner reads the
// procedures, not the encoding. The UE reaches the stub through its
// access network interface, so an implementation that speaks real NAS
// can stand in its place.

// MessageType is the name of a message as the "Message type" line of
// its clause in TS 24.501 8.2 and 8.3 writes it. On the event stream it
// is the event name.
type MessageType string

const (
	TypeRegistrationRequest   MessageType = "REGISTRATION REQUEST"   // 8.2.6
	TypeRegistrationAccept    MessageType = "REGISTRATION ACCEPT"    // 8.2.7
	TypeDeregistrationRequest MessageType = "DEREGISTRATION REQUEST" // 8.2.12, 8.2.14
	TypeDeregistrationAccept  MessageType = "DEREGISTRATION ACCEPT"  // 8.2.13, 8.2.15

	TypePDUSessionEstablishmentRequest      MessageType = "PDU SESSION ESTABLISHMENT REQUEST"       // 8.3.1
	TypePDUSessionEstablishmentAccept       MessageType = "PDU SESSION ESTABLISHMENT ACCEPT"        // 8.3.2
	TypePDUSessionModificationCommand       MessageType = "PDU SESSION MODIFICATION COMMAND"        // 8.3.9
	TypePDUSessionModificationComplete      MessageType = "PDU SESSION MODIFICATION COMPLETE"       // 8.3.10
	TypePDUSessionModificationCommandReject MessageType = "PDU SESSION MODIFICATION COMMAND REJECT" // 8.3.11
	TypePDUSessionReleaseRequest            MessageType = "PDU SESSION RELEASE REQUEST"             // 8.3.12
	TypePDUSessionReleaseCommand            MessageType = "PDU SESSION RELEASE COMMAND"             // 8.3.14
	TypePDUSessionReleaseComplete           MessageType = "PDU SESSION RELEASE COMPLETE"            // 8.3.15
)

// Path is where a UE posts a message of type t, such as
// /ues/001010000000001/pdu-session-establishment-request.
func Path(imsi string, t MessageType) string {
	return "/ues/" + imsi + "/" + strings.ReplaceAll(strings.ToLower(string(t)), " ", "-")
}

// EventsPath is the event stream of a UE: Server-Sent Events whose name
// is a MessageType and whose data is the message as JSON.
func EventsPath(imsi string) string { return "/ues/" + imsi + "/events" }

// SystemInformationPath is where a UE reads the identity of the NR cell
// it camps on.
const SystemInformationPath = "/system-information"

// RegistrationRequest is REGISTRATION REQUEST (TS 24.501 8.2.6).
type RegistrationRequest struct {
	FiveGSRegistrationType RegistrationType `json:"5gs_registration_type"`
}

// RegistrationAccept is REGISTRATION ACCEPT (TS 24.501 8.2.7). The
// network feature support tells the UE whether it may use IMS voice
// over PS sessions (TS 23.501 5.16.3.2).
type RegistrationAccept struct {
	FiveGSRegistrationResult    RegistrationResult     `json:"5gs_registration_result"`
	TAIList                     []TrackingAreaIdentity `json:"tai_list"`
	FiveGSNetworkFeatureSupport NetworkFeatureSupport  `json:"5gs_network_feature_support"`
}

// DeregistrationRequest is DEREGISTRATION REQUEST: from the UE (TS
// 24.501 8.2.12), which the network answers with DEREGISTRATION ACCEPT
// unless the UE is switching off, or from the network (8.2.14), which
// the UE answers.
type DeregistrationRequest struct {
	DeregistrationType DeregistrationType `json:"deregistration_type"`
}

// DeregistrationAccept is DEREGISTRATION ACCEPT (TS 24.501 8.2.13,
// 8.2.15). It has no information elements beyond its header.
type DeregistrationAccept struct{}

// RegistrationType is the value of the 5GS registration type IE (TS
// 24.501 9.11.3.7).
type RegistrationType string

const InitialRegistration RegistrationType = "initial registration"

// RegistrationResult is the 5GS registration result IE (TS 24.501
// 9.11.3.6).
type RegistrationResult struct {
	Value AccessType `json:"value"`
	// SMSAllowed is the "SMS over NAS transport allowed" bit.
	//
	// SIMPLIFIED: TS 24.501 9.11.3.6. The stub carries no SMS over NAS
	// and never sets the bit, so that a UE of this stub sends its short
	// messages over IP through the IP-SM-GW.
	SMSAllowed bool `json:"sms_over_nas_allowed"`
}

// NetworkFeatureSupport is the 5GS network feature support IE (TS
// 24.501 9.11.3.5). Only the bit the IMS needs is named.
type NetworkFeatureSupport struct {
	// IMSVoPS3GPP is "IMS voice over PS session supported over 3GPP
	// access" (octet 3, bit 1).
	IMSVoPS3GPP bool `json:"ims_vops_3gpp"`
}

// DeregistrationType is the De-registration type IE (TS 24.501
// 9.11.3.20).
type DeregistrationType struct {
	// SwitchOff is set by a UE that is powering off; the network then
	// sends no DEREGISTRATION ACCEPT (TS 24.501 5.5.2.2.2).
	SwitchOff bool `json:"switch_off,omitempty"`
	// ReRegistrationRequired is set by the network when it wants the UE
	// to register again.
	ReRegistrationRequired bool       `json:"re_registration_required,omitempty"`
	AccessType             AccessType `json:"access_type"`
}

// AccessType is an access type value of TS 24.501 9.11.3.6 and
// 9.11.3.20. The stub serves 3GPP access only.
type AccessType string

const Access3GPP AccessType = "3GPP access"

// ULNASTransport is UL NAS TRANSPORT (TS 24.501 8.2.10) carrying the
// 5GSM message M in its payload container. The DNN and the request
// type are here, not in the 5GSM message.
type ULNASTransport[M any] struct {
	PayloadContainerType PayloadContainerType `json:"payload_container_type"`
	PayloadContainer     M                    `json:"payload_container"`
	PDUSessionID         uint8                `json:"pdu_session_id"`
	RequestType          RequestType          `json:"request_type,omitempty"`
	DNN                  string               `json:"dnn,omitempty"`
}

// DLNASTransport is DL NAS TRANSPORT (TS 24.501 8.2.11) carrying the
// 5GSM message M in its payload container.
type DLNASTransport[M any] struct {
	PayloadContainerType PayloadContainerType `json:"payload_container_type"`
	PayloadContainer     M                    `json:"payload_container"`
	PDUSessionID         uint8                `json:"pdu_session_id"`
}

// NewULNASTransport wraps the 5GSM message m of the PDU session
// pduSessionID. Set RequestType and DNN for an establishment.
func NewULNASTransport[M any](pduSessionID uint8, m M) ULNASTransport[M] {
	return ULNASTransport[M]{PayloadContainerType: N1SMInformation, PayloadContainer: m, PDUSessionID: pduSessionID}
}

func newDLNASTransport[M any](pduSessionID uint8, m M) DLNASTransport[M] {
	return DLNASTransport[M]{PayloadContainerType: N1SMInformation, PayloadContainer: m, PDUSessionID: pduSessionID}
}

// PayloadContainerType is the Payload container type IE (TS 24.501
// 9.11.3.40). A 5GSM message is "N1 SM information".
type PayloadContainerType string

const N1SMInformation PayloadContainerType = "N1 SM information"

// RequestType is the Request type IE (TS 24.501 9.11.3.47).
type RequestType string

const InitialRequest RequestType = "initial request"

// PDUSessionEstablishmentRequest is PDU SESSION ESTABLISHMENT REQUEST
// (TS 24.501 8.3.1). The UE asks for the P-CSCF addresses in the
// extended PCO (TS 24.501 6.4.1.3).
//
// SIMPLIFIED: TS 24.501 8.3.1. The Integrity protection maximum data
// rate IE is left out: there is no user plane to protect.
type PDUSessionEstablishmentRequest struct {
	PDUSessionID                         uint8                                `json:"pdu_session_id"`
	PTI                                  uint8                                `json:"pti"`
	PDUSessionType                       PDUSessionType                       `json:"pdu_session_type,omitempty"`
	SSCMode                              SSCMode                              `json:"ssc_mode,omitempty"`
	ExtendedProtocolConfigurationOptions ExtendedProtocolConfigurationOptions `json:"extended_protocol_configuration_options"`
}

// PDUSessionEstablishmentAccept is PDU SESSION ESTABLISHMENT ACCEPT
// (TS 24.501 8.3.2). The PDU address is the UE's IP address, the QoS
// rules and QoS flow descriptions describe the QoS flow of the default
// QoS rule, and the extended PCO lists the P-CSCFs, the first one
// preferred.
type PDUSessionEstablishmentAccept struct {
	PDUSessionID                  uint8                `json:"pdu_session_id"`
	PTI                           uint8                `json:"pti"`
	SelectedPDUSessionType        PDUSessionType       `json:"selected_pdu_session_type"`
	SelectedSSCMode               SSCMode              `json:"selected_ssc_mode"`
	AuthorizedQoSRules            []QoSRule            `json:"authorized_qos_rules"`
	SessionAMBR                   SessionAMBR          `json:"session_ambr"`
	FiveGSMCause                  Cause                `json:"5gsm_cause,omitempty"`
	PDUAddress                    PDUAddress           `json:"pdu_address"`
	AuthorizedQoSFlowDescriptions []QoSFlowDescription `json:"authorized_qos_flow_descriptions"`
	// The network echoes the IM CN Subsystem Signaling Flag and lists
	// the P-CSCF addresses here (TS 24.501 6.4.1.3).
	ExtendedProtocolConfigurationOptions ExtendedProtocolConfigurationOptions `json:"extended_protocol_configuration_options"`
	DNN                                  string                               `json:"dnn"`
}

// PDUSessionModificationCommand is PDU SESSION MODIFICATION COMMAND (TS
// 24.501 8.3.9). The network sends it when the PCF has authorised the
// media of a call, to add the voice QoS flow, and when the flow goes
// (TS 24.501 6.3.2.2).
type PDUSessionModificationCommand struct {
	PDUSessionID                  uint8                `json:"pdu_session_id"`
	PTI                           uint8                `json:"pti"`
	AuthorizedQoSRules            []QoSRule            `json:"authorized_qos_rules,omitempty"`
	AuthorizedQoSFlowDescriptions []QoSFlowDescription `json:"authorized_qos_flow_descriptions,omitempty"`
}

// PDUSessionModificationComplete is PDU SESSION MODIFICATION COMPLETE
// (TS 24.501 8.3.10, 6.3.2.3).
type PDUSessionModificationComplete struct {
	PDUSessionID uint8 `json:"pdu_session_id"`
	PTI          uint8 `json:"pti"`
}

// PDUSessionModificationCommandReject is PDU SESSION MODIFICATION
// COMMAND REJECT (TS 24.501 8.3.11, 6.3.2.4).
type PDUSessionModificationCommandReject struct {
	PDUSessionID uint8 `json:"pdu_session_id"`
	PTI          uint8 `json:"pti"`
	FiveGSMCause Cause `json:"5gsm_cause"`
}

// PDUSessionReleaseRequest is PDU SESSION RELEASE REQUEST (TS 24.501
// 8.3.12, 6.4.3.2).
type PDUSessionReleaseRequest struct {
	PDUSessionID uint8 `json:"pdu_session_id"`
	PTI          uint8 `json:"pti"`
	FiveGSMCause Cause `json:"5gsm_cause,omitempty"`
}

// PDUSessionReleaseCommand is PDU SESSION RELEASE COMMAND (TS 24.501
// 8.3.14, 6.3.3.2): the answer to the UE's request, with its PTI, or
// the network releasing the session on its own, with no PTI.
type PDUSessionReleaseCommand struct {
	PDUSessionID uint8 `json:"pdu_session_id"`
	PTI          uint8 `json:"pti"`
	FiveGSMCause Cause `json:"5gsm_cause"`
}

// PDUSessionReleaseComplete is PDU SESSION RELEASE COMPLETE (TS 24.501
// 8.3.15, 6.3.3.3).
type PDUSessionReleaseComplete struct {
	PDUSessionID uint8 `json:"pdu_session_id"`
	PTI          uint8 `json:"pti"`
	FiveGSMCause Cause `json:"5gsm_cause,omitempty"`
}

// PDUSessionType is a PDU session type value (TS 24.501 9.11.4.11,
// 9.11.4.10). The stub gives IPv4 addresses only, as every node of this
// project uses IPv4 on the one Docker network.
type PDUSessionType string

const (
	IPv4   PDUSessionType = "IPv4"
	IPv6   PDUSessionType = "IPv6"
	IPv4v6 PDUSessionType = "IPv4v6"
)

// SSCMode is the SSC mode IE (TS 24.501 9.11.4.16). The stub serves SSC
// mode 1, whose PDU session anchor stays as long as the session.
type SSCMode string

const SSCMode1 SSCMode = "SSC mode 1"

// Cause is the 5GSM cause IE (TS 24.501 9.11.4.2, Table 9.11.4.2.1).
// Only the causes this project sends or expects are named.
type Cause uint8

const (
	InsufficientResources          Cause = 26
	RequestRejectedUnspecified     Cause = 31
	RegularDeactivation            Cause = 36
	SemanticErrorsInPacketFilters  Cause = 44
	PDUSessionTypeIPv4OnlyAllowed  Cause = 50
	SemanticErrorInTheQoSOperation Cause = 83
)

func (c Cause) String() string {
	switch c {
	case InsufficientResources:
		return "#26 insufficient resources"
	case RequestRejectedUnspecified:
		return "#31 request rejected, unspecified"
	case RegularDeactivation:
		return "#36 regular deactivation"
	case SemanticErrorsInPacketFilters:
		return "#44 semantic errors in packet filter(s)"
	case PDUSessionTypeIPv4OnlyAllowed:
		return "#50 PDU session type IPv4 only allowed"
	case SemanticErrorInTheQoSOperation:
		return "#83 semantic error in the QoS operation"
	}
	return "#" + strconv.Itoa(int(c))
}

// FiveQI is the 5G QoS identifier of a QoS flow (TS 23.501 5.7.4).
type FiveQI uint8

const (
	// FiveQIConversationalVoice is the GBR class of voice (TS 23.501
	// Table 5.7.4-1): priority 20, 100 ms, a packet error rate of 10^-2.
	FiveQIConversationalVoice FiveQI = 1
	// FiveQIIMSSignalling is the non-GBR class of IMS signalling (TS
	// 23.501 Table 5.7.4-1): priority 10, 100 ms, 10^-6.
	FiveQIIMSSignalling FiveQI = 5
)

// SessionAMBR is the Session-AMBR IE (TS 24.501 9.11.4.14), in kbit/s:
// the most that all the non-GBR QoS flows of the session carry together
// (TS 23.501 5.7.2.6).
type SessionAMBR struct {
	SessionAMBRForDownlink uint32 `json:"session_ambr_for_downlink"`
	SessionAMBRForUplink   uint32 `json:"session_ambr_for_uplink"`
}

// PDUAddress is the PDU address IE (TS 24.501 9.11.4.10): for an IPv4
// PDU session, the IPv4 address of the UE.
type PDUAddress struct {
	PDUSessionTypeValue PDUSessionType `json:"pdu_session_type_value"`
	IPv4Address         netip.Addr     `json:"ipv4_address"`
}

// QoSRule is one QoS rule of the QoS rules IE (TS 24.501 9.11.4.13):
// which uplink packets the UE sends on which QoS flow. A rule to delete
// carries only its identifier and the operation code.
type QoSRule struct {
	QoSRuleIdentifier uint8                `json:"qos_rule_identifier"`
	RuleOperationCode QoSRuleOperationCode `json:"rule_operation_code"`
	// DQR is true for the default QoS rule of the PDU session.
	DQR           bool           `json:"dqr"`
	PacketFilters []PacketFilter `json:"packet_filters,omitempty"`
	// QoSRulePrecedence orders the rules: the UE evaluates them from the
	// lowest value up (TS 23.501 5.7.1.9).
	QoSRulePrecedence uint8 `json:"qos_rule_precedence,omitempty"`
	QFI               uint8 `json:"qfi,omitempty"`
}

// QoSRuleOperationCode is a rule operation code of TS 24.501 Table
// 9.11.4.13.1.
type QoSRuleOperationCode string

const (
	CreateNewQoSRule      QoSRuleOperationCode = "create new QoS rule"
	DeleteExistingQoSRule QoSRuleOperationCode = "delete existing QoS rule"
)

// PacketFilter is one packet filter of a QoS rule (TS 24.501
// 9.11.4.13). Local is the UE and remote the far end. The components
// that are zero are left out.
type PacketFilter struct {
	Direction  PacketFilterDirection `json:"packet_filter_direction"`
	Identifier uint8                 `json:"packet_filter_identifier"`
	// MatchAll is the match-all component, which only the default QoS
	// rule may carry (TS 23.501 5.7.1.4).
	MatchAll bool `json:"match_all,omitempty"`
	// IPv4RemoteAddress is the address and the mask of the far end.
	IPv4RemoteAddress            netip.Prefix `json:"ipv4_remote_address,omitzero"`
	ProtocolIdentifierNextHeader uint8        `json:"protocol_identifier_next_header,omitempty"`
	SingleLocalPort              uint16       `json:"single_local_port,omitempty"`
	SingleRemotePort             uint16       `json:"single_remote_port,omitempty"`
}

// PacketFilterDirection is the packet filter direction of TS 24.501
// Table 9.11.4.13.1.
type PacketFilterDirection string

const (
	DownlinkOnly  PacketFilterDirection = "downlink only"
	UplinkOnly    PacketFilterDirection = "uplink only"
	Bidirectional PacketFilterDirection = "bidirectional"
)

// QoSFlowDescription is one QoS flow description of the QoS flow
// descriptions IE (TS 24.501 9.11.4.12): the QoS of the flow QFI. The
// bit rates are in kbit/s, and only a GBR flow has them. A description
// to delete carries only the QFI and the operation code.
type QoSFlowDescription struct {
	QFI           uint8                           `json:"qfi"`
	OperationCode QoSFlowDescriptionOperationCode `json:"operation_code"`
	FiveQI        FiveQI                          `json:"5qi,omitempty"`
	GFBRUplink    uint32                          `json:"gfbr_uplink,omitempty"`
	GFBRDownlink  uint32                          `json:"gfbr_downlink,omitempty"`
	MFBRUplink    uint32                          `json:"mfbr_uplink,omitempty"`
	MFBRDownlink  uint32                          `json:"mfbr_downlink,omitempty"`
}

// QoSFlowDescriptionOperationCode is an operation code of TS 24.501
// Table 9.11.4.12.1.
type QoSFlowDescriptionOperationCode string

const (
	CreateNewQoSFlowDescription      QoSFlowDescriptionOperationCode = "create new QoS flow description"
	DeleteExistingQoSFlowDescription QoSFlowDescriptionOperationCode = "delete existing QoS flow description"
)

// TrackingAreaIdentity is the 5GS tracking area identity IE (TS 24.501
// 9.11.3.8).
type TrackingAreaIdentity struct {
	PLMN PLMNIdentity `json:"plmn"`
	// TAC is the tracking area code, three octets.
	TAC uint32 `json:"tac"`
}

// PLMNIdentity is the mobile country code and the mobile network code
// of a network, as decimal digits (TS 23.003 2.2).
type PLMNIdentity struct {
	MCC string `json:"mcc"`
	MNC string `json:"mnc"`
}

// SystemInformation is what a UE learns from the NR cell it camps on:
// the network, the tracking area and the cell identity (TS 38.331
// SIB1). A UE needs them for
// P-Access-Network-Info.
//
// SIMPLIFIED: TS 38.331. There is no radio to broadcast system
// information, so the stub serves it at SystemInformationPath.
type SystemInformation struct {
	TrackingAreaIdentity TrackingAreaIdentity `json:"tracking_area_identity"`
	// NRCellIdentity is the NR cell identity (NCI), 36 bits, as TS 24.501
	// 9.11.3.100 describes it after TS 38.413.
	NRCellIdentity uint64 `json:"nr_cell_identity"`
}

// ExtendedProtocolConfigurationOptions is the extended PCO IE (TS
// 24.501 9.11.4.6). Its contents are coded as those of the PCO (TS
// 24.008 10.5.6.3A): a list of containers, each with an identifier and
// contents.
type ExtendedProtocolConfigurationOptions struct {
	Containers []Container `json:"containers,omitempty"`
}

// Container is one container of the extended PCO. Contents is empty
// for a request and for the IM CN Subsystem Signaling Flag.
type Container struct {
	ID       ContainerID `json:"id"`
	Contents string      `json:"contents,omitempty"`
}

// ContainerID is a container identifier of TS 24.008 10.5.6.3, written
// in JSON as the spec writes it, such as "000CH".
type ContainerID uint16

// The containers of TS 24.008 10.5.6.3 that the IMS uses. From the UE
// a P-CSCF container is an empty request; from the network it holds
// one P-CSCF address.
const (
	PCSCFIPv6Address ContainerID = 0x0001
	// IMCNSubsystemSignalingFlag: from the UE, the session is for IMS
	// signalling; from the network, it was set up as such.
	IMCNSubsystemSignalingFlag ContainerID = 0x0002
	PCSCFIPv4Address           ContainerID = 0x000c
)

func (id ContainerID) MarshalText() ([]byte, error) {
	return fmt.Appendf(nil, "%04XH", uint16(id)), nil
}

func (id *ContainerID) UnmarshalText(b []byte) error {
	s, ok := strings.CutSuffix(string(b), "H")
	n, err := strconv.ParseUint(s, 16, 16)
	if !ok || len(s) != 4 || err != nil {
		return fmt.Errorf("container identifier %q is not four hexadecimal digits and H, such as 000CH", b)
	}
	*id = ContainerID(n)
	return nil
}

// PCSCFAddressRequest is the extended PCO of a UE that sets up its PDU
// session for IMS signalling and asks for the P-CSCF addresses of both
// IP versions.
func PCSCFAddressRequest() ExtendedProtocolConfigurationOptions {
	return ExtendedProtocolConfigurationOptions{Containers: []Container{
		{ID: IMCNSubsystemSignalingFlag}, {ID: PCSCFIPv6Address}, {ID: PCSCFIPv4Address},
	}}
}

// PCSCFAddresses lists the P-CSCF addresses of the extended PCO in its
// order, the most preferred first. Contents that are not an address of
// the container's IP version are skipped.
func (p ExtendedProtocolConfigurationOptions) PCSCFAddresses() []netip.Addr {
	var addrs []netip.Addr
	for _, c := range p.Containers {
		a, err := netip.ParseAddr(c.Contents)
		if err != nil {
			continue
		}
		if (c.ID == PCSCFIPv4Address && a.Is4()) || (c.ID == PCSCFIPv6Address && a.Is6()) {
			addrs = append(addrs, a)
		}
	}
	return addrs
}

// has reports whether the extended PCO holds a container id.
func (p ExtendedProtocolConfigurationOptions) has(id ContainerID) bool {
	for _, c := range p.Containers {
		if c.ID == id {
			return true
		}
	}
	return false
}

// RegistrationState is the 5GMM state of a UE on the network side.
type RegistrationState string

// Registered is 5GMM-REGISTERED, which the AMF enters when it accepts
// the registration (TS 24.501 5.5.1.2.4).
const Registered RegistrationState = "5GMM-REGISTERED"

// PDUSessionState is the 5GSM state of a PDU session on the network side
// (TS 24.501 6.1.3.3).
type PDUSessionState string

const (
	PDUSessionActive PDUSessionState = "PDU SESSION ACTIVE"
	// PDUSessionModificationPending: a PDU SESSION MODIFICATION COMMAND
	// has gone to the UE, which has not answered yet.
	PDUSessionModificationPending PDUSessionState = "PDU SESSION MODIFICATION PENDING"
)

// Status is what the API shows of a registered UE at GET /ues/{imsi}:
// its 5GMM state and, when it has one, its PDU session.
type Status struct {
	IMSI              string            `json:"imsi"`
	RegistrationState RegistrationState `json:"registration_state"`
	PDUSession        *PDUSessionStatus `json:"pdu_session,omitempty"`
}

// PDUSessionStatus is the PDU session of a Status: its QoS flows and
// the application session contexts of N5 bound to it (TS 29.513 6.2).
type PDUSessionStatus struct {
	PDUSessionID uint8           `json:"pdu_session_id"`
	DNN          string          `json:"dnn"`
	PDUAddress   netip.Addr      `json:"pdu_address"`
	State        PDUSessionState `json:"state"`
	QoSFlows     []QoSFlow       `json:"qos_flows"`
	AppSessions  []AppSession    `json:"app_sessions"`
}
