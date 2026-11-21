package sip

import (
	"reflect"
	"testing"
)

func TestReplaces(t *testing.T) {
	tests := []struct {
		in   string
		want Replaces
		out  string
	}{
		// The examples of RFC 3891 6.1.
		{"98732@sip.example.com ;from-tag=r33th4x0r ;to-tag=ff87ff",
			Replaces{CallID: "98732@sip.example.com", ToTag: "ff87ff", FromTag: "r33th4x0r"},
			"98732@sip.example.com;to-tag=ff87ff;from-tag=r33th4x0r"},
		{"12adf2f34456gs5;to-tag=12345;from-tag=54321;early-only",
			Replaces{CallID: "12adf2f34456gs5", ToTag: "12345", FromTag: "54321", EarlyOnly: true},
			"12adf2f34456gs5;to-tag=12345;from-tag=54321;early-only"},
	}
	for _, tt := range tests {
		got, err := ParseReplaces(tt.in)
		if err != nil {
			t.Errorf("ParseReplaces(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseReplaces(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if s := got.String(); s != tt.out {
			t.Errorf("String() = %q, want %q", s, tt.out)
		}
	}
	for _, in := range []string{"", ";to-tag=a;from-tag=b", "call-id;to-tag=a", "call-id;from-tag=b"} {
		if _, err := ParseReplaces(in); err == nil {
			t.Errorf("ParseReplaces(%q) succeeded", in)
		}
	}
	// The receiver of the INVITE looks its dialog up by this ID.
	want := DialogID{CallID: "12adf2f34456gs5", LocalTag: "12345", RemoteTag: "54321"}
	if got := tests[1].want.DialogID(); got != want {
		t.Errorf("DialogID() = %+v, want %+v", got, want)
	}
}

func TestReferTo(t *testing.T) {
	plain, err := ParseReferTo("sip:carol@example.com")
	if err != nil || plain.URI != "sip:carol@example.com" || plain.Replaces != nil {
		t.Errorf("plain Refer-To = %+v, %v", plain, err)
	}

	// The transferor asks the transferee to call the target in the place
	// of the transferor's own dialog with the target.
	replaces := Replaces{CallID: "12345@192.0.2.10", ToTag: "callee-tag", FromTag: "caller-tag"}
	out := ReferTo{URI: "sip:conf@example.com", Replaces: &replaces}
	const want = "<sip:conf@example.com?Replaces=12345%40192.0.2.10%3Bto-tag%3Dcallee-tag%3Bfrom-tag%3Dcaller-tag>"
	if got := out.Header(); got != (HeaderField{Name: "Refer-To", Value: want}) {
		t.Fatalf("Header() = %+v, want %q", got, want)
	}
	back, err := ParseReferTo(want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, out) {
		t.Errorf("read back %+v with Replaces %+v, want %+v", back, back.Replaces, out)
	}

	if _, err := ParseReferTo("<sip:conf@example.com?Replaces=broken>"); err == nil {
		t.Error("a Refer-To with a malformed Replaces parsed")
	}
}

// The compact forms of RFC 3515 2.1 and RFC 3892 3, and a Referred-By
// read as the name-addr it is.
func TestReferHeadersInCompactForm(t *testing.T) {
	m, err := Parse([]byte("REFER sip:b@example.com SIP/2.0\r\n" +
		"r: <sip:carol@example.com>\r\n" +
		"b: \"Alice\" <sip:alice@example.com>;cid=\"2UWQFN309shb3@example.com\"\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Get("Refer-To"); got != "<sip:carol@example.com>" {
		t.Errorf("Refer-To = %q", got)
	}
	by, err := ParseNameAddr(m.Get("Referred-By"))
	if err != nil {
		t.Fatal(err)
	}
	if cid, _ := by.Params.Get("cid"); by.URI != "sip:alice@example.com" || by.DisplayName != "Alice" || cid != `"2UWQFN309shb3@example.com"` {
		t.Errorf("Referred-By = %+v", by)
	}
}
