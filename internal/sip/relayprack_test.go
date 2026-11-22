package sip

import (
	"testing"
	"time"
)

// A PRACK without a body gets its 200 from the UA, and the user of the
// call does not see it. After RelayPRACK the user gets it like any
// request within the dialog, with its Arrival, and the 200 is the
// user's: the caller has none until the user accepts the PRACK.
func TestRelayPRACKHandsThePRACKToTheUser(t *testing.T) {
	for _, relay := range []bool{false, true} {
		name := "PRACK of the UA"
		if relay {
			name = "PRACK of the user"
		}
		t.Run(name, func(t *testing.T) {
			got := make(chan *InDialogRequest, 1)
			progress := make(chan error, 1)
			calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
				c.Handle(func(r *InDialogRequest) { got <- r })
				if relay {
					c.RelayPRACK()
				}
				progress <- c.Progress(183, answer)
			})
			peer := rawSocket(t)
			invite := rawINVITE(peer)
			invite.Add("Supported", Option100rel)
			send(t, peer, invite, calleeUA.Stack())
			prov := readSkippingTrying(t, peer)
			if prov.StatusCode != 183 || !prov.Requires(Option100rel) {
				t.Fatalf("got %s with Require %q, want a reliable 183", startLine(prov), prov.Get("Require"))
			}
			send(t, peer, rawPRACK(t, peer, invite, prov, 2), calleeUA.Stack())
			select {
			case err := <-progress:
				if err != nil {
					t.Fatalf("Progress: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Progress did not return after the PRACK")
			}

			if relay {
				var r *InDialogRequest
				select {
				case r = <-got:
				case <-time.After(2 * time.Second):
					t.Fatal("the user did not get the PRACK")
				}
				if r.Request.Method != PRACK || len(r.Request.Body) != 0 || r.Offer || r.Arrival != 1 {
					t.Fatalf("the user got %s with %d bytes of body, Offer %t and Arrival %d, want the PRACK without a body as the first request",
						r.Request.Method, len(r.Request.Body), r.Offer, r.Arrival)
				}
				peer.SetReadDeadline(time.Now().Add(3 * testTimers.T1))
				buf := make([]byte, 65535)
				for {
					n, err := peer.Read(buf)
					if err != nil {
						break
					}
					if m, _ := Parse(buf[:n]); m != nil && m.StatusCode != 183 {
						t.Fatalf("the caller got %s before the user accepted the PRACK", startLine(m))
					}
				}
				if _, err := r.Accept(Body{}); err != nil {
					t.Fatalf("Accept: %v", err)
				}
			}
			if resp := readSkipping183(t, peer); resp.StatusCode != 200 || resp.Get("CSeq") != "2 PRACK" {
				t.Fatalf("the PRACK got %s with CSeq %q, want 200", startLine(resp), resp.Get("CSeq"))
			}
			if !relay {
				select {
				case r := <-got:
					t.Errorf("the user got the %s without a body, which the UA answers", r.Request.Method)
				case <-time.After(3 * testTimers.T1):
				}
			}
		})
	}
}
