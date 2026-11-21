package sipdns

import (
	"io"
	"log/slog"
	"net"
	"slices"
	"testing"

	"github.com/miekg/dns"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

const domain = "ims.mnc001.mcc001.3gppnetwork.org."

// zone is a test zone like the one in deploy/compose/dns.
var zone = []string{
	domain + ` 60 IN NAPTR 10 50 "s" "SIP+D2T" "" _sip._tcp.` + domain,
	domain + ` 60 IN NAPTR 20 50 "s" "SIP+D2U" "" _sip._udp.` + domain,
	`_sip._udp.` + domain + ` 60 IN SRV 10 50 5060 icscf.` + domain,
	`_sip._tcp.` + domain + ` 60 IN SRV 10 50 5060 icscf.` + domain,
	`icscf.` + domain + ` 60 IN A 172.28.0.20`,
	`scscf.` + domain + ` 60 IN A 172.28.0.30`,
	`_sip._udp.srvonly.example. 60 IN SRV 20 0 5070 b.srvonly.example.`,
	`_sip._udp.srvonly.example. 60 IN SRV 10 0 5080 a.srvonly.example.`,
	`a.srvonly.example. 60 IN A 192.0.2.1`,
	`b.srvonly.example. 60 IN A 192.0.2.2`,
}

func serve(t *testing.T, records []string) string {
	t.Helper()
	var rrs []dns.RR
	for _, z := range records {
		rr, err := dns.NewRR(z)
		if err != nil {
			t.Fatal(err)
		}
		rrs = append(rrs, rr)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		q := req.Question[0]
		for _, rr := range rrs {
			if rr.Header().Rrtype == q.Qtype && dns.CanonicalName(rr.Header().Name) == dns.CanonicalName(q.Name) {
				resp.Answer = append(resp.Answer, rr)
			}
		}
		w.WriteMsg(resp)
	})}
	go srv.ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown() })
	return pc.LocalAddr().String()
}

func newTestResolver(t *testing.T, supported ...sip.Transport) *Resolver {
	t.Helper()
	return newResolverFor(t, zone, supported...)
}

func newTestResolverWith(t *testing.T, records []string) *Resolver {
	t.Helper()
	return newResolverFor(t, records, sip.UDP)
}

func newResolverFor(t *testing.T, records []string, supported ...sip.Transport) *Resolver {
	t.Helper()
	r, err := NewResolver(serve(t, records), supported, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func targets(t *testing.T, got []sip.Destination, err error) []string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range got {
		out = append(out, g.String())
	}
	return out
}

func TestLocate(t *testing.T) {
	tests := []struct {
		name      string
		supported []sip.Transport
		host      string
		port      int
		transport sip.Transport
		want      []string
	}{
		// RFC 3263 4.1: NAPTR order decides; TCP is preferred in the
		// zone but skipped when the node only speaks UDP.
		{"NAPTR skips an unsupported transport", []sip.Transport{sip.UDP}, domain, 0, "",
			[]string{"UDP 172.28.0.20:5060"}},
		{"NAPTR order picks TCP first", []sip.Transport{sip.UDP, sip.TCP}, domain, 0, "",
			[]string{"TCP 172.28.0.20:5060"}},
		// RFC 3263 4.2: an explicit port means A records only.
		{"explicit port skips NAPTR and SRV", []sip.Transport{sip.UDP}, "scscf." + domain, 5060, "",
			[]string{"UDP 172.28.0.30:5060"}},
		{"numeric host is used as is", []sip.Transport{sip.UDP}, "192.0.2.9", 0, "",
			[]string{"UDP 192.0.2.9:5060"}},
		{"no NAPTR falls back to SRV in priority order", []sip.Transport{sip.UDP}, "srvonly.example", 0, "",
			[]string{"UDP 192.0.2.1:5080", "UDP 192.0.2.2:5070"}},
		{"no NAPTR or SRV falls back to A and 5060", []sip.Transport{sip.UDP}, "scscf." + domain, 0, "",
			[]string{"UDP 172.28.0.30:5060"}},
		{"transport parameter skips NAPTR", []sip.Transport{sip.UDP, sip.TCP}, domain, 0, sip.UDP,
			[]string{"UDP 172.28.0.20:5060"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestResolver(t, tt.supported...)
			got, err := r.Locate(t.Context(), tt.host, tt.port, tt.transport)
			if g := targets(t, got, err); !slices.Equal(g, tt.want) {
				t.Errorf("got %v, want %v", g, tt.want)
			}
		})
	}
}

func TestLocateUnknownHostFails(t *testing.T) {
	r := newTestResolver(t, sip.UDP)
	if _, err := r.Locate(t.Context(), "nowhere.example", 0, ""); err == nil {
		t.Error("want an error for a name with no records")
	}
}

func TestOrderSRVFollowsWeights(t *testing.T) {
	heavy := &dns.SRV{Priority: 10, Weight: 90, Target: "heavy."}
	light := &dns.SRV{Priority: 10, Weight: 10, Target: "light."}
	backup := &dns.SRV{Priority: 20, Weight: 100, Target: "backup."}
	first := map[string]int{}
	for range 2000 {
		order := orderSRV([]*dns.SRV{light, backup, heavy})
		if order[2] != backup {
			t.Fatalf("a lower priority came before %s", backup.Target)
		}
		first[order[0].Target]++
	}
	// RFC 2782: within a priority, the chance to come first follows the
	// weight, so heavy should lead roughly nine times in ten.
	if first["heavy."] < 1600 || first["heavy."] > 1990 {
		t.Errorf("heavy came first %d times in 2000, want about 1800", first["heavy."])
	}
}
