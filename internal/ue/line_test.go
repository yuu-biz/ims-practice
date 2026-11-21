package ue_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

func newTestUA(t *testing.T, user string, onCall func(*sip.IncomingCall)) *sip.UA {
	t.Helper()
	tr := listenTransport(t)
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy:  tr.LocalAddr().String(),
		Contact: "sip:" + user + "@" + tr.LocalAddr().String(),
		OnCall:  onCall,
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return ua
}

// finalStatus calls callee and delivers the status code of the final
// response.
func finalStatus(t *testing.T, caller *sip.UA, callee string) <-chan int {
	t.Helper()
	call, err := caller.Invite(sip.Invitation{RequestURI: callee, From: "sip:caller@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	status := make(chan int, 1)
	go func() {
		last := 0
		for resp := range call.Responses() {
			last = resp.StatusCode
		}
		status <- last
	}()
	return status
}

func wantStatus(t *testing.T, status <-chan int, want int) {
	t.Helper()
	select {
	case got := <-status:
		if got != want {
			t.Errorf("final response %d, want %d", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("no final response, want %d", want)
	}
}

func TestLineTakesOneCallAndIsBusyForTheNext(t *testing.T) {
	l := ue.NewLine(true)
	callee := newTestUA(t, "callee", l.Ring)
	caller := newTestUA(t, "caller", func(ic *sip.IncomingCall) { ic.Reject(486) })
	uri := "sip:callee@" + callee.Stack().LocalAddr().String()

	finalStatus(t, caller, uri)
	var first *sip.IncomingCall
	select {
	case first = <-l.Calls():
	case <-time.After(5 * time.Second):
		t.Fatal("the first call never reached the line")
	}
	// The first call is still ringing.
	wantStatus(t, finalStatus(t, caller, uri), 486)
	first.Reject(480)
	wantStatus(t, finalStatus(t, caller, uri), 486)
}

func TestLineOfACommandThatTakesNoCallIsBusy(t *testing.T) {
	callee := newTestUA(t, "callee", ue.NewLine(false).Ring)
	caller := newTestUA(t, "caller", func(ic *sip.IncomingCall) { ic.Reject(486) })
	wantStatus(t, finalStatus(t, caller, "sip:callee@"+callee.Stack().LocalAddr().String()), 486)
}
