package mmtel_test

import (
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
// UASs, each with its own To tag and SDP answer, and then with a 200
// from one of them (RFC 3261 13.2.2.4, 16.7). With reliable, the 183s
// are reliable and carry the answer, and the 200 comes without one
// once the 183 of the UAS that answers has its PRACK (RFC 3262 3, 5).
// Without, the 183s are not reliable, and the 200 carries the answer
// (RFC 3261 13.2.1).
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
}

func startForkedCallee(t *testing.T, answering int, reliable bool) *forkedCallee {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	f := &forkedCallee{addr: tr.LocalAddr(), answering: answering, reliable: reliable,
		next: make(chan struct{}), pracked: make(chan string, 4)}
	f.stack, err = sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), OnRequest: f.onRequest, OnACK: func(*sip.Message) {}}, quiet())
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
	if f.reliable {
		if code == 200 {
			return resp
		}
		resp.Add("Require", sip.Option100rel)
		resp.Add("RSeq", "1")
	}
	offer, err := sdp.Parse(req.Body)
	if err != nil {
		return resp
	}
	answer, err := sdp.AudioAnswer(offer, fmt.Sprint(7000+branch), branchMedia(branch), pcmuOnly)
	if err != nil {
		return resp
	}
	resp.Set("Content-Type", "application/sdp")
	resp.Body = answer.Bytes()
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

// The INVITE forks beyond the AS: two UASs answer it with a 183 each,
// and one of them with the 200. The AS is one UAS to the caller (RFC
// 3261 8.2.6.2): the caller has one early dialog, whose session
// description is that of the first branch, and gets the 183 of the
// second branch without its SDP, since a UAC takes the first answer and
// ignores the rest (13.2.1). The 200 of the second branch does not
// bring it either.
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
				if len(got[2].Body) != 0 && mediaOf(got[2].Body) != branchMedia(1) {
					t.Errorf("the 200 brought the caller the SDP\n%s\nwhich is not the first branch's", got[2].Body)
				}
			})
		}
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
