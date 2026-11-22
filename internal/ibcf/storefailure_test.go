package ibcf_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the IBCF on a Store that it cannot reach. The IBCF
// answers a request whose record it cannot write with 500 (RFC 3261
// 21.5.1), goes on with what it has already answered, and does not
// start without the records of its earlier process.

// An INVITE whose TrGW context cannot be kept in the record of its call
// gets 500, and goes no further: the context is released, and network
// B sees no INVITE.
func TestAnINVITEWhoseRecordCannotBeWrittenGets500(t *testing.T) {
	p := newPair(t, nil)
	p.a.store.down.Store(true)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	if resp := final(t, call); resp.StatusCode != 500 {
		t.Fatalf("the INVITE got %d, want 500", resp.StatusCode)
	}
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 0 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one in A, released", reservedA, reservedB)
	}
	if got := p.b.rec.requests("ue", sip.INVITE); len(got) != 0 {
		t.Errorf("network B's UE got %d INVITEs", len(got))
	}
}

// After an INVITE without an offer and a reliable 183 without one, the
// caller's PRACK has the offer (RFC 3262 5), which gets the call its
// TrGW context. When the record cannot then be written, the PRACK gets
// 500 and does not cross; the call keeps the context until it ends.
func TestARequestWhoseRecordCannotBeWrittenGets500(t *testing.T) {
	p := newPair(t, nil)
	p.a.store.down.Store(true)
	call, err := p.a.ue.Invite(sip.Invitation{
		RequestURI: p.b.sipURI(), From: "sip:" + p.a.number + "@" + p.a.domain + ";user=phone",
		Route: []string{p.a.ueRoute}, ManualPRACK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := p.b.nextCall(t)
	pracked := make(chan *sip.Message, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		if r.Request.Method == sip.PRACK {
			pracked <- r.Request
		}
		r.Accept(sip.Body{})
	})
	go in.Progress(183, sip.Body{})

	var progress *sip.Message
	select {
	case progress = <-call.Responses():
	case <-time.After(10 * time.Second):
		t.Fatal("no 183 reached the caller")
	}
	if progress.StatusCode != 183 || !progress.Requires(sip.Option100rel) {
		t.Fatalf("the caller got %d with Require %q, want a reliable 183", progress.StatusCode, progress.Get("Require"))
	}
	resp, err := call.PRACK(progress, p.a.offer(t, sdp.VoLTECodecs(), 1, ""))
	if err != nil || resp.StatusCode != 500 {
		t.Fatalf("the PRACK with the offer got %v %v, want 500", resp, err)
	}
	select {
	case prack := <-pracked:
		t.Errorf("a PRACK reached network B's UE: %s", prack.Get("CSeq"))
	case <-time.After(200 * time.Millisecond):
	}
	if _, reserved := p.b.trgw.open(); reserved != 0 {
		t.Errorf("network B's TrGW reserved %d contexts", reserved)
	}
	call.Cancel()
	waitDone(t, "the caller's call", call.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 0 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one in A, released", reservedA, reservedB)
	}
}

// Once the call is answered, the records that follow it are written
// after the response has gone, and no request waits for them: a call
// whose store goes down goes on, and ends, as any other.
func TestACallGoesOnWhenItsRecordCannotBeWritten(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	in.Handle(p.b.answerInDialog(nil))
	go in.Accept(p.b.answer(t, in.Request))
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the call got %d", resp.StatusCode)
	}
	p.a.store.down.Store(true)
	if resp, err := call.ReInvite(p.a.offer(t, sdp.VoLTECodecs(), 2, "")); err != nil || resp.StatusCode != 200 {
		t.Fatalf("re-INVITE with the store down: %v %v", resp, err)
	}
	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the callee's call", in.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
}

// An instance that cannot read the records of its earlier process does
// not start, so that it does not take calls while those of its earlier
// process stay up. Once the store is back, the next process ends them.
func TestAnInstanceThatCannotReadItsCallsDoesNotStart(t *testing.T) {
	p := newPair(t, nil)
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	go in.Accept(p.b.answer(t, in.Request))
	if resp := final(t, call); resp.StatusCode != 200 {
		t.Fatalf("the call got %d", resp.StatusCode)
	}
	inA, _ := planA.instanceHosts(0)
	deadline := time.Now().Add(5 * time.Second)
	for records := p.a.callsOf(t, inA); len(records) != 1 || records[0].Home == nil; records = p.a.callsOf(t, inA) {
		if time.Now().After(deadline) {
			t.Fatalf("records of the instance: %+v", records)
		}
		time.Sleep(10 * time.Millisecond)
	}

	p.a.store.down.Store(true)
	p.a.stopIBCF(t, 0)
	if err := p.a.listenIBCF(t, 0); err == nil {
		t.Fatal("an instance that cannot read its Store started")
	}

	p.a.store.down.Store(false)
	p.a.restartIBCF(t, 0)
	waitDone(t, "the caller's call", call.Done())
	waitDone(t, "the callee's call", in.Done())
	if reservedA, reservedB := p.waitReleased(t); reservedA != 1 || reservedB != 1 {
		t.Errorf("TrGW contexts reserved: A %d, B %d; want one each", reservedA, reservedB)
	}
}
