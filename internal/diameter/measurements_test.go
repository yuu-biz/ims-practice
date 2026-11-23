package diameter

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// countingGroup is a peer group of the S-CSCF over peers, with the
// registry of its node to read the counts from. The requests of the
// tests are MARs, the command 303 of testRequest, so the dictionary
// that names the command has to be loaded.
func countingGroup(t *testing.T, peers ...*Peer) (*PeerGroup, *metrics.Registry) {
	t.Helper()
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	reg := metrics.NewRegistry()
	group, err := NewPeerGroup(reg, "scscf", peers...)
	if err != nil {
		t.Fatal(err)
	}
	return group, reg
}

// answeringCounters counts the requests that reach role, with the
// registry of its node to read the counts from. The requests of the
// tests are MARs, as those of countingGroup are.
func answeringCounters(t *testing.T, role string) (*Answering, *metrics.Registry) {
	t.Helper()
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	reg := metrics.NewRegistry()
	answering, err := NewAnswering(reg, role)
	if err != nil {
		t.Fatal(err)
	}
	return answering, reg
}

func metricsText(t *testing.T, reg *metrics.Registry) string {
	t.Helper()
	var text bytes.Buffer
	if err := reg.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	return text.String()
}

// hasSeries fails unless every line of want is a sample line of reg.
func hasSeries(t *testing.T, reg *metrics.Registry, want ...string) {
	t.Helper()
	text := metricsText(t, reg)
	for _, line := range want {
		if !strings.Contains(text, "\n"+line+"\n") {
			t.Errorf("no sample line %q in:\n%s", line, text)
		}
	}
}

// answeringResult is the handler of the instance named host: it answers
// with result, as the HSS does, in Result-Code or in Experimental-Result
// according to the kind of result.
func answeringResult(host string, result Result) Handler {
	return func(req *diam.Message) *diam.Message {
		// Answer(0) writes no Result-Code, so AddTo is what puts the
		// result where its kind belongs.
		answer := req.Answer(0)
		answer.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(host))
		result.AddTo(answer)
		return answer
	}
}

// TS 32.409 4.1.6.1 counts the MAR on transmission and 4.1.6.2 the MAA
// on receipt, each under the role that sent the request and the command
// it carries. The result of the answer is named, not numbered: a 2001 in
// Result-Code is DIAMETER_SUCCESS.
func TestPeerGroupCountsARequestAndItsAnswer(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	peer := openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), Base(DiameterSuccess)))
	group, reg := countingGroup(t, peer)

	if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
		t.Fatal(err)
	}
	hasSeries(t, reg,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 1`,
		`ims_diameter_answers_total{role="scscf",direction="received",command="MAR",result="DIAMETER_SUCCESS"} 1`)
}

// An Experimental-Result-Code is named apart from the Result-Code of the
// same number (RFC 6733 7.7): the failure causes of TS 32.409 4.1.6.3
// are those of TS 29.229 6.2.2, which travel there.
func TestPeerGroupCountsTheResultOfAnAnswer(t *testing.T) {
	for _, tt := range []struct {
		result Result
		want   string
	}{
		{Experimental(5001), "DIAMETER_ERROR_USER_UNKNOWN"},
		{Experimental(2001), "DIAMETER_FIRST_REGISTRATION"},
		{Base(DiameterUnableToComply), "DIAMETER_UNABLE_TO_COMPLY"},
		// The failure that an S-CSCF answers a PPR with when it cannot
		// take the user data, one cause of TS 32.409 4.1.7.3.
		{Experimental(5009), "DIAMETER_ERROR_NOT_SUPPORTED_USER_DATA"},
		// A result that the label does not name one by one, so that the
		// causes still add up to all the answers (metrics.OtherValue).
		{Experimental(5065), metrics.OtherValue},
	} {
		node, logs := newTestNode(scscf, testTimers)
		peer := openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), tt.result))
		group, reg := countingGroup(t, peer)

		if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
			t.Fatal(err)
		}
		hasSeries(t, reg,
			`ims_diameter_answers_total{role="scscf",direction="received",command="MAR",result="`+tt.want+`"} 1`)
	}
}

// A failover sends a second attempt, which TS 32.409 4.1.6.1 counts on
// its transmission as it counted the first. Both instances answered, so
// both answers are counted, the DIAMETER_TOO_BUSY among them.
func TestPeerGroupCountsEveryAttemptOfAFailover(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	group, reg := countingGroup(t,
		openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), Base(DiameterTooBusy))),
		openFarmPeer(t, node, logs, farmHost(2), answeringResult(farmHost(2), Base(DiameterSuccess))))

	if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
		t.Fatal(err)
	}
	hasSeries(t, reg,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 2`,
		`ims_diameter_answers_total{role="scscf",direction="received",command="MAR",result="DIAMETER_TOO_BUSY"} 1`,
		`ims_diameter_answers_total{role="scscf",direction="received",command="MAR",result="DIAMETER_SUCCESS"} 1`)
}

// An instance that does not answer in time leaves the request counted
// and no answer against it. The difference is the cause 'No Reply' of
// TS 32.409 4.1.6.3, which no result code names.
func TestPeerGroupCountsNoAnswerFromAPeerThatIsSilent(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	release := make(chan struct{})
	defer close(release)
	silent := func(req *diam.Message) *diam.Message {
		answer := answeringResult(farmHost(1), Base(DiameterSuccess))(req)
		<-release
		return answer
	}
	group, reg := countingGroup(t, openFarmPeer(t, node, logs, farmHost(1), silent))

	if _, err := group.Request(t.Context(), testRequest(node)); err == nil {
		t.Fatal("a silent instance answered")
	}
	hasSeries(t, reg, `ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 1`)
	if text := metricsText(t, reg); strings.Contains(text, "ims_diameter_answers_total{") {
		t.Errorf("an answer was counted although none came:\n%s", text)
	}
}

// A request that a peer refused was never transmitted, so it is no
// request of TS 32.409 4.1.6.1: only the attempt that went out on a
// connection is counted.
func TestPeerGroupCountsNoRequestThatWasRefused(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	group, reg := countingGroup(t,
		deadFarmPeer(t, node, farmHost(1)),
		openFarmPeer(t, node, logs, farmHost(2), answeringResult(farmHost(2), Base(DiameterSuccess))))

	if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
		t.Fatal(err)
	}
	hasSeries(t, reg,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 1`,
		`ims_diameter_answers_total{role="scscf",direction="received",command="MAR",result="DIAMETER_SUCCESS"} 1`)
}

// A request that the peer never took is still with its sender when the
// caller gives up, so it is no request of TS 32.409 4.1.6.1 either,
// although no peer refused it: only the attempt that went out on a
// connection is counted.
func TestPeerGroupCountsNoRequestThatThePeerNeverTook(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	// The state machine of this peer is not running, so nothing reads its
	// outbox, as nothing reads it while a loop is busy with something
	// else.
	group, reg := countingGroup(t, node.newPeer(farmHost(1)))

	// The caller gives up before the request has got anywhere.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := group.Request(ctx, testRequest(node))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancel of the caller", err)
	}
	if text := metricsText(t, reg); strings.Contains(text, "ims_diameter_requests_total{") {
		t.Errorf("a request that reached no connection was counted:\n%s", text)
	}
}

// The roles of one node count in one registry, each under its own role,
// as the roles of one node share the registry of the node.
func TestTheRolesOfANodeShareTheDiameterCounters(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	peer := openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), Base(DiameterSuccess)))
	reg := metrics.NewRegistry()
	for _, role := range []string{"icscf", "scscf"} {
		group, err := NewPeerGroup(reg, role, peer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
			t.Fatal(err)
		}
	}
	hasSeries(t, reg,
		`ims_diameter_requests_total{role="icscf",direction="sent",command="MAR"} 1`,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 1`)
}

// A role whose name the role label does not know would count under
// "other", which is a mistake in the node rather than a measurement, so
// the group, and the counter of the requests a role answers, say so
// when they are built. The MRFC speaks no Diameter at all.
func TestTheDiameterCountersRefuseARoleThatCountsNothing(t *testing.T) {
	if _, err := NewPeerGroup(metrics.NewRegistry(), "mrfc"); err == nil {
		t.Error("a group of a role that counts no Diameter messages was built")
	}
	if _, err := NewAnswering(metrics.NewRegistry(), "mrfc"); err == nil {
		t.Error("the answering counters of a role that counts no Diameter messages were built")
	}
}

// TS 32.409 4.2.12.1 counts a request on its receipt by the HSS and
// 4.2.12.2 the answer the HSS sends back, which is the other value of
// the direction label from the request a role sends out and the answer
// that comes back to it.
func TestAnsweringCountsARequestThatArrivedAndItsAnswer(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	answering, reg := answeringCounters(t, "hss")
	request := testRequest(node)

	answering.RequestReceived(request)
	answering.AnswerSent(request, answeringResult(farmHost(1), Base(DiameterSuccess))(request))

	hasSeries(t, reg,
		`ims_diameter_requests_total{role="hss",direction="received",command="MAR"} 1`,
		`ims_diameter_answers_total{role="hss",direction="sent",command="MAR",result="DIAMETER_SUCCESS"} 1`)
}

// The failures that TS 32.409 4.1.1.3.2.3 and 4.1.7.3 split by Diameter
// result code are the result label of the answer the S-CSCF sends,
// named as the result of an answer that came back is named.
func TestAnsweringCountsTheResultOfTheAnswerItSends(t *testing.T) {
	node, _ := newTestNode(scscf, testTimers)
	for _, tt := range []struct {
		result Result
		want   string
	}{
		{Base(DiameterSuccess), "DIAMETER_SUCCESS"},
		{Experimental(5001), "DIAMETER_ERROR_USER_UNKNOWN"},
		{Base(DiameterUnableToComply), "DIAMETER_UNABLE_TO_COMPLY"},
		// The failure that an S-CSCF answers a PPR with when it cannot
		// take the user data, one cause of TS 32.409 4.1.7.3.
		{Experimental(5009), "DIAMETER_ERROR_NOT_SUPPORTED_USER_DATA"},
		// A result that the label does not name one by one, so that the
		// causes still add up to all the answers (metrics.OtherValue).
		{Experimental(5065), metrics.OtherValue},
	} {
		answering, reg := answeringCounters(t, "scscf")
		request := testRequest(node)

		answering.RequestReceived(request)
		answering.AnswerSent(request, answeringResult(farmHost(1), tt.result)(request))

		hasSeries(t, reg,
			`ims_diameter_answers_total{role="scscf",direction="sent",command="MAR",result="`+tt.want+`"} 1`)
	}
}

// The two roles of a node that answer Diameter requests count in one
// registry, each under its own role, and the group of a role that also
// sends requests shares the same two metrics: the direction label tells
// the two ways apart.
func TestTheRolesOfANodeShareTheAnsweringCounters(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	peer := openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), Base(DiameterSuccess)))
	reg := metrics.NewRegistry()
	group, err := NewPeerGroup(reg, "scscf", peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := group.Request(t.Context(), testRequest(node)); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"hss", "scscf"} {
		answering, err := NewAnswering(reg, role)
		if err != nil {
			t.Fatal(err)
		}
		answering.RequestReceived(testRequest(node))
	}

	hasSeries(t, reg,
		`ims_diameter_requests_total{role="scscf",direction="sent",command="MAR"} 1`,
		`ims_diameter_requests_total{role="hss",direction="received",command="MAR"} 1`,
		`ims_diameter_requests_total{role="scscf",direction="received",command="MAR"} 1`)
}

// ims_diameter_peer_up is read at the scrape from the state of each
// peer: 1 for the open connection, 0 for the instance that is not
// there, and 0 for the peer that has stopped.
func TestPeerUpFollowsTheStateOfEachPeer(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	open := openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), Base(DiameterSuccess)))
	dead := deadFarmPeer(t, node, farmHost(2))
	reg := metrics.NewRegistry()
	if err := RegisterPeerUp(reg, []*Peer{open, dead}); err != nil {
		t.Fatal(err)
	}

	hasSeries(t, reg,
		`ims_diameter_peer_up{peer="`+farmHost(1)+`"} 1`,
		`ims_diameter_peer_up{peer="`+farmHost(2)+`"} 0`)
	open.Stop()
	// The gauge reads the peers at each scrape, so the series follows
	// the peer that has gone without anything writing to it.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(metricsText(t, reg), `ims_diameter_peer_up{peer="`+farmHost(1)+`"} 0`) {
		if time.Now().After(deadline) {
			t.Fatalf("the peer that stopped is still up:\n%s", metricsText(t, reg))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Two entries of one Origin-Host share one series, whichever of them
// the gauge reads first: a request for that host can go out while one
// of the two connections takes requests, so the series is 1 then, and 0
// only once neither does.
func TestPeerUpOfTwoEntriesOfOneHost(t *testing.T) {
	node, logs := newTestNode(scscf, testTimers)
	open := openFarmPeer(t, node, logs, farmHost(1), answeringResult(farmHost(1), Base(DiameterSuccess)))
	dead := deadFarmPeer(t, node, farmHost(1))
	for _, peers := range [][]*Peer{{open, dead}, {dead, open}} {
		reg := metrics.NewRegistry()
		if err := RegisterPeerUp(reg, peers); err != nil {
			t.Fatal(err)
		}
		hasSeries(t, reg, `ims_diameter_peer_up{peer="`+farmHost(1)+`"} 1`)
	}
}

// A node that connects to no peer has no peer to report, and a label
// needs at least one value, so it registers no gauge at all rather than
// one that is always empty.
func TestPeerUpOfANodeWithoutPeers(t *testing.T) {
	reg := metrics.NewRegistry()
	if err := RegisterPeerUp(reg, nil); err != nil {
		t.Fatal(err)
	}
	if text := metricsText(t, reg); strings.Contains(text, "ims_diameter_peer_up") {
		t.Errorf("a node without peers serves the gauge:\n%s", text)
	}
}
