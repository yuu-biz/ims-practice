package hss_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/hssstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// The tests of this file run the instances of the HSS on
// internal/kvstore/hssstore, with the lock of each subscriber in the same
// store (internal/kvstore/kvlock), on each backend of
// storetest.Backends: the settings that an operator changes, and the
// pushes that a change calls for, as the instances of a deployment
// share them.

// trialAS and defaultAS are the application servers of the trial and
// the default profile, which tell a PPR of one from a PPR of the other.
const (
	trialAS   = "sip:trial." + realm + ":5060"
	defaultAS = "sip:mmtel." + realm + ":5060"
)

// sharedInstance is one instance of the HSS on kv, with two service
// profiles, and the maintenance API of its node.
type sharedInstance struct {
	*hss.HSS
	api *maintenance.Node
}

// newSharedInstance is an instance on kv as cfg sees it, whose claims of
// a push last lease.
func newSharedInstance(t *testing.T, kv kvstore.Store, cfg kvjson.Config, lease time.Duration) sharedInstance {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	criterion := func(server string) []ifc.InitialFilterCriteria {
		return []ifc.InitialFilterCriteria{{
			TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "MESSAGE"}}},
			ApplicationServer: ifc.ApplicationServer{ServerName: server},
		}}
	}
	h, err := hss.New(hss.Config{Realm: realm, VisitedNetworks: []string{realm},
		ServiceProfiles: map[string]hss.ServiceProfile{
			"default": {Criteria: criterion(defaultAS)},
			"trial":   {Criteria: criterion(trialAS)},
		},
		DefaultServiceProfile: "default", TestUserServiceProfile: "default",
		UserLocks:     kvlock.New(kv, kvlock.Config{Prefix: cfg.Prefix, Role: "hss"}),
		SweepInterval: 5 * time.Millisecond, PushLease: lease,
	}, hssstore.New(kv, cfg), node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	api, err := maintenance.New(maintenance.Config{Node: "hss", LogLevel: new(slog.LevelVar),
		Functions: []maintenance.Function{{Role: "hss"}}, Configuration: func() any { return struct{}{} },
		Settings: h.Settings()})
	if err != nil {
		t.Fatal(err)
	}
	return sharedInstance{h, api}
}

// changeDefaultProfile sends the change of the default profile to the
// trial one that the OAM server sends every instance of the role, with
// one generation.
func (i sharedInstance) changeDefaultProfile(t *testing.T) {
	t.Helper()
	if _, err := i.api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{Generation: 1,
		Settings: map[string]json.RawMessage{"hss.default_service_profile": json.RawMessage(`"trial"`)}}); err != nil {
		t.Fatal(err)
	}
}

// registered adds n subscribers through store and registers each with a
// SAR through instance, and returns their private identities in order.
func registered(t *testing.T, kv kvstore.Store, cfg kvjson.Config, instance *hss.HSS, n int) []string {
	t.Helper()
	store := hssstore.New(kv, cfg)
	var ids []string
	for i := range n {
		id := fmt.Sprintf("0010100004%05d@%s", i, realm)
		if err := store.Add(t.Context(), hss.Subscription{PrivateIdentity: id, PublicIdentities: []string{"sip:" + id},
			Password: "test-password-shared"}); err != nil {
			t.Fatal(err)
		}
		saa, err := cx.ParseServerAssignmentAnswer(exchange(t, instance, cx.ServerAssignmentRequest{Header: header,
			UserName: id, PublicIdentities: []string{"sip:" + id}, ServerName: scscf,
			ServerAssignmentType: cx.AssignRegistration}.Message()))
		if err != nil || !saa.Result.Success() {
			t.Fatalf("SAA for %s: %+v, %v", id, saa.Result, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// recordingSCSCF is the S-CSCF of every subscriber, which each instance
// of the HSS reaches. It records the private identity and the
// application server of each PPR it answers. With stuckAfter above
// zero, it answers that many PPRs and none after them: those wait until
// their sender gives them up, and stuck is closed when the first comes.
type recordingSCSCF struct {
	mu         sync.Mutex
	pushed     []pushed
	stuckAfter int
	stuck      chan struct{}
	once       sync.Once
}

// pushed is one PPR that the S-CSCF answered.
type pushed struct{ user, server string }

func (r *recordingSCSCF) Request(ctx context.Context, originHost string, m *diam.Message) (*diam.Message, error) {
	b, err := m.Serialize()
	if err != nil {
		return nil, err
	}
	req, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		return nil, err
	}
	ppr, err := cx.ParsePushProfileRequest(req)
	if err != nil {
		return nil, err
	}
	profile, err := cx.ParseUserProfile(ppr.UserData)
	if err != nil || len(profile.InitialFilterCriteria) != 1 {
		return nil, fmt.Errorf("the profile of the PPR for %s: %v", ppr.UserName, err)
	}
	r.mu.Lock()
	if r.stuckAfter > 0 && len(r.pushed) >= r.stuckAfter {
		r.mu.Unlock()
		r.once.Do(func() { close(r.stuck) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r.pushed = append(r.pushed, pushed{ppr.UserName, profile.InitialFilterCriteria[0].ApplicationServer.ServerName})
	r.mu.Unlock()
	return cx.PushProfileAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(req, diameter.Identity{OriginHost: originHost, OriginRealm: realm}), nil
}

// answered is what the S-CSCF has answered so far.
func (r *recordingSCSCF) answered() []pushed {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.pushed)
}

// awaitPushes waits until r has answered n PPRs and the store on kv
// records the rounds of the pushes as done for the settings in force
// (roundsDoneOn), then stops the instances, and returns what r answered.
// Every PPR of a round is answered before the round records it, so a
// PPR that should not have gone at all is among them.
func (r *recordingSCSCF) awaitPushes(t *testing.T, n int, kv kvstore.Store, cfg kvjson.Config, instances ...sharedInstance) []pushed {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); len(r.answered()) < n; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting 5 s for %d PPRs; %d came", n, len(r.answered()))
		}
	}
	roundsDoneOn(t, hssstore.New(kv, cfg))
	for _, i := range instances {
		i.Close()
	}
	return r.answered()
}

// roundsDoneOn waits until store records the push of each source as done
// for the name that its settings give the source.
func roundsDoneOn(t *testing.T, store hss.Store) {
	t.Helper()
	names := func(s hss.StoredSettings) map[hss.ProfileSource]string {
		return map[hss.ProfileSource]string{hss.ProfileOfTestUsers: s.TestUserServiceProfile, hss.ProfileOfOtherUsers: s.DefaultServiceProfile}
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		stored, ok, err := store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		done := ok
		for source, name := range names(stored) {
			p, recorded, err := store.ProfilePush(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			done = done && recorded && p.Done && p.Name == name
		}
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("gave up waiting 5 s for the rounds of the pushes to be done")
		}
	}
}

// checkOnceEach fails the test unless pushed holds one PPR of the trial
// profile for each of ids and for no one else.
func checkOnceEach(t *testing.T, pushed []pushed, ids []string) {
	t.Helper()
	var got []string
	for _, p := range pushed {
		got = append(got, p.user)
		if p.server != trialAS {
			t.Errorf("%s got a PPR of %s, want one of the trial profile", p.user, p.server)
		}
	}
	if slices.Sort(got); !slices.Equal(got, ids) {
		t.Errorf("the PPRs went to %v, want %v, each once", got, ids)
	}
}

// TestAChangeOfTheRoleOnTheStorePushesEachSubscriberOnce: the OAM server
// sends the change to both instances, and each served subscriber gets
// one PPR.
func TestAChangeOfTheRoleOnTheStorePushesEachSubscriberOnce(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			first := newSharedInstance(t, kv, cfg, 0)
			second := newSharedInstance(t, kv, cfg, 0)
			ids := registered(t, kv, cfg, first.HSS, 5)
			scscfs := &recordingSCSCF{}
			first.SetPeers(scscfs)
			second.SetPeers(scscfs)
			first.changeDefaultProfile(t)
			second.changeDefaultProfile(t)
			checkOnceEach(t, scscfs.awaitPushes(t, len(ids), kv, cfg, first, second), ids)
		})
	}
}

// TestAPushOnTheStoreGoesOnWhenItsInstanceStops: the instance that took
// the change stops with a PPR on its way, and the other goes on from
// that subscriber once the claim of the first has run out. Between them
// each subscriber has one PPR answered, and the ones after the stop are
// the other instance's.
func TestAPushOnTheStoreGoesOnWhenItsInstanceStops(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			const lease = 300 * time.Millisecond
			first := newSharedInstance(t, kv, cfg, lease)
			ids := registered(t, kv, cfg, first.HSS, 5)
			stopping := &recordingSCSCF{stuckAfter: 2, stuck: make(chan struct{})}
			first.SetPeers(stopping)
			first.changeDefaultProfile(t)
			select {
			case <-stopping.stuck:
			case <-time.After(5 * time.Second):
				t.Fatal("gave up waiting 5 s for the third PPR of the first instance")
			}
			first.Close()
			before := stopping.answered()

			second := newSharedInstance(t, kv, cfg, lease)
			scscfs := &recordingSCSCF{}
			second.SetPeers(scscfs)
			after := scscfs.awaitPushes(t, len(ids)-len(before), kv, cfg, second)
			checkOnceEach(t, append(before, after...), ids)
			for _, p := range after {
				if slices.ContainsFunc(before, func(b pushed) bool { return b.user == p.user }) {
					t.Errorf("the other instance pushed %s, which the first had reached", p.user)
				}
			}
		})
	}
}

// TestTwoChangesOnTheStoreArePushedEach: a change of the default profile
// and the change back, one right after the other, each push the
// subscriber, in their order, as the round of the second follows that
// of the first.
func TestTwoChangesOnTheStoreArePushedEach(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			instance := newSharedInstance(t, kv, cfg, 0)
			ids := registered(t, kv, cfg, instance.HSS, 1)
			scscfs := &recordingSCSCF{}
			instance.SetPeers(scscfs)
			for _, profile := range []string{"trial", "default"} {
				if _, err := instance.api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{
					Settings: map[string]json.RawMessage{"hss.default_service_profile": json.RawMessage(`"` + profile + `"`)}}); err != nil {
					t.Fatal(err)
				}
			}
			got := scscfs.awaitPushes(t, 2, kv, cfg, instance)
			if want := []pushed{{ids[0], trialAS}, {ids[0], defaultAS}}; !slices.Equal(got, want) {
				t.Errorf("the PPRs were %v, want %v", got, want)
			}
		})
	}
}

var _ ims.UserLocker = (*kvlock.Locker)(nil)
