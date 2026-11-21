package epc

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// This file is the API between a UE and the EPC stub: JSON messages
// named after the EPS NAS messages of TS 24.301 8.2 (EMM) and 8.3 (ESM),
// with their information elements as fields. The UE posts its messages
// to /ues/{imsi}/{message}, and receives the messages of the network on
// the event stream /ues/{imsi}/events.
//
// SIMPLIFIED: TS 24.301 and TS 36.413. There is no NAS encoding, no
// S1-AP and no radio: the messages travel as HTTP and JSON over the one
// Docker network, and the IMSI in the URL stands for the S1 connection
// that tells the MME which UE a message is from. Security, GUTI,
// tracking area updates and the timers of the network side (T3450,
// T3485, T3495) are left out; a learner reads the procedures, not the
// encoding. Real NAS can stand behind the same UE interface.

// MessageType is the name of a message as TS 24.301 8.2 and 8.3 head
// it. On the event stream it is the event name.
type MessageType string

const (
	TypeAttachRequest                            MessageType = "ATTACH REQUEST"                                // 8.2.4
	TypeAttachAccept                             MessageType = "ATTACH ACCEPT"                                 // 8.2.1
	TypeAttachComplete                           MessageType = "ATTACH COMPLETE"                               // 8.2.2
	TypeDetachRequest                            MessageType = "DETACH REQUEST"                                // 8.2.11
	TypeDetachAccept                             MessageType = "DETACH ACCEPT"                                 // 8.2.10
	TypeActivateDedicatedEPSBearerContextRequest MessageType = "ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST" // 8.3.3
	TypeActivateDedicatedEPSBearerContextAccept  MessageType = "ACTIVATE DEDICATED EPS BEARER CONTEXT ACCEPT"  // 8.3.1
	TypeActivateDedicatedEPSBearerContextReject  MessageType = "ACTIVATE DEDICATED EPS BEARER CONTEXT REJECT"  // 8.3.2
	TypeDeactivateEPSBearerContextRequest        MessageType = "DEACTIVATE EPS BEARER CONTEXT REQUEST"         // 8.3.12
	TypeDeactivateEPSBearerContextAccept         MessageType = "DEACTIVATE EPS BEARER CONTEXT ACCEPT"          // 8.3.11
)

// Path is where a UE posts a message of type t, such as
// /ues/001010000000001/attach-request.
func Path(imsi string, t MessageType) string {
	return "/ues/" + imsi + "/" + strings.ReplaceAll(strings.ToLower(string(t)), " ", "-")
}

// EventsPath is the event stream of a UE: Server-Sent Events whose name
// is a MessageType and whose data is the message as JSON.
func EventsPath(imsi string) string { return "/ues/" + imsi + "/events" }

// SystemInformationPath is where a UE reads the identity of the cell it
// camps on.
const SystemInformationPath = "/system-information"

// AttachRequest is ATTACH REQUEST (TS 24.301 8.2.4), which carries the
// UE's PDN CONNECTIVITY REQUEST for the IMS APN in its ESM message
// container (TS 23.401 5.3.2.1).
type AttachRequest struct {
	ESMMessageContainer PDNConnectivityRequest `json:"esm_message_container"`
}

// PDNConnectivityRequest is PDN CONNECTIVITY REQUEST (TS 24.301 8.3.20).
type PDNConnectivityRequest struct {
	ProcedureTransactionIdentity uint8       `json:"procedure_transaction_identity"`
	RequestType                  RequestType `json:"request_type"`
	PDNType                      PDNType     `json:"pdn_type"`
	AccessPointName              string      `json:"access_point_name"`
	// The UE asks for the P-CSCF addresses here (TS 24.229 L.2.2.1).
	ProtocolConfigurationOptions ProtocolConfigurationOptions `json:"protocol_configuration_options"`
}

// AttachAccept is ATTACH ACCEPT (TS 24.301 8.2.1), which carries the
// ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST of the PDN connection.
type AttachAccept struct {
	TAIList             []TrackingAreaIdentity                 `json:"tai_list"`
	ESMMessageContainer ActivateDefaultEPSBearerContextRequest `json:"esm_message_container"`
}

// ActivateDefaultEPSBearerContextRequest is ACTIVATE DEFAULT EPS BEARER
// CONTEXT REQUEST (TS 24.301 8.3.6). The PDN address is the UE's IP
// address, and the PCO lists the P-CSCFs, the first one preferred (TS
// 24.229 L.2.2.1).
type ActivateDefaultEPSBearerContextRequest struct {
	EPSBearerIdentity            uint8                        `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8                        `json:"procedure_transaction_identity"`
	EPSQoS                       EPSQoS                       `json:"eps_qos"`
	AccessPointName              string                       `json:"access_point_name"`
	PDNAddress                   PDNAddress                   `json:"pdn_address"`
	ProtocolConfigurationOptions ProtocolConfigurationOptions `json:"protocol_configuration_options"`
}

// AttachComplete is ATTACH COMPLETE (TS 24.301 8.2.2), which carries
// the UE's ACTIVATE DEFAULT EPS BEARER CONTEXT ACCEPT.
type AttachComplete struct {
	ESMMessageContainer ActivateDefaultEPSBearerContextAccept `json:"esm_message_container"`
}

// ActivateDefaultEPSBearerContextAccept is ACTIVATE DEFAULT EPS BEARER
// CONTEXT ACCEPT (TS 24.301 8.3.4).
type ActivateDefaultEPSBearerContextAccept struct {
	EPSBearerIdentity            uint8 `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8 `json:"procedure_transaction_identity"`
}

// ActivateDedicatedEPSBearerContextRequest is ACTIVATE DEDICATED EPS
// BEARER CONTEXT REQUEST (TS 24.301 8.3.3). The network sends it when
// the PCRF has authorised the media of a call (TS 23.401 5.4.1). The UE
// matches the TFT against its sessions (TS 24.229 L.2.2.5.1B).
type ActivateDedicatedEPSBearerContextRequest struct {
	EPSBearerIdentity            uint8               `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8               `json:"procedure_transaction_identity"`
	LinkedEPSBearerIdentity      uint8               `json:"linked_eps_bearer_identity"`
	EPSQoS                       EPSQoS              `json:"eps_qos"`
	TFT                          TrafficFlowTemplate `json:"tft"`
}

// ActivateDedicatedEPSBearerContextAccept is ACTIVATE DEDICATED EPS
// BEARER CONTEXT ACCEPT (TS 24.301 8.3.1).
type ActivateDedicatedEPSBearerContextAccept struct {
	EPSBearerIdentity            uint8 `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8 `json:"procedure_transaction_identity"`
}

// ActivateDedicatedEPSBearerContextReject is ACTIVATE DEDICATED EPS
// BEARER CONTEXT REJECT (TS 24.301 8.3.2, 6.4.2.4).
type ActivateDedicatedEPSBearerContextReject struct {
	EPSBearerIdentity            uint8    `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8    `json:"procedure_transaction_identity"`
	ESMCause                     ESMCause `json:"esm_cause"`
}

// DeactivateEPSBearerContextRequest is DEACTIVATE EPS BEARER CONTEXT
// REQUEST (TS 24.301 8.3.12, 6.4.4.2).
type DeactivateEPSBearerContextRequest struct {
	EPSBearerIdentity            uint8    `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8    `json:"procedure_transaction_identity"`
	ESMCause                     ESMCause `json:"esm_cause"`
}

// DeactivateEPSBearerContextAccept is DEACTIVATE EPS BEARER CONTEXT
// ACCEPT (TS 24.301 8.3.11).
type DeactivateEPSBearerContextAccept struct {
	EPSBearerIdentity            uint8 `json:"eps_bearer_identity"`
	ProcedureTransactionIdentity uint8 `json:"procedure_transaction_identity"`
}

// DetachRequest is DETACH REQUEST: from the UE (TS 24.301 8.2.11.1),
// which the network answers with DETACH ACCEPT, or from the network
// (8.2.11.2), which the UE answers.
type DetachRequest struct {
	DetachType DetachType `json:"detach_type"`
}

// DetachAccept is DETACH ACCEPT (TS 24.301 8.2.10). It has no
// information elements beyond its header.
type DetachAccept struct{}

// RequestType is the Request type IE (TS 24.301 9.9.4.14).
type RequestType string

const InitialRequest RequestType = "initial request"

// PDNType is the PDN type IE (TS 24.301 9.9.4.10). The stub serves IPv4
// only, as every node of this project does.
type PDNType string

const IPv4 PDNType = "IPv4"

// DetachType is the type of detach (TS 24.301 9.9.3.7).
type DetachType string

const (
	// EPSDetach is the UE leaving the network.
	EPSDetach DetachType = "EPS detach"
	// ReattachNotRequired is the network detaching the UE for good.
	ReattachNotRequired DetachType = "re-attach not required"
)

// ESMCause is the ESM cause IE (TS 24.301 9.9.4.4, Table 9.9.4.4.1).
// Only the causes this project sends or expects are named.
type ESMCause uint8

const (
	InsufficientResources       ESMCause = 26
	RequestRejectedUnspecified  ESMCause = 31
	RegularDeactivation         ESMCause = 36
	NetworkFailure              ESMCause = 38
	SemanticErrorInTFTOperation ESMCause = 41
)

func (c ESMCause) String() string {
	switch c {
	case InsufficientResources:
		return "#26 insufficient resources"
	case RequestRejectedUnspecified:
		return "#31 request rejected, unspecified"
	case RegularDeactivation:
		return "#36 regular deactivation"
	case NetworkFailure:
		return "#38 network failure"
	case SemanticErrorInTFTOperation:
		return "#41 semantic error in the TFT operation"
	}
	return "#" + strconv.Itoa(int(c))
}

// QCI is the QoS class identifier of a bearer (TS 23.203 6.1.7).
type QCI uint8

const (
	// QCIConversationalVoice is the GBR class of the voice bearer (TS
	// 23.203 Table 6.1.7-A, IR.92 4.4.2).
	QCIConversationalVoice QCI = 1
	// QCIIMSSignalling is the non-GBR class of the default bearer of the
	// IMS APN (TS 23.203 Table 6.1.7-A, IR.92 4.4.1).
	QCIIMSSignalling QCI = 5
)

// EPSQoS is the EPS quality of service IE (TS 24.301 9.9.4.3). The bit
// rates are in kbit/s and only a GBR bearer has them.
type EPSQoS struct {
	QCI                          QCI    `json:"qci"`
	MaximumBitRateForUplink      uint32 `json:"maximum_bit_rate_for_uplink,omitempty"`
	MaximumBitRateForDownlink    uint32 `json:"maximum_bit_rate_for_downlink,omitempty"`
	GuaranteedBitRateForUplink   uint32 `json:"guaranteed_bit_rate_for_uplink,omitempty"`
	GuaranteedBitRateForDownlink uint32 `json:"guaranteed_bit_rate_for_downlink,omitempty"`
}

// PDNAddress is the PDN address IE (TS 24.301 9.9.4.9): the IP address
// of the UE on the PDN.
type PDNAddress struct {
	PDNTypeValue PDNType    `json:"pdn_type_value"`
	IPv4Address  netip.Addr `json:"ipv4_address"`
}

// TrackingAreaIdentity is the Tracking area identity IE (TS 24.301
// 9.9.3.32).
type TrackingAreaIdentity struct {
	PLMN PLMNIdentity `json:"plmn"`
	// TAC is the tracking area code, 16 bits.
	TAC uint16 `json:"tac"`
}

// PLMNIdentity is the mobile country code and the mobile network code
// of a network, as decimal digits (TS 23.003 2.2).
type PLMNIdentity struct {
	MCC string `json:"mcc"`
	MNC string `json:"mnc"`
}

// SystemInformation is what a UE learns from the cell it camps on: the
// network, the tracking area and the cell identity (TS 36.331
// SystemInformationBlockType1). A UE needs them
// for P-Access-Network-Info (TS 24.229 7.2A.4.3).
//
// SIMPLIFIED: TS 36.331. There is no radio to broadcast system
// information, so the stub serves it at SystemInformationPath.
type SystemInformation struct {
	TrackingAreaIdentity TrackingAreaIdentity `json:"tracking_area_identity"`
	// CellIdentity is the E-UTRAN cell identity (ECI), 28 bits.
	CellIdentity uint32 `json:"cell_identity"`
}

// ProtocolConfigurationOptions is the PCO IE (TS 24.301 9.9.4.11),
// whose contents TS 24.008 10.5.6.3 defines: a list of containers, each
// with an identifier and contents.
type ProtocolConfigurationOptions struct {
	Containers []Container `json:"containers,omitempty"`
}

// Container is one container of the PCO. Contents is empty for a
// request.
type Container struct {
	ID       ContainerID `json:"id"`
	Contents string      `json:"contents,omitempty"`
}

// ContainerID is a container identifier of TS 24.008 10.5.6.3, written
// in JSON as the spec writes it, such as "000CH".
type ContainerID uint16

// PCSCFIPv4Address is container 000CH: from the UE an empty request for
// the P-CSCF addresses, from the network one P-CSCF IPv4 address per
// container (TS 24.008 10.5.6.3).
const PCSCFIPv4Address ContainerID = 0x000c

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

// PCSCFIPv4AddressRequest is the PCO with which a UE asks for the P-CSCF
// addresses (TS 24.229 L.2.2.1).
func PCSCFIPv4AddressRequest() ProtocolConfigurationOptions {
	return ProtocolConfigurationOptions{Containers: []Container{{ID: PCSCFIPv4Address}}}
}

// PCSCFIPv4Addresses lists the P-CSCF addresses of the PCO in its order,
// the most preferred first (TS 24.229 L.2.2.1). Contents that are not an
// IPv4 address are skipped.
func (p ProtocolConfigurationOptions) PCSCFIPv4Addresses() []netip.Addr {
	var addrs []netip.Addr
	for _, c := range p.Containers {
		if c.ID != PCSCFIPv4Address {
			continue
		}
		if a, err := netip.ParseAddr(c.Contents); err == nil && a.Is4() {
			addrs = append(addrs, a)
		}
	}
	return addrs
}

// asksForPCSCF reports whether the PCO holds the request for the P-CSCF
// addresses.
func (p ProtocolConfigurationOptions) asksForPCSCF() bool {
	for _, c := range p.Containers {
		if c.ID == PCSCFIPv4Address {
			return true
		}
	}
	return false
}

// TrafficFlowTemplate is the TFT IE (TS 24.301 9.9.4.16), coded as TS
// 24.008 10.5.6.12: which packets a dedicated bearer carries.
type TrafficFlowTemplate struct {
	OperationCode TFTOperationCode `json:"tft_operation_code"`
	PacketFilters []PacketFilter   `json:"packet_filters"`
}

// TFTOperationCode is the TFT operation code of TS 24.008 Table
// 10.5.162. A new dedicated bearer gets "create new TFT".
type TFTOperationCode string

const CreateNewTFT TFTOperationCode = "create new TFT"

// PacketFilter is one packet filter of a TFT (TS 24.008 10.5.6.12). The
// components that are zero are left out. Remote is the far end of the
// flow, local the UE.
type PacketFilter struct {
	Identifier           uint8                 `json:"packet_filter_identifier"`
	Direction            PacketFilterDirection `json:"packet_filter_direction"`
	EvaluationPrecedence uint8                 `json:"packet_filter_evaluation_precedence"`
	IPv4RemoteAddress    netip.Addr            `json:"ipv4_remote_address,omitzero"`
	ProtocolIdentifier   uint8                 `json:"protocol_identifier,omitempty"`
	SingleLocalPort      uint16                `json:"single_local_port,omitempty"`
	SingleRemotePort     uint16                `json:"single_remote_port,omitempty"`
}

// PacketFilterDirection is the packet filter direction of TS 24.008
// 10.5.6.12.
type PacketFilterDirection string

const (
	DownlinkOnly  PacketFilterDirection = "downlink only"
	UplinkOnly    PacketFilterDirection = "uplink only"
	Bidirectional PacketFilterDirection = "bidirectional"
)

// BearerState is the state of an EPS bearer context on the network side
// (TS 24.301 6.1.3.3).
type BearerState string

const (
	// BearerActivePending: the request to activate it has gone to the UE,
	// which has not answered yet.
	BearerActivePending BearerState = "BEARER CONTEXT ACTIVE PENDING"
	BearerActive        BearerState = "BEARER CONTEXT ACTIVE"
)

// Status is what the API shows of an attached UE, at GET /ues/{imsi}: its PDN
// connection, its bearers and the Rx sessions bound to it.
type Status struct {
	IMSI       string         `json:"imsi"`
	PDNAddress netip.Addr     `json:"pdn_address"`
	APN        string         `json:"access_point_name"`
	Bearers    []BearerStatus `json:"bearers"`
	// RxSessions are the Session-Ids of the Rx sessions bound to the
	// IP-CAN session (TS 29.213 5.2).
	RxSessions []string `json:"rx_sessions"`
}

// BearerStatus is one EPS bearer of a Status.
type BearerStatus struct {
	EPSBearerIdentity uint8                `json:"eps_bearer_identity"`
	State             BearerState          `json:"state"`
	EPSQoS            EPSQoS               `json:"eps_qos"`
	TFT               *TrafficFlowTemplate `json:"tft,omitempty"`
}
