package sip

import (
	"reflect"
	"strconv"
	"testing"
	"time"
)

// forked is a response to invite from one UAS behind a forking proxy:
// the UAS has its own To tag and its own Contact. With rseq not 0 the
// response is reliable.
func forked(invite *Message, code int, tag, contact string, rseq uint32) *Message {
	resp := NewResponse(invite, code, tag)
	resp.Add("Contact", "<"+contact+">")
	if rseq != 0 {
		resp.Add("Require", "100rel")
		resp.Add("RSeq", strconv.FormatUint(uint64(rseq), 10))
	}
	return resp
}

// RFC 3261 13.2.2.4: each UAS that answers a forked INVITE has a dialog
// of its own with the UAC, told apart by the To tag. Each dialog counts
// its reliable provisional responses and its CSeq numbers by itself,
// and the 2xx picks the dialog that the call goes on in.
func TestForkedINVITEHasADialogPerToTag(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	at := addrOf(peer).String()
	first, second := "sip:first@"+at, "sip:second@"+at
	out, err := callerUA.Invite(Invitation{RequestURI: "sip:" + callee + "@" + at, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)

	for _, uas := range []struct {
		tag, contact string
		code         int
		rseq         uint32
	}{{"first", first, 183, 1}, {"second", second, 180, 7}} {
		send(t, peer, forked(invite, uas.code, uas.tag, uas.contact, uas.rseq), callerUA.Stack())
		prack := readDatagram(t, peer)
		// RFC 3262 4: the PRACK goes within the dialog of its response,
		// and each dialog starts its CSeq numbers after the INVITE's.
		wantRAck := strconv.Itoa(int(uas.rseq)) + " 1 INVITE"
		if prack.Method != PRACK || prack.RequestURI != uas.contact || tagOf(prack.Get("To")) != uas.tag ||
			prack.Get("RAck") != wantRAck || prack.Get("CSeq") != "2 PRACK" {
			t.Fatalf("got %s %s to tag %q with RAck %q and CSeq %q, want PRACK %s to tag %q with %s and 2 PRACK",
				startLine(prack), prack.RequestURI, tagOf(prack.Get("To")), prack.Get("RAck"), prack.Get("CSeq"), uas.contact, uas.tag, wantRAck)
		}
		send(t, peer, NewResponse(prack, 200, ""), callerUA.Stack())
	}

	early := out.Dialogs()
	if len(early) != 2 {
		t.Fatalf("the call has %d dialogs, want one per To tag", len(early))
	}
	for i, tag := range []string{"first", "second"} {
		if d, _ := early[i].Dialog(); d.ID.RemoteTag != tag || d.State != DialogEarly {
			t.Errorf("dialog %d has remote tag %q in state %s, want %q and Early", i, d.ID.RemoteTag, d.State, tag)
		}
	}
	// Before the 2xx, the call's own methods work on the earliest early
	// dialog.
	if d, _ := out.Dialog(); d.ID.RemoteTag != "first" {
		t.Errorf("before the 2xx the call goes on in the dialog with tag %q, want first", d.ID.RemoteTag)
	}

	send(t, peer, forked(invite, 200, "second", second, 0), callerUA.Stack())
	ack := readDatagram(t, peer)
	if ack.Method != ACK || ack.RequestURI != second || tagOf(ack.Get("To")) != "second" || ack.Get("CSeq") != "1 ACK" {
		t.Fatalf("got %s %s to tag %q with CSeq %q, want ACK %s to tag second with 1 ACK",
			startLine(ack), ack.RequestURI, tagOf(ack.Get("To")), ack.Get("CSeq"), second)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{183, 180, 200}) {
		t.Fatalf("caller got %v, want 183, 180 and 200", codes(got))
	}
	if out.DialogOf(got[0]) != early[0] || out.DialogOf(got[2]) != early[1] {
		t.Error("DialogOf does not map the 183 to the first dialog and the 200 to the second")
	}
	if d, _ := out.Dialog(); d.ID.RemoteTag != "second" || d.State != DialogConfirmed {
		t.Errorf("after the 2xx the call goes on in the dialog with tag %q in state %s, want second and Confirmed", d.ID.RemoteTag, d.State)
	}

	// RFC 3261 13.2.2.4: 64*T1 after the first 2xx, the early dialogs
	// that no 2xx confirmed end.
	deadline := time.Now().Add(64*testTimers.T1 + 2*time.Second)
	for {
		if d, _ := early[0].Dialog(); d.State == DialogTerminated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the early dialog of the UAS that did not answer never ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-out.Done():
		t.Fatal("the end of the other early dialog ended the call")
	default:
	}

	done := make(chan error, 1)
	go func() { done <- out.Bye() }()
	bye := readDatagram(t, peer)
	// The PRACK took CSeq 2 in this dialog.
	if bye.Method != BYE || bye.RequestURI != second || tagOf(bye.Get("To")) != "second" || bye.Get("CSeq") != "3 BYE" {
		t.Fatalf("got %s %s to tag %q with CSeq %q, want BYE %s to tag second with 3 BYE",
			startLine(bye), bye.RequestURI, tagOf(bye.Get("To")), bye.Get("CSeq"), second)
	}
	send(t, peer, NewResponse(bye, 200, ""), callerUA.Stack())
	if err := <-done; err != nil {
		t.Errorf("Bye: %v", err)
	}
	waitDone(t, out.Done(), "caller")
}

// RFC 3261 13.2.2.4: when two UASs accept a forked INVITE, the UAC
// acknowledges both 2xx and ends the dialog it does not want with a
// BYE. The call goes on with the UAS that answered first.
func TestSecond2xxOfAForkGetsItsACKAndABYE(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	at := addrOf(peer).String()
	first, second := "sip:first@"+at, "sip:second@"+at
	out, err := callerUA.Invite(Invitation{RequestURI: "sip:" + callee + "@" + at, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)

	send(t, peer, forked(invite, 200, "first", first, 0), callerUA.Stack())
	if ack := readDatagram(t, peer); ack.Method != ACK || ack.RequestURI != first {
		t.Fatalf("got %s %s, want ACK %s", startLine(ack), ack.RequestURI, first)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Fatalf("caller got %v, want one 200", got)
	}

	send(t, peer, forked(invite, 200, "second", second, 0), callerUA.Stack())
	if ack := readDatagram(t, peer); ack.Method != ACK || ack.RequestURI != second || tagOf(ack.Get("To")) != "second" {
		t.Fatalf("got %s %s to tag %q, want ACK %s to tag second", startLine(ack), ack.RequestURI, tagOf(ack.Get("To")), second)
	}
	bye := readDatagram(t, peer)
	if bye.Method != BYE || bye.RequestURI != second || tagOf(bye.Get("To")) != "second" || bye.Get("CSeq") != "2 BYE" {
		t.Fatalf("got %s %s to tag %q with CSeq %q, want BYE %s to tag second with 2 BYE",
			startLine(bye), bye.RequestURI, tagOf(bye.Get("To")), bye.Get("CSeq"), second)
	}
	send(t, peer, NewResponse(bye, 200, ""), callerUA.Stack())

	select {
	case <-out.Done():
		t.Fatal("the BYE for the second UAS ended the call with the first")
	case <-time.After(5 * testTimers.T1):
	}
	if d, _ := out.Dialog(); d.ID.RemoteTag != "first" || d.State != DialogConfirmed {
		t.Errorf("the call goes on in the dialog with tag %q in state %s, want first and Confirmed", d.ID.RemoteTag, d.State)
	}
	if ds := out.Dialogs(); len(ds) != 2 {
		t.Errorf("the call has %d dialogs, want 2", len(ds))
	} else if d, _ := ds[1].Dialog(); d.State != DialogTerminated {
		t.Errorf("the dialog with the second UAS is %s, want Terminated", d.State)
	}
}
