package lab

import (
	"github.com/yuu-biz/ims-practice/internal/netann"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Ref points a learner at the part of a specification that describes a
// message. Clause is empty where the repository has not checked one.
type Ref struct {
	Spec   string `json:"spec"`
	Clause string `json:"clause"`
	Title  string `json:"title"`
}

// The clauses follow the editions the guides use: Release 18, TS 24.229
// V18.11.0, TS 29.228 V18.0.0 and TS 29.229 V18.1.0. Each one is cited
// for the same step in docs/guide/en/registration.md, docs/guide/en/
// call.md or the code that implements it. A response gets the
// references of the method it answers.
var sipRefTable = refTable[sip.Method]{
	sip.REGISTER: {
		{"TS 24.229", "5.1.1.2.1", "UE: initial registration"},
		{"TS 24.229", "5.2.2.1", "P-CSCF: forwarding a REGISTER"},
		{"TS 24.229", "5.3.1.2", "I-CSCF: REGISTER and the UAR to the HSS"},
		{"TS 24.229", "5.4.1.2.1", "S-CSCF: challenging an unprotected REGISTER"},
		{"TS 24.229", "5.4.1.2.2F", "S-CSCF: the 200 OK of a successful registration"},
		{"TS 24.229", "5.1.1.6.1", "UE: deregistration"},
		{"RFC 3261", "10.3", "Registrar: processing a REGISTER"},
		// SMS over IP follows TS 24.229 V20.1.0, TS 24.341 V19.0.0 and
		// TS 23.204 V19.1.0.
		{"TS 24.229", "5.4.1.7", "S-CSCF: third-party REGISTER to an application server"},
		{"TS 24.341", "5.3.3.2", "IP-SM-GW: the third-party REGISTER and the HSS"},
		{"TS 23.204", "6.1", "SMS over IP: registration"},
	},
	sip.MESSAGE: {
		{"TS 24.341", "5.3.1.2", "SM-over-IP sender: submitting a short message"},
		{"TS 24.229", "5.4.3.2", "S-CSCF: originating requests and the filter criteria"},
		{"TS 24.341", "5.3.3.4.1", "IP-SM-GW: 202 to a short message, RP-SMMA or delivery report"},
		{"TS 24.341", "5.3.3.4.3", "IP-SM-GW: the submit report"},
		{"TS 24.341", "5.3.3.4.2", "IP-SM-GW: delivering a short message"},
		{"TS 24.341", "5.3.2.4", "SM-over-IP receiver: the delivery report"},
		{"TS 24.341", "5.3.3.4.4", "IP-SM-GW: delivering a status report"},
		{"RFC 3428", "7", "UAS: the 202 of a message relay"},
		{"RFC 3841", "9.2", "Accept-Contact: only a contact with +g.3gpp.smsip"},
		{"RFC 3841", "9.1", "Request-Disposition: fork or no-fork"},
		{"RFC 3261", "20.21", "In-Reply-To: the Call-ID the report answers"},
	},
	sip.SUBSCRIBE: {
		{"TS 24.229", "5.1.1.3", "UE: subscribing to its registration state"},
		{"TS 24.229", "5.2.3", "P-CSCF: subscribing to the registration state"},
		{"TS 24.229", "5.4.2.1.1", "S-CSCF: accepting a reg event subscription"},
		{"RFC 6665", "4.1.2.1", "Subscriber: creating a subscription"},
	},
	sip.NOTIFY: {
		{"TS 24.229", "5.4.2.1.2", "S-CSCF: notifying the registration state"},
		{"RFC 6665", "4.2.1.2", "Notifier: the NOTIFY right after accepting"},
		{"RFC 3680", "5", "The reginfo document"},
	},
	sip.INVITE: {
		{"TS 24.229", "5.1.3.1", "UE: originating a session"},
		{"TS 24.229", "5.2.6.3.3", "P-CSCF: an INVITE from the caller"},
		{"TS 24.229", "5.4.3.2", "S-CSCF: originating requests"},
		{"TS 24.229", "5.3.2.1", "I-CSCF: terminating requests and the LIR"},
		{"TS 24.229", "5.4.3.3", "S-CSCF: terminating requests"},
		{"TS 24.229", "5.2.6.4.3", "P-CSCF: an INVITE to the callee"},
		// The MMTel AS follows TS 24.229 V20.1.0 and the editions of the
		// service specifications named in docs/guide/en/mmtel.md.
		{"TS 24.229", "5.7.5", "AS: routeing B2BUA"},
		{"RFC 5502", "6", "P-Served-User: the served user and the session case"},
		{"RFC 3323", "4.2", "Privacy: the privacy the user asks for"},
		{"TS 24.607", "4.5.2.4", "OIR at the AS of the caller"},
		{"TS 24.611", "4.5.2.6.2", "ACR: 433 to an anonymous caller"},
		{"TS 24.604", "4.5.2.6.2.2", "CDIV: the Request-URI and History-Info of a diverted call"},
		{"TS 24.604", "4.5.2.6.4", "CDIV: the 181 to the caller"},
		{"RFC 7044", "10.3", "History-Info: the index of each entry"},
		{"RFC 3261", "13", "Setting up a session with INVITE"},
		{"RFC 3264", "5.1", "The SDP offer"},
		{"RFC 3264", "6.1", "The SDP answer"},
	},
	sip.PRACK: {
		{"RFC 3262", "3", "UAS: reliable provisional responses"},
		{"RFC 3262", "4", "UAC: acknowledging with PRACK"},
	},
	sip.ACK: {
		{"RFC 3261", "13.2.2.4", "UAC: the ACK for a 2xx"},
	},
	sip.BYE: {
		{"RFC 3261", "15.1.1", "UAC: ending a session with BYE"},
		{"RFC 3261", "15.1.2", "UAS: receiving a BYE"},
	},
	sip.CANCEL: {
		{"RFC 3261", "9.1", "Client: cancelling a pending INVITE"},
		{"RFC 3261", "9.2", "Server: the 200 to CANCEL and the 487 to the INVITE"},
	},
}

// diameterRefTable is keyed by the command abbreviation. A request and its
// answer share the procedure but each has its own command format in TS
// 29.229 6.1.
var diameterRefTable = refTable[string]{
	"UAR": {
		{"TS 29.228", "6.1.1", "User registration status query"},
		{"TS 29.229", "6.1.1", "User-Authorization-Request"},
		{"TS 24.229", "5.3.1.2", "I-CSCF: REGISTER and the UAR to the HSS"},
	},
	"UAA": {
		{"TS 29.228", "6.1.1", "User registration status query"},
		{"TS 29.229", "6.1.2", "User-Authorization-Answer"},
		{"TS 29.229", "6.2.1.1", "DIAMETER_FIRST_REGISTRATION"},
	},
	"MAR": {
		{"TS 29.228", "6.3", "Authentication procedure"},
		{"TS 29.229", "6.1.7", "Multimedia-Auth-Request"},
		{"TS 24.229", "5.4.1.2.1", "S-CSCF: challenging an unprotected REGISTER"},
	},
	"MAA": {
		{"TS 29.228", "6.3", "Authentication procedure"},
		{"TS 29.229", "6.1.8", "Multimedia-Auth-Answer"},
	},
	"SAR": {
		{"TS 29.228", "6.1.2", "S-CSCF registration and deregistration notification"},
		{"TS 29.229", "6.1.3", "Server-Assignment-Request"},
	},
	"SAA": {
		{"TS 29.228", "6.1.2", "S-CSCF registration and deregistration notification"},
		{"TS 29.229", "6.1.4", "Server-Assignment-Answer"},
	},
	"LIR": {
		{"TS 29.228", "6.1.4", "User location query"},
		{"TS 29.229", "6.1.5", "Location-Info-Request"},
		{"TS 24.229", "5.3.2.1", "I-CSCF: terminating requests and the LIR"},
	},
	"LIA": {
		{"TS 29.228", "6.1.4", "User location query"},
		{"TS 29.229", "6.1.6", "Location-Info-Answer"},
	},
	"RTR": {
		{"TS 29.228", "6.1.3", "Network initiated de-registration by the HSS"},
		{"TS 29.229", "6.1.9", "Registration-Termination-Request"},
	},
	"RTA": {
		{"TS 29.228", "6.1.3", "Network initiated de-registration by the HSS"},
		{"TS 29.229", "6.1.10", "Registration-Termination-Answer"},
	},
	// Sh follows TS 29.328 V20.0.0 and TS 29.329 V19.1.0, the editions
	// that docs/guide/en/sms.md follows.
	"UDR": {
		{"TS 29.328", "6.1.1", "Data read (Sh-Pull)"},
		{"TS 29.329", "6.1.1", "User-Data-Request"},
	},
	"UDA": {
		{"TS 29.328", "6.1.1", "Data read (Sh-Pull)"},
		{"TS 29.329", "6.1.2", "User-Data-Answer"},
	},
	"PUR": {
		{"TS 29.328", "6.1.2", "Data Update (Sh-Update)"},
		{"TS 29.329", "6.1.3", "Profile-Update-Request"},
	},
	"PUA": {
		{"TS 29.328", "6.1.2", "Data Update (Sh-Update)"},
		{"TS 29.329", "6.1.4", "Profile-Update-Answer"},
	},
	// Rx follows TS 29.214 V20.0.0.
	"AAR": {
		{"TS 29.214", "4.4.1", "Initial provisioning of session information"},
		{"TS 29.214", "4.4.5", "Subscription to notification of signalling path status"},
		{"TS 29.214", "5.6.1", "AA-Request"},
	},
	"AAA": {{"TS 29.214", "5.6.2", "AA-Answer"}},
	"RAR": {
		{"TS 29.214", "4.4.6.2", "Service data flow deactivation and resource allocation failure"},
		{"TS 29.214", "5.6.3", "Re-Auth-Request"},
	},
	"RAA": {{"TS 29.214", "5.6.4", "Re-Auth-Answer"}},
	"STR": {
		{"TS 29.214", "4.4.4", "AF session termination"},
		{"TS 29.214", "5.6.5", "Session-Termination-Request"},
	},
	"STA": {{"TS 29.214", "5.6.6", "Session-Termination-Answer"}},
	"ASR": {
		{"TS 29.214", "4.4.6.1", "IP-CAN session termination"},
		{"TS 29.214", "5.6.7", "Abort-Session-Request"},
	},
	"ASA": {{"TS 29.214", "5.6.8", "Abort-Session-Answer"}},
	"CER": {{"RFC 6733", "5.3.1", "Capabilities-Exchange-Request"}},
	"CEA": {{"RFC 6733", "5.3.2", "Capabilities-Exchange-Answer"}},
	"DWR": {{"RFC 6733", "5.5.1", "Device-Watchdog-Request"}},
	"DWA": {{"RFC 6733", "5.5.2", "Device-Watchdog-Answer"}},
	"DPR": {{"RFC 6733", "5.4.1", "Disconnect-Peer-Request"}},
	"DPA": {{"RFC 6733", "5.4.2", "Disconnect-Peer-Answer"}},
}

type refTable[K comparable] map[K][]Ref

// lookup never returns nil, so that a message without references
// encodes its refs as [].
func (t refTable[K]) lookup(k K) []Ref {
	if refs, ok := t[k]; ok {
		return refs
	}
	return []Ref{}
}

// netannRefTable is for an INVITE from an AS to the MRFC, whose
// Request-URI names a service of RFC 4240 (netann.Service). The MRF
// follows TS 24.229 V20.1.0 and TS 24.147 V19.0.0.
var netannRefTable = refTable[string]{
	netann.ServiceAnnouncement: {
		{"RFC 4240", "3", "Announcement service: sip:annc with play="},
		{"RFC 4240", "3.3", "The parameters play, repeat, delay and duration"},
		{"TS 24.229", "10.3.2.2", "MRFC: announcements with RFC 4240"},
		{"TS 24.229", "5.8.2.1.1.2", "MRFC: an INVITE for a tone or announcement"},
		{"TS 24.229", "5.8.2.2.1", "MRFC: the ACK starts the announcement"},
		{"TS 24.229", "5.8.3.2.1", "MRFC: the BYE once the announcement has played"},
		{"RFC 3261", "13", "Setting up a session with INVITE"},
	},
	netann.ServiceConference: {
		{"RFC 4240", "5", "Conference service: sip:conf=<id>"},
		{"TS 24.229", "10.3.3.2", "MRFC: conferences with RFC 4240"},
		{"TS 24.229", "5.8.2.1.1.3", "MRFC: an INVITE for an ad-hoc conference"},
		{"TS 24.147", "4", "Conferencing: what the AS, the MRFC and the MRFP do"},
		{"RFC 3261", "13", "Setting up a session with INVITE"},
	},
}

func sipRefs(m *sip.Message) []Ref {
	if m.IsRequest() && m.Method == sip.INVITE {
		if u, err := sip.ParseURI(m.RequestURI); err == nil {
			if refs, ok := netannRefTable[netann.Service(u)]; ok {
				return refs
			}
		}
	}
	return sipRefTable.lookup(cseqMethod(m))
}

func (d diameterMessage) refs() []Ref { return diameterRefTable.lookup(d.abbreviation()) }
