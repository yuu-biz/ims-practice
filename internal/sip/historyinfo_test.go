package sip

import (
	"reflect"
	"testing"
)

// The example of RFC 7044 5, with its line breaks removed.
func TestParseHistoryInfo(t *testing.T) {
	m := &Message{Method: INVITE, RequestURI: calleeURI}
	m.Add("History-Info", "<sip:UserA@ims.example.com?Reason=SIP%3Bcause%3D302>;index=1.1,"+
		"<sip:UserB@example.com?Privacy=history&Reason=SIP%3Bcause%3D486>;index=1.2;mp=1.1")
	m.Add("History-Info", "<sip:45432@192.168.0.3>;index=1.3;rc=1.2")

	entries, err := ParseHistoryInfo(m.GetAll("History-Info"))
	if err != nil {
		t.Fatal(err)
	}
	type summary struct {
		target, index string
		reasons       []Reason
		privacy       Privacy
		params        Params
	}
	var got []summary
	for _, e := range entries {
		got = append(got, summary{e.Target(), e.Index, e.Reasons(), e.Privacy(), e.Params})
	}
	want := []summary{
		{"sip:UserA@ims.example.com", "1.1", []Reason{{Protocol: "SIP", Cause: 302, HasCause: true}}, nil, nil},
		{"sip:UserB@example.com", "1.2", []Reason{{Protocol: "SIP", Cause: 486, HasCause: true}}, Privacy{PrivacyHistory}, Params{{"mp", "1.1"}}},
		{"sip:45432@192.168.0.3", "1.3", nil, nil, Params{{"rc", "1.2"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries\n%+v\nwant\n%+v", got, want)
	}
	// What was read goes out again unchanged, which a B2BUA that relays
	// the header field relies on.
	if s := entries[1].String(); s != "<sip:UserB@example.com?Privacy=history&Reason=SIP%3Bcause%3D486>;index=1.2;mp=1.1" {
		t.Errorf("String() = %q", s)
	}

	for _, bad := range []string{"<sip:a@example.com>", "<sip:a@example.com>;index=1..2", "<sip:a@example.com>;index=x", "no-uri;index=1"} {
		if _, err := ParseHistoryInfo([]string{bad}); err == nil {
			t.Errorf("ParseHistoryInfo(%q) succeeded", bad)
		}
	}
}

// A diverting AS adds two entries: the target that did not answer, with
// the reason, and the target the call goes to now, marked mp (TS 24.604
// 4.5.2.6.2).
func TestNewHistoryInfoEntry(t *testing.T) {
	diverting := NewHistoryInfoEntry("sip:+12025550101@"+imsDomain+";user=phone", "1",
		[]Reason{NewReason(ReasonSIP, 408, "")}, Privacy{PrivacyHistory})
	target := NewHistoryInfoEntry("sip:+12025550102@"+imsDomain+";user=phone;cause=408", "1.1", nil, nil)
	target.Params.Set("mp", "1")

	rows := HistoryInfoHeaders([]HistoryInfoEntry{diverting, target})
	want := []HeaderField{
		{Name: "History-Info", Value: "<sip:+12025550101@" + imsDomain + ";user=phone?Privacy=history&Reason=SIP%3Bcause%3D408>;index=1"},
		{Name: "History-Info", Value: "<sip:+12025550102@" + imsDomain + ";user=phone;cause=408>;index=1.1;mp=1"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows\n%+v\nwant\n%+v", rows, want)
	}
	back, err := ParseHistoryInfo([]string{rows[0].Value})
	if err != nil {
		t.Fatal(err)
	}
	if r := back[0].Reasons(); len(r) != 1 || r[0].Cause != 408 || !back[0].Privacy().Has(PrivacyHistory) {
		t.Errorf("read back reasons %+v and privacy %q", r, []string(back[0].Privacy()))
	}

	// RFC 7044 5: a tel URI has no headers part to carry them in.
	tel := NewHistoryInfoEntry("tel:+12025550103", "1", []Reason{NewReason(ReasonSIP, 302, "")}, nil)
	if tel.URI != "tel:+12025550103" {
		t.Errorf("tel entry has URI %q, want it without a Reason", tel.URI)
	}
}

func TestURIHeaderValueEscapes(t *testing.T) {
	uri := withURIHeader("sip:bob@example.com", "Reason", `SIP;cause=486;text="Busy Here"`)
	if want := "sip:bob@example.com?Reason=SIP%3Bcause%3D486%3Btext%3D%22Busy%20Here%22"; uri != want {
		t.Errorf("escaped URI = %q, want %q", uri, want)
	}
	if got := uriHeaders(uri, "reason"); !reflect.DeepEqual(got, []string{`SIP;cause=486;text="Busy Here"`}) {
		t.Errorf("unescaped = %q", got)
	}
}
