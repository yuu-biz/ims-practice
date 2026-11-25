package smsc

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file tries the SC while the operator has it out of service
// through the maintenance API of its node.

// manage gives sc the maintenance API of a node, as the node of the SC
// does, and returns the node.
func manage(t *testing.T, sc *SMSC, hssAvailable func() bool) *maintenance.Node {
	t.Helper()
	node, err := maintenance.New(maintenance.Config{
		Node:          role,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{sc.Function(hssAvailable)},
		Configuration: func() any { return map[string]string{"node": role} },
	})
	if err != nil {
		t.Fatal(err)
	}
	sc.Manage(node.ManagedFunction(role))
	return node
}

func hssUp() bool { return true }

// scState is the state of the SC in the maintenance API of node.
func scState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	state, err := node.State()
	if err != nil {
		t.Fatal(err)
	}
	return state.Functions[0]
}

// inHand is how much work the SC counts, which must be a count that the
// role gives.
func inHand(t *testing.T, node *maintenance.Node) int {
	t.Helper()
	f := scState(t, node)
	if f.Users == nil {
		t.Fatalf("the SC is %+v, want a count of its users", f)
	}
	return *f.Users
}

// setState makes the event that takes the SC to want, and returns its
// state after it.
func setState(t *testing.T, node *maintenance.Node, want maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: want, Operator: "test"})
	if err != nil {
		t.Fatalf("%s: %v", want, err)
	}
	return f
}

// waitFor waits up to ten seconds for cond, which work that ends in
// another goroutine brings about.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The SC is ENABLED while an instance of the HSS takes its S6c
// requests, and DISABLED without one: it could then neither check a
// recipient nor find the node that serves one. A shut down of an SC with
// nothing in its hands locks it at once (X.731 7.1.3.3).
func TestTheSCIsEnabledWithTheHSSAndLocksAtOnceWithNothingInHand(t *testing.T) {
	n := newNetwork(t)
	up := true
	node := manage(t, n.sc, func() bool { return up })

	if f := scState(t, node); f.OperationalState != maintenance.Enabled || f.Users == nil || *f.Users != 0 {
		t.Errorf("the SC is %+v, want ENABLED with no user", f)
	}
	up = false
	if f := scState(t, node); f.OperationalState != maintenance.Disabled {
		t.Errorf("the SC is %+v without the HSS, want DISABLED", f)
	}
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Errorf("shut down gave %+v, want LOCKED with nothing in hand", f)
	}
}

// A shut down SC finishes the delivery attempt it has begun, and then
// claims neither the next short message of that queue nor any other
// recipient: the queue is due again, and another instance delivers what
// is left of it. The shut down ends once the attempt has.
func TestAShutDownSCFinishesTheAttemptInHandAndLeavesTheQueue(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.submit(bob, hello(t, bob, 2))
	node := manage(t, n.sc, hssUp)

	// The MME holds the TFR of the retry until the test lets it go.
	release := make(chan struct{})
	// The MME lets the TFR go when the test ends too, so that a failed
	// wait below does not leave the delivery behind.
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	arrived := make(chan struct{})
	var once sync.Once
	n.nodes.set(mmeHost, func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		once.Do(func() { close(arrived) })
		<-release
		return sgd.MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}, nil
	})
	n.clock.Advance(DefaultRetrySchedule[0])
	// The sweep returns once it has started the attempt, and has found
	// nothing else due.
	n.sc.ProcessDue()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("no TFR of the retry reached the MME within 5 s")
	}
	if got := inHand(t, node); got != 1 {
		t.Errorf("the SC counts %d users during the attempt, want the one lease", got)
	}
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while the attempt is in hand", f)
	}

	letGo()
	done := make(chan struct{})
	go func() {
		n.sc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the delivery attempt did not end within 5 s of its TFA")
	}
	waitFor(t, func() bool { return scState(t, node).AdministrativeState == maintenance.Locked },
		"the SC to be LOCKED once its attempt ended")
	if got := len(n.nodes.sent()); got != 2 {
		t.Fatalf("%d TFRs, want the first attempt and the retry of the first message only", got)
	}
	if got := inHand(t, node); got != 0 {
		t.Errorf("the SC counts %d users once LOCKED, want none", got)
	}

	// The second short message is due at once, and an instance in
	// service takes it.
	other := n.newSC()
	n.nodes.set(mmeHost, accepts)
	sweep(t, other)
	sent := n.nodes.sent()
	if len(sent) != 3 {
		t.Fatalf("%d TFRs, want the other instance to deliver the second message", len(sent))
	}
	if d := tpdu(t, sent[2]).(sms.Deliver); !d.ServiceCentreTimeStamp.Equal(t0.Add(time.Second)) {
		t.Errorf("the other instance delivered %+v, want the second short message", d)
	}
}

// A locked SC still takes the short message of an OFR that reaches it,
// and the alert of the HSS, and delivers neither: both leave the queue
// due in the Store, where an instance in service claims it. Nor does it
// claim a retry whose time has come.
func TestALockedSCLeavesEveryDeliveryToAnInstanceInService(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(carol, hello(t, carol, 1))
	n.submit(alice, hello(t, alice, 1))
	node := manage(t, n.sc, hssUp)
	setState(t, node, maintenance.Locked)
	n.nodes.set(mmeHost, accepts)
	other := n.newSC()
	// deliveredTo are the users that the TFRs after the first two went to.
	deliveredTo := func() map[string]bool {
		got := map[string]bool{}
		for _, tfr := range n.nodes.sent()[2:] {
			got[tfr.UserName] = true
		}
		return got
	}

	if ofa := n.submit(bob, hello(t, bob, 1)); ofa.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("OFA = %+v while locked, want the short message taken", ofa)
	}
	if ala := n.alert(scAddress, carol); ala.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("ALA = %+v while locked, want success", ala)
	}
	if got := deliveredTo(); len(got) != 0 {
		t.Fatalf("the locked SC sent TFRs to %v, want none", got)
	}
	// Bob's short message and Carol's alerted queue are due now; Alice's
	// retry is not.
	sweep(t, other)
	if got := deliveredTo(); len(got) != 2 || !got[imsiOf[bob]] || !got[imsiOf[carol]] {
		t.Errorf("the other instance sent TFRs to %v, want Bob and Carol", got)
	}
	n.advance(DefaultRetrySchedule[0])
	if got := deliveredTo(); got[imsiOf[alice]] {
		t.Error("the locked SC claimed the retry of Alice's short message")
	}
	if got := inHand(t, node); got != 0 {
		t.Errorf("the locked SC counts %d users, want none", got)
	}
	sweep(t, other)
	if got := deliveredTo(); !got[imsiOf[alice]] {
		t.Error("the other instance did not take the retry of Alice's short message")
	}

	// Unlocked, the SC delivers again.
	setState(t, node, maintenance.Unlocked)
	n.submit(bob, hello(t, bob, 2))
	if sent := n.nodes.sent(); len(sent) != 6 || sent[5].UserName != imsiOf[bob] {
		t.Errorf("%d TFRs after the unlock, want a sixth to Bob", len(sent))
	}
}

// Once Wait returns, a shut down that the last delivery ended has taken
// effect: the node has been told that the SC is LOCKED, and has ended
// the connections that the deliveries sent on. A node that stops waits
// for the SC in the same way (Run).
func TestWaitReturnsOnceTheShutDownHasTakenEffect(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	var told atomic.Bool
	f := n.sc.Function(hssUp)
	f.AdministrativeStateChanged = func(_, to maintenance.AdministrativeState) {
		if to == maintenance.Locked {
			// The node takes a while to end its connections.
			time.Sleep(100 * time.Millisecond)
			told.Store(true)
		}
	}
	node, err := maintenance.New(maintenance.Config{
		Node:          role,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{f},
		Configuration: func() any { return map[string]string{"node": role} },
	})
	if err != nil {
		t.Fatal(err)
	}
	n.sc.Manage(node.ManagedFunction(role))
	letGo := n.holdTFRsTo(bob)

	n.clock.Advance(DefaultRetrySchedule[0])
	n.sc.ProcessDue()
	waitFor(t, func() bool { return n.sentTo(bob) == 2 }, "the retry to Bob")
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while the attempt is in hand", f)
	}
	letGo()
	done := make(chan struct{})
	go func() {
		n.sc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the delivery did not end within 10 s of its TFA")
	}
	if !told.Load() {
		t.Error("Wait returned before the node was told that the SC is LOCKED")
	}
}

// A request that the node is answering is a user of the SC until its
// answer is on its way, so a shut down waits for it, and an answer that
// is told twice counts once.
func TestARequestBeingAnsweredHoldsTheShutDown(t *testing.T) {
	n := newNetwork(t)
	node := manage(t, n.sc, hssUp)
	answered := n.sc.Answering()
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a request is answered", f)
	}
	answered()
	answered()
	if f := scState(t, node); f.AdministrativeState != maintenance.Locked || *f.Users != 0 {
		t.Errorf("the SC is %+v after the answer, want LOCKED with no user", f)
	}
}
