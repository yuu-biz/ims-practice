package rtp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// TelephoneEvent is the payload of one named telephone event (RFC 4733
// 2.3), the way a UE sends a DTMF digit in the audio stream (TS 26.114
// Annex G).
type TelephoneEvent struct {
	Event uint8
	// End is the E bit: this report ends the event.
	End bool
	// Volume is the power of the tone in -dBm0, 0 to 63: larger is
	// quieter.
	Volume uint8
	// Duration is how long the event has lasted so far, in RTP
	// timestamp units from the packet's timestamp.
	Duration uint16
}

// Marshal writes the four octets of RFC 4733 2.3. The R bit is 0.
func (e TelephoneEvent) Marshal() []byte {
	b := make([]byte, 4)
	b[0] = e.Event
	b[1] = e.Volume & 0x3f
	if e.End {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:], e.Duration)
	return b
}

// UnmarshalTelephoneEvent reads the first event of a payload. A packet
// may pack further events after it (RFC 4733 2.5.1.5); a UE that sends
// one digit at a time never does.
func UnmarshalTelephoneEvent(b []byte) (TelephoneEvent, error) {
	if len(b) < 4 {
		return TelephoneEvent{}, errors.New("rtp: a telephone event is 4 octets")
	}
	return TelephoneEvent{
		Event:    b[0],
		End:      b[1]&0x80 != 0, // the R bit, 0x40, is ignored
		Volume:   b[1] & 0x3f,
		Duration: binary.BigEndian.Uint16(b[2:]),
	}, nil
}

// dtmfDigits are the DTMF events of RFC 4733 3.2 Table 3, by event code.
const dtmfDigits = "0123456789*#ABCD"

// DTMFEvent is the event code of a DTMF digit: 0-9, *, # and A-D.
func DTMFEvent(digit rune) (uint8, bool) {
	i := strings.IndexRune(dtmfDigits, unicode.ToUpper(digit))
	return uint8(i), i >= 0
}

// DTMFDigit is the digit of a DTMF event code.
func DTMFDigit(event uint8) (rune, bool) {
	if int(event) >= len(dtmfDigits) {
		return 0, false
	}
	return rune(dtmfDigits[event]), true
}

// The timing of DTMF in this project. TS 26.114 G.2 asks for tones and
// pauses of at least 65 ms in whole speech frames, so a digit lasts
// five frames and the next one follows at least four frames later.
// Each report goes out at the audio packet interval (RFC 4733 2.5.1.2).
// The volume is a level often used for DTMF; RFC 4733 fixes none.
const (
	DTMFDuration    = 100 * time.Millisecond
	DTMFMinPause    = 80 * time.Millisecond
	dtmfMinDuration = 65 * time.Millisecond
	dtmfStep        = 20 * time.Millisecond
	DTMFVolume      = 10
	// endReports is how many times the final report is sent (RFC 4733
	// 2.5.1.4), so that losing one does not lose the end.
	endReports = 3
)

// DTMFReport is one packet of an event. All of an event's packets carry
// the RTP timestamp of its start; each takes the next sequence number
// (RFC 4733 2.2.1, 2.5.1.6), and the marker bit is set on the first
// (2.2.2).
type DTMFReport struct {
	Marker  bool
	Payload TelephoneEvent
}

// DTMF lists the packets that send one digit lasting d, at a clock rate
// of clockRate: a report every 20 ms whose duration grows, then the
// final report with the E bit three times (RFC 4733 2.5.1.2, 2.5.1.4).
// The caller sends one every 20 ms, and the next audio or event after
// them starts at the timestamp d plus a pause of at least DTMFMinPause
// later (TS 26.114 G.2, G.4).
//
// SIMPLIFIED: RFC 4733 2.5.1.3. A digit of more than 65535 timestamp
// units, about 4 s at 16 kHz, would need segments; DTMF refuses it
// instead. A UE sends short digits only.
func DTMF(digit rune, clockRate int, d time.Duration) ([]DTMFReport, error) {
	event, ok := DTMFEvent(digit)
	if !ok {
		return nil, fmt.Errorf("rtp: %q is not a DTMF digit", digit)
	}
	if d < dtmfMinDuration || d%dtmfStep != 0 {
		return nil, fmt.Errorf("rtp: a DTMF digit lasts whole 20 ms frames and at least 65 ms, not %v (TS 26.114 G.2)", d)
	}
	units := func(t time.Duration) int { return int(t) * clockRate / int(time.Second) }
	if units(d) > 0xffff {
		return nil, fmt.Errorf("rtp: %v is too long for one event at %d Hz", d, clockRate)
	}
	var out []DTMFReport
	for t := dtmfStep; t < d; t += dtmfStep {
		out = append(out, DTMFReport{
			Marker:  t == dtmfStep,
			Payload: TelephoneEvent{Event: event, Volume: DTMFVolume, Duration: uint16(units(t))},
		})
	}
	for range endReports {
		out = append(out, DTMFReport{
			Marker:  len(out) == 0,
			Payload: TelephoneEvent{Event: event, End: true, Volume: DTMFVolume, Duration: uint16(units(d))},
		})
	}
	return out, nil
}

// DTMFReceiver turns the reports of one stream into digits, each digit
// once. An event is told apart from the next by its timestamp (RFC
// 4733 2.5.2.2), so the retransmitted final reports add nothing.
type DTMFReceiver struct {
	started   bool
	timestamp uint32
	reported  bool
}

// Report reads one packet and returns the digit when this is the first
// final report of its event.
func (r *DTMFReceiver) Report(timestamp uint32, e TelephoneEvent) (rune, bool) {
	if !r.started || timestamp != r.timestamp {
		r.started, r.timestamp, r.reported = true, timestamp, false
	}
	if !e.End || r.reported {
		return 0, false
	}
	r.reported = true
	return DTMFDigit(e.Event)
}
