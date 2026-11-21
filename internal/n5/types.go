package n5

// The structured data types of TS 29.514 5.6.2 that the IMS procedures
// use. Each comment names the clause of its table; the JSON names and
// which attributes are required come from Annex A.2.

// AppSessionContext is the Individual Application Session Context
// resource (TS 29.514 5.6.2.2). The AF sends the request part in a
// Create; the PCF answers with the context, which may also carry the
// response part and event reports.
type AppSessionContext struct {
	AscReqData  *AppSessionContextReqData  `json:"ascReqData,omitempty"`
	AscRespData *AppSessionContextRespData `json:"ascRespData,omitempty"`
	// EvsNotif reports events whose information the PCF already has when
	// it answers (TS 29.514 4.2.2.2).
	EvsNotif *EventsNotification `json:"evsNotif,omitempty"`
}

// AppSessionContextReqData is the service information of the AF (TS
// 29.514 5.6.2.3). NotifUri and SuppFeat are required, and exactly one
// of UeIpv4, UeIpv6 and ueMac, which the PCF uses to bind the context
// to the UE's PDU session (session binding, TS 29.514 4.2.2.2).
//
// SIMPLIFIED: TS 29.514 5.6.2.3. ueMac, for Ethernet PDU sessions, is
// left out; IMS runs over IP PDU sessions.
type AppSessionContextReqData struct {
	// AfAppId identifies the service, such as the ICSI of the IMS
	// session (TS 29.514 B.1).
	AfAppId string `json:"afAppId,omitempty"`
	// AfChargId correlates charging; it needs IMS_SBI (TS 29.514
	// 4.2.2.2).
	AfChargId string `json:"afChargId,omitempty"`
	// EvSubsc subscribes to events and says where the PCF notifies them.
	// It creates the Events Subscription sub-resource.
	EvSubsc *EventsSubscReqData `json:"evSubsc,omitempty"`
	// MedComponents is keyed by the medCompN of each component, written
	// as a decimal string (TS 29.514 5.6.2.3).
	MedComponents map[string]MediaComponent `json:"medComponents,omitempty"`
	// ServInfStatus says whether the service information is final, from
	// an SDP answer, or preliminary, from an offer alone (TS 29.514
	// 4.2.2.15, B.1).
	ServInfStatus ServiceInfoStatus `json:"servInfStatus,omitempty"`
	// NotifUri is where the PCF asks the AF to delete the context: the
	// PCF posts to {NotifUri}/terminate (TS 29.514 4.2.5.3).
	NotifUri string            `json:"notifUri"`
	SuppFeat SupportedFeatures `json:"suppFeat"`
	UeIpv4   string            `json:"ueIpv4,omitempty"`
	UeIpv6   string            `json:"ueIpv6,omitempty"`
}

// AppSessionContextRespData is the authorization data the PCF returns
// (TS 29.514 5.6.2.4). SuppFeat is the subset of the AF's features that
// the PCF supports too.
//
// SIMPLIFIED: TS 29.514 5.6.2.4. servAuthInfo, directNotifReports and
// ueIds are left out; they serve background data transfer, QoS
// monitoring and the exposure of 5GS UE identities, which IMS does not
// use here.
type AppSessionContextRespData struct {
	SuppFeat SupportedFeatures `json:"suppFeat,omitempty"`
}

// AppSessionContextUpdateDataPatch is the body of a PATCH to an
// Individual Application Session Context (TS 29.514 5.6.2.43). It is a
// JSON Merge Patch (RFC 7396): what it holds replaces the same
// attributes of the context, and what it leaves out stays as it was.
type AppSessionContextUpdateDataPatch struct {
	AscReqData *AppSessionContextUpdateData `json:"ascReqData,omitempty"`
}

// AppSessionContextUpdateData is the modification of the service
// information (TS 29.514 5.6.2.5).
type AppSessionContextUpdateData struct {
	AfAppId string `json:"afAppId,omitempty"`
	// EvSubsc, when present, holds the new complete list of events
	// (TS 29.514 4.2.3.2).
	EvSubsc       *EventsSubscReqDataRm       `json:"evSubsc,omitempty"`
	MedComponents map[string]MediaComponentRm `json:"medComponents,omitempty"`
	ServInfStatus ServiceInfoStatus           `json:"servInfStatus,omitempty"`
}

// MediaComponent is the service information of one media component
// (TS 29.514 5.6.2.7): one "m=" line of the SDP, or the AF signalling
// when MedCompN is 0 (4.2.6.7). It corresponds to the
// Media-Component-Description AVP of Rx.
//
// The bit rates include the overhead of IP and the layers above it, as
// on Rx: the P-CSCF derives them from b=AS, b=RR and b=RS (TS 29.514
// B.1, which points to TS 29.513 7.2 for the rules).
type MediaComponent struct {
	AfAppId string `json:"afAppId,omitempty"`
	// Codecs holds the codec data of the offer and of the answer,
	// encoded as for the Codec-Data AVP (TS 29.214 5.3.7).
	Codecs      []CodecData                  `json:"codecs,omitempty"`
	FStatus     FlowStatus                   `json:"fStatus,omitempty"`
	MarBwDl     BitRate                      `json:"marBwDl,omitempty"`
	MarBwUl     BitRate                      `json:"marBwUl,omitempty"`
	MedCompN    int                          `json:"medCompN"`
	MedSubComps map[string]MediaSubComponent `json:"medSubComps,omitempty"`
	MedType     MediaType                    `json:"medType,omitempty"`
	RrBw        BitRate                      `json:"rrBw,omitempty"`
	RsBw        BitRate                      `json:"rsBw,omitempty"`
}

// MediaSubComponent is one IP flow, or a pair of flows in opposite
// directions, of a media component (TS 29.514 5.6.2.8). MedSubComps is
// keyed by FNum written as a decimal string.
type MediaSubComponent struct {
	FNum int `json:"fNum"`
	// FDescs holds at most one uplink and one downlink filter (TS 29.514
	// 4.2.2.2).
	FDescs    []FlowDescription `json:"fDescs,omitempty"`
	FlowUsage FlowUsage         `json:"flowUsage,omitempty"`
}

// SIMPLIFIED: TS 29.514 5.6.2.25 to 5.6.2.27. The "Rm" variants of the
// types differ from the plain ones in that an attribute may be null,
// which a JSON Merge Patch uses to remove it. The P-CSCF never removes
// an attribute in a PATCH, so the same Go types serve, and an omitted
// attribute keeps its earlier value.
type (
	// MediaComponentRm is MediaComponent in a PATCH (TS 29.514 5.6.2.26).
	MediaComponentRm = MediaComponent
	// MediaSubComponentRm is MediaSubComponent in a PATCH (TS 29.514
	// 5.6.2.27).
	MediaSubComponentRm = MediaSubComponent
	// EventsSubscReqDataRm is EventsSubscReqData in a PATCH (TS 29.514
	// 5.6.2.25).
	EventsSubscReqDataRm = EventsSubscReqData
)

// EventsSubscReqData is the Events Subscription sub-resource (TS
// 29.514 5.6.2.6): the events the AF wants to hear about, and the URI
// the PCF posts them to, at {NotifUri}/notify (4.2.5.2).
//
// SIMPLIFIED: TS 29.514 5.6.2.6. The thresholds and parameters of
// usage, QoS monitoring and access network reports are left out.
type EventsSubscReqData struct {
	Events   []AfEventSubscription `json:"events"`
	NotifUri string                `json:"notifUri,omitempty"`
}

// AfEventSubscription is one subscribed event (TS 29.514 5.6.2.10).
//
// SIMPLIFIED: TS 29.514 5.6.2.10. notifMethod, repPeriod and waitTime
// are left out. Without notifMethod the PCF reports on event detection,
// which is what IMS needs.
type AfEventSubscription struct {
	Event AfEvent `json:"event"`
}

// EventsNotification is the body of a notification of events (TS
// 29.514 5.6.2.9). EvSubsUri names the Events Subscription the
// notification is for: the context's resource URI with
// "/events-subscription" appended (4.2.2.2).
//
// SIMPLIFIED: TS 29.514 5.6.2.9. The access network, location,
// charging, QoS monitoring and usage reports are left out.
type EventsNotification struct {
	EvSubsUri                 string                    `json:"evSubsUri"`
	EvNotifs                  []AfEventNotification     `json:"evNotifs"`
	FailedResourcAllocReports []ResourcesAllocationInfo `json:"failedResourcAllocReports,omitempty"`
	SuccResourcAllocReports   []ResourcesAllocationInfo `json:"succResourcAllocReports,omitempty"`
}

// AfEventNotification is one reported event (TS 29.514 5.6.2.11).
// Flows names the flows the event affects when it does not affect them
// all (4.2.5.5).
type AfEventNotification struct {
	Event AfEvent `json:"event"`
	Flows []Flows `json:"flows,omitempty"`
}

// Flows names flows of one media component (TS 29.514 5.6.2.21): FNums
// within the component MedCompN, or the whole component when FNums is
// empty.
type Flows struct {
	FNums    []int `json:"fNums,omitempty"`
	MedCompN int   `json:"medCompN"`
}

// ResourcesAllocationInfo is the outcome of resource allocation for
// some flows (TS 29.514 5.6.2.14, used in 4.2.5.8).
type ResourcesAllocationInfo struct {
	McResourcStatus MediaComponentResourcesStatus `json:"mcResourcStatus,omitempty"`
	Flows           []Flows                       `json:"flows,omitempty"`
}

// TerminationInfo is the body of a termination request (TS 29.514
// 5.6.2.12): why the context is no longer valid, and the URI of the
// context the AF is to delete.
type TerminationInfo struct {
	TermCause TerminationCause `json:"termCause"`
	ResUri    string           `json:"resUri"`
}

// CodecData is the codec data of a media component (TS 29.514
// 5.6.3.2), encoded as the Codec-Data AVP of TS 29.214 5.3.7.
type CodecData string

// FlowDescription is a packet filter (TS 29.514 5.6.3.2): an
// IPFilterRule of RFC 6733 4.3, restricted as TS 29.214 5.3.8 says,
// such as "permit out 17 from 192.0.2.10 to 198.51.100.20 49170".
type FlowDescription string

// The enumerations of TS 29.514 5.6.3. Each is a string, so a value
// this package does not name still decodes. Only the values the IMS
// procedures use are named.

// AfEvent is an event the AF can subscribe to (TS 29.514 5.6.3.7).
type AfEvent string

const (
	// FailedResourcesAllocation: resources for some flows could not be
	// allocated, or were lost (TS 29.514 4.2.5.5, 4.2.5.8). It also
	// reports the loss of the signalling path (4.2.5.10).
	FailedResourcesAllocation AfEvent = "FAILED_RESOURCES_ALLOCATION"
	// SuccessfulResourcesAllocation: resources for some flows were
	// allocated (TS 29.514 4.2.5.8).
	SuccessfulResourcesAllocation AfEvent = "SUCCESSFUL_RESOURCES_ALLOCATION"
)

// FlowStatus says which directions of a media component may pass (TS
// 29.514 5.6.3.12).
type FlowStatus string

const (
	FlowStatusEnabledUplink   FlowStatus = "ENABLED-UPLINK"
	FlowStatusEnabledDownlink FlowStatus = "ENABLED-DOWNLINK"
	FlowStatusEnabled         FlowStatus = "ENABLED"
	FlowStatusDisabled        FlowStatus = "DISABLED"
	FlowStatusRemoved         FlowStatus = "REMOVED"
)

// FlowUsage says what a flow carries (TS 29.514 5.6.3.14).
type FlowUsage string

const (
	FlowUsageNoInfo       FlowUsage = "NO_INFO"
	FlowUsageRTCP         FlowUsage = "RTCP"
	FlowUsageAFSignalling FlowUsage = "AF_SIGNALLING"
)

// MediaType is the media of an "m=" line (TS 29.514 5.6.3.3).
type MediaType string

const (
	MediaTypeAudio       MediaType = "AUDIO"
	MediaTypeVideo       MediaType = "VIDEO"
	MediaTypeData        MediaType = "DATA"
	MediaTypeApplication MediaType = "APPLICATION"
	MediaTypeControl     MediaType = "CONTROL"
	MediaTypeText        MediaType = "TEXT"
	MediaTypeMessage     MediaType = "MESSAGE"
	MediaTypeOther       MediaType = "OTHER"
)

// ServiceInfoStatus says whether the service information is final (TS
// 29.514 5.6.3.16).
type ServiceInfoStatus string

const (
	ServiceInfoStatusFinal       ServiceInfoStatus = "FINAL"
	ServiceInfoStatusPreliminary ServiceInfoStatus = "PRELIMINARY"
)

// TerminationCause is why the PCF asks the AF to delete a context (TS
// 29.514 5.6.3.10, 4.2.5.3).
type TerminationCause string

const (
	// AllSDFDeactivation: all the service data flows of the context are
	// gone, or none could be allocated.
	AllSDFDeactivation TerminationCause = "ALL_SDF_DEACTIVATION"
	// PDUSessionTermination: the SM Policy Context of the PDU session
	// ended.
	PDUSessionTermination TerminationCause = "PDU_SESSION_TERMINATION"
	// InsufficientServerResources: the PCF is overloaded.
	InsufficientServerResources TerminationCause = "INSUFFICIENT_SERVER_RESOURCES"
	// InsufficientQoSFlowResources: the PDU session has no room for
	// another QoS flow.
	InsufficientQoSFlowResources TerminationCause = "INSUFFICIENT_QOS_FLOW_RESOURCES"
)

// MediaComponentResourcesStatus says whether the PCC rules of a media
// component are active (TS 29.514 5.6.3.13).
type MediaComponentResourcesStatus string

const (
	MediaComponentResourcesActive   MediaComponentResourcesStatus = "ACTIVE"
	MediaComponentResourcesInactive MediaComponentResourcesStatus = "INACTIVE"
)
