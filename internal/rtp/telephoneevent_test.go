package rtp

import (
	"bytes"
	"testing"
	"time"
)

// TestTelephoneEventLayout is Figure 1 of RFC 4733 2.3: digit 5, the E
// bit, volume 10 and a duration of 1600 units.
func TestTelephoneEventLayout(t *testing.T) {
	e := TelephoneEvent{Event: 5, End: true, Volume: 10, Duration: 1600}
	if got, want := e.Marshal(), []byte{0x05, 0x8a, 0x06, 0x40}; !bytes.Equal(got, want) {
		t.Errorf("Marshal = %x, want %x", got, want)
	}
	// The R bit is ignored on receipt (RFC 4733 2.3.3).
	back, err := UnmarshalTelephoneEvent([]byte{0x05, 0xca, 0x06, 0x40})
	if err != nil || back != e {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
	if _, err := UnmarshalTelephoneEvent([]byte{1, 2, 3}); err == nil {
		t.Error("a 3-octet payload was accepted")
	}
}

func TestDTMFCodes(t *testing.T) {
	// RFC 4733 3.2 Table 3.
	for digit, want := range map[rune]uint8{'0': 0, '9': 9, '*': 10, '#': 11, 'A': 12, 'd': 15} {
		got, ok := DTMFEvent(digit)
		if !ok || got != want {
			t.Errorf("DTMFEvent(%q) = %d, %v; want %d", digit, got, ok, want)
		}
	}
	for _, bad := range []rune{'E', 'e', 'x', ' '} {
		if _, ok := DTMFEvent(bad); ok {
			t.Errorf("DTMFEvent(%q) accepted", bad)
		}
	}
	if d, ok := DTMFDigit(11); !ok || d != '#' {
		t.Errorf("DTMFDigit(11) = %q, %v", d, ok)
	}
	if _, ok := DTMFDigit(16); ok {
		t.Error("DTMFDigit(16) is not DTMF")
	}
}

// TestDTMFTiming sends '#' for 100 ms at 16 kHz: four reports of growing
// duration every 20 ms, the first with the marker, then the final one
// three times (RFC 4733 2.5.1.2, 2.5.1.4).
func TestDTMFTiming(t *testing.T) {
	reports, err := DTMF('#', 16000, DTMFDuration)
	if err != nil {
		t.Fatal(err)
	}
	want := []DTMFReport{
		{Marker: true, Payload: TelephoneEvent{Event: 11, Volume: DTMFVolume, Duration: 320}},
		{Payload: TelephoneEvent{Event: 11, Volume: DTMFVolume, Duration: 640}},
		{Payload: TelephoneEvent{Event: 11, Volume: DTMFVolume, Duration: 960}},
		{Payload: TelephoneEvent{Event: 11, Volume: DTMFVolume, Duration: 1280}},
		{Payload: TelephoneEvent{Event: 11, End: true, Volume: DTMFVolume, Duration: 1600}},
		{Payload: TelephoneEvent{Event: 11, End: true, Volume: DTMFVolume, Duration: 1600}},
		{Payload: TelephoneEvent{Event: 11, End: true, Volume: DTMFVolume, Duration: 1600}},
	}
	if len(reports) != len(want) {
		t.Fatalf("%d reports, want %d: %+v", len(reports), len(want), reports)
	}
	for i := range want {
		if reports[i] != want[i] {
			t.Errorf("report %d = %+v, want %+v", i+1, reports[i], want[i])
		}
	}
	for _, d := range []time.Duration{60 * time.Millisecond, 90 * time.Millisecond, 5 * time.Second} {
		if _, err := DTMF('1', 16000, d); err == nil {
			t.Errorf("a digit of %v was accepted", d)
		}
	}
	if _, err := DTMF('x', 8000, DTMFDuration); err == nil {
		t.Error("digit x was accepted")
	}
}

func TestDTMFReceiver(t *testing.T) {
	var r DTMFReceiver
	var got []rune
	send := func(timestamp uint32, digit rune) {
		reports, _ := DTMF(digit, 8000, 80*time.Millisecond)
		for _, rep := range reports {
			if d, ok := r.Report(timestamp, rep.Payload); ok {
				got = append(got, d)
			}
		}
	}
	send(1000, '1')
	send(2000, '1') // the same digit again is a new event: a new timestamp
	send(3000, '#')
	if string(got) != "11#" {
		t.Errorf("digits %q, want \"11#\"", string(got))
	}
}
