package mediactl

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// MpProfile is the profile of Mp that Gateway names when it runs a
// Processor, and that a Controller set up with it accepts. It is not
// MRF/8 (TS 29.333 5.1, Table 5.1.1), for the same reason that Profile
// is not threegIq/7: this gateway realizes few of the packages of the
// MRF profile. The procedures are those of Mp for the packages in
// MpPackages.
var MpProfile = h248.ServiceChangeProfile{ProfileName: "imsPracticeMp", Version: 1}

// MpPackages lists the packages that a Gateway with a Processor
// realizes. The versions are those of the definitions read:
//
//   - g, root, nt and hangterm: the mandatory packages of Mp (TS 29.333
//     5.14.1, Table 5.14.1). g brings the event g/sc, which reports the
//     end of an announcement (5.17.2.11).
//   - it (H.248.14 (03/2009) 6), which Mp makes mandatory on UDP (TS
//     29.333 5.14.2, Table 5.14.2).
//   - rtp (H.248.1 (05/2002) E.12): the packet counts on Subtract.
//   - an (H.248.7 (03/2004) 4): the signal an/apf of Start Announcement
//     (TS 29.333 5.17.2.9), which an MRFP that plays announcements must
//     realize (Table 5.14.2). TS 29.333 lists version 2; the edition of
//     H.248.7 read defines version 1, which is the one written here.
//   - aasb and bannsyx (H.248.9 (12/2009) 8 and 6.2): the signal
//     aasb/play, the one signal of the profile with an interval of
//     silence between two plays. aasb/play needs bannsyx, the syntax of
//     its announcement parameter (H.248.9 (12/2009) 8).
var MpPackages = h248.PackagesDescriptor{
	{PackageName: "g", PackageVersion: 1},
	{PackageName: "root", PackageVersion: 2},
	{PackageName: "nt", PackageVersion: 1},
	{PackageName: "hangterm", PackageVersion: 1},
	{PackageName: "it", PackageVersion: 1},
	{PackageName: "rtp", PackageVersion: 1},
	{PackageName: "an", PackageVersion: 1},
	{PackageName: "aasb", PackageVersion: 3},
	{PackageName: "bannsyx", PackageVersion: 1},
}

// mpProfile is the profile of Mp.
var mpProfile = profile{name: MpProfile, packages: MpPackages}

// The package items of Mp, beyond those of Iq.
const (
	signalAnnouncementPlay h248.PkgdName = "an/apf"
	signalAASPlay          h248.PkgdName = "aasb/play"
	eventSignalCompletion  h248.PkgdName = "g/sc"
)

// Parameters of those items.
const (
	// an/apf (H.248.7 (03/2004) 4.3.1): the name of the announcement,
	// the number of cycles (0 plays on until stopped), and the
	// direction.
	paramAnnouncementName = "an"
	paramNumberOfCycles   = "noc"
	paramDirection        = "di"
	directionExternal     = "ext"
	// aasb/play (H.248.9 (12/2009) 8.3.1.1): the announcement in the
	// syntax of bannsyx, the iterations (0 plays on until stopped), and
	// the interval of silence between plays in steps of 10 ms. The
	// announcement shares the ID an with an/apf, and the direction di.
	paramIterations = "it"
	paramInterval   = "iv"
	// g/sc (H.248.1 (05/2002) E.1.2): the signal that ended, and how.
	paramSignalIdentity    = "SigID"
	paramTerminationMethod = "Meth"
)

// mpTerminationID is the ID that a Gateway gives a termination of an
// Mp context: ip/<context>/<n>, with n counting up within the context.
// TS 29.333 5.6.1.1 leaves the structure to provisioning and calls the
// ID of an IP termination a number; the text encoding needs a name that
// starts with a letter (H.248.1 (05/2002) B.2, pathNAME), and the
// context in the ID lets a learner tell from a capture where each
// termination is.
func mpTerminationID(contextID h248.ContextID, n int) h248.TerminationID {
	return h248.TerminationID(fmt.Sprintf("ip/%d/%d", contextID, n))
}

// streamModeOf is the StreamMode of the LocalControl descriptor for m
// (TS 29.333 5.7.1.1, Table 5.7.1.1.2; H.248.1 (05/2002) 7.1.7). The
// modes of agw.Mode are seen from outside the context, as those of
// H.248 are.
func streamModeOf(m agw.Mode) (h248.StreamMode, bool) {
	switch m {
	case agw.SendReceive:
		return h248.SendRecv, true
	case agw.SendOnly:
		return h248.SendOnly, true
	case agw.ReceiveOnly:
		return h248.RecvOnly, true
	case agw.Inactive:
		return h248.Inactive, true
	}
	return "", false
}

// modeOf is the agw.Mode for a StreamMode of a LocalControl descriptor.
func modeOf(m h248.StreamMode) (agw.Mode, bool) {
	switch m {
	case h248.SendRecv:
		return agw.SendReceive, true
	case h248.SendOnly:
		return agw.SendOnly, true
	case h248.RecvOnly:
		return agw.ReceiveOnly, true
	case h248.Inactive:
		return agw.Inactive, true
	}
	return "", false
}

// maxSignalDuration is the longest Duration a signal can carry: a
// 16-bit count of hundredths of a second (H.248.1 (05/2002) 7.1.11,
// Annex B.2).
const maxSignalDuration = 65535 * 10 * time.Millisecond

// tenMilliseconds rounds d up to steps of 10 ms, the unit of the
// signal duration (H.248.1 (05/2002) 7.1.11) and of the interval of
// aasb/play (H.248.9 (12/2009) 8.3.1.1.3).
func tenMilliseconds(d time.Duration) uint64 {
	return uint64((d + 10*time.Millisecond - 1) / (10 * time.Millisecond))
}

// cycles is the number of plays as noc and it write it: 0 plays until
// stopped (H.248.7 (03/2004) 4.3.1, H.248.9 (12/2009) 8.3.1.1.2).
func cycles(repeat int) string {
	switch {
	case repeat == agw.RepeatForever:
		return "0"
	case repeat <= 1:
		return "1"
	}
	return strconv.Itoa(repeat)
}

// playSignal is the signal that plays p, with the completion that Start
// Announcement asks for when the MRFC is to be told of the end (TS
// 29.333 5.17.2.9, Request End Of Signal Notification).
//
// It is an/apf, the signal of Start Announcement, when the plays follow
// each other without silence. an/apf has no parameter for the silence
// between plays (H.248.7 (03/2004) 4.3.1), so a Play with a Delay uses
// aasb/play, whose interval iv is that silence (H.248.9 (12/2009)
// 8.3.1.1.3). The profile has both packages (TS 29.333 5.14.3.14,
// 5.14.3.18).
//
// The duration is that of the signal (H.248.1 (05/2002) 7.1.11). an/apf
// is of type TimeOut, and plays as many cycles as noc gives within the
// duration; a duration of 0 lifts the limit, where a missing one would
// leave it to provisioning (H.248.7 (03/2004) 4.5, Table 1). aasb/play
// is of type Brief and ignores a duration unless its type is set to
// TimeOut (H.248.9 (12/2009) 8.5), so the type is written only with a
// limit.
func playSignal(p agw.Play) (h248.Signal, error) {
	delay, length := time.Duration(p.DelayMS)*time.Millisecond, time.Duration(p.DurationMS)*time.Millisecond
	if p.Announcement == "" {
		return h248.Signal{}, fmt.Errorf("mediactl: a Play needs the name of an announcement")
	}
	if p.Repeat < agw.RepeatForever || delay < 0 || length < 0 {
		return h248.Signal{}, fmt.Errorf("mediactl: repeat %d, delay %v, duration %v", p.Repeat, delay, length)
	}
	if length > maxSignalDuration {
		return h248.Signal{}, fmt.Errorf("mediactl: a signal lasts %v at most, not %v", maxSignalDuration, length)
	}
	duration := uint16(tenMilliseconds(length))
	completion := []h248.NotificationReason{h248.OnTimeOut}
	if delay == 0 {
		return h248.Signal{
			SignalName:       signalAnnouncementPlay,
			Duration:         &duration,
			NotifyCompletion: completion,
			SigParList: []h248.SigParameter{
				{Name: paramAnnouncementName, Value: h248.Value{Text: p.Announcement}},
				{Name: paramNumberOfCycles, Value: h248.Value{Text: cycles(p.Repeat)}},
			},
		}, nil
	}
	s := h248.Signal{
		SignalName:       signalAASPlay,
		NotifyCompletion: completion,
		SigParList: []h248.SigParameter{
			{Name: paramAnnouncementName, Value: h248.Value{Text: segmentSpecification(p.Announcement), Quoted: true}},
			{Name: paramIterations, Value: h248.Value{Text: cycles(p.Repeat)}},
			{Name: paramInterval, Value: h248.Value{Text: strconv.FormatUint(tenMilliseconds(delay), 10)}},
		},
	}
	if length > 0 {
		s.SigType, s.Duration = h248.TimeOut, &duration
	}
	return s, nil
}

// segmentSpecification is the announcement parameter of aasb/play for
// one provisioned segment local to the MRFP, in the basic announcement
// syntax: sid=<file://name> (H.248.9 (12/2009) 6.1.4, 6.2.5.1.2,
// 6.2.5.2.1).
func segmentSpecification(name string) string {
	return "sid=<file://" + name + ">"
}

// readSegmentSpecification reads the name back from the forms of one
// provisioned segment that segmentSpecification and the simple names of
// H.248.9 (12/2009) 6.2.5.2.1 give.
//
// SIMPLIFIED: H.248.9 (12/2009) 6.2.5.1.2. An announcement may be a
// list of segments, variables, and segments on other hosts; the MRFP of
// this project plays one provisioned announcement at a time, so a
// specification of anything else is refused.
func readSegmentSpecification(spec string) (string, bool) {
	keyword, rest, ok := strings.Cut(spec, "=")
	if !ok || !strings.EqualFold(keyword, "sid") || !strings.HasPrefix(rest, "<") || !strings.HasSuffix(rest, ">") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(rest, "<"), ">")
	name = strings.TrimPrefix(name, "file://")
	if name == "" || strings.IndexFunc(name, func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '_')
	}) >= 0 {
		return "", false
	}
	return name, true
}

// eventsDescriptor is the Events descriptor that Controller writes on a
// termination of Mp: the termination heartbeat, which TS 29.333 asks
// for on every new termination (5.17.2.2, 5.17.2.9 NOTE 4; 5.17.2.32),
// and the events of e. The heartbeat is KeepActive (TS 29.333 5.7.2,
// Table 5.7.2.3): without it, its detection would stop the announcement
// that plays (H.248.1 (05/2002) 7.1.9).
func (c *Controller) mpEventsDescriptor(e *agw.Events) *h248.EventsDescriptor {
	d := &h248.EventsDescriptor{RequestID: requestIDTermination, EventList: []h248.RequestedEvent{{
		PkgdName:   eventHeartbeat,
		KeepActive: true,
		EvParList: []h248.EventParameter{{
			Name:  paramTimerX,
			Value: h248.Value{Text: strconv.FormatInt(int64(c.timerX/time.Second), 10)},
		}},
	}}}
	if e == nil {
		return d
	}
	if e.RequestID != 0 {
		d.RequestID = e.RequestID
	}
	if slices.Contains(e.Detect, agw.SignalCompleted) {
		d.EventList = append(d.EventList, h248.RequestedEvent{PkgdName: eventSignalCompletion})
	}
	return d
}
