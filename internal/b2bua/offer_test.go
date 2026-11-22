package b2bua

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// An offer of the Call's own has crossed one of the peer when the peer
// refuses it with 491 (Request Pending), and when the UA does not send
// it at all because an offer or an INVITE of one of the two sides is
// still open: each of those ends with an answer that is on its way, so
// the offer is worth another try (RFC 3261 14.1, RFC 3311 5.2). Any
// other answer is the end of it.
func TestCrossedOffers(t *testing.T) {
	for _, c := range []struct {
		what string
		resp *sip.Message
		err  error
		want bool
	}{
		{what: "491", resp: &sip.Message{StatusCode: 491}, want: true},
		{what: "an offer of the peer that waits for an answer", err: sip.ErrOfferUnanswered, want: true},
		{what: "an offer of this UA that waits for an answer", err: sip.ErrOfferPending, want: true},
		{what: "an INVITE in progress", err: sip.ErrInviteInProgress, want: true},
		{what: "a wrapped error", err: fmt.Errorf("offer: %w", sip.ErrOfferPending), want: true},
		{what: "200", resp: &sip.Message{StatusCode: 200}},
		{what: "488", resp: &sip.Message{StatusCode: 488}},
		{what: "the end of the call", err: sip.ErrCallEnded},
		{what: "a timeout", err: errors.New("sip: no response")},
	} {
		t.Run(c.what, func(t *testing.T) {
			if got := crossed(c.resp, c.err); got != c.want {
				t.Errorf("crossed(%v, %v) is %t, want %t", c.resp, c.err, got, c.want)
			}
		})
	}
}
