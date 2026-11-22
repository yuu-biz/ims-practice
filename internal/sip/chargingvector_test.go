package sip

import (
	"reflect"
	"testing"
)

func TestChargingVector(t *testing.T) {
	tests := []struct {
		in   string
		want ChargingVector
		out  string
	}{
		// The value of the example of RFC 7315 4.6.2.3.
		{"icid-value=1234bc9876e;\r\n icid-generated-at=192.0.6.8;\r\n orig-ioi=home1.net",
			ChargingVector{ICID: "1234bc9876e", OrigIOI: "home1.net", Params: Params{{"icid-generated-at", "192.0.6.8"}}},
			"icid-value=1234bc9876e;orig-ioi=home1.net;icid-generated-at=192.0.6.8"},
		{"term-ioi=home2.net; ICID-Value=a1;orig-ioi=home1.net",
			ChargingVector{ICID: "a1", OrigIOI: "home1.net", TermIOI: "home2.net"},
			"icid-value=a1;orig-ioi=home1.net;term-ioi=home2.net"},
		{"icid-value=a1;transit-ioi=\"x.1,void\"",
			ChargingVector{ICID: "a1", Params: Params{{"transit-ioi", "\"x.1,void\""}}},
			"icid-value=a1;transit-ioi=\"x.1,void\""},
	}
	for _, tt := range tests {
		got := ParseChargingVector(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseChargingVector(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if s := got.String(); s != tt.out {
			t.Errorf("String() = %q, want %q", s, tt.out)
		}
	}
}

func TestChargingVectorOf(t *testing.T) {
	m := &Message{}
	if _, ok := ChargingVectorOf(m); ok {
		t.Error("a message without P-Charging-Vector has one")
	}
	m.Add("P-Charging-Vector", "orig-ioi=home1.net")
	if _, ok := ChargingVectorOf(m); ok {
		t.Error("a P-Charging-Vector without icid-value counts")
	}
	m.Set("P-Charging-Vector", "icid-value=a1;orig-ioi=home1.net")
	if c, ok := ChargingVectorOf(m); !ok || c.ICID != "a1" {
		t.Errorf("ChargingVectorOf = %+v, %v", c, ok)
	}
}

// TestChargingVectorOfAResponse: the response keeps the icid-value and
// the orig-ioi of the request, and names the network that answers in
// the term-ioi; nothing else of the request goes with it.
func TestChargingVectorOfAResponse(t *testing.T) {
	req := ChargingVector{ICID: "a1", OrigIOI: "home1.net", TermIOI: "stale.net", Params: Params{{"transit-ioi", "\"x.1\""}}}
	want := ChargingVector{ICID: "a1", OrigIOI: "home1.net", TermIOI: "home2.net"}
	if got := req.Response("home2.net"); !reflect.DeepEqual(got, want) {
		t.Errorf("Response = %+v, want %+v", got, want)
	}
	if got := (ChargingVector{ICID: "a1"}).Response("home2.net").String(); got != "icid-value=a1;term-ioi=home2.net" {
		t.Errorf("without an orig-ioi: %q", got)
	}
}
