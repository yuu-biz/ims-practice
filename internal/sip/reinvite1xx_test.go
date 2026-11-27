package sip

import "testing"

// readFinal reads from the raw peer until the final response to the
// request with CSeq cseq comes, and returns it. Provisional responses
// and the responses to other requests are skipped.
func readFinal(t *testing.T, p *rawCallee, cseq string) *Message {
	t.Helper()
	for {
		if m := readDatagram(t, p.peer); !m.IsRequest() && m.StatusCode >= 200 && m.Get("CSeq") == cseq {
			return m
		}
	}
}

// RFC 3262 3: a PRACK that matches no reliable provisional response
// waiting for one gets 481. The UAC of a call takes PRACKs too, as its
// Allow says: the callee may send it a re-INVITE whose provisional
// responses go reliably.
func TestPRACKToTheCallerMatchingNoResponseGets481(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	_, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) { r.Reject(488) }})
	prack := p.request(PRACK, Body{}, HeaderField{Name: "RAck", Value: "1 1 INVITE"})
	if resp := readFinal(t, p, prack.Get("CSeq")); resp.StatusCode != 481 {
		t.Errorf("PRACK got %s, want 481", startLine(resp))
	}
}
