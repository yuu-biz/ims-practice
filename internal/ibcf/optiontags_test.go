package ibcf_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file checks that an option tag that a profile removes stays off
// every message that the IBCF sends the peer: the ones that cross the
// border, and the ones that its own UA writes on its leg of a call.

// atTheNNI is what network n's IBCF received from the other network's
// IBCF: for each kind of message, such as "re-INVITE" or "200 BYE", the
// option tags that its Supported, Require and Proxy-Require listed.
func atTheNNI(n *network) map[string][]string {
	got := map[string][]string{}
	for _, c := range n.rec.received() {
		if c.node != "ibcf1-external" {
			continue
		}
		m := c.Message
		kind := string(m.Method)
		switch {
		case !m.IsRequest():
			cseq, _ := sip.ParseCSeq(m.Get("CSeq"))
			kind = fmt.Sprintf("%d %s", m.StatusCode, cseq.Method)
		case m.Method == sip.INVITE && hasToTag(m):
			kind = "re-INVITE"
		}
		tags := got[kind]
		for _, name := range []string{"Supported", "Require", "Proxy-Require"} {
			tags = append(tags, m.GetAll(name)...)
		}
		slices.Sort(tags)
		got[kind] = slices.Compact(tags)
	}
	return got
}

func hasToTag(m *sip.Message) bool {
	to, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		return false
	}
	_, ok := to.Params.Get("tag")
	return ok
}

// noneOf fails for each kind of message in got that lists one of tags,
// and for each kind in want that did not come at all.
func noneOf(t *testing.T, got map[string][]string, want []string, tags ...string) {
	t.Helper()
	for _, kind := range want {
		if _, ok := got[kind]; !ok {
			t.Errorf("no %s reached the peer; got %v", kind, got)
		}
	}
	for kind, listed := range got {
		for _, tag := range tags {
			if slices.Contains(listed, tag) {
				t.Errorf("a %s to the peer lists %s, which the profile removes: %v", kind, tag, listed)
			}
		}
	}
}

// allOf fails for each kind in want whose messages do not list all of
// tags.
func allOf(t *testing.T, got map[string][]string, want []string, tags ...string) {
	t.Helper()
	for _, kind := range want {
		for _, tag := range tags {
			if !slices.Contains(got[kind], tag) {
				t.Errorf("a %s to the peer lists %v, want %s among them", kind, got[kind], tag)
			}
		}
	}
}

// profileOfB has network A's profile of network B give policy to each
// of tags.
func profileOfB(policy interconnect.OptionTagPolicy, tags ...string) func(ofB, _ *interconnect.Profile) {
	return func(ofB, _ *interconnect.Profile) {
		ofB.OptionTags = map[string]interconnect.OptionTagPolicy{}
		for _, tag := range tags {
			ofB.OptionTags[tag] = policy
		}
	}
}

// A call from network A, whose profile of network B removes timer and
// replaces: neither tag reaches network B in any request or response
// that network A's IBCF sends, from the INVITE to the BYE. With a
// profile that passes them, its own UA lists both in each request
// within the dialog, and in its 200 to a re-INVITE.
func TestRemovedOptionTagsStayOffTheMessagesOfACallThatLeaves(t *testing.T) {
	tags := []string{sip.OptionTimer, sip.OptionReplaces}
	for _, policy := range []interconnect.OptionTagPolicy{interconnect.OptionPass, interconnect.OptionRemove} {
		t.Run(string(policy), func(t *testing.T) {
			p := newPair(t, profileOfB(policy, tags...))
			call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
			in := p.b.nextCall(t)
			in.Handle(p.b.answerInDialog(nil))
			// Reliable, so that network A's IBCF sends a PRACK across.
			if err := in.Progress(183, p.b.answer(t, in.Request)); err != nil {
				t.Fatalf("183: %v", err)
			}
			go in.Accept(sip.Body{})
			if ok := final(t, call); ok.StatusCode != 200 {
				t.Fatalf("the INVITE got %d, want 200", ok.StatusCode)
			}
			for _, step := range []struct {
				what string
				send func() (*sip.Message, error)
			}{
				{"the caller's re-INVITE", func() (*sip.Message, error) { return call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 2, "")) }},
				{"the caller's UPDATE", func() (*sip.Message, error) { return call.Update(p.a.offer(t, sdp.VoLTECodecs(), 3, "")) }},
				{"the callee's re-INVITE", func() (*sip.Message, error) { return in.ReInvite(p.b.offer(t, sdp.VoLTECodecs(), 4, "")) }},
				{"the callee's UPDATE", func() (*sip.Message, error) { return in.Update(p.b.offer(t, sdp.VoLTECodecs(), 5, "")) }},
			} {
				if resp, err := step.send(); err != nil || resp.StatusCode != 200 {
					t.Fatalf("%s got %v %v, want 200", step.what, resp, err)
				}
			}
			if err := call.Bye(); err != nil {
				t.Fatalf("BYE: %v", err)
			}
			waitDone(t, "the callee's call after the caller's BYE", in.Done())

			got := atTheNNI(p.b)
			if policy == interconnect.OptionPass {
				allOf(t, got, []string{"PRACK", "re-INVITE", "UPDATE", "BYE", "200 INVITE"}, tags...)
				return
			}
			noneOf(t, got, []string{"INVITE", "PRACK", "re-INVITE", "UPDATE", "BYE", "200 INVITE", "200 UPDATE"}, tags...)
		})
	}
}

// When the caller's Supported lists only the tag that the profile
// removes, the INVITE that leaves has none of the caller's tags, and
// the IBCF does not fill the gap with its own Supported, which lists
// the tag.
func TestRemovedOptionTagStaysOffAnInviteThatListedOnlyIt(t *testing.T) {
	p := newPair(t, profileOfB(interconnect.OptionRemove, sip.OptionTimer))
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil, sip.HeaderField{Name: "Supported", Value: sip.OptionTimer})
	in := p.b.nextCall(t)
	go in.Accept(p.b.answer(t, in.Request))
	if ok := final(t, call); ok.StatusCode != 200 {
		t.Fatalf("the INVITE got %d, want 200", ok.StatusCode)
	}
	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the callee's call after the caller's BYE", in.Done())
	noneOf(t, atTheNNI(p.b), []string{"INVITE", "BYE"}, sip.OptionTimer)
}

// A call from network B, for which network A's profile removes 100rel
// and timer. Network A's IBCF takes the INVITE as one without them: it
// sends network B the 183 of its callee without reliability, which it
// would otherwise add (RFC 3262 3), and neither tag reaches network B
// in its responses or in its requests within the dialog.
func TestRemovedOptionTagsStayOffTheMessagesOfACallThatComesIn(t *testing.T) {
	tags := []string{sip.Option100rel, sip.OptionTimer}
	for _, policy := range []interconnect.OptionTagPolicy{interconnect.OptionPass, interconnect.OptionRemove} {
		t.Run(string(policy), func(t *testing.T) {
			p := newPair(t, profileOfB(policy, tags...))
			call := p.b.invite(t, p.a.sipURI(), p.b.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
			in := p.a.nextCall(t)
			in.Handle(p.a.answerInDialog(nil))
			if err := in.Progress(183, sip.Body{}); err != nil {
				t.Fatalf("183: %v", err)
			}
			go in.Accept(p.a.answer(t, in.Request))
			if ok := final(t, call); ok.StatusCode != 200 {
				t.Fatalf("the INVITE got %d, want 200", ok.StatusCode)
			}
			if resp, err := in.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 2, "")); err != nil || resp.StatusCode != 200 {
				t.Fatalf("the callee's re-INVITE got %v %v, want 200", resp, err)
			}
			if resp, err := in.Update(p.a.offer(t, sdp.VoLTECodecs(), 3, "")); err != nil || resp.StatusCode != 200 {
				t.Fatalf("the callee's UPDATE got %v %v, want 200", resp, err)
			}
			if err := in.Bye(); err != nil {
				t.Fatalf("BYE: %v", err)
			}
			waitDone(t, "the caller's call after the callee's BYE", call.Done())

			got := atTheNNI(p.b)
			prack := len(p.a.rec.requests("ibcf1-external", sip.PRACK))
			if policy == interconnect.OptionPass {
				allOf(t, got, []string{"183 INVITE"}, sip.Option100rel)
				allOf(t, got, []string{"200 INVITE", "re-INVITE", "UPDATE", "BYE"}, tags...)
				if prack != 1 {
					t.Errorf("network A's IBCF received %d PRACKs from the peer, want 1 for its reliable 183", prack)
				}
				return
			}
			noneOf(t, got, []string{"183 INVITE", "200 INVITE", "re-INVITE", "UPDATE", "BYE"}, tags...)
			for _, c := range p.b.rec.received() {
				if c.node == "ibcf1-external" && c.Message.StatusCode == 183 && c.Message.Get("RSeq") != "" {
					t.Errorf("the 183 to the peer has RSeq %s, as a reliable one has", c.Message.Get("RSeq"))
				}
			}
			if prack != 0 {
				t.Errorf("network A's IBCF received %d PRACKs from the peer, want none", prack)
			}
		})
	}
}
