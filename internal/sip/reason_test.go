package sip

import (
	"reflect"
	"testing"
)

func TestParseReason(t *testing.T) {
	tests := []struct {
		in   string
		want Reason
	}{
		// The examples of RFC 3326 2.
		{`SIP ;cause=200 ;text="Call completed elsewhere"`, Reason{Protocol: "SIP", Cause: 200, HasCause: true, Text: "Call completed elsewhere"}},
		{`Q.850 ;cause=16 ;text="Terminated"`, Reason{Protocol: "Q.850", Cause: 16, HasCause: true, Text: "Terminated"}},
		{`SIP;cause=408`, Reason{Protocol: "SIP", Cause: 408, HasCause: true}},
		{`RELEASE_CAUSE;cause=1;text="User ends call"`, Reason{Protocol: "RELEASE_CAUSE", Cause: 1, HasCause: true, Text: "User ends call"}},
		{`Q.850;cause=0`, Reason{Protocol: "Q.850", Cause: 0, HasCause: true}},
		{`SIP;text="a \"quoted\", word";x=y`, Reason{Protocol: "SIP", Text: `a "quoted", word`, Params: Params{{"x", "y"}}}},
	}
	for _, tt := range tests {
		got, err := ParseReason(tt.in)
		if err != nil {
			t.Errorf("ParseReason(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseReason(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		again, err := ParseReason(got.String())
		if err != nil || !reflect.DeepEqual(again, tt.want) {
			t.Errorf("ParseReason(%q) = %+v, %v; want the value that String wrote", got.String(), again, err)
		}
	}
	for _, in := range []string{"", ";cause=1", "SIP;cause=busy", "SIP;cause=-1"} {
		if _, err := ParseReason(in); err == nil {
			t.Errorf("ParseReason(%q) succeeded", in)
		}
	}
}

func TestReasonString(t *testing.T) {
	if got := NewReason(ReasonSIP, 486, "Busy Here").String(); got != `SIP;cause=486;text="Busy Here"` {
		t.Errorf("String() = %q", got)
	}
	if got := NewReason(ReasonQ850, 16, "").Header(); got != (HeaderField{Name: "Reason", Value: "Q.850;cause=16"}) {
		t.Errorf("Header() = %+v", got)
	}
}

// RFC 3326 2: one message may carry a value per protocol, in one row or
// in several, and a comma inside the text does not split a value.
func TestReasonsOfAMessage(t *testing.T) {
	m := &Message{Method: BYE, RequestURI: calleeURI}
	m.Add("Reason", `SIP;cause=600;text="Busy, everywhere", Q.850;cause=17`)
	m.Add("Reason", `RELEASE_CAUSE;cause=3`)
	m.Add("Reason", `;broken`)
	want := []Reason{
		{Protocol: "SIP", Cause: 600, HasCause: true, Text: "Busy, everywhere"},
		{Protocol: "Q.850", Cause: 17, HasCause: true},
		{Protocol: "RELEASE_CAUSE", Cause: 3, HasCause: true},
	}
	if got := Reasons(m); !reflect.DeepEqual(got, want) {
		t.Errorf("Reasons = %+v, want %+v", got, want)
	}
}
