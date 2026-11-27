package sip

import (
	"slices"
	"sync"
	"testing"
)

// leftOut are the option tags that the UA of these tests leaves out with
// every far end.
var leftOut = []string{Option100rel, OptionTimer}

// newBorderUA is a UA that leaves out the tags of leftOut, and the
// requests that its transport received.
func newBorderUA(t *testing.T, onCall func(*IncomingCall)) (*UA, func() []*Message) {
	t.Helper()
	var mu sync.Mutex
	var received []*Message
	tr, err := ListenObserved("127.0.0.1:0", testLogger(t), func(c Capture) {
		if !c.Sent && c.Message.IsRequest() {
			mu.Lock()
			defer mu.Unlock()
			received = append(received, c.Message)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ua, err := NewUA(tr, UAConfig{
		SentBy: tr.LocalAddr().String(), Timers: testTimers, Contact: "sip:border@" + tr.LocalAddr().String(),
		OnCall:         onCall,
		OptionsLeftOut: func(*Message, Destination, bool) []string { return leftOut },
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua, func() []*Message {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(received)
	}
}

// lists tells which of leftOut m lists in Supported or Require.
func lists(m *Message) []string {
	var got []string
	for _, tag := range leftOut {
		if m.Supports(tag) {
			got = append(got, tag)
		}
	}
	return got
}

func TestOptionsLeftOutOfWhatTheUASends(t *testing.T) {
	invites := make(chan *Message, 1)
	far := newUA(t, testTimers, callee, func(c *IncomingCall) {
		invites <- c.Request
		c.Progress(183, Body{})
		c.Accept(answer)
	})
	border, _ := newBorderUA(t, noCalls)
	out, err := border.Invite(invitation(far))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if got := lists(<-invites); got != nil {
		t.Errorf("the far end's INVITE lists %v", got)
	}
	// The UA keeps the INVITE as it went.
	if got := lists(out.Request); got != nil {
		t.Errorf("the UA's own INVITE lists %v", got)
	}
	if len(got) != 2 || got[0].StatusCode != 183 || got[0].Get("RSeq") != "" {
		t.Fatalf("the UA got %v, want an unreliable 183 and the 200", codes(got))
	}
	// A response is left as it came.
	if got := lists(got[1]); !slices.Equal(got, leftOut) {
		t.Errorf("the 200 lists %v of %v, want all as the far end sent them", got, leftOut)
	}
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsLeftOutOfWhatTheUAReceives(t *testing.T) {
	invites := make(chan *Message, 1)
	border, received := newBorderUA(t, func(c *IncomingCall) {
		invites <- c.Request
		c.Progress(183, Body{})
		c.Accept(answer)
	})
	far := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := far.Invite(invitation(border))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if got := lists(<-invites); got != nil {
		t.Errorf("the INVITE reached the user of the UA with %v", got)
	}
	// The UA answers as one that the caller offered no 100rel: the 183
	// is not reliable (RFC 3262 3).
	if len(got) != 2 || got[0].StatusCode != 183 || got[0].Get("RSeq") != "" || got[0].Requires(Option100rel) {
		t.Fatalf("the caller got %v, want an unreliable 183 and the 200", codes(got))
	}
	if got := lists(got[1]); got != nil {
		t.Errorf("the 200 lists %v", got)
	}
	// The transport recorded the INVITE as it came.
	if i := slices.IndexFunc(received(), func(m *Message) bool { return m.Method == INVITE }); i < 0 || !slices.Equal(lists(received()[i]), leftOut) {
		t.Errorf("the recorded INVITE does not list %v as the caller sent it", leftOut)
	}
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
}

func TestWithoutOptions(t *testing.T) {
	m := &Message{Method: INVITE}
	m.Add("Supported", "100rel, timer")
	m.Add("Require", "timer")
	m.Add("Proxy-Require", "foo")
	got := withoutOptions(m, []string{OptionTimer})
	if got == m {
		t.Fatal("withoutOptions changed the message it was given")
	}
	if !slices.Equal(got.GetAll("Supported"), []string{"100rel"}) || len(got.GetAll("Require")) != 0 || got.Get("Proxy-Require") != "foo" {
		t.Errorf("got Supported %v, Require %v and Proxy-Require %v; want 100rel, none and foo",
			got.GetAll("Supported"), got.GetAll("Require"), got.GetAll("Proxy-Require"))
	}
	if !slices.Equal(m.GetAll("Supported"), []string{"100rel", "timer"}) {
		t.Errorf("the message given has Supported %v now", m.GetAll("Supported"))
	}
	if withoutOptions(m, []string{"199"}) != m {
		t.Error("withoutOptions copied a message that lists none of the tags")
	}
}
