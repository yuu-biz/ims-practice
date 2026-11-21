package rtp

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p := Packet{Marker: true, PayloadType: 0, SequenceNumber: 65535, Timestamp: 160, SSRC: 0xdeadbeef, Payload: []byte{1, 2, 3}}
	b := p.Marshal()
	if b[0] != 0x80 || b[1] != 0x80 {
		t.Errorf("first bytes %#x %#x, want V=2 and M set", b[0], b[1])
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.SequenceNumber != p.SequenceNumber || got.Timestamp != p.Timestamp || got.SSRC != p.SSRC ||
		!got.Marker || !bytes.Equal(got.Payload, p.Payload) {
		t.Errorf("got %+v, want %+v", got, p)
	}
}

func TestUnmarshalSkipsCSRCsExtensionAndPadding(t *testing.T) {
	b := []byte{
		0xb1, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, // V=2, P=1, X=1, CC=1
		0, 0, 0, 9, // one CSRC
		0xbe, 0xde, 0, 1, 1, 2, 3, 4, // an extension of one word
		0xaa, 0xbb, // the payload
		0, 0, 3, // three octets of padding
	}
	p, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Payload, []byte{0xaa, 0xbb}) {
		t.Errorf("payload = %x, want aabb", p.Payload)
	}
}

func TestUnmarshalRejectsShortAndWrongVersion(t *testing.T) {
	if _, err := Unmarshal(make([]byte, 11)); err == nil {
		t.Error("accepted 11 bytes")
	}
	if _, err := Unmarshal(make([]byte, 12)); err == nil {
		t.Error("accepted version 0")
	}
}
