package storetest_test

import (
	"context"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ipsmgwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The benchmarks make, through the adapters, the Store calls that one
// registration and one call make, one after the other as a node does
// them. They report the time of a whole mix at the median and the 99th
// percentile, and how many operations on kvstore the mix took, in all
// and those of the P-CSCF.
//
//	go test ./internal/kvstore/storetest -run '^$' -bench . -benchtime 2000x
//
// With KVSTORE_VALKEY_ADDR set they run against that server too.

func BenchmarkMixes(b *testing.B) {
	// The MemoryStores of the role packages, for the time that the
	// mixes take without kvstore.
	b.Run("RoleMemoryStore", func(b *testing.B) {
		benchmarkMixes(b, roles{scscf: scscf.NewMemoryStore(), pcscf: pcscf.NewMemoryStore(),
			ipsmgw: ipsmgw.NewMemoryStore(), sc: sc.NewMemoryStore()}, nil)
	})
	for _, backend := range storetest.Backends() {
		b.Run(backend.Name, func(b *testing.B) {
			var store kvstore.Store
			if backend.Name == "Memory" {
				store = kvstore.NewMemoryStore(nil)
			} else {
				v := kvstore.NewValkey(kvstore.ValkeyConfig{Address: os.Getenv("KVSTORE_VALKEY_ADDR")})
				defer v.Close()
				store = v
			}
			kv := &counting{Store: store}
			cfg := kvjson.Config{Prefix: benchPrefix}
			benchmarkMixes(b, roles{scscf: scscfstore.New(kv, scscfstore.Config{Config: cfg}), pcscf: pcscfstore.New(kv, cfg),
				ipsmgw: ipsmgwstore.New(kv, ipsmgwstore.Config{Config: cfg}), sc: scstore.New(kv, cfg)}, kv)
		})
	}
}

// roles are the Stores that the mixes use.
type roles struct {
	scscf  scscf.Store
	pcscf  pcscf.Store
	ipsmgw ipsmgw.Store
	sc     sc.Store
}

// counting counts the operations on the store, by the role whose key
// or time index they name.
type counting struct {
	kvstore.Store
	mu     sync.Mutex
	counts map[string]int
}

func (c *counting) count(key string) {
	role, _, _ := strings.Cut(strings.TrimPrefix(key, benchPrefix), ":")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[role]++
	c.counts[""]++
}

func (c *counting) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := c.counts
	c.counts = nil
	return counts
}

func (c *counting) Get(ctx context.Context, key string) (kvstore.Item, error) {
	c.count(key)
	return c.Store.Get(ctx, key)
}

func (c *counting) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	c.count(key)
	return c.Store.Put(ctx, key, value, ttl)
}

func (c *counting) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	c.count(key)
	return c.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

func (c *counting) Delete(ctx context.Context, key string) error {
	c.count(key)
	return c.Store.Delete(ctx, key)
}

func (c *counting) CompareAndDelete(ctx context.Context, key, version string) error {
	c.count(key)
	return c.Store.CompareAndDelete(ctx, key, version)
}

func (c *counting) Schedule(ctx context.Context, index, member string, at time.Time) error {
	c.count(index)
	return c.Store.Schedule(ctx, index, member, at)
}

func (c *counting) Unschedule(ctx context.Context, index, member string) error {
	c.count(index)
	return c.Store.Unschedule(ctx, index, member)
}

// benchPrefix is the Prefix of the benchmarks, the same for every run so
// that count finds the role after it.
var benchPrefix = storetest.Prefix()

// benchmarkMixes runs the mixes on r. kv, when not nil, is the store
// under the Stores, whose operations it counts.
func benchmarkMixes(b *testing.B, r roles, kv *counting) {
	s, p, g, center := r.scscf, r.pcscf, r.ipsmgw, r.sc
	realm := "ims.mnc001.mcc001.3gppnetwork.org"

	// The callee of the calls is registered, with an association.
	callee := sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.20:5060")}
	p.PutAssociation(pcscf.Association{Source: callee, Contact: "sip:ue2@192.0.2.20:5060", PrivateIdentity: "001010000000002@" + realm,
		PublicIdentities: []string{"sip:001010000000002@" + realm}, Expires: time.Now().Add(time.Hour)})
	s.PutRegistration(scscf.Registration{PrivateIdentity: "001010000000002@" + realm,
		PublicIdentities: []string{"sip:001010000000002@" + realm, "tel:+12025550102"}, Contact: "sip:ue2@192.0.2.20:5060",
		Expires: time.Now().Add(time.Hour), InitialFilterCriteria: registrationOfTheSuite().InitialFilterCriteria})

	flows := []struct {
		name string
		run  func(user string, n int)
	}{
		// 13.1: the S-CSCF calls Challenge 2, PutChallenge 1,
		// DeleteChallenge 1, Registration 1, PutRegistration 1,
		// SubscriptionsOf 1, RegistrationOf 2 and PutSubscription 1; the
		// P-CSCF Association 2, PutAssociation 1 and SecurityAssociations
		// 1 or 2 (here 2).
		{"Registration", func(user string, n int) {
			impi := user + "@" + realm
			source := sip.Destination{Transport: sip.UDP, Addr: netip.AddrPortFrom(netip.MustParseAddr("192.0.2.10"), uint16(10000+n%50000))}
			p.Association(source)
			p.SecurityAssociations(source.Addr)
			s.Challenge(impi)
			s.PutChallenge(scscf.Challenge{PrivateIdentity: impi, Nonce: user, HA1: strings.Repeat("0", 32)})
			s.Challenge(impi)
			s.DeleteChallenge(impi)
			s.Registration(impi)
			reg := registrationOfTheSuite()
			reg.PrivateIdentity, reg.PublicIdentities = impi, []string{"sip:" + impi, "tel:+1202555" + strconv.Itoa(100+n%100)}
			s.PutRegistration(reg)
			s.SubscriptionsOf(impi)
			s.RegistrationOf("sip:" + impi)
			s.RegistrationOf("sip:" + impi)
			var sub scscf.Subscription
			sub.PrivateIdentity, sub.Dialog.ID = impi, sip.DialogID{CallID: "sub-" + user, LocalTag: "l", RemoteTag: "r"}
			sub.Expires = time.Now().Add(time.Hour)
			s.PutSubscription(sub)
			p.Association(source)
			p.SecurityAssociations(source.Addr)
			p.PutAssociation(pcscf.Association{Source: source, Contact: "sip:" + user + "@192.0.2.10", PrivateIdentity: impi,
				PublicIdentities: reg.PublicIdentities, Expires: time.Now().Add(time.Hour)})
		}},
		// 13.1: the P-CSCF calls Association 1, AssociationOfContact 7,
		// SecurityAssociations 10, PutDialog 2, UpdateDialog 8 and
		// TakeDialog 2; the S-CSCF RegistrationOf 3.
		{"Call", func(user string, n int) {
			p.Association(callee)
			for range 7 {
				p.AssociationOfContact("sip:ue2@192.0.2.20:5060")
			}
			for range 10 {
				p.SecurityAssociations(callee.Addr)
			}
			for range 3 {
				s.RegistrationOf("tel:+12025550102")
			}
			keys := []pcscf.DialogKey{{CallID: "call-" + user, UETag: "caller"}, {CallID: "call-" + user, UETag: "callee"}}
			for _, k := range keys {
				p.PutDialog(pcscf.Dialog{Key: k, Caller: pcscf.Party{NameAddr: "<sip:ue1>", Target: "sip:ue1@192.0.2.10"},
					Callee: pcscf.Party{NameAddr: "<sip:ue2>", Target: "sip:ue2@192.0.2.20"}})
			}
			for i := range 8 {
				p.UpdateDialog(keys[i%2], func(d *pcscf.Dialog) { d.Caller.CSeq++ })
			}
			for _, k := range keys {
				p.TakeDialog(k)
			}
		}},
		// ipsmgw.Deliver and the report (deliver.go, message.go), with a
		// status report request at the SC stub.
		{"ShortMessage", func(user string, n int) {
			center.PutStatusReportRequest(sc.StatusReportRequest{Reference: "ref-" + user, Expires: time.Now().Add(time.Hour)})
			mr := g.NextMessageReference("tel:+12025550102")
			g.PutDelivery(ipsmgw.PendingDelivery{CallID: "msg-" + user, Reference: "ref-" + user, MessageReference: mr,
				Expires: time.Now().Add(time.Hour)})
			g.TakeDelivery("msg-" + user)
			center.TakeStatusReportRequest("ref-" + user)
		}},
	}
	for _, f := range flows {
		b.Run(f.name, func(b *testing.B) {
			times := make([]time.Duration, 0, b.N)
			var counts map[string]int
			if kv != nil {
				kv.take()
			}
			i := 0
			for b.Loop() {
				user := "bench" + strconv.Itoa(1_000_000+i)
				start := time.Now()
				f.run(user, i)
				i++
				times = append(times, time.Since(start))
				if kv != nil && counts == nil {
					counts = kv.take()
				}
			}
			slices.Sort(times)
			b.ReportMetric(float64(times[len(times)/2])/1e3, "p50-µs")
			b.ReportMetric(float64(times[(len(times)-1)*99/100])/1e3, "p99-µs")
			if kv != nil {
				b.ReportMetric(float64(counts[""]), "kv-ops")
				b.ReportMetric(float64(counts["pcscf"]), "pcscf-kv-ops")
			}
		})
	}
}

// registrationOfTheSuite is a registration of about the size of 13.1,
// with three criteria.
func registrationOfTheSuite() scscf.Registration {
	r := scscf.Registration{Contact: "sip:ue@192.0.2.10:5060", Path: []string{"<sip:term@pcscf;lr>"}, HSSHost: "hss",
		ContactID: "c1", CallID: "reg-1", CSeq: 2, Expires: time.Now().Add(time.Hour)}
	for i, as := range []string{"ipsmgw", "mmtel", "as"} {
		r.InitialFilterCriteria = append(r.InitialFilterCriteria, ifc.InitialFilterCriteria{Priority: i,
			TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Method: "INVITE", Group: []int{0}}, {Method: "MESSAGE", Group: []int{1}}}},
			ApplicationServer: ifc.ApplicationServer{ServerName: "sip:" + as + ".ims.mnc001.mcc001.3gppnetwork.org", ServiceInfo: strings.Repeat("x", 200)}})
	}
	return r
}
