package sdp

import (
	"errors"
	"fmt"
)

// EVSCapabilities is what the answerer of an EVS offer can use.
type EVSCapabilities struct {
	// Configuration holds the EVS Primary bit rates and bandwidths this
	// side uses, which an answer keeps within.
	Configuration EVSConfiguration
	// AMRWBIOOnly tells that this side sends EVS AMR-WB IO frames only.
	// Its answers then start the session in EVS AMR-WB IO, and it
	// refuses an offer that fixes EVS Primary as the start mode.
	AMRWBIOOnly bool
}

// ErrEVSNotAnswerable is wrapped by the errors of AnswerEVSParams: the
// answerer has to reject the payload type (TS 26.445 A.3.3.1).
var ErrEVSNotAnswerable = errors.New("sdp: the EVS payload type cannot be accepted")

// AnswerEVSParams is the a=fmtp of an answer that accepts an EVS payload
// type offered with offer, following the offer-answer rules of TS
// 26.445 A.3.3.1 and the answer of an MTSI client in terminal of TS
// 26.114 6.2.2.3 (Tables 6.3, 6.3a, 6.3b and 6.3c). It fails when the
// payload type has to be rejected.
//
//   - br and bw come back identical or narrowed to what local uses; an
//     offered single bit rate comes back as it was or not at all. When
//     the offer has neither, the answer states local's.
//   - The parameters of one direction swap: an offered br-send is
//     answered with br-recv, and so on for br-recv, bw-send, bw-recv,
//     ch-send and ch-recv.
//   - evs-mode-switch, hf-only, dtx and cmr stay as offered. Absent, the
//     answer adds evs-mode-switch=1 when local sends AMR-WB IO only,
//     and nothing else.
//   - An offered mode-set stays (Table 6.3 through Table 6.3c). Without
//     one, the answer carries the mode-set of NG.114 3.2.2.3 when it
//     selects A1, B0 or B1.
//   - max-red is this side's own: 0, as it sends no redundancy (Table
//     6.3). dtx-recv, ch-aw-recv and the mode change parameters are left
//     out (Tables 6.2a, 6.3 and 6.3c), and so are parameters A.3.1 does
//     not define, which a receiver ignores (A.3.1).
func AnswerEVSParams(offer EVSParams, local EVSCapabilities) (EVSParams, error) {
	if err := offer.Validate(); err != nil {
		return EVSParams{}, fmt.Errorf("%w: %w", ErrEVSNotAnswerable, err)
	}
	var a EVSParams
	var err error
	brs, bws := local.Configuration.BitRates, local.Configuration.Bandwidths
	if a.BR, err = answerRange(offer.BR, brs, "br"); err != nil {
		return EVSParams{}, err
	}
	if a.BRRecv, err = answerRange(offer.BRSend, brs, "br-send"); err != nil {
		return EVSParams{}, err
	}
	if a.BRSend, err = answerRange(offer.BRRecv, brs, "br-recv"); err != nil {
		return EVSParams{}, err
	}
	if !offer.BR.Present && !offer.BRSend.Present && !offer.BRRecv.Present {
		a.BR = Some(brs)
	}
	if a.BW, err = answerRange(offer.BW, bws, "bw"); err != nil {
		return EVSParams{}, err
	}
	if a.BWRecv, err = answerRange(offer.BWSend, bws, "bw-send"); err != nil {
		return EVSParams{}, err
	}
	if a.BWSend, err = answerRange(offer.BWRecv, bws, "bw-recv"); err != nil {
		return EVSParams{}, err
	}
	if !offer.BW.Present && !offer.BWSend.Present && !offer.BWRecv.Present {
		a.BW = Some(bws)
	}

	// The evs package carries one channel only; an answer would have to
	// repeat the offered number (A.3.3.1 ch-send, ch-recv).
	if offer.ChSend.Value > 1 || offer.ChRecv.Value > 1 {
		return EVSParams{}, fmt.Errorf("%w: it asks for more than one channel", ErrEVSNotAnswerable)
	}
	a.ChRecv, a.ChSend = offer.ChSend, offer.ChRecv

	a.EVSModeSwitch = offer.EVSModeSwitch
	if local.AMRWBIOOnly {
		if offer.EVSModeSwitch.Present && !offer.EVSModeSwitch.Value {
			return EVSParams{}, fmt.Errorf("%w: evs-mode-switch=0 starts in EVS Primary, which this side does not send, and an answer cannot change it", ErrEVSNotAnswerable)
		}
		a.EVSModeSwitch = Some(true)
	}
	a.HFOnly, a.DTX, a.CMR = offer.HFOnly, offer.DTX, offer.CMR

	a.ModeSet = offer.ModeSet
	if a.ModeSet == nil && a.BR.Present && a.BW.Present {
		a.ModeSet = ng114ModeSet(a.BR.Value, a.BW.Value)
	}
	a.MaxRed = Some(0)

	if err := a.Validate(); err != nil {
		return EVSParams{}, fmt.Errorf("%w: %w", ErrEVSNotAnswerable, err)
	}
	return a, nil
}

// answerRange is the answer to one offered bit rate or bandwidth
// parameter: identical to the offer or a subset of it, within what this
// side uses. Narrowing keeps br1 or raises it and keeps br2 or lowers
// it (A.3.3.1), which is what taking the common part does.
func answerRange[T interface {
	intersect(T) (T, bool)
	String() string
}](offered Optional[T], local T, name string) (Optional[T], error) {
	if !offered.Present {
		return Optional[T]{}, nil
	}
	r, ok := offered.Value.intersect(local)
	if !ok {
		return Optional[T]{}, fmt.Errorf("%w: %s=%s has nothing in common with %s", ErrEVSNotAnswerable, name, offered.Value, local)
	}
	return Some(r), nil
}
