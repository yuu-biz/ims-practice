package sip

import (
	"reflect"
	"testing"
)

func TestPEarlyMedia(t *testing.T) {
	invite := &Message{Method: INVITE, RequestURI: calleeURI}
	invite.Add(PEarlyMedia{Supported: true}.Header().Name, PEarlyMedia{Supported: true}.Header().Value)
	if got := invite.Get("P-Early-Media"); got != "supported" {
		t.Errorf("P-Early-Media of the INVITE = %q, want supported", got)
	}
	if p := ParsePEarlyMedia(invite.GetAll("P-Early-Media")); !p.Supported || len(p.Directions) != 0 {
		t.Errorf("parsed %+v, want supported without a direction", p)
	}

	ringing := &Message{StatusCode: 180}
	ringing.Add("P-Early-Media", "sendonly, Recvonly , future-param, gated")
	p := ParsePEarlyMedia(ringing.GetAll("P-Early-Media"))
	want := PEarlyMedia{Directions: []string{EarlyMediaSendonly, EarlyMediaRecvonly}, Gated: true}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("parsed %+v, want %+v", p, want)
	}
	if got := p.String(); got != "sendonly, recvonly, gated" {
		t.Errorf("String() = %q, want gated after the directions (RFC 5009 8)", got)
	}
	// RFC 5009 8: the last direction covers the media lines that have no
	// parameter of their own.
	for index, dir := range []string{EarlyMediaSendonly, EarlyMediaRecvonly, EarlyMediaRecvonly} {
		if got, ok := p.Direction(index); !ok || got != dir {
			t.Errorf("Direction(%d) = %q, %v; want %q", index, got, ok, dir)
		}
	}
	if _, ok := (PEarlyMedia{Gated: true}).Direction(0); ok {
		t.Error("a value without a direction parameter gave a direction")
	}
}
