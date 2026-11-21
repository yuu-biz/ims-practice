package sip

import (
	"errors"
	"testing"
	"time"
)

func TestOfferStateFollowsTheExchange(t *testing.T) {
	type step struct {
		event OfferEvent
		want  OfferState
		err   error
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{"offer in the INVITE, answer in a reliable 183, new offer in an UPDATE", []step{
			{OfferSent, OfferLocal, nil},
			{AnswerReceived, OfferAnswered, nil},
			{OfferSent, OfferLocal, nil},
			{AnswerReceived, OfferAnswered, nil},
		}},
		{"INVITE without an offer: offer in the 183, answer in the PRACK", []step{
			{OfferReceived, OfferRemote, nil},
			{AnswerSent, OfferAnswered, nil},
		}},
		{"RFC 3311 5.2: an offer that crosses this UA's own gets 491", []step{
			{OfferSent, OfferLocal, nil},
			{OfferReceived, OfferLocal, ErrOfferPending},
			{AnswerReceived, OfferAnswered, nil},
			{OfferReceived, OfferRemote, nil},
		}},
		{"RFC 3311 5.2: a second offer before the answer to the first gets 500", []step{
			{OfferReceived, OfferRemote, nil},
			{OfferReceived, OfferRemote, ErrOfferUnanswered},
			{OfferSent, OfferRemote, ErrOfferUnanswered},
			{AnswerSent, OfferAnswered, nil},
		}},
		{"RFC 3261 14.1: a rejected offer leaves the session as it was", []step{
			{OfferSent, OfferLocal, nil},
			{AnswerReceived, OfferAnswered, nil},
			{OfferSent, OfferLocal, nil},
			{OfferRejected, OfferAnswered, nil},
			{OfferSent, OfferLocal, nil},
		}},
		{"an answer needs an offer", []step{
			{AnswerSent, OfferNone, ErrNoOffer},
			{AnswerReceived, OfferNone, ErrNoOffer},
			{OfferSent, OfferLocal, nil},
			{AnswerSent, OfferLocal, ErrNoOffer},
			{OfferSent, OfferLocal, ErrOfferPending},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := OfferNone
			for i, st := range tt.steps {
				next, err := s.Next(st.event)
				if next != st.want || !errors.Is(err, st.err) {
					t.Fatalf("step %d: %s after %q = %s, %v; want %s, %v", i, s, st.event, next, err, st.want, st.err)
				}
				s = next
			}
		})
	}
}

// RFC 3261 14.1: the two ranges keep the UAs of a dialog apart.
func TestGlareDelayRanges(t *testing.T) {
	for range 200 {
		if d := GlareDelay(true); d < 2100*time.Millisecond || d > 4*time.Second || d%(10*time.Millisecond) != 0 {
			t.Fatalf("owner of the Call-ID waits %v, want 2.1 s to 4 s in steps of 10 ms", d)
		}
		if d := GlareDelay(false); d < 0 || d > 2*time.Second || d%(10*time.Millisecond) != 0 {
			t.Fatalf("the other UA waits %v, want 0 to 2 s in steps of 10 ms", d)
		}
	}
}
