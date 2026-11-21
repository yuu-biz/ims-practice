package h248

import "strings"

// The tokens of the text encoding that this package reads, in their
// long form (RFC 3525 Annex B.2). The long form is also what Marshal
// writes.
const (
	tokMegaco         = "MEGACO"
	tokTransaction    = "Transaction"
	tokReply          = "Reply"
	tokPending        = "Pending"
	tokResponseAck    = "TransactionResponseAck"
	tokImmAckRequired = "ImmAckRequired"
	tokContext        = "Context"
	tokError          = "Error"

	tokMedia            = "Media"
	tokStream           = "Stream"
	tokLocalControl     = "LocalControl"
	tokLocal            = "Local"
	tokRemote           = "Remote"
	tokTerminationState = "TerminationState"
	tokMode             = "Mode"
	tokReservedValue    = "ReservedValue"
	tokReservedGroup    = "ReservedGroup"
	tokServiceStates    = "ServiceStates"

	tokEvents           = "Events"
	tokObservedEvents   = "ObservedEvents"
	tokSignals          = "Signals"
	tokSignalList       = "SignalList"
	tokSignalType       = "SignalType"
	tokDuration         = "Duration"
	tokNotifyCompletion = "NotifyCompletion"
	tokKeepActive       = "KeepActive"
	tokEmbed            = "Embed"
	tokDigitMap         = "DigitMap"

	tokServices             = "Services"
	tokMethod               = "Method"
	tokReason               = "Reason"
	tokProfile              = "Profile"
	tokVersion              = "Version"
	tokMgcIDToTry           = "MgcIdToTry"
	tokDelay                = "Delay"
	tokServiceChangeAddress = "ServiceChangeAddress"

	tokAudit      = "Audit"
	tokPackages   = "Packages"
	tokStatistics = "Statistics"

	tokTopology     = "Topology"
	tokPriority     = "Priority"
	tokEmergency    = "Emergency"
	tokContextAudit = "ContextAudit"
)

// shortTokens gives the short form of each token. A receiver has to
// accept both forms (TS 29.334 5.9, Table 5.9.1, NOTE 1).
var shortTokens = map[string]string{
	tokMegaco:         "!",
	tokTransaction:    "T",
	tokReply:          "P",
	tokPending:        "PN",
	tokResponseAck:    "K",
	tokImmAckRequired: "IA",
	tokContext:        "C",
	tokError:          "ER",

	tokMedia:            "M",
	tokStream:           "ST",
	tokLocalControl:     "O",
	tokLocal:            "L",
	tokRemote:           "R",
	tokTerminationState: "TS",
	tokMode:             "MO",
	tokReservedValue:    "RV",
	tokReservedGroup:    "RG",
	tokServiceStates:    "SI",

	string(SendOnly): "SO",
	string(RecvOnly): "RC",
	string(SendRecv): "SR",
	string(Inactive): "IN",
	string(LoopBack): "LB",

	string(ServiceStateTest):         "TE",
	string(ServiceStateOutOfService): "OS",
	string(ServiceStateInService):    "IV",

	tokEvents:           "E",
	tokObservedEvents:   "OE",
	tokSignals:          "SG",
	tokSignalList:       "SL",
	tokSignalType:       "SY",
	tokDuration:         "DR",
	tokNotifyCompletion: "NC",
	tokKeepActive:       "KA",
	tokEmbed:            "EM",
	tokDigitMap:         "DM",

	// TimeOut is both a signal type and, as OnTimeOut, a reason for a
	// signal to end.
	string(Brief):   "BR",
	string(OnOff):   "OO",
	string(TimeOut): "TO",

	string(OnInterruptByEvent):          "IBE",
	string(OnInterruptByNewSignalDescr): "IBS",
	string(OtherReason):                 "OR",

	tokServices:             "SV",
	tokMethod:               "MT",
	tokReason:               "RE",
	tokProfile:              "PF",
	tokVersion:              "V",
	tokMgcIDToTry:           "MG",
	tokDelay:                "DL",
	tokServiceChangeAddress: "AD",

	string(Graceful):     "GR",
	string(Forced):       "FO",
	string(Restart):      "RS",
	string(Disconnected): "DC",
	string(HandOff):      "HO",
	string(Failover):     "FL",

	tokAudit:      "AT",
	tokPackages:   "PG",
	tokStatistics: "SA",

	tokTopology:     "TP",
	tokPriority:     "PR",
	tokEmergency:    "EG",
	tokContextAudit: "CA",

	string(Isolate): "IS",
	string(Oneway):  "OW",
	string(Bothway): "BW",

	string(Add):             "A",
	string(Move):            "MV",
	string(Modify):          "MF",
	string(Subtract):        "S",
	string(AuditValue):      "AV",
	string(AuditCapability): "AC",
	string(Notify):          "N",
	string(ServiceChange):   "SC",
}

// longTokens maps a token in either form, in lower case, to its long
// form. The text encoding is case insensitive (RFC 3525 Annex B.2).
var longTokens = func() map[string]string {
	m := make(map[string]string, 2*len(shortTokens))
	for long, short := range shortTokens {
		m[strings.ToLower(long)] = long
		m[strings.ToLower(short)] = long
	}
	return m
}()

// longToken returns the long form of the token that text spells, or
// false when text is not a token.
func longToken(text string) (string, bool) {
	long, ok := longTokens[strings.ToLower(text)]
	return long, ok
}
