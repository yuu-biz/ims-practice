package mediactl

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// Profile is the profile that Gateway names when it registers and that
// Controller accepts (TS 29.334 5.1 gives the form). It is not
// threegIq/7: that name would claim every package that Iq makes
// mandatory (TS 29.334 5.14.1), such as gm, tman, ds and rtcph, which
// this gateway does not realize. The procedures are those of Iq for the
// packages in Packages. In particular, Subtract reports no statistics
// (TS 29.334 5.7.7, Table 5.7.7.2).
//
// H.248.1 (05/2002) 14.4 sets out how a profile name is registered with
// IANA. This one is not: it is for this learning network only.
var Profile = h248.ServiceChangeProfile{ProfileName: "imsPracticeIq", Version: 1}

// ProtocolVersion is the version of H.248.1 that both sides use: the
// lowest that the profiles allow (TS 29.334 5.3, Table 5.8.8.6).
const ProtocolVersion = 2

// Packages lists the packages that a Gateway realizes, as an
// AuditValue of ROOT with the Packages token reports them (TS 29.334
// 5.17.3.10). The versions are those of the definitions read:
//
//   - g (H.248.1 (05/2002) E.1): the event g/cause, bearer released.
//   - root (E.2): the ROOT termination itself.
//   - it (H.248.14 (03/2009) 6): the event it/ito, inactivity of the
//     controller, which a gateway on UDP must support (TS 29.334
//     5.17.3.1, Table 5.17.3.1.1, NOTE 4).
//   - hangterm (H.248.36 (03/2013) 5): the event hangterm/thb,
//     termination heartbeat.
//   - ipnapt (H.248.37 (06/2008) 6): the signal ipnapt/latch.
var Packages = h248.PackagesDescriptor{
	{PackageName: "g", PackageVersion: 1},
	{PackageName: "root", PackageVersion: 2},
	{PackageName: "it", PackageVersion: 1},
	{PackageName: "hangterm", PackageVersion: 1},
	{PackageName: "ipnapt", PackageVersion: 1},
}

// profile is what tells apart the profiles that this package speaks:
// the name that a Gateway registers with and a Controller accepts, and
// the packages that the Gateway realizes.
type profile struct {
	name     h248.ServiceChangeProfile
	packages h248.PackagesDescriptor
}

// iqProfile is the profile of Iq.
var iqProfile = profile{name: Profile, packages: Packages}

// mp tells whether p is the profile of Mp.
func (p profile) mp() bool { return p.name == MpProfile }

// The package items that the two sides use.
const (
	eventCause             h248.PkgdName = "g/cause"
	eventInactivityTimeout h248.PkgdName = "it/ito"
	eventHeartbeat         h248.PkgdName = "hangterm/thb"
	signalLatch            h248.PkgdName = "ipnapt/latch"

	statPacketsSent     h248.PkgdName = "rtp/ps"
	statPacketsReceived h248.PkgdName = "rtp/pr"
)

// Parameters of those items.
const (
	// paramMaximumInactivityTime (mit) is in steps of 10 ms (H.248.14
	// (03/2009) 6.2.1.1.1).
	paramMaximumInactivityTime = "mit"
	// paramTimerX (timerx) is in seconds (H.248.36 (03/2013)
	// 5.2.1.1.1).
	paramTimerX = "timerx"
	// paramNAPT (napt) is OFF, LATCH or RELATCH (H.248.37 (06/2008)
	// 6.3.1.1.1).
	paramNAPT = "napt"
	naptLatch = "LATCH"
)

// The RequestIDs of the Events descriptors that a Controller sets. A
// Notify carries the RequestID of the descriptor that asked for its
// event, so the controller can tell the reports apart (H.248.1
// (05/2002) 7.1.9, 7.1.17).
const (
	requestIDTermination uint32 = 1
	requestIDRoot        uint32 = 2
)

// terminationGroup is the <group> field of the termination IDs that a
// Controller asks for (TS 29.334 5.6.1.1.1). The media relay has one
// group of interfaces.
const terminationGroup = 1

// terminationID is ip/<group>/<interface>/<id> (TS 29.334 5.6.1.1.1).
// A Gateway gives both terminations of a context the context ID as
// their <id>: the field must be unique for each interface only, and a
// learner can then tell from a termination ID which context it is in.
func terminationID(group uint16, side agw.TerminationID, id h248.ContextID) h248.TerminationID {
	return h248.TerminationID(fmt.Sprintf("ip/%d/%s/%d", group, side, id))
}

// chooseTerminationID asks the gateway for a termination on side. TS
// 29.334 5.6.1.1.1 (NOTE 4) has the controller always use CHOOSE in an
// Add.
func chooseTerminationID(side agw.TerminationID) h248.TerminationID {
	return h248.TerminationID(fmt.Sprintf("ip/%d/%s/$", terminationGroup, side))
}

// ipTermination is a termination ID taken apart. A field that is a
// wildcard is "$" or "*" in its text and zero in its number.
type ipTermination struct {
	group     uint16
	groupText string
	side      agw.TerminationID
	id        uint32
	idText    string
}

// parseTerminationID reads ip/<group>/<interface>/<id>. Case does not
// matter in termination IDs (H.248.1 Annex B.2).
func parseTerminationID(t h248.TerminationID) (ipTermination, bool) {
	fields := strings.Split(strings.ToLower(string(t)), "/")
	if len(fields) != 4 || fields[0] != "ip" {
		return ipTermination{}, false
	}
	p := ipTermination{groupText: fields[1], side: agw.TerminationID(fields[2]), idText: fields[3]}
	if !isWildcard(p.groupText) {
		g, err := strconv.ParseUint(p.groupText, 10, 16)
		if err != nil {
			return ipTermination{}, false
		}
		p.group = uint16(g)
	}
	if !isWildcard(p.idText) {
		id, err := strconv.ParseUint(p.idText, 10, 32)
		if err != nil || id == 0 {
			return ipTermination{}, false
		}
		p.id = uint32(id)
	}
	return p, p.side != ""
}

func isWildcard(field string) bool { return field == "$" || field == "*" }

// errorDescriptor is the Error descriptor of a command, an action or a
// transaction. The texts are the names of the codes in H.248.8
// (03/2013) clause 4.2, followed by what went wrong where that helps a
// reader of the capture.
func errorDescriptor(code int, detail string) *h248.ErrorDescriptor {
	text := errorNames[code]
	if detail != "" {
		text += ": " + detail
	}
	return &h248.ErrorDescriptor{ErrorCode: code, ErrorText: text}
}

// errorNames holds the codes that the two sides send, from the lists
// of TS 29.334 5.7.10, with the clause of H.248.8 (03/2013) that
// defines each. 507 is not in the lists of Iq; see Controller.notify.
var errorNames = map[int]string{
	411: "The transaction refers to an unknown ContextID",                              // 4.2.7
	430: "Unknown TerminationID",                                                       // 4.2.12
	431: "No TerminationID matched a wildcard",                                         // 4.2.13
	434: "Max number of Terminations in a Context exceeded",                            // 4.2.16
	435: "Termination ID is not in specified Context",                                  // 4.2.17
	440: "Unsupported or unknown Package",                                              // 4.2.18
	441: "Missing Remote or Local Descriptor",                                          // 4.2.19
	444: "Unsupported or Unknown Descriptor",                                           // 4.2.22
	446: "Unsupported or Unknown Parameter",                                            // 4.2.24
	449: "Unsupported or Unknown Parameter or Property Value",                          // 4.2.27
	450: "No such property in this package",                                            // 4.2.28
	451: "No such event in this package",                                               // 4.2.29
	452: "No such signal in this package",                                              // 4.2.30
	457: "Missing parameter in signal or event",                                        // 4.2.35
	458: "Unexpected Event/RequestID",                                                  // 4.2.36
	500: "Internal software Failure in MG or MGC",                                      // 4.2.41
	501: "Not Implemented",                                                             // 4.2.42
	505: "Transaction Request Received before a ServiceChange Reply has been received", // 4.2.46
	506: "Number of TransactionPendings Exceeded",                                      // 4.2.47
	406: "Version Not Supported",                                                       // 4.2.5
	507: "Unknown Control Association",                                                 // 4.2.48
	510: "Insufficient resources",                                                      // 4.2.49
	515: "Unsupported Media Type",                                                      // 4.2.54
	517: "Unsupported or invalid mode",                                                 // 4.2.55
	522: "Functionality Requested in Topology Triple Not Supported",                    // 4.2.60
	533: "Response exceeds maximum transport PDU size",                                 // 4.2.66
}

// timeStamp is the TimeStamp of a ServiceChange and its reply, which
// H.248.1 (05/2002) 7.2.8 asks for with a registration. It is the time
// of the sender, to hundredths of a second.
func timeStamp(t time.Time) h248.TimeNotation {
	t = t.UTC()
	return h248.TimeNotation{
		Date: t.Format("20060102"),
		Time: fmt.Sprintf("%s%02d", t.Format("150405"), t.Nanosecond()/int(10*time.Millisecond)),
	}
}
