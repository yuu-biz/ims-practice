package ue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// TestPhoneRegistersAgainWhenARequestGetsNoResponse is TS 24.229
// 5.1.2A.1.6: a request from the registered contact gets no response at
// all, and Timer F fires. The UE then takes its contact as bound to
// nothing and makes an initial registration. A P-CSCF that has lost the
// UE's registration discards its requests in this way (5.2.6.3.2A
// NOTE 2). The P-CSCF of this test registers each REGISTER and discards
// each other request.
func TestPhoneRegistersAgainWhenARequestGetsNoResponse(t *testing.T) {
	registers := make(chan *sip.Message, 4)
	var discarded atomic.Int32
	pcscf := stackConfig(t, sip.Config{
		Screen: func(req *sip.Message, _ sip.Destination) bool {
			if req.Method == sip.REGISTER {
				return true
			}
			discarded.Add(1)
			return false
		},
		OnRequest: func(st *sip.ServerTransaction) {
			resp := sip.NewResponse(st.Request, 200, sip.NewTag())
			resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
			resp.Add("P-Associated-URI", "<"+impu+">")
			resp.Add("Contact", st.Request.Get("Contact")+";expires=3600")
			st.Respond(resp)
			registers <- st.Request
		},
	})
	// Timer F is 64*T1: 640 ms here.
	fast := sip.Timers{T1: 10 * time.Millisecond, T2: 80 * time.Millisecond, T4: 50 * time.Millisecond}
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	contact := ue.NewContact(addr)
	line := ue.NewLine(true)
	ua, err := sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy:  addr.String(),
		Timers:  fast,
		Contact: contact.URI(),
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
		ua.Stack(), contact, quiet())
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
	nextRegister("first REGISTER")
	waitRegistered := func() {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !p.Status().Registered; {
			if time.Now().After(deadline) {
				t.Fatal("the phone does not say it is registered")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitRegistered()

	_, err = p.SendSMS(context.Background(), ue.OutgoingSMS{To: "+12025550102", Text: "hello"})
	if !errors.Is(err, sip.ErrTimeout) {
		t.Fatalf("SendSMS: %v, want a timeout", err)
	}
	nextRegister("REGISTER after the timeout")
	waitRegistered()
	if discarded.Load() == 0 {
		t.Error("the P-CSCF saw no request besides REGISTER")
	}
}
