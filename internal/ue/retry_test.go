package ue_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// TestRetryDelay is the retry delay of RFC 5626 4.5 with its defaults,
// which TS 24.229 5.1.1.2.1 has the UE use: from 50 to 100% of
// min(1800 s, 30 s * 2^consecutive-failures).
func TestRetryDelay(t *testing.T) {
	for _, c := range []struct {
		failures int
		min, max time.Duration
	}{
		// The first retry comes 30 to 60 s after the first failure.
		{1, 30 * time.Second, 60 * time.Second},
		{2, 60 * time.Second, 120 * time.Second},
		// The example of RFC 5626 4.5: three failures, 240 s at most.
		{3, 120 * time.Second, 240 * time.Second},
		{5, 480 * time.Second, 960 * time.Second},
		// From here on max-time holds: 15 to 30 minutes.
		{6, 900 * time.Second, 1800 * time.Second},
		{100, 900 * time.Second, 1800 * time.Second},
	} {
		if got := ue.RetryDelay(c.failures, 0); got != c.min {
			t.Errorf("%d failures, lowest delay %v, want %v", c.failures, got, c.min)
		}
		if got := ue.RetryDelay(c.failures, 0.999999); got < c.max-time.Millisecond || got > c.max {
			t.Errorf("%d failures, highest delay %v, want just under %v", c.failures, got, c.max)
		}
	}
}

// TestBackoffKeepsToRetryAfter is TS 24.229 5.1.1.2.1: a failure with
// Retry-After waits that long, and the count of failures starts again.
func TestBackoffKeepsToRetryAfter(t *testing.T) {
	var b ue.RegistrationBackoff
	b.Failed(errors.New("no response"))
	b.Failed(errors.New("no response"))
	if got := b.Failed(&ue.RegisterFailure{StatusCode: 500, RetryAfter: 120 * time.Second}); got != 120*time.Second {
		t.Errorf("wait after Retry-After 120: %v", got)
	}
	if got := b.Failed(errors.New("no response")); got < 30*time.Second || got > 60*time.Second {
		t.Errorf("wait after the next failure: %v, want the delay of a first failure", got)
	}
}

// TestPhoneRetriesAFailedReregistration is TS 24.229 5.1.1.4.1 and
// 5.1.1.2.1: the refresh of the registration gets 500, so the UE makes
// an initial registration at once. That fails, and so do the next two.
// Before each of them the UE waits the retry delay of RFC 5626 4.5,
// which doubles with each failure in a row. The fourth initial
// registration succeeds, and a later failure starts the count again.
// The test records the waits that the phone asks for and lets each end
// at once.
func TestPhoneRetriesAFailedReregistration(t *testing.T) {
	waits := make(chan time.Duration, 8)
	ue.SetRetryTimer(t, func(d time.Duration) *time.Timer {
		waits <- d
		return time.NewTimer(0)
	})
	// A REGISTER without the answer to a challenge gets 401. Those with
	// one get the status of the script in turn; each 200 has expires 1,
	// so that the UE refreshes after half a second, and the last one an
	// hour.
	script := []int{200, 500, 500, 500, 500, 200, 500, 500, 200}
	expires := []string{"1", "", "", "", "", "1", "", "", "3600"}
	registers := make(chan *sip.Message, 2*len(script)+1)
	var answered atomic.Int32
	pcscf := stackConfig(t, sip.Config{
		OnRequest: func(st *sip.ServerTransaction) {
			if st.Request.Method != sip.REGISTER {
				st.Respond(sip.NewResponse(st.Request, 403, sip.NewTag()))
				return
			}
			registers <- st.Request
			if !answersChallenge(st.Request) {
				resp := sip.NewResponse(st.Request, 401, sip.NewTag())
				resp.Add("WWW-Authenticate", `Digest realm="`+domain+`", nonce="bm9uY2U=", algorithm=MD5, qop="auth"`)
				st.Respond(resp)
				return
			}
			n := min(int(answered.Add(1))-1, len(script)-1)
			resp := sip.NewResponse(st.Request, script[n], sip.NewTag())
			if script[n] == 200 {
				resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
				resp.Add("P-Associated-URI", "<"+impu+">")
				contact, _, _ := strings.Cut(st.Request.Get("Contact"), ";expires=")
				resp.Add("Contact", contact+";expires="+expires[n])
			}
			st.Respond(resp)
		},
	})
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	line := ue.NewLine(true)
	ua, err := sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy:  addr.String(),
		Contact: ue.ContactURI(impu, addr),
		OnCall:  line.Ring,
		Locate: func(sip.URI) (sip.Destination, error) {
			return sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()}, nil
		},
	}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	u := ue.New(ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain,
		PCSCF: pcscf.LocalAddr(), PCSCFURI: "sip:pcscf." + domain + ":5060", Features: ue.DefaultFeatures, SCAddress: scAddress},
		ua.Stack(), addr, quiet())
	p := ue.NewPhone(u, ua, line, ue.PhoneConfig{MediaListen: "127.0.0.1:0"}, quiet())
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- p.Serve(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after its context ended")
		}
	})

	nextWait := func(failures int) {
		t.Helper()
		lowest := ue.RetryDelay(failures, 0)
		select {
		case d := <-waits:
			if d < lowest || d > 2*lowest {
				t.Errorf("wait after %d failures in a row: %v, want from %v to %v", failures, d, lowest, 2*lowest)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the phone did not wait after %d failures in a row", failures)
		}
	}
	nextRegister := func(what string) *sip.Message {
		t.Helper()
		select {
		case m := <-registers:
			return m
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s", what)
			return nil
		}
	}

	// An initial registration starts without the answer to an earlier
	// challenge, and gets challenged (TS 24.229 5.1.1.2.3, 5.1.1.5.4); a
	// refresh answers the challenge it had.
	initial := func(what string) {
		t.Helper()
		if m := nextRegister(what); answersChallenge(m) {
			t.Errorf("%s is no initial registration: Authorization %q", what, m.Get("Authorization"))
		}
		nextRegister("answer to the challenge of the " + what)
	}
	refresh := func(what string) {
		t.Helper()
		if m := nextRegister(what); !answersChallenge(m) {
			t.Errorf("%s carries no answer to the challenge: Authorization %q", what, m.Get("Authorization"))
		}
	}

	noWait := func(what string) {
		t.Helper()
		select {
		case d := <-waits:
			t.Errorf("the phone waits %v %s", d, what)
		default:
		}
	}

	initial("first REGISTER")
	refresh("refresh")
	initial("REGISTER after the failed refresh")
	noWait("between the failed refresh and the initial registration")
	for failures := 1; failures <= 3; failures++ {
		nextWait(failures)
		initial("REGISTER after the wait")
	}
	refresh("refresh of the new registration")
	initial("REGISTER after the second failed refresh")
	noWait("between the second failed refresh and the initial registration")
	nextWait(1)
	initial("REGISTER after the wait")
	for deadline := time.Now().Add(5 * time.Second); !p.Status().Registered; {
		if time.Now().After(deadline) {
			t.Fatal("the phone does not say it is registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	noWait("once registered")
}

// answersChallenge tells whether a REGISTER answers a challenge: the
// initial one has an empty response (TS 24.229 5.1.1.2.3 item a).
func answersChallenge(m *sip.Message) bool {
	return !strings.Contains(m.Get("Authorization"), `response=""`)
}
