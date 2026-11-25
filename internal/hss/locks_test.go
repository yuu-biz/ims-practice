package hss_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

// This file runs two HSS instances on one store, as a network with two
// HSS nodes does, and follows one SAR at each of them for the same
// subscriber.

// secondSCSCF is the S-CSCF that sends the SAR to the second instance.
const secondSCSCF = "sip:scscf2." + realm + ":5060"

// atLock runs hook once, as soon as its locker holds the lock of a
// user: the moment the HSS enters the part of a request that reads the
// subscriber and then writes it.
type atLock struct {
	ims.UserLocker
	once sync.Once
	hook func()
}

func (l *atLock) Lock(ctx context.Context, user string) (func(), error) {
	unlock, err := l.UserLocker.Lock(ctx, user)
	if err == nil {
		l.once.Do(l.hook)
	}
	return unlock, err
}

// heldLock is the store of a Locker that reports when it finds the lock
// of a user already taken, which is how the second instance waits.
type heldLock struct {
	kvstore.Store
	found chan struct{}
	once  sync.Once
}

func (s *heldLock) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	v, err := s.Store.CompareAndSwap(ctx, key, version, value, ttl)
	if errors.Is(err, kvstore.ErrConflict) {
		s.once.Do(func() { close(s.found) })
	}
	return v, err
}

// wire is a request as its peer puts it on the wire, which is where
// HSS.Handle takes it from.
func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	b, err := m.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	got, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func registrationSARFrom(serverName string) *diam.Message {
	return cx.ServerAssignmentRequest{Header: header, UserName: impi, PublicIdentities: []string{impu},
		ServerName: serverName, ServerAssignmentType: cx.AssignRegistration}.Message()
}

// TestTwoInstancesSerialiseOneSubscriber: two HSS instances on one
// store each get a SAR that registers the same user at a different
// S-CSCF. Each reads the subscriber and then writes it, so the one that
// is second must find what the first wrote: it answers
// DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED and names the S-CSCF that
// now serves the user (TS 29.228 6.1.2.1), rather than taking the user
// for itself.
//
// The test puts the two instances in the order that a lock has to
// survive: the second starts while the first holds the lock and has not
// written yet. It goes on as soon as the second is waiting for the lock
// in the store, or, if nothing holds the second back, as soon as it has
// answered. With the lock per user in the memory of each process the
// second is not held back, its write lands between the read and the
// write of the first, and both answers come out the wrong way round.
func TestTwoInstancesSerialiseOneSubscriber(t *testing.T) {
	kv := provisioned(t)
	locker := func(store kvstore.Store) *kvlock.Locker {
		return kvlock.New(store, kvlock.Config{Role: "hss", Timeout: time.Second, Log: quiet()})
	}

	waiting := &heldLock{Store: kv, found: make(chan struct{})}
	second := newHSSWithLocks(t, kv, locker(waiting))
	secondSAR := wire(t, registrationSARFrom(secondSCSCF))

	var secondAnswer *diam.Message
	answered := make(chan struct{})
	locks := &atLock{UserLocker: locker(kv)}
	locks.hook = func() {
		go func() {
			defer close(answered)
			secondAnswer = second.Handle(secondSAR)
		}()
		select {
		case <-waiting.found:
		case <-answered:
		}
	}
	first := newHSSWithLocks(t, kv, locks)

	a, err := cx.ParseServerAssignmentAnswer(exchange(t, first, registrationSARFrom(scscf)))
	if err != nil {
		t.Fatal(err)
	}
	if a.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("the SAA of the instance that held the lock: %v, want DIAMETER_SUCCESS", a.Result)
	}

	<-answered
	b, err := cx.ParseServerAssignmentAnswer(secondAnswer)
	if err != nil {
		t.Fatal(err)
	}
	if b.Result != diameter.Experimental(cx.DiameterErrorIdentityAlreadyRegistered) {
		t.Errorf("the SAA of the instance that waited: %v, want DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED", b.Result)
	}
	if b.ServerName != scscf {
		t.Errorf("the SAA of the instance that waited names %q as the S-CSCF of the user, want %q", b.ServerName, scscf)
	}
}

// TestADeleteAndAnAddOfOneSubscriber: one instance removes a
// subscription while another adds it again, with the same private and
// public identities. The removal takes the subscriber's item away
// first, and the index items of its public identities and its entry in
// the listing after that, so an Add that lands in between takes over
// the index items it finds there and the rest of the removal then takes
// them away from it: the subscription is stored, but no request by a
// public identity finds it and the listing leaves it out.
//
// The test starts the POST at the second instance at that moment, and
// goes on as soon as the POST waits for the lock in the store, or, if
// nothing holds it back, as soon as it has been answered.
func TestADeleteAndAnAddOfOneSubscriber(t *testing.T) {
	const tel = "tel:+12025550101"
	body := `{"private_identity": "` + impi + `", "public_identities": ["` + impu + `", "` + tel + `"], "password": "test-password-1"}`
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			locker := func(store kvstore.Store) *kvlock.Locker {
				return kvlock.New(store, kvlock.Config{Prefix: cfg.Prefix, Role: "hss", Timeout: time.Second, Log: quiet()})
			}
			if w := do(newHSSOn(t, kv, cfg, locker(kv)).Provisioning(noSCSCFs{}), "POST", "/subscriptions", body); w.Code != http.StatusCreated {
				t.Fatalf("POST: %d %s", w.Code, w.Body)
			}

			waiting := &heldLock{Store: kv, found: make(chan struct{})}
			adding := newHSSOn(t, kv, cfg, locker(waiting)).Provisioning(noSCSCFs{})
			var added *httptest.ResponseRecorder
			answered := make(chan struct{})
			addMeanwhile := func() {
				go func() {
					defer close(answered)
					added = do(adding, "POST", "/subscriptions", body)
				}()
				select {
				case <-waiting.found:
				case <-answered:
				}
			}
			// The first write of the removal takes the subscriber's item
			// away, the second the index item of a public identity.
			removing := newHSSOn(t, storetest.BeforeWrite(kv, 2, addMeanwhile), cfg, locker(kv))
			if w := do(removing.Provisioning(noSCSCFs{}), "DELETE", "/subscriptions/"+impi, ""); w.Code != http.StatusNoContent {
				t.Fatalf("DELETE: %d %s", w.Code, w.Body)
			}
			<-answered
			if added.Code != http.StatusCreated {
				t.Fatalf("the POST that came during the DELETE: %d %s, want 201", added.Code, added.Body)
			}

			h := newHSSOn(t, kv, cfg, locker(kv))
			for _, public := range []string{impu, tel} {
				a, err := cx.ParseUserAuthorizationAnswer(exchange(t, h, cx.UserAuthorizationRequest{Header: header,
					UserName: impi, PublicIdentity: public, VisitedNetworkIdentifier: realm, UserAuthorizationType: cx.Registration}.Message()))
				if err != nil {
					t.Fatal(err)
				}
				if a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
					t.Errorf("UAR for %s after the DELETE and the POST: %v, want DIAMETER_FIRST_REGISTRATION", public, a.Result)
				}
			}
			w := do(h.Provisioning(noSCSCFs{}), "GET", "/subscriptions", "")
			var listed []struct {
				PrivateIdentity string `json:"private_identity"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &listed); w.Code != http.StatusOK || err != nil {
				t.Fatalf("GET /subscriptions: %d %s %v", w.Code, w.Body, err)
			}
			if len(listed) != 1 || listed[0].PrivateIdentity != impi {
				t.Errorf("the listing after the DELETE and the POST: %+v, want %s alone", listed, impi)
			}
		})
	}
}

// TestALockThatAStoppedInstanceLeft: an instance stopped while it held
// the lock of a subscriber, which stays taken until its lease runs out.
// A SAR for that subscriber at another instance gets
// DIAMETER_UNABLE_TO_COMPLY before the S-CSCF stops waiting for the
// answer (5 s), rather than an answer after the S-CSCF has given the
// request up, for a registration that it then never learns of.
func TestALockThatAStoppedInstanceLeft(t *testing.T) {
	kv := provisioned(t)
	locker := func() *kvlock.Locker {
		return kvlock.New(kv, kvlock.Config{Role: "hss", Timeout: time.Second, Log: quiet()})
	}
	if _, err := locker().Lock(context.Background(), impi); err != nil {
		t.Fatal(err)
	}
	h := newHSSWithLocks(t, kv, locker())
	start := time.Now()
	a, err := cx.ParseServerAssignmentAnswer(h.Handle(wire(t, registrationSARFrom(scscf))))
	if err != nil {
		t.Fatal(err)
	}
	if a.Result != diameter.Base(diameter.DiameterUnableToComply) {
		t.Errorf("the SAA while another instance holds the lock: %v, want DIAMETER_UNABLE_TO_COMPLY", a.Result)
	}
	if took := time.Since(start); took >= 5*time.Second {
		t.Errorf("the SAA took %v, longer than the S-CSCF waits for it", took)
	}
}
