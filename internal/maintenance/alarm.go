package maintenance

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AlarmType says roughly in which area of the node an alarm arose (TS
// 28.111 6.3; the values are those of the alarmType attribute in TS
// 28.111 7.4.1, after ITU-T X.733 8.1.1).
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.1.3 and 7.4.1. The security alarm
// types (INTEGRITY_VIOLATION, OPERATIONAL_VIOLATION,
// PHYSICAL_VIOLATION, SECURITY_SERVICE_OR_MECHANISM_VIOLATION,
// TIME_DOMAIN_VIOLATION) are left out. A security alarm must carry
// serviceUser, serviceProvider and securityAlarmDetector, and the
// service user would be a subscriber, which an alarm here never names,
// so that the alarm list does not grow with the subscribers.
type AlarmType string

const (
	// CommunicationsAlarm: conveying information from one point to
	// another, such as a Diameter connection that is down.
	CommunicationsAlarm AlarmType = "COMMUNICATIONS_ALARM"
	// QualityOfServiceAlarm: a degraded service, often raised beside an
	// alarm of another type for the same fault (TS 28.111 6.3).
	QualityOfServiceAlarm AlarmType = "QUALITY_OF_SERVICE_ALARM"
	// ProcessingErrorAlarm: a software or processing fault.
	ProcessingErrorAlarm AlarmType = "PROCESSING_ERROR_ALARM"
	// EquipmentAlarm: an equipment fault.
	EquipmentAlarm AlarmType = "EQUIPMENT_ALARM"
	// EnvironmentalAlarm: the enclosure of the equipment.
	EnvironmentalAlarm AlarmType = "ENVIRONMENTAL_ALARM"
	// OtherAlarm: none of the types above fits, or the type is not
	// known.
	OtherAlarm AlarmType = "OTHER"
)

var alarmTypes = []AlarmType{CommunicationsAlarm, QualityOfServiceAlarm, ProcessingErrorAlarm, EquipmentAlarm, EnvironmentalAlarm, OtherAlarm}

// PerceivedSeverity is how urgently an alarm needs the operator (the
// perceivedSeverity attribute of TS 28.111 7.4.1, after ITU-T X.733
// 8.1.2.3).
type PerceivedSeverity string

const (
	// Critical: service is affected and needs action at once, such as
	// a role that is out of service.
	Critical PerceivedSeverity = "CRITICAL"
	// Major: service is affected and needs urgent action, such as a
	// severe loss of capacity.
	Major PerceivedSeverity = "MAJOR"
	// Minor: a fault that does not affect service yet.
	Minor PerceivedSeverity = "MINOR"
	// Warning: a fault that may come to affect service.
	Warning PerceivedSeverity = "WARNING"
	// Indeterminate: the severity cannot be determined.
	Indeterminate PerceivedSeverity = "INDETERMINATE"
	// Cleared: the condition of the alarm is no longer present. Only
	// ClearAlarm sets it.
	Cleared PerceivedSeverity = "CLEARED"
)

// severityRanks orders the severities for the answers and the
// min_severity filter. X.733 8.1.2.3 orders only the four that affect
// service, from CRITICAL to WARNING. INDETERMINATE comes after them and
// CLEARED last; the filter never hides INDETERMINATE (AlarmFilter).
var severityRanks = map[PerceivedSeverity]int{Critical: 0, Major: 1, Minor: 2, Warning: 3, Indeterminate: 4, Cleared: 5}

// AckState is whether an operator has acknowledged an alarm (the
// ackState attribute of TS 28.111 7.4.1). The operator sets it (6.7),
// and a new alarm starts UNACKNOWLEDGED.
type AckState string

const (
	Acknowledged   AckState = "ACKNOWLEDGED"
	Unacknowledged AckState = "UNACKNOWLEDGED"
)

// ProbableCause qualifies an alarm beyond its type (TS 28.111 6.3,
// after ITU-T X.733 8.1.2.1). The informative Annex B of TS 28.111
// lists the standard ones; the OpenAPI definition in the same archive
// (TS28111_FaultNrm.yaml) spells them in capitals with underscores, as
// the constants here are. A cause that Annex B does not list may be
// used in the same spelling (Annex B).
type ProbableCause string

// The probable causes that the roles raise. The comment names the table
// of TS 28.111 Annex B.
const (
	// CommunicationsSubsystemFailure: a connection to a peer, such as a
	// Diameter peer, is down (B.2, from X.733).
	CommunicationsSubsystemFailure ProbableCause = "COMMUNICATIONS_SUBSYSTEM_FAILURE"
	// ConnectionEstablishmentError: a connection to a peer cannot be set
	// up (B.3).
	ConnectionEstablishmentError ProbableCause = "CONNECTION_ESTABLISHMENT_ERROR"
	// UnderlyingResourceUnavailable: something the role depends on, such
	// as its store or every peer of an application, has become
	// unavailable (B.2).
	UnderlyingResourceUnavailable ProbableCause = "UNDERLYING_RESOURCE_UNAVAILABLE"
	// RoutingFailure: the next hop cannot be found, such as a DNS name
	// that does not resolve (B.3).
	RoutingFailure ProbableCause = "ROUTING_FAILURE"
	// ReducedEventReporting: copies of the signalling are being lost,
	// such as HEP packets that the sender drops (B.3).
	ReducedEventReporting ProbableCause = "REDUCED_EVENT_REPORTING"
)

// Peer names the far end of an alarm about a link, such as the Diameter
// peer hss.ims.mnc001.mcc001.3gppnetwork.org or the store 10.0.0.7:6379:
// a host name or an IP address, with a port or without. It is never a
// subscriber: RaiseAlarm refuses one that has the shape of a
// SIP or tel URI, a user@realm, an E.164 number or an IMSI.
type Peer string

// SpecificProblem refines the probable cause (TS 28.111 6.3, after
// ITU-T X.733 8.1.2.2), such as "Diameter peer down" or "no answer to
// DWR". It is a short phrase of letters, digits, spaces and - _ . ( ),
// and never names a subscriber.
type SpecificProblem string

// AlarmIdentity is what makes two alarms the same alarm: the object
// instance, the alarm type, the probable cause and the specific problem
// (TS 28.111 6.4). The object instance is the node, the role on it and
// the peer (ObjectInstance); there is no field for a subscriber, and
// RaiseAlarm refuses a peer, a cause or a problem that looks like one,
// so that the alarm list grows with the links of the node, never with
// its users.
type AlarmIdentity struct {
	// Role is the role of the node that raises the alarm, such as
	// "scscf"; empty for an alarm of the node as a whole, such as its
	// HEP sender.
	Role string
	// Peer is the far end of an alarm about a link; empty for an alarm
	// of the role (or node) itself.
	Peer            Peer
	AlarmType       AlarmType
	ProbableCause   ProbableCause
	SpecificProblem SpecificProblem
}

// Alarm is what a role raises with Node.RaiseAlarm.
type Alarm struct {
	AlarmIdentity
	// PerceivedSeverity is any severity but CLEARED.
	PerceivedSeverity PerceivedSeverity
	// AdditionalText is free text for the operator (TS 28.111 7.4.1,
	// after X.733 8.1.2.13), such as "no answer to 3 DWR in 90 s". It
	// must hold no secret and no subscriber identity: RaiseAlarm
	// refuses one that looks like either.
	AdditionalText string
}

// AlarmRecord is one alarm in the alarm list of the node (TS 28.111
// 7.3.1), with the attributes of 7.4.1 that this API keeps.
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.1.2. Of the mandatory attributes,
// notificationId is left out: the node sends no notifications (6.12),
// and the OAM server reads the list instead. Of the optional ones, only
// alarmChangedTime, specificProblem and additionalText are kept, with
// the acknowledgement attributes ackState, ackTime and ackUserId (6.7).
// Left out are backedUpStatus, backUpObject, trendIndication,
// thresholdInfo, stateChangeDefinition, monitoredAttributes,
// proposedRepairActions, additionalInformation, comments (6.9), the
// correlation attributes (6.10), ackSystemId, the ADMC attributes
// clearUserId, clearSystemId and clearingType (every alarm here is
// cleared by the node, 6.8), and the security alarm attributes.
type AlarmRecord struct {
	// AlarmID tells the record apart in the list. It is random, so that
	// it differs across the lists of all nodes and over time (6.4).
	AlarmID string `json:"alarm_id"`
	// ObjectInstance is the alarmed object, written like a DN (see
	// ObjectInstance).
	ObjectInstance string `json:"object_instance"`
	// Role and Peer are the parts of ObjectInstance, for a screen to
	// show.
	Role              string            `json:"role,omitempty"`
	Peer              Peer              `json:"peer,omitempty"`
	AlarmRaisedTime   time.Time         `json:"alarm_raised_time"`
	AlarmChangedTime  *time.Time        `json:"alarm_changed_time,omitempty"`
	AlarmClearedTime  *time.Time        `json:"alarm_cleared_time,omitempty"`
	AlarmType         AlarmType         `json:"alarm_type"`
	ProbableCause     ProbableCause     `json:"probable_cause"`
	SpecificProblem   SpecificProblem   `json:"specific_problem,omitempty"`
	PerceivedSeverity PerceivedSeverity `json:"perceived_severity"`
	AdditionalText    string            `json:"additional_text,omitempty"`
	AckState          AckState          `json:"ack_state"`
	AckTime           *time.Time        `json:"ack_time,omitempty"`
	// AckUserID is the operator who last changed AckState, as the
	// request named them.
	AckUserID string `json:"ack_user_id,omitempty"`
}

// AlarmList is the answer to GET /maintenance/v1/alarms: the alarm list
// of the node, the AlarmList of TS 28.111 7.3.2 that a ManagedElement
// contains.
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.2.2 and 6.13. The mandatory
// operationalState of the list is left out: the list works whenever
// the node answers, so it would always be ENABLED. The optional
// administrativeState (a LOCKED list stops recording) and
// unreliableAlarmScope (6.11) are left out too; DroppedRecords says
// instead when the list stopped holding every alarm.
type AlarmList struct {
	Node     string `json:"node"`
	Instance string `json:"instance,omitempty"`
	// NumOfAlarmRecords counts the records in the list, whether the
	// filter of the request kept them or not.
	NumOfAlarmRecords int `json:"num_of_alarm_records"`
	// LastModification is when a record was last added, changed or
	// removed; the start of the node before any.
	LastModification time.Time `json:"last_modification"`
	// DroppedRecords counts the records removed because the list was
	// full (Config.MaxAlarmRecords), since the node started. It is not
	// an attribute of TS 28.111: it says that the list stopped holding
	// every alarm.
	DroppedRecords uint64 `json:"dropped_records"`
	// AlarmRecords are the records that the filter kept, the most severe
	// first and, within a severity, the newest first.
	AlarmRecords []AlarmRecord `json:"alarm_records"`
}

// AckStateRequest is the body of PUT
// /maintenance/v1/alarms/{id}/ack-state.
type AckStateRequest struct {
	AckState AckState `json:"ack_state"`
	// Operator names who acknowledges; the record keeps it as
	// ackUserId.
	Operator string `json:"operator,omitempty"`
}

// AlarmFilter picks the records of an answer. The zero value keeps
// every record.
type AlarmFilter struct {
	// Cleared, when set, keeps only the cleared records (true) or only
	// those that are not (false).
	Cleared *bool
	// MinSeverity, when set, keeps the records of this severity or a
	// more severe one, and those of INDETERMINATE, whose severity cannot
	// be told. It is one of CRITICAL, MAJOR, MINOR and WARNING.
	MinSeverity PerceivedSeverity
}

// The query parameters of GET /maintenance/v1/alarms.
const (
	clearedParameter     = "cleared"
	minSeverityParameter = "min_severity"
)

// ParseAlarmFilter reads the query of GET /maintenance/v1/alarms:
// cleared=true|false and min_severity=CRITICAL|MAJOR|MINOR|WARNING. A
// parameter that it does not know is an error, so that a misspelt
// filter is not silently ignored.
func ParseAlarmFilter(q url.Values) (AlarmFilter, error) {
	var f AlarmFilter
	for name, values := range q {
		if len(values) != 1 {
			return AlarmFilter{}, fmt.Errorf("%w: query parameter %s more than once", ErrInvalidRequest, name)
		}
		v := values[0]
		switch name {
		case clearedParameter:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return AlarmFilter{}, fmt.Errorf("%w: cleared: %q is not true or false", ErrInvalidRequest, v)
			}
			f.Cleared = &b
		case minSeverityParameter:
			s := PerceivedSeverity(v)
			if rank, ok := severityRanks[s]; !ok || rank > severityRanks[Warning] {
				return AlarmFilter{}, fmt.Errorf("%w: min_severity: %q is none of %s, %s, %s, %s", ErrInvalidRequest, v, Critical, Major, Minor, Warning)
			}
			f.MinSeverity = s
		default:
			return AlarmFilter{}, fmt.Errorf("%w: unknown query parameter %s; the filters are %s and %s", ErrInvalidRequest, name, clearedParameter, minSeverityParameter)
		}
	}
	return f, nil
}

// Query is the query of a request with the filter, which
// ParseAlarmFilter reads back.
func (f AlarmFilter) Query() url.Values {
	q := url.Values{}
	if f.Cleared != nil {
		q.Set(clearedParameter, strconv.FormatBool(*f.Cleared))
	}
	if f.MinSeverity != "" {
		q.Set(minSeverityParameter, string(f.MinSeverity))
	}
	return q
}

// keeps says whether the filter keeps r.
func (f AlarmFilter) keeps(r AlarmRecord) bool {
	cleared := r.PerceivedSeverity == Cleared
	switch {
	case f.Cleared != nil && *f.Cleared != cleared:
		return false
	case f.MinSeverity == "":
		return true
	case cleared:
		return false
	case r.PerceivedSeverity == Indeterminate:
		return true
	}
	return severityRanks[r.PerceivedSeverity] <= severityRanks[f.MinSeverity]
}

// CompareAlarmRecords orders alarm records as the answers list them:
// the most severe first, then the newest first. Records of the same
// severity raised at the same time compare equal.
func CompareAlarmRecords(a, b AlarmRecord) int {
	return cmp.Or(
		cmp.Compare(severityRanks[a.PerceivedSeverity], severityRanks[b.PerceivedSeverity]),
		b.AlarmRaisedTime.Compare(a.AlarmRaisedTime),
	)
}

// ObjectInstance writes the alarmed object of an alarm of node like a
// DN: ManagedElement=pcscf-1,ManagedFunction=pcscf,Peer=hss.ims.example.
//
// SIMPLIFIED: TS 28.111 V20.0.0 6.3 and 7.4.1. objectInstance is the
// Distinguished Name of the alarmed object. The node is no managed
// object of an NRM here, so the name only looks like a DN: the classes
// are the generic ManagedElement and ManagedFunction of TS 28.622, not
// the IMS classes of TS 28.705, which not every role has, and Peer is
// no class of any NRM. The values are not escaped.
func ObjectInstance(node string, id AlarmIdentity) string {
	s := "ManagedElement=" + node
	if id.Role != "" {
		s += ",ManagedFunction=" + id.Role
	}
	if id.Peer != "" {
		s += ",Peer=" + string(id.Peer)
	}
	return s
}

// ErrInvalidAlarm is the answer of RaiseAlarm and ClearAlarm to an alarm
// that the list cannot take: an unknown value, a role that the node
// does not run, or a field that may hold a secret or a subscriber.
var ErrInvalidAlarm = errors.New("invalid alarm")

// ErrUnknownAlarm is the answer for an alarm id that is not in the list.
var ErrUnknownAlarm = errors.New("no such alarm in the list")

// The bounds of the fields of an alarm, which keep a record small.
const (
	maxSpecificProblem = 80
	maxProbableCause   = 64
	maxAdditionalText  = 512
)

var (
	probableCausePattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	specificProblemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.()-]*$`)
	// A run of digits as long as an MSISDN without its country code or
	// an IMSI; a port or a year is shorter.
	longDigitRun = regexp.MustCompile(`[0-9]{6,}`)
	// The 128 bits of a K, an OPc or a derived key, in hexadecimal.
	longHexRun = regexp.MustCompile(`[0-9A-Fa-f]{32,}`)
	// A name followed by a value, as in password=... or k: ...
	assignment = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*)\s*[:=]\s*\S`)
)

// checkIdentity says why the node cannot raise or clear an alarm of id.
func (n *Node) checkIdentity(id AlarmIdentity) error {
	if id.Role != "" && n.roles[id.Role] == nil {
		return fmt.Errorf("%w: role %q is not a role of this node", ErrInvalidAlarm, id.Role)
	}
	if id.Peer != "" {
		if err := checkPeer(string(id.Peer)); err != nil {
			return fmt.Errorf("%w: peer: %v", ErrInvalidAlarm, err)
		}
	}
	if !isAlarmType(id.AlarmType) {
		return fmt.Errorf("%w: alarm type %q is not one of TS 28.111 7.4.1 that this API takes", ErrInvalidAlarm, id.AlarmType)
	}
	cause := string(id.ProbableCause)
	if len(cause) > maxProbableCause || !probableCausePattern.MatchString(cause) || longDigitRun.MatchString(cause) {
		return fmt.Errorf("%w: probable cause %q is not a name of capitals, digits and underscores, as in TS 28.111 Annex B", ErrInvalidAlarm, cause)
	}
	if p := string(id.SpecificProblem); p != "" {
		if len(p) > maxSpecificProblem || !specificProblemPattern.MatchString(p) || longDigitRun.MatchString(p) {
			return fmt.Errorf("%w: specific problem: a phrase of letters, digits, spaces and - _ . ( ), at most %d long and without a long number", ErrInvalidAlarm, maxSpecificProblem)
		}
	}
	return nil
}

func isAlarmType(t AlarmType) bool {
	for _, x := range alarmTypes {
		if t == x {
			return true
		}
	}
	return false
}

// checkPeer refuses a peer that is not a host name or an IP address,
// with a port or without, or that has the shape of a subscriber.
func checkPeer(p string) error {
	if err := checkNoSubscriber(p); err != nil {
		return err
	}
	host := p
	if h, port, err := net.SplitHostPort(p); err == nil {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%q has no port number", p)
		}
		host = h
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > 253 {
		return errors.New("longer than a host name")
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || strings.Trim(label, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-") != "" {
			return fmt.Errorf("%q is not a host name or an IP address", p)
		}
		if len(label) > 3 && strings.Trim(label, "0123456789") == "" {
			return fmt.Errorf("%q has a label of digits only, as a number of a subscriber would be", p)
		}
	}
	return nil
}

// checkNoSubscriber refuses text with the shape of a subscriber
// identity: a SIP or tel URI, a user@realm such as a private
// identity, a number in E.164 form, or a run of digits as long as an
// IMSI or an MSISDN. It cannot recognise every way of writing one; the
// role must not put one there.
func checkNoSubscriber(s string) error {
	lower := strings.ToLower(s)
	for _, scheme := range []string{"sip:", "sips:", "tel:"} {
		if strings.Contains(lower, scheme) {
			return fmt.Errorf("holds a %s URI, which names a user", strings.TrimSuffix(scheme, ":"))
		}
	}
	switch {
	case strings.Contains(s, "@"):
		return errors.New("holds an @, as a private identity user@realm would")
	case plusNumber(s):
		return errors.New("holds a number in E.164 form")
	case longDigitRun.MatchString(s):
		return errors.New("holds a run of digits as long as an IMSI or an MSISDN")
	}
	return nil
}

// plusNumber says whether s has a + followed by a digit.
func plusNumber(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '+' && isDigit(s[i+1]) {
			return true
		}
	}
	return false
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// checkAlarmText refuses an additional text that may carry a secret
// or a subscriber. A secret is a name of the redaction check
// (isSecretName) with a value after it, such as password=... or k: ...,
// or 32 hexadecimal digits in a row, the length of a K or an OPc.
func checkAlarmText(text string) error {
	if len(text) > maxAdditionalText {
		return fmt.Errorf("additional text is longer than %d", maxAdditionalText)
	}
	for _, m := range assignment.FindAllStringSubmatch(text, -1) {
		if isSecretName(m[1]) {
			return fmt.Errorf("additional text gives a value to %q, the name of a secret", m[1])
		}
	}
	if longHexRun.MatchString(text) {
		return errors.New("additional text holds 32 hexadecimal digits in a row, the length of a key")
	}
	if err := checkNoSubscriber(text); err != nil {
		return fmt.Errorf("additional text %v", err)
	}
	return nil
}

// CheckAlarmRecord says why a record must not be shown: it has a field
// that may hold a secret or a subscriber. The node checks each alarm
// when it is raised; the OAM server checks the records that a node
// answers with again, as a node of another build might not.
func CheckAlarmRecord(r AlarmRecord) error {
	if err := CheckRedacted(r); err != nil {
		return err
	}
	if err := checkAlarmText(r.AdditionalText); err != nil {
		return fmt.Errorf("alarm %s: %w", r.AlarmID, err)
	}
	for _, s := range []string{r.ObjectInstance, string(r.SpecificProblem), string(r.ProbableCause)} {
		if err := checkNoSubscriber(s); err != nil {
			return fmt.Errorf("alarm %s: %w", r.AlarmID, err)
		}
	}
	return nil
}
