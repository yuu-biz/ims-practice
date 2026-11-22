package mmtel_test

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// forkedCallee stands beyond the AS for a forking proxy and the two
// UASs behind it: it answers each INVITE with a 183 from each of two
// UASs, each with its own To tag, and then with a 200 from one of them
// (RFC 3261 13.2.2.4, 16.7). With reliable, the 183s are reliable, and
// the 200 comes once the 183 of the UAS that answers has its PRACK (RFC
// 3262 3).
//
// Each UAS answers the offer of an INVITE with SDP of its own: with
// reliable in its 183 only (RFC 3262 5), and without in its 183 and its
// 200 (RFC 3261 13.2.1). After an INVITE without an offer, which a test
// sends only with reliable, the first UAS offers in its 183 and the
// second in its 200.
type forkedCallee struct {
	// addr is where stack listens, known before it takes requests.
	addr      netip.AddrPort
	stack     *sip.Stack
	answering int
	reliable  bool
	// next lets the next response go.
	next chan struct{}
	// pracked gets the To tag of each PRACK.
	pracked chan string
	// acked gets the body of each ACK.
	acked chan []byte
}

func startForkedCallee(t *testing.T, answering int, reliable bool) *forkedCallee {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	f := &forkedCallee{addr: tr.LocalAddr(), answering: answering, reliable: reliable,
		next: make(chan struct{}), pracked: make(chan string, 4), acked: make(chan []byte, 4)}
	onACK := func(ack *sip.Message) {
		select {
		case f.acked <- ack.Body:
		default:
		}
	}
	f.stack, err = sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), OnRequest: f.onRequest, OnACK: onACK}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.stack.Close)
	return f
}

func (f *forkedCallee) Stack() *sip.Stack { return f.stack }

func branchTag(i int) string { return fmt.Sprintf("branch-%d", i) }

// branchMedia is where branch i takes its media.
func branchMedia(i int) netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr("192.0.2.2"), uint16(41000+1000*i))
}

func (f *forkedCallee) response(req *sip.Message, code, branch int) *sip.Message {
	resp := sip.NewResponse(req, code, branchTag(branch))
	for _, rr := range req.GetAll("Record-Route") {
		resp.Add("Record-Route", rr)
	}
	resp.Add("Contact", fmt.Sprintf("<sip:%s@%s>", branchTag(branch), f.addr))
	if f.reliable && code != 200 {
		resp.Add("Require", sip.Option100rel)
		resp.Add("RSeq", "1")
	}
	var body sdp.Session
	switch offer, err := sdp.Parse(req.Body); {
	case err == nil && !(f.reliable && code == 200):
		if body, err = sdp.AudioAnswer(offer, fmt.Sprint(7000+branch), branchMedia(branch), pcmuOnly); err != nil {
			return resp
		}
	case err != nil && (code == 183 && branch == 1 || code == 200 && branch == 2):
		body = sdp.Offer(fmt.Sprint(7000+branch), branchMedia(branch))
	default:
		return resp
	}
	resp.Set("Content-Type", "application/sdp")
	resp.Body = body.Bytes()
	return resp
}

func (f *forkedCallee) onRequest(st *sip.ServerTransaction) {
	if st.Request.Method != sip.INVITE {
		if st.Request.Method == sip.PRACK {
			to, _ := sip.ParseNameAddr(st.Request.Get("To"))
			tag, _ := to.Params.Get("tag")
			f.pracked <- tag
		}
		st.Respond(sip.NewResponse(st.Request, 200, ""))
		return
	}
	go func() {
		for branch := 1; branch <= 2; branch++ {
			st.Respond(f.response(st.Request, 183, branch))
			<-f.next
		}
		if f.reliable {
			for tag := range f.pracked {
				if tag == branchTag(f.answering) {
					break
				}
			}
		}
		st.Respond(f.response(st.Request, 200, f.answering))
	}()
}

// relayed is what the caller of out gets: the copy of each 183 of f,
// for each of which f waits before it sends the next response, and the
// final response.
func (f *forkedCallee) relayed(t *testing.T, out *sip.OutgoingCall) []*sip.Message {
	t.Helper()
	var got []*sip.Message
	for len(got) < 3 {
		select {
		case resp, ok := <-out.Responses():
			if !ok {
				t.Fatalf("the caller got %v and then the call ended: %v", statuses(got), out.Err())
			}
			got = append(got, resp)
			if len(got) < 3 {
				f.next <- struct{}{}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the caller got %v and then nothing", statuses(got))
		}
	}
	return got
}

// mediaOf is the address of the audio of the SDP in body.
func mediaOf(body []byte) netip.AddrPort {
	s, err := sdp.Parse(body)
	if err != nil || s.AudioMedia() == nil {
		return netip.AddrPort{}
	}
	return s.Address(*s.AudioMedia())
}

var callerMedia = netip.MustParseAddrPort("192.0.2.1:4000")

// answerAt is the caller's handler of the requests within its dialog
// with the AS: it answers each offer with its media at callerMedia, and
// passes each request on to got.
func answerAt(got chan<- *sip.Message) func(*sip.InDialogRequest) {
	return func(r *sip.InDialogRequest) {
		got <- r.Request
		offer, _ := sdp.Parse(r.Request.Body)
		a, _ := sdp.AudioAnswer(offer, "1", callerMedia, pcmuOnly)
		r.Accept(sip.Body{Type: "application/sdp", Content: a.Bytes()})
	}
}

// The INVITE forks beyond the AS: two UASs answer it with a 183 each,
// and one of them with the 200. The AS is one UAS to the caller (RFC
// 3261 8.2.6.2): the caller has one early dialog, whose session
// description is that of the first branch, and gets the 183 of the
// second branch without its SDP, since a UAC takes the first answer and
// ignores the rest (13.2.1). The 200 of the second branch does not
// bring it either. When no reliable 183 had the answer, the 200 has
// to carry it, and carries the same as the first 183 (13.2.1).
func TestForkBeyondTheAS(t *testing.T) {
	for _, reliable := range []bool{true, false} {
		for _, answering := range []int{1, 2} {
			t.Run(fmt.Sprintf("reliable %t, branch %d answers", reliable, answering), func(t *testing.T) {
				as := startAS(t, noServices{}, mmtel.Config{})
				f := startForkedCallee(t, answering, reliable)
				caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
				out, err := caller.Invite(viaAS(t, as, f, "term", pcmuOffer(t, callerMedia, pcmuOnly)))
				if err != nil {
					t.Fatal(err)
				}
				defer out.Bye()
				got := f.relayed(t, out)
				if !slices.Equal(statuses(got), []int{183, 183, 200}) {
					t.Fatalf("the caller got %v, want both 183s and the 200", statuses(got))
				}
				if mediaOf(got[0].Body) != branchMedia(1) {
					t.Errorf("the caller's first 183 has the SDP\n%s\nwant the first branch's", got[0].Body)
				}
				if len(got[1].Body) != 0 || got[1].Get("Content-Type") != "" {
					t.Errorf("the caller got a second session description in its early dialog:\n%s", got[1].Body)
				}
				var want []byte
				if !reliable {
					want = got[0].Body
				}
				if !bytes.Equal(got[2].Body, want) {
					t.Errorf("the 200 brought the caller the SDP\n%s\nwant\n%s", got[2].Body, want)
				}
			})
		}
	}
}

// When the branch that answers is not the one whose session
// description the caller got, the caller's media goes to that branch:
// once the caller has acknowledged the 200, the AS offers it the
// session of the branch, in an UPDATE to a caller that allows UPDATE
// (RFC 3311), and otherwise in a re-INVITE (RFC 3261 14.1). The offer
// goes on in the session that the caller has, that of the first branch,
// with its version one up (RFC 3264 8).
func TestSessionOfTheBranchThatAnswered(t *testing.T) {
	for _, reliable := range []bool{true, false} {
		for _, method := range []sip.Method{sip.UPDATE, sip.INVITE} {
			t.Run(fmt.Sprintf("reliable %t, %s", reliable, method), func(t *testing.T) {
				as := startAS(t, noServices{}, mmtel.Config{})
				f := startForkedCallee(t, 2, reliable)
				caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
				inv := viaAS(t, as, f, "term", pcmuOffer(t, callerMedia, pcmuOnly))
				if method == sip.INVITE {
					inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Allow", Value: "INVITE, ACK, CANCEL, BYE, PRACK"})
				}
				offers := make(chan *sip.Message, 1)
				inv.OnRequest = answerAt(offers)
				out, err := caller.Invite(inv)
				if err != nil {
					t.Fatal(err)
				}
				defer out.Bye()
				got := f.relayed(t, out)
				req := receive(t, offers, "offer at the caller")
				if req.Method != method {
					t.Errorf("the caller got the session of the branch that answered in a %s, want a %s", req.Method, method)
				}
				if m := mediaOf(req.Body); m != branchMedia(2) {
					t.Errorf("the %s points the caller's media at %v, want %v of the branch that answered", req.Method, m, branchMedia(2))
				}
				had, err := sdp.Parse(got[0].Body)
				if err != nil {
					t.Fatalf("the caller's first 183 has no SDP: %v", err)
				}
				offer, err := sdp.Parse(req.Body)
				if err != nil {
					t.Fatalf("the %s has no SDP: %v", req.Method, err)
				}
				want := had.Origin
				want.Version++
				if offer.Origin != want {
					t.Errorf("the %s has the o= line %+v, want %+v", req.Method, offer.Origin, want)
				}
			})
		}
	}
}

// After an INVITE without an offer, the first branch offers in its
// reliable 183, which the caller answers in its PRACK, and the branch
// that answers offers in its 200 (RFC 3262 5, RFC 3261 13.2.1). The AS
// offers the caller the session of that branch after the 200, and the
// caller's answer goes to the branch in the ACK (13.2.2.4).
func TestOfferOfTheBranchThatAnswered(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	f := startForkedCallee(t, 2, true)
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, f, "term", sip.Body{})
	offers := make(chan *sip.Message, 1)
	inv.OnRequest = answerAt(offers)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Bye()
	first := receive(t, out.Responses(), "the first 183")
	offer, err := sdp.Parse(first.Body)
	if err != nil {
		t.Fatalf("the first 183 has no offer: %v", err)
	}
	answer, err := sdp.AudioAnswer(offer, "1", callerMedia, pcmuOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.PRACK(first, sip.Body{Type: "application/sdp", Content: answer.Bytes()}); err != nil {
		t.Fatal(err)
	}
	f.next <- struct{}{}
	if second := receive(t, out.Responses(), "the second 183"); len(second.Body) != 0 {
		t.Errorf("the caller got a second session description in its early dialog:\n%s", second.Body)
	}
	f.next <- struct{}{}
	if final := receive(t, out.Responses(), "the 200"); final.StatusCode != 200 || len(final.Body) != 0 {
		t.Fatalf("the caller got %d with\n%s\nwant a 200 without SDP", final.StatusCode, final.Body)
	}
	if m := mediaOf(receive(t, offers, "offer at the caller").Body); m != branchMedia(2) {
		t.Errorf("the offer to the caller points its media at %v, want %v of the branch that answered", m, branchMedia(2))
	}
	if m := mediaOf(receive(t, f.acked, "ACK at the branch that answered")); m != callerMedia {
		t.Errorf("the ACK to the branch that answered points its media at %v, want the caller's %v", m, callerMedia)
	}
}

// With CAT, the caller has the tone of the MRF until the callee
// answers, and the UPDATE that ends the tone moves the caller's media
// to the branch that answered, whichever 183 came last (TS 24.182
// 4.5.5.3.7).
func TestToneOfAForkedCall(t *testing.T) {
	for _, answering := range []int{1, 2} {
		t.Run(fmt.Sprintf("branch %d answers", answering), func(t *testing.T) {
			var log events
			mrfc := fakeMRFC(t, &log, make(chan string, 1), make(chan struct{}))
			as := startAS(t, toneHSS{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc)})
			f := startForkedCallee(t, answering, true)
			caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
			inv := viaAS(t, as, f, "term", pcmuOffer(t, callerMedia, pcmuOnly))
			moved := make(chan netip.AddrPort, 1)
			inv.OnRequest = func(r *sip.InDialogRequest) {
				if r.Request.Method == sip.UPDATE {
					moved <- mediaOf(r.Request.Body)
				}
				offer, _ := sdp.Parse(r.Request.Body)
				a, _ := sdp.AudioAnswer(offer, "1", callerMedia, pcmuOnly)
				r.Accept(sip.Body{Type: "application/sdp", Content: a.Bytes()})
			}
			out, err := caller.Invite(inv)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Bye()
			got := f.relayed(t, out)
			if !slices.Equal(statuses(got), []int{183, 183, 200}) {
				t.Fatalf("the caller got %v, want both 183s and the 200", statuses(got))
			}
			if m := mediaOf(got[0].Body); m != netip.MustParseAddrPort("192.0.2.9:40000") {
				t.Errorf("the caller's first 183 points its media at %v, want the MRFP", m)
			}
			if len(got[1].Body) != 0 || len(got[2].Body) != 0 {
				t.Errorf("the caller got SDP in the second 183 or the 200 while it had the tone:\n%s\n%s", got[1].Body, got[2].Body)
			}
			if m := receive(t, moved, "UPDATE at the caller"); m != branchMedia(answering) {
				t.Errorf("the UPDATE that ends the tone moves the caller's media to %v, want %v of the branch that answered", m, branchMedia(answering))
			}
		})
	}
}

// A caller that does not take 199 (RFC 6228) gets the answer of a
// callee whose call may still be diverted only in the 200. When the
// INVITE forks beyond the AS, that is the answer of the branch that
// answered, whichever 183 came last.
func TestWithheldAnswerOfAForkedCall(t *testing.T) {
	doc := cdiv("5", forwardRule("cfb", "<busy/>", carolTel, ""))
	for _, answering := range []int{1, 2} {
		t.Run(fmt.Sprintf("branch %d answers", answering), func(t *testing.T) {
			as := startAS(t, &documents{docs: map[string]string{aliceURI: mmtelServices(doc)}}, mmtel.Config{})
			f := startForkedCallee(t, answering, true)
			caller := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
			inv := toServedUser(t, as, f, "reg", sip.HeaderField{Name: "Supported", Value: "100rel"})
			inv.Body = pcmuOffer(t, callerMedia, pcmuOnly)
			out, err := caller.Invite(inv)
			if err != nil {
				t.Fatal(err)
			}
			defer out.Bye()
			got := f.relayed(t, out)
			if !slices.Equal(statuses(got), []int{183, 183, 200}) {
				t.Fatalf("the caller got %v, want both 183s and the 200", statuses(got))
			}
			if len(got[0].Body) != 0 || len(got[1].Body) != 0 {
				t.Errorf("the caller got the callee's SDP before the 200:\n%s\n%s", got[0].Body, got[1].Body)
			}
			if m := mediaOf(got[2].Body); m != branchMedia(answering) {
				t.Errorf("the 200 points the caller's media at %v, want %v of the branch that answered", m, branchMedia(answering))
			}
		})
	}
}
