package sip

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"time"
)

// This file holds the rules for when a UA may send or take an offer:
// the offer/answer model of RFC 3264 as RFC 3261 13.2.1, RFC 3262 5 and
// RFC 3311 5.1 fit it into the INVITE, its responses, PRACK, ACK and
// UPDATE. The SDP itself is none of this package's business; the UA in
// ua.go only has to know, for each body it carries, whether it is an
// offer or an answer, and whether another offer is still open. Like the
// transaction state machines, the table here never touches the network.

// OfferState is where the offer/answer exchange of a dialog stands.
type OfferState int

const (
	// OfferNone is a dialog in which no session description has gone
	// either way yet, as after an INVITE without a body.
	OfferNone OfferState = iota
	// OfferLocal is an offer of this UA that waits for its answer.
	OfferLocal
	// OfferRemote is an offer of the peer that this UA has not answered.
	OfferRemote
	// OfferAnswered is the stable state: every offer has its answer.
	OfferAnswered
)

func (s OfferState) String() string {
	switch s {
	case OfferNone:
		return "None"
	case OfferLocal:
		return "Local offer"
	case OfferRemote:
		return "Remote offer"
	case OfferAnswered:
		return "Answered"
	}
	return "unknown"
}

// OfferEvent is a session description that this UA sends or receives,
// or the failure of an offer.
type OfferEvent int

const (
	OfferSent OfferEvent = iota
	OfferReceived
	AnswerSent
	AnswerReceived
	// OfferRejected is a non-2xx final response to the request that
	// carried the open offer. The session stays as it was before the
	// offer (RFC 3261 14.1, RFC 3311 5.3).
	OfferRejected
)

func (e OfferEvent) String() string {
	return [...]string{"offer sent", "offer received", "answer sent", "answer received", "offer rejected"}[e]
}

var (
	// ErrOfferPending reports an offer that crosses an offer of this UA
	// still waiting for its answer. A UAS answers such an UPDATE with
	// 491 (Request Pending) (RFC 3311 5.2), and a UAC must not send a
	// second offer (5.1).
	ErrOfferPending = errors.New("sip: an offer of this UA waits for its answer")
	// ErrOfferUnanswered reports an offer that comes while an offer of
	// the peer waits for this UA's answer. A UAS answers such an UPDATE
	// with 500 and Retry-After (RFC 3311 5.2), and a UAC must answer
	// before it offers (5.1).
	ErrOfferUnanswered = errors.New("sip: an offer of the peer waits for this UA's answer")
	// ErrNoOffer reports an answer where no offer is open.
	ErrNoOffer = errors.New("sip: no offer to answer")
)

// offerTransitions is the table of the exchange: for each state, the
// state after each event that is allowed in it.
var offerTransitions = map[OfferState]map[OfferEvent]OfferState{
	OfferNone: {
		OfferSent:     OfferLocal,
		OfferReceived: OfferRemote,
	},
	OfferLocal: {
		AnswerReceived: OfferAnswered,
		OfferRejected:  OfferAnswered,
	},
	OfferRemote: {
		AnswerSent:    OfferAnswered,
		OfferRejected: OfferAnswered,
	},
	OfferAnswered: {
		OfferSent:     OfferLocal,
		OfferReceived: OfferRemote,
	},
}

// Next is the state after e. An event that the table does not allow
// leaves the state as it is and reports why.
func (s OfferState) Next(e OfferEvent) (OfferState, error) {
	if next, ok := offerTransitions[s][e]; ok {
		return next, nil
	}
	switch {
	case e == OfferSent || e == OfferReceived:
		if s == OfferLocal {
			return s, ErrOfferPending
		}
		return s, ErrOfferUnanswered
	default:
		return s, ErrNoOffer
	}
}

// GlareDelay is how long a UAC waits before it tries a re-INVITE or an
// UPDATE again after a 491 (Request Pending): 2.1 to 4 seconds when it
// owns the Call-ID of the dialog, 0 to 2 seconds when the peer does, in
// steps of 10 ms (RFC 3261 14.1, RFC 3311 5.3). The two ranges do not
// overlap, so the two UAs of a dialog do not collide a second time.
func GlareDelay(ownsCallID bool) time.Duration {
	if ownsCallID {
		return 2100*time.Millisecond + time.Duration(rand.IntN(191))*10*time.Millisecond
	}
	return time.Duration(rand.IntN(201)) * 10 * time.Millisecond
}

// retryAfter is the Retry-After of the 500 that a UAS sends for a
// request it cannot take yet: a random number of seconds from 0 to 10
// (RFC 3261 14.2, RFC 3311 5.2).
func retryAfter() HeaderField {
	return HeaderField{Name: "Retry-After", Value: strconv.Itoa(rand.IntN(11))}
}
