package hssstore_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/hssstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. The instance that stops learns that
// the store failed, never that there is no subscriber or that another
// instance changed it first, so that the HSS answers
// DIAMETER_UNABLE_TO_COMPLY rather than act on what it was told.

// akaSecret is K and OPc of test set 1 of TS 35.208.
var akaSecret = aka.Secret{
	K:   [16]byte{0x46, 0x5b, 0x5c, 0xe8, 0xb1, 0x99, 0xb4, 0x9f, 0xaa, 0x5f, 0x0a, 0x2e, 0xe2, 0x38, 0xa6, 0xbc},
	OPc: [16]byte{0xcd, 0x63, 0xcb, 0x71, 0x95, 0x4a, 0x9f, 0x4e, 0x48, 0xa5, 0x99, 0x4e, 0x37, 0xa0, 0x2b, 0xaf},
}

// otherIMPI is the private identity of a second subscription.
const otherIMPI = "001010000000002@ims.mnc001.mcc001.3gppnetwork.org"

var mmtelServices = sh.TransparentData{ServiceIndication: "MMTEL-Services", ServiceData: &sh.ServiceData{XML: []byte("<a/>")}}

// added is a store that holds one subscriber, which another instance
// added, and the Config of the instances.
func added(t *testing.T, backend storetest.Backend) (kvstore.Store, kvjson.Config) {
	t.Helper()
	kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
	sub := hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p",
		AKA: &hss.AKACredentials{Secret: akaSecret}, RepositoryData: []sh.TransparentData{mmtelServices}}
	if err := hssstore.New(kv, cfg).Add(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	return kv, cfg
}

// TestAWriteThatFailsChangesNothing: each write of a stopped instance
// returns the failure of the store, and the other instances find the
// subscriber as it was.
func TestAWriteThatFailsChangesNothing(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(*hssstore.Store) error{
		"Add": func(s *hssstore.Store) error {
			return s.Add(ctx, hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{"sip:" + otherIMPI}, Password: "p"})
		},
		"Put": func(s *hssstore.Store) error {
			return s.Put(ctx, hss.Subscriber{Subscription: hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}}, State: hss.Registered})
		},
		"CompareAndSwapSQN": func(s *hssstore.Store) error {
			_, err := s.CompareAndSwapSQN(ctx, impi, 0, 1)
			return err
		},
		"CompareAndSwapRepositoryData": func(s *hssstore.Store) error {
			update := mmtelServices
			update.SequenceNumber, update.ServiceData = 1, &sh.ServiceData{XML: []byte("<b/>")}
			_, err := s.CompareAndSwapRepositoryData(ctx, impi, &mmtelServices, update)
			return err
		},
		"Delete": func(s *hssstore.Store) error {
			_, _, err := s.Delete(ctx, impi)
			return err
		},
	}
	for _, backend := range storetest.Backends() {
		for name, write := range writes {
			t.Run(backend.Name+"/"+name, func(t *testing.T) {
				kv, cfg := added(t, backend)
				if err := write(hssstore.New(storetest.StopAfter(kv, 0), cfg)); err == nil || errors.Is(err, hss.ErrIdentityTaken) {
					t.Fatalf("%s on a stopped instance = %v, want the failure of the store", name, err)
				}
				other := hssstore.New(kv, cfg)
				got, ok, err := other.Subscriber(ctx, impi)
				if err != nil || !ok || got.State != hss.NotRegistered || got.AKA == nil || got.AKA.SQN != 0 {
					t.Errorf("the other instance finds %+v, %v, %v; want the subscriber as added", got, ok, err)
				}
				if d, ok, err := other.RepositoryData(ctx, impi, mmtelServices.ServiceIndication); err != nil || !ok || d.SequenceNumber != 0 {
					t.Errorf("the other instance finds the repository data %+v, %v, %v; want it as added", d, ok, err)
				}
				if _, ok, err := other.Subscriber(ctx, otherIMPI); err != nil || ok {
					t.Errorf("the other instance finds the subscriber of the failed Add: %v, %v", ok, err)
				}
			})
		}
	}
}

// TestAReadThatFailsIsNoMissingSubscriber: once the instance has
// stopped, each read returns the failure of the store, not a subscriber
// that is not there.
func TestAReadThatFailsIsNoMissingSubscriber(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := added(t, backend)
			stopped := hssstore.New(storetest.StopAfter(kv, 0), cfg)
			if err := stopped.Put(ctx, hss.Subscriber{Subscription: hss.Subscription{PrivateIdentity: impi}}); err == nil {
				t.Fatal("Put on a stopped instance returned no error")
			}
			if _, _, err := stopped.Subscriber(ctx, impi); err == nil {
				t.Error("Subscriber on a stopped instance returned no error")
			}
			if _, _, err := stopped.SubscriberOf(ctx, tel); err == nil {
				t.Error("SubscriberOf on a stopped instance returned no error")
			}
			if _, _, err := stopped.RepositoryData(ctx, impi, mmtelServices.ServiceIndication); err == nil {
				t.Error("RepositoryData on a stopped instance returned no error")
			}
			if _, _, err := stopped.List(ctx, "", 10); err == nil {
				t.Error("List on a stopped instance returned no error")
			}
		})
	}
}

// TestADeleteThatStopsAfterTheSubscriber: an instance that stops once
// it has removed the subscriber, before its index items, has removed
// the subscriber. The index items it leaves find nothing, and the next
// Add takes them over.
func TestADeleteThatStopsAfterTheSubscriber(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := added(t, backend)
			got, ok, err := hssstore.New(storetest.StopAfter(kv, 1), cfg).Delete(ctx, impi)
			if err != nil || !ok || got.PrivateIdentity != impi {
				t.Fatalf("Delete that stops after the subscriber = %+v, %v, %v; want the subscriber", got, ok, err)
			}
			other := hssstore.New(kv, cfg)
			if _, ok, err := other.SubscriberOf(ctx, tel); err != nil || ok {
				t.Errorf("the index item left behind finds a subscriber: %v, %v", ok, err)
			}
			if subs, _, err := other.List(ctx, "", 10); err != nil || len(subs) != 0 {
				t.Errorf("List = %+v, %v; want none", subs, err)
			}
			sub := hss.Subscription{PrivateIdentity: otherIMPI, PublicIdentities: []string{tel}, Password: "p"}
			if err := other.Add(ctx, sub); err != nil {
				t.Fatalf("Add of an identity whose index item was left: %v", err)
			}
			if got, ok, err := other.SubscriberOf(ctx, tel); err != nil || !ok || got.PrivateIdentity != sub.PrivateIdentity {
				t.Errorf("SubscriberOf = %+v, %v, %v", got, ok, err)
			}
		})
	}
}

// TestAStoppedWriteLeavesNoServedSubscriberOutOfItsIndex: an instance
// that stops at any write of a Put or a Replace may leave the
// subscriber in an index too many, never with an item that names a
// source whose index lacks it. The latter would stay so: a request sent
// again finds the subscription as it asks for and writes nothing.
func TestAStoppedWriteLeavesNoServedSubscriberOutOfItsIndex(t *testing.T) {
	ctx := context.Background()
	registered := hss.Subscriber{Subscription: hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p",
		AKA: &hss.AKACredentials{Secret: akaSecret}}, State: hss.Registered,
		ServerName: "sip:scscf.ims.example:5060", SCSCF: diameter.Identity{OriginHost: "scscf.ims.example", OriginRealm: "ims.example"}}
	ownProfile := registered
	ownProfile.ServiceProfile = "trial"
	writes := map[string]func(*hssstore.Store) error{
		"Put":     func(s *hssstore.Store) error { return s.Put(ctx, registered) },
		"Replace": func(s *hssstore.Store) error { return s.Replace(ctx, ownProfile) },
	}
	for _, backend := range storetest.Backends() {
		for name, write := range writes {
			for stop := 0; stop < 8; stop++ {
				t.Run(fmt.Sprintf("%s/%s/after %d writes", backend.Name, name, stop), func(t *testing.T) {
					kv, cfg := added(t, backend)
					healthy := hssstore.New(kv, cfg)
					if name == "Replace" {
						if err := healthy.Put(ctx, registered); err != nil {
							t.Fatal(err)
						}
					}
					write(hssstore.New(storetest.StopAfter(kv, stop), cfg))
					u, ok, err := healthy.Subscriber(ctx, impi)
					if err != nil || !ok {
						t.Fatalf("Subscriber: %v %v", ok, err)
					}
					source := hss.ServedSource(u)
					if source == "" {
						return
					}
					ids, _, err := healthy.ListServed(ctx, source, "", 10)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Contains(ids, strings.ToLower(impi)) {
						t.Errorf("the item names the source %q, whose index holds %v", source, ids)
					}
				})
			}
		}
	}
}
