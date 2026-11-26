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

// trialAS is the application server of the trial profile, which tells a
// PPR of that profile from one of the default profile.
const trialAS = "sip:trial." + realm + ":5060"

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
			"default": {Criteria: criterion("sip:mmtel." + realm + ":5060")},
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
// application server of each PPR. With stuckAfter above zero, it answers
// that many PPRs and none after them: the next one waits until its
// sender gives it up, and stuck is closed when it comes.
type recordingSCSCF struct {
	mu         sync.Mutex
	pushed     map[string][]string
	stuckAfter int
	stuck      chan struct{}
}

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
	n := 0
	for _, servers := range r.pushed {
		n += len(servers)
	}
	if r.stuckAfter > 0 && n >= r.stuckAfter {
		r.mu.Unlock()
		close(r.stuck)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if r.pushed == nil {
		r.pushed = map[string][]string{}
	}
	r.pushed[ppr.UserName] = append(r.pushed[ppr.UserName], profile.InitialFilterCriteria[0].ApplicationServer.ServerName)
	r.mu.Unlock()
	return cx.PushProfileAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(req, diameter.Identity{OriginHost: originHost, OriginRealm: realm}), nil
}

// awaitPushes waits until n PPRs have been answered, and a moment more
// for one that should not come, and returns them.
func (r *recordingSCSCF) awaitPushes(t *testing.T, n int) map[string][]string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		r.mu.Lock()
		got := 0
		for _, servers := range r.pushed {
			got += len(servers)
		}
		r.mu.Unlock()
		if got >= n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting 5 s for %d PPRs; %d came", n, got)
		}
	}
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pushed
}

// checkOnceEach fails the test unless pushed holds one PPR of the trial
// profile for each of ids and for no one else.
func checkOnceEach(t *testing.T, pushed map[string][]string, ids []string) {
	t.Helper()
	var got []string
	for id, servers := range pushed {
		got = append(got, id)
		if len(servers) != 1 || servers[0] != trialAS {
			t.Errorf("%s got the PPRs %v, want one of the trial profile", id, servers)
		}
	}
	if slices.Sort(got); !slices.Equal(got, ids) {
		t.Errorf("the PPRs went to %v, want %v", got, ids)
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
			checkOnceEach(t, scscfs.awaitPushes(t, len(ids)), ids)
		})
	}
}

// TestAPushOnTheStoreGoesOnWhenItsInstanceStops: the instance that took
// the change stops with a PPR on its way, and the other goes on from
// that subscriber once the claim of the first has run out.
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

			second := newSharedInstance(t, kv, cfg, lease)
			scscfs := &recordingSCSCF{}
			second.SetPeers(scscfs)
			checkOnceEach(t, scscfs.awaitPushes(t, len(ids)-2), ids[2:])
		})
	}
}

var _ ims.UserLocker = (*kvlock.Locker)(nil)
