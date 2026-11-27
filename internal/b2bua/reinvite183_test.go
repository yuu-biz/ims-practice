package b2bua_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// reliableCallee is a callee on a stack of its own, whose responses the test
// writes, so that it answers a re-INVITE in a reliable 183 as the UA of
// package sip cannot. It answers the INVITE with 200 and the answer, and
// a re-INVITE with a reliable 183 that carries the answer, a 200 to the
// PRACK of that 183, and then a 200 with late, which is no body for a
// callee that sends no SDP after the 183.
func reliableCallee(t *testing.T, late sip.Body) *sip.Stack {
	t.Helper()
	tl, err := sip.Listen("127.0.0.1:0", discard)
	if err != nil {
		t.Fatal(err)
	}
	tag := sip.NewTag()
	var stack *sip.Stack
	pending := make(chan *sip.ServerTransaction, 1)
	stack, err = sip.NewStack(tl, sip.Config{SentBy: tl.LocalAddr().String(), Timers: timers, OnRequest: func(st *sip.ServerTransaction) {
		contact := "<sip:callee@" + tl.LocalAddr().String() + ">"
		switch st.Request.Method {
		case sip.INVITE:
			if tagOfTo(st.Request) == "" {
				ok := sip.NewResponse(st.Request, 200, tag)
				ok.Add("Contact", contact)
				ok.Add("Content-Type", answer.Type)
				ok.Body = answer.Content
				st.Respond(ok)
				return
			}
			progress := sip.NewResponse(st.Request, 183, tag)
			progress.Add("Contact", contact)
			progress.Add("Require", sip.Option100rel)
			progress.Add("RSeq", "1")
			progress.Add("Content-Type", answer.Type)
			progress.Body = answer.Content
			st.Respond(progress)
			pending <- st
		case sip.PRACK:
			st.Respond(sip.NewResponse(st.Request, 200, ""))
			reinvite := <-pending
			ok := sip.NewResponse(reinvite.Request, 200, tag)
			ok.Add("Contact", contact)
			if len(late.Content) > 0 {
				ok.Add("Content-Type", late.Type)
				ok.Body = late.Content
			}
			reinvite.Respond(ok)
		default:
			st.Respond(sip.NewResponse(st.Request, 200, ""))
		}
	}}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	return stack
}

func tagOfTo(m *sip.Message) string {
	to, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		return ""
	}
	tag, _ := to.Params.Get("tag")
	return tag
}

// TestReINVITEAnsweredInAReliable183ReachesTheCaller: the callee answers
// the caller's re-INVITE in a reliable 183, which the B2BUA acknowledges
// with a PRACK, and its 200 then carries no SDP. The caller's re-INVITE
// still gets a 200 with that answer: the B2BUA keeps the answer of the
// 183 for the 2xx that it gives the caller.
func TestReINVITEAnsweredInAReliable183ReachesTheCaller(t *testing.T) {
	reInviteAnsweredInAReliable183(t, sip.Body{})
}

// TestSDPOfThe2xxAfterAReliable183DoesNotReachTheCaller: the 200 that
// follows the 183 with the answer carries SDP of its own. The UA of the
// callee's leg took the answer of the 183 and ignores any later SDP
// (RFC 3261 13.2.1), so the caller gets the answer of the 183 too, and
// both legs have the same session.
func TestSDPOfThe2xxAfterAReliable183DoesNotReachTheCaller(t *testing.T) {
	reInviteAnsweredInAReliable183(t, answer2)
}

func reInviteAnsweredInAReliable183(t *testing.T, late sip.Body) {
	callee := reliableCallee(t, late)
	b2b := newB2BUA(t, &hooks{}, "sip:callee@"+callee.LocalAddr().String())
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	type result struct {
		resp *sip.Message
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := out.ReInvite(offer)
		done <- result{resp, err}
	}()
	r := within(t, "final response to the caller's re-INVITE", done)
	resp, err := r.resp, r.err
	if err != nil {
		t.Fatalf("re-INVITE: %v", err)
	}
	if resp.StatusCode != 200 || string(resp.Body) != string(answer.Content) {
		t.Errorf("the caller's re-INVITE got %d with %q, want 200 with the answer of the 183", resp.StatusCode, resp.Body)
	}
}
