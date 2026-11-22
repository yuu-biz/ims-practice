package mediactl

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// A Play goes through playSignal, the text encoding and readPlay, and
// comes back as it was, with the times rounded up to steps of 10 ms.
func TestPlaySignalRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		play   agw.Play
		signal h248.PkgdName
		want   agw.Play
	}{
		{"once", agw.Play{Announcement: "notice"}, signalAnnouncementPlay, agw.Play{Announcement: "notice", Repeat: 1}},
		{"three cycles", agw.Play{Announcement: "notice", Repeat: 3}, signalAnnouncementPlay, agw.Play{Announcement: "notice", Repeat: 3}},
		{"forever, for 10 s", agw.Play{Announcement: "notice", Repeat: agw.RepeatForever, DurationMS: 10000}, signalAnnouncementPlay,
			agw.Play{Announcement: "notice", Repeat: agw.RepeatForever, DurationMS: 10000}},
		{"a duration that is not a step", agw.Play{Announcement: "notice", DurationMS: 1001}, signalAnnouncementPlay,
			agw.Play{Announcement: "notice", Repeat: 1, DurationMS: 1010}},
		{"with silence between plays", agw.Play{Announcement: "welcome", Repeat: 2, DelayMS: 1500}, signalAASPlay,
			agw.Play{Announcement: "welcome", Repeat: 2, DelayMS: 1500}},
		{"with silence, forever, for 2 min", agw.Play{Announcement: "welcome", Repeat: agw.RepeatForever, DelayMS: 5, DurationMS: 120000}, signalAASPlay,
			agw.Play{Announcement: "welcome", Repeat: agw.RepeatForever, DelayMS: 10, DurationMS: 120000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := playSignal(tt.play)
			if err != nil {
				t.Fatal(err)
			}
			text, err := h248.Message{Version: 2, MId: mrfcMId, Transactions: []h248.Transaction{h248.TransactionRequest{
				TransactionID: 1,
				Actions: []h248.ActionRequest{{ContextID: 1, Commands: []h248.CommandRequest{{
					Command: h248.Modify, TerminationID: "ip/1/1", Signals: &h248.SignalsDescriptor{Signals: []h248.Signal{s}},
				}}}},
			}}}.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			m, err := h248.Parse(text)
			if err != nil {
				t.Fatalf("%v:\n%s", err, text)
			}
			got, e := checkMpSignals(m.Transactions[0].(h248.TransactionRequest).Actions[0].Commands[0].Signals)
			if e != nil {
				t.Fatalf("error %d %s:\n%s", e.ErrorCode, e.ErrorText, text)
			}
			if got.name != tt.signal || got.play == nil || *got.play != tt.want || !got.notifyOnTimeOut {
				t.Errorf("%s %+v, want %s %+v:\n%s", got.name, got.play, tt.signal, tt.want, text)
			}
		})
	}
}

func TestPlaySignalRefuses(t *testing.T) {
	for _, p := range []agw.Play{
		{},
		{Announcement: "notice", Repeat: -2},
		{Announcement: "notice", DelayMS: -1000},
		{Announcement: "notice", DurationMS: int(maxSignalDuration/time.Millisecond) + 1},
	} {
		if _, err := playSignal(p); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
}

func TestReadSegmentSpecification(t *testing.T) {
	for spec, want := range map[string]string{
		"sid=<file://notice>":                   "notice",
		"SID=<notice_2>":                        "notice_2",
		"sid=<http://localhost/notice>":         "",
		"sid=<file://audio/notice>":             "",
		"var=<t=dig,v=5>":                       "",
		"sid=<file://notice>,sid=<file://more>": "",
	} {
		got, ok := readSegmentSpecification(spec)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v, want %q", spec, got, ok, want)
		}
	}
}
