package sip

import (
	"testing"
	"time"
)

// replacingUAs is a callee that has a confirmed call with a caller, and
// a third UA that sends the callee INVITEs with Replaces. Each INVITE
// after the first reaches the test on calls.
type replacingUAs struct {
	established
	third *UA
	calls chan *IncomingCall
}

func newReplacingUAs(t *testing.T) replacingUAs {
	t.Helper()
	first := make(chan *IncomingCall, 1)
	calls := make(chan *IncomingCall, 2)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		if c.Request.Get("Replaces") == "" {
			c.Accept(answer)
			first <- c
			return
		}
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	responses(t, out)
	return replacingUAs{established: established{callerUA: callerUA, calleeUA: calleeUA, out: out, in: <-first},
		third: newUA(t, testTimers, "conference", noCalls), calls: calls}
}

// replacesTo names the callee's dialog as the callee has it, which is
// how an INVITE with Replaces must name it (RFC 3891 3).
func (u replacingUAs) replacesTo() Replaces {
	d, _ := u.in.Dialog()
	return Replaces{CallID: d.ID.CallID, ToTag: d.ID.LocalTag, FromTag: d.ID.RemoteTag}
}

func (u replacingUAs) inviteWith(t *testing.T, replaces ...string) *IncomingCall {
	t.Helper()
	inv := invitation(u.calleeUA)
	for _, r := range replaces {
		inv.Headers = append(inv.Headers, HeaderField{Name: "Replaces", Value: r})
	}
	if _, err := u.third.Invite(inv); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-u.calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no INVITE with Replaces at the callee")
		return nil
	}
}

// TestInviteReplacesAConfirmedDialog is RFC 3891 3: the new INVITE names
// the callee's confirmed call, which the callee accepts in its place and
// ends with BYE.
func TestInviteReplacesAConfirmedDialog(t *testing.T) {
	u := newReplacingUAs(t)
	c := u.inviteWith(t, u.replacesTo().String())
	d, refusal := c.ReplacedDialog()
	if refusal != 0 || d != &u.in.CallDialog {
		t.Fatalf("ReplacedDialog = %p, %d; want the callee's call", d, refusal)
	}
	if _, err := c.Accept(answer); err != nil {
		t.Fatal(err)
	}
	if err := d.Bye(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, u.out.Done(), "replaced caller")
}

// TestInviteReplacesAnEndedDialog is RFC 3891 3: a Replaces that names
// a dialog which has just ended is declined with 603. Once the UA no
// longer knows of the dialog, 64*T1 later, it finds no dialog.
func TestInviteReplacesAnEndedDialog(t *testing.T) {
	u := newReplacingUAs(t)
	ended := u.replacesTo().String()
	if err := u.in.Bye(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, u.out.Done(), "caller")

	c := u.inviteWith(t, ended)
	if d, refusal := c.ReplacedDialog(); d != nil || refusal != 603 {
		t.Errorf("just ended: ReplacedDialog = %p, %d; want 603", d, refusal)
	}
	c.Reject(603)

	// The UA forgets the dialog 64*T1 after it ended, on a timer of its
	// own; one T1 more leaves that timer room to run.
	time.Sleep(65 * testTimers.T1)
	c = u.inviteWith(t, ended)
	if d, refusal := c.ReplacedDialog(); d != nil || refusal != 481 {
		t.Errorf("64*T1 later: ReplacedDialog = %p, %d; want 481", d, refusal)
	}
	c.Reject(481)
}

func TestInviteReplacesRefused(t *testing.T) {
	u := newReplacingUAs(t)
	known := u.replacesTo()
	earlyOnly := known
	earlyOnly.EarlyOnly = true
	unknown := known
	unknown.FromTag = "someone-else"
	for _, tc := range []struct {
		name, replaces string
		want           int
	}{
		{"no such dialog", unknown.String(), 481},
		{"early-only for a confirmed dialog", earlyOnly.String(), 486},
		{"malformed", known.CallID, 400},
	} {
		c := u.inviteWith(t, tc.replaces)
		if d, refusal := c.ReplacedDialog(); d != nil || refusal != tc.want {
			t.Errorf("%s: ReplacedDialog = %p, %d; want %d", tc.name, d, refusal, tc.want)
		}
		c.Reject(tc.want)
	}
	c := u.inviteWith(t, known.String(), unknown.String())
	if d, refusal := c.ReplacedDialog(); d != nil || refusal != 400 {
		t.Errorf("two Replaces: ReplacedDialog = %p, %d; want 400", d, refusal)
	}
	c.Reject(400)
}
