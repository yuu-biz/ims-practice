package sdp

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

// EVSBitRate is an EVS Primary source bit rate of the br, br-send and
// br-recv parameters (TS 26.445 A.3.1), in increasing order. From 7.2
// on, the value is also the frame type of TS 26.445 Table A.4.
type EVSBitRate uint8

const (
	EVS5900 EVSBitRate = iota // the average of source controlled VBR (SC-VBR)
	EVS7200
	EVS8000
	EVS9600
	EVS13200
	EVS16400
	EVS24400
	EVS32000
	EVS48000
	EVS64000
	EVS96000
	EVS128000
)

// evsBitRateNames are the bit rates as A.3.1 writes them, in kbit/s.
var evsBitRateNames = [...]string{"5.9", "7.2", "8", "9.6", "13.2", "16.4", "24.4", "32", "48", "64", "96", "128"}

func (r EVSBitRate) String() string { return evsBitRateNames[r] }

// parseEVSBitRate reads a bit rate by its value, so that "8.0" is read
// as 8.
func parseEVSBitRate(s string) (EVSBitRate, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err == nil {
		for r, name := range evsBitRateNames {
			if n, _ := strconv.ParseFloat(name, 64); n == v {
				return EVSBitRate(r), nil
			}
		}
	}
	return 0, fmt.Errorf("%q is not an EVS bit rate of TS 26.445 A.3.1", s)
}

// frameType is the EVS Primary frame type whose size b=AS counts for
// the bit rate. SC-VBR counts as its highest component bit rate, 8
// kbit/s (TS 26.114 6.2.5.2 NOTE 2).
func (r EVSBitRate) frameType() evs.FrameType {
	if r == EVS5900 {
		return evs.Primary8000
	}
	return evs.FrameType(r)
}

// EVSBitRateRange is the value of br, br-send or br-recv: one bit rate
// when Min equals Max, or Min-Max (TS 26.445 A.3.1).
type EVSBitRateRange struct {
	Min, Max EVSBitRate
}

func (b EVSBitRateRange) String() string {
	if b.Min == b.Max {
		return b.Min.String()
	}
	return b.Min.String() + "-" + b.Max.String()
}

// parseEVSBitRateRange reads br1 or br1-br2, where br1 is smaller than
// br2 (A.3.1).
func parseEVSBitRateRange(s string) (EVSBitRateRange, error) {
	s1, s2, isRange := strings.Cut(s, "-")
	br1, err := parseEVSBitRate(s1)
	if err != nil {
		return EVSBitRateRange{}, err
	}
	if !isRange {
		return EVSBitRateRange{br1, br1}, nil
	}
	br2, err := parseEVSBitRate(s2)
	if err != nil {
		return EVSBitRateRange{}, err
	}
	if br1 >= br2 {
		return EVSBitRateRange{}, fmt.Errorf("%s: the first bit rate must be the smaller one (TS 26.445 A.3.1)", s)
	}
	return EVSBitRateRange{br1, br2}, nil
}

// intersect is the bit rates in both b and o.
func (b EVSBitRateRange) intersect(o EVSBitRateRange) (EVSBitRateRange, bool) {
	r := EVSBitRateRange{max(b.Min, o.Min), min(b.Max, o.Max)}
	return r, r.Min <= r.Max
}

// EVSBandwidth is an audio bandwidth of EVS Primary (TS 26.445 A.3.1),
// from the narrowest.
type EVSBandwidth uint8

const (
	EVSNB  EVSBandwidth = iota // narrowband
	EVSWB                      // wideband
	EVSSWB                     // super-wideband
	EVSFB                      // fullband
)

var evsBandwidthNames = [...]string{"nb", "wb", "swb", "fb"}

func (w EVSBandwidth) String() string { return evsBandwidthNames[w] }

// EVSBandwidthRange is the value of bw, bw-send or bw-recv: one audio
// bandwidth when Min equals Max, or every bandwidth from narrowband to
// Max, which are the only ranges A.3.1 defines (nb-wb, nb-swb, nb-fb).
type EVSBandwidthRange struct {
	Min, Max EVSBandwidth
}

func (w EVSBandwidthRange) String() string {
	if w.Min == w.Max {
		return w.Min.String()
	}
	return w.Min.String() + "-" + w.Max.String()
}

func parseEVSBandwidthRange(s string) (EVSBandwidthRange, error) {
	for _, w := range []EVSBandwidthRange{
		{EVSNB, EVSNB}, {EVSWB, EVSWB}, {EVSSWB, EVSSWB}, {EVSFB, EVSFB},
		{EVSNB, EVSWB}, {EVSNB, EVSSWB}, {EVSNB, EVSFB},
	} {
		if strings.EqualFold(s, w.String()) {
			return w, nil
		}
	}
	return EVSBandwidthRange{}, fmt.Errorf("%q is not an EVS audio bandwidth of TS 26.445 A.3.1", s)
}

// evsBandwidthBitRates are the bit rates TS 26.445 Table A.6 allows
// with each audio bandwidth.
var evsBandwidthBitRates = [...]EVSBitRateRange{
	EVSNB:  {EVS5900, EVS24400},
	EVSWB:  {EVS5900, EVS128000},
	EVSSWB: {EVS9600, EVS128000},
	EVSFB:  {EVS16400, EVS128000},
}

// highestUsable is the highest bit rate of br that Table A.6 allows
// with one of the bandwidths of bw. Only those bit rates may be used
// (TS 26.445 A.3.1 br). It fails when there is none, which is a br and
// bw that do not agree with Table A.6 (A.3.3.1).
func highestUsable(br EVSBitRateRange, bw EVSBandwidthRange) (EVSBitRate, bool) {
	best, found := EVSBitRate(0), false
	for w := bw.Min; w <= bw.Max; w++ {
		if r, ok := br.intersect(evsBandwidthBitRates[w]); ok && (!found || r.Max > best) {
			best, found = r.Max, true
		}
	}
	return best, found
}

// Optional is a parameter of a=fmtp that may be absent. An absent
// parameter and one present with its default value are not the same in
// offer-answer (TS 26.445 A.3.3.1), so the two are kept apart.
type Optional[T any] struct {
	Value   T
	Present bool
}

// Some is a present parameter with value v.
func Some[T any](v T) Optional[T] { return Optional[T]{Value: v, Present: true} }

// EVSParams are the parameters of the a=fmtp line of an EVS payload type
// (TS 26.445 A.3.1 and A.3.2). Parameters A.3.1 does not define are
// kept in Other, as written, so that a description can be read and
// written again without loss; a receiver ignores them.
type EVSParams struct {
	// EVS Primary: the bit rates and audio bandwidths for both
	// directions (br, bw) or for one of them (-send, -recv), the
	// channels of each direction, and the channel-aware mode the sender
	// of the SDP wants to receive.
	BR, BRSend, BRRecv Optional[EVSBitRateRange]
	BW, BWSend, BWRecv Optional[EVSBandwidthRange]
	ChSend, ChRecv     Optional[int]
	ChAwRecv           Optional[int]

	// Both operation modes.
	EVSModeSwitch Optional[bool] // true starts both directions in EVS AMR-WB IO
	HFOnly        Optional[bool] // true allows the Header-Full format only
	DTX, DTXRecv  Optional[bool]
	CMR           Optional[int] // -1, 0 or 1
	MaxRed        Optional[int] // in ms (RFC 4867 8.2)

	// EVS AMR-WB IO: the modes allowed, as the frame types of Table A.5
	// (nil when mode-set is absent and all are allowed), and the mode
	// change restrictions of RFC 4867 8.2.
	ModeSet                                                    []evs.FrameType
	ModeChangePeriod, ModeChangeCapability, ModeChangeNeighbor Optional[int]

	Other []string
}

// ParseEVSParams reads the a=fmtp value of an EVS payload type: a list
// of name=value pairs separated by semicolons (A.3.2). Names are
// case-insensitive (RFC 4855 3). It refuses a value outside what A.3.1
// allows, a parameter given twice, and the combinations Validate
// refuses.
func ParseEVSParams(fmtp string) (EVSParams, error) {
	var p EVSParams
	seen := map[string]bool{}
	for _, item := range strings.Split(fmtp, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, value, _ := strings.Cut(item, "=")
		name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
		if seen[name] {
			return EVSParams{}, fmt.Errorf("sdp: EVS parameter %s appears twice", name)
		}
		seen[name] = true
		if err := p.set(name, value, item); err != nil {
			return EVSParams{}, fmt.Errorf("sdp: EVS parameter %s: %w", name, err)
		}
	}
	if err := p.Validate(); err != nil {
		return EVSParams{}, err
	}
	return p, nil
}

func (p *EVSParams) set(name, value, item string) error {
	var err error
	switch name {
	case "br":
		p.BR, err = parseOptional(value, parseEVSBitRateRange)
	case "br-send":
		p.BRSend, err = parseOptional(value, parseEVSBitRateRange)
	case "br-recv":
		p.BRRecv, err = parseOptional(value, parseEVSBitRateRange)
	case "bw":
		p.BW, err = parseOptional(value, parseEVSBandwidthRange)
	case "bw-send":
		p.BWSend, err = parseOptional(value, parseEVSBandwidthRange)
	case "bw-recv":
		p.BWRecv, err = parseOptional(value, parseEVSBandwidthRange)
	case "ch-send":
		p.ChSend, err = parseInt(value, 1, math.MaxInt)
	case "ch-recv":
		p.ChRecv, err = parseInt(value, 1, math.MaxInt)
	case "ch-aw-recv":
		p.ChAwRecv, err = parseIntOf(value, -1, 0, 2, 3, 5, 7)
	case "evs-mode-switch":
		p.EVSModeSwitch, err = parseFlag(value)
	case "hf-only":
		p.HFOnly, err = parseFlag(value)
	case "dtx":
		p.DTX, err = parseFlag(value)
	case "dtx-recv":
		p.DTXRecv, err = parseFlag(value)
	case "cmr":
		p.CMR, err = parseIntOf(value, -1, 0, 1)
	case "max-red":
		p.MaxRed, err = parseInt(value, 0, 65535) // RFC 4867 8.2
	case "mode-set":
		p.ModeSet, err = parseEVSModeSet(value)
	case "mode-change-period":
		p.ModeChangePeriod, err = parseIntOf(value, 1, 2) // RFC 4867 8.2
	case "mode-change-capability":
		// A.3.1: 2 is the default and the only value for EVS AMR-WB
		// IO, unlike AMR-WB (RFC 4867 8.2).
		p.ModeChangeCapability, err = parseIntOf(value, 2)
	case "mode-change-neighbor":
		p.ModeChangeNeighbor, err = parseIntOf(value, 0, 1) // RFC 4867 8.2
	default:
		p.Other = append(p.Other, item)
	}
	return err
}

func parseOptional[T any](value string, parse func(string) (T, error)) (Optional[T], error) {
	v, err := parse(value)
	if err != nil {
		return Optional[T]{}, err
	}
	return Some(v), nil
}

// parseInt reads an integer from lo to hi.
func parseInt(value string, lo, hi int) (Optional[int], error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < lo || n > hi {
		return Optional[int]{}, fmt.Errorf("%q is out of range", value)
	}
	return Some(n), nil
}

// parseIntOf reads an integer that is one of allowed.
func parseIntOf(value string, allowed ...int) (Optional[int], error) {
	n, err := strconv.Atoi(value)
	if err != nil || !slices.Contains(allowed, n) {
		return Optional[int]{}, fmt.Errorf("%q is not one of %v", value, allowed)
	}
	return Some(n), nil
}

// parseFlag reads a parameter whose permissible values are 0 and 1.
func parseFlag(value string) (Optional[bool], error) {
	n, err := parseIntOf(value, 0, 1)
	return Optional[bool]{Value: n.Value == 1, Present: n.Present}, err
}

// parseEVSModeSet reads a list of EVS AMR-WB IO modes 0 to 8, the
// frame types of TS 26.201 Table 1a (A.3.1 mode-set).
func parseEVSModeSet(value string) ([]evs.FrameType, error) {
	var modes []evs.FrameType
	for _, s := range strings.Split(value, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || !evs.AMRWBIO.IsSpeech(evs.FrameType(n)) || slices.Contains(modes, evs.FrameType(n)) {
			return nil, fmt.Errorf("%q is not a list of different modes 0 to 8", value)
		}
		modes = append(modes, evs.FrameType(n))
	}
	return modes, nil
}

// Validate checks what A.3.1 asks of parameters together: br-send and
// br-recv identical to br when both are present, the same for bw, dtx-recv
// identical to dtx, and for each direction bit rates and bandwidths that
// Table A.6 allows together (A.3.3.1).
func (p EVSParams) Validate() error {
	for _, dir := range []Optional[EVSBitRateRange]{p.BRSend, p.BRRecv} {
		if p.BR.Present && dir.Present && dir.Value != p.BR.Value {
			return fmt.Errorf("sdp: EVS br=%s with br-send or br-recv=%s; they must be identical (TS 26.445 A.3.1)", p.BR.Value, dir.Value)
		}
	}
	for _, dir := range []Optional[EVSBandwidthRange]{p.BWSend, p.BWRecv} {
		if p.BW.Present && dir.Present && dir.Value != p.BW.Value {
			return fmt.Errorf("sdp: EVS bw=%s with bw-send or bw-recv=%s; they must be identical (TS 26.445 A.3.1)", p.BW.Value, dir.Value)
		}
	}
	if p.DTX.Present && p.DTXRecv.Present && p.DTX.Value != p.DTXRecv.Value {
		return errors.New("sdp: EVS dtx and dtx-recv differ; they must be identical (TS 26.445 A.3.1)")
	}
	for _, d := range []struct {
		name string
		br   EVSBitRateRange
		bw   EVSBandwidthRange
	}{
		{"send", p.sendBitRates(), p.sendBandwidths()},
		{"receive", p.receiveBitRates(), p.receiveBandwidths()},
	} {
		if _, ok := highestUsable(d.br, d.bw); !ok {
			return fmt.Errorf("sdp: EVS bit rates %s and bandwidths %s of the %s direction have no combination in TS 26.445 Table A.6", d.br, d.bw, d.name)
		}
	}
	return nil
}

// allEVSBitRates and allEVSBandwidths are what a direction allows when
// none of its parameters is present (A.3.1).
var (
	allEVSBitRates   = EVSBitRateRange{EVS5900, EVS128000}
	allEVSBandwidths = EVSBandwidthRange{EVSNB, EVSFB}
)

// sendBitRates and the three functions after it are the bit rates and
// bandwidths of each direction, as seen by the sender of the SDP: br or
// br-send for its send direction, br or br-recv for its receive one,
// and the same for bw (A.3.1).
func (p EVSParams) sendBitRates() EVSBitRateRange {
	return firstPresent(allEVSBitRates, p.BR, p.BRSend)
}

func (p EVSParams) receiveBitRates() EVSBitRateRange {
	return firstPresent(allEVSBitRates, p.BR, p.BRRecv)
}

func (p EVSParams) sendBandwidths() EVSBandwidthRange {
	return firstPresent(allEVSBandwidths, p.BW, p.BWSend)
}

func (p EVSParams) receiveBandwidths() EVSBandwidthRange {
	return firstPresent(allEVSBandwidths, p.BW, p.BWRecv)
}

func firstPresent[T any](absent T, values ...Optional[T]) T {
	for _, v := range values {
		if v.Present {
			return v.Value
		}
	}
	return absent
}

// String writes p as an a=fmtp value. The order follows the examples of
// TS 26.114 A.14: bit rates and bandwidths first, max-red last, and the
// parameters A.3.1 does not define after them.
func (p EVSParams) String() string {
	var out []string
	add := func(name string, present bool, value string) {
		if present {
			out = append(out, name+"="+value)
		}
	}
	flag := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	add("br", p.BR.Present, p.BR.Value.String())
	add("br-send", p.BRSend.Present, p.BRSend.Value.String())
	add("br-recv", p.BRRecv.Present, p.BRRecv.Value.String())
	add("bw", p.BW.Present, p.BW.Value.String())
	add("bw-send", p.BWSend.Present, p.BWSend.Value.String())
	add("bw-recv", p.BWRecv.Present, p.BWRecv.Value.String())
	add("evs-mode-switch", p.EVSModeSwitch.Present, flag(p.EVSModeSwitch.Value))
	add("cmr", p.CMR.Present, strconv.Itoa(p.CMR.Value))
	add("dtx", p.DTX.Present, flag(p.DTX.Value))
	add("dtx-recv", p.DTXRecv.Present, flag(p.DTXRecv.Value))
	add("mode-set", p.ModeSet != nil, modeSetString(p.ModeSet))
	add("mode-change-period", p.ModeChangePeriod.Present, strconv.Itoa(p.ModeChangePeriod.Value))
	add("mode-change-capability", p.ModeChangeCapability.Present, strconv.Itoa(p.ModeChangeCapability.Value))
	add("mode-change-neighbor", p.ModeChangeNeighbor.Present, strconv.Itoa(p.ModeChangeNeighbor.Value))
	add("hf-only", p.HFOnly.Present, flag(p.HFOnly.Value))
	add("ch-send", p.ChSend.Present, strconv.Itoa(p.ChSend.Value))
	add("ch-recv", p.ChRecv.Present, strconv.Itoa(p.ChRecv.Value))
	add("ch-aw-recv", p.ChAwRecv.Present, strconv.Itoa(p.ChAwRecv.Value))
	add("max-red", p.MaxRed.Present, strconv.Itoa(p.MaxRed.Value))
	return strings.Join(append(out, p.Other...), "; ")
}

func modeSetString(modes []evs.FrameType) string {
	s := make([]string, len(modes))
	for i, m := range modes {
		s[i] = strconv.Itoa(int(m))
	}
	return strings.Join(s, ",")
}

// StartMode is the operation mode both directions start the session in:
// EVS AMR-WB IO with evs-mode-switch=1, else EVS Primary (A.3.1).
func (p EVSParams) StartMode() evs.Mode {
	if p.EVSModeSwitch.Value {
		return evs.AMRWBIO
	}
	return evs.Primary
}
