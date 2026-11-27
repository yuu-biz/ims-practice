package sipdns

import (
	"io"
	"log/slog"
	"net"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// truncatingServer answers on UDP and TCP at one address. Over UDP it
// keeps only the first record of an answer and sets TC, as a server
// does when the answer does not fit in a UDP message (RFC 1035 4.2.1),
// unless truncate is false. Over TCP it answers in full, or closes the
// connection when tcpFails is set.
type truncatingServer struct {
	addr       string
	udpQueries atomic.Int32
	tcpQueries atomic.Int32
}

func startTruncatingServer(t *testing.T, records []string, truncate, tcpFails bool) *truncatingServer {
	t.Helper()
	var rrs []dns.RR
	for _, z := range records {
		rr, err := dns.NewRR(z)
		if err != nil {
			t.Fatal(err)
		}
		rrs = append(rrs, rr)
	}
	s := &truncatingServer{}
	answer := func(req *dns.Msg) *dns.Msg {
		resp := new(dns.Msg)
		resp.SetReply(req)
		q := req.Question[0]
		for _, rr := range rrs {
			if rr.Header().Rrtype == q.Qtype && dns.CanonicalName(rr.Header().Name) == dns.CanonicalName(q.Name) {
				resp.Answer = append(resp.Answer, rr)
			}
		}
		return resp
	}
	udpHandler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		s.udpQueries.Add(1)
		resp := answer(req)
		if truncate && len(resp.Answer) > 1 {
			resp.Answer = resp.Answer[:1]
			resp.Truncated = true
		}
		w.WriteMsg(resp)
	})
	tcpHandler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		s.tcpQueries.Add(1)
		if tcpFails {
			w.Close()
			return
		}
		w.WriteMsg(answer(req))
	})

	// The UDP and TCP sockets share a port number, and an ephemeral UDP
	// port can already be taken for TCP, so try again with another one.
	for range 20 {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("tcp", pc.LocalAddr().String())
		if err != nil {
			pc.Close()
			continue
		}
		udp := &dns.Server{PacketConn: pc, Handler: udpHandler}
		tcp := &dns.Server{Listener: ln, Handler: tcpHandler}
		go udp.ActivateAndServe()
		go tcp.ActivateAndServe()
		t.Cleanup(func() { udp.Shutdown(); tcp.Shutdown() })
		s.addr = pc.LocalAddr().String()
		return s
	}
	t.Fatal("no ephemeral port was free for both UDP and TCP")
	return nil
}

func resolverFor(t *testing.T, server string) *Resolver {
	t.Helper()
	r, err := NewResolver(server, []sip.Transport{sip.UDP}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The zone has two SRV records, so a truncated UDP answer holds only
// the first of them.
var srvOnly = []string{
	`_sip._udp.srvonly.example. 60 IN SRV 10 0 5080 a.srvonly.example.`,
	`_sip._udp.srvonly.example. 60 IN SRV 20 0 5070 b.srvonly.example.`,
	`a.srvonly.example. 60 IN A 192.0.2.1`,
	`b.srvonly.example. 60 IN A 192.0.2.2`,
}

func TestLocateAsksAgainOverTCPWhenTruncated(t *testing.T) {
	s := startTruncatingServer(t, srvOnly, true, false)
	got, err := resolverFor(t, s.addr).Locate(t.Context(), "srvonly.example", 0, "")
	want := []string{"UDP 192.0.2.1:5080", "UDP 192.0.2.2:5070"}
	if g := targets(t, got, err); !slices.Equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
	if n := s.tcpQueries.Load(); n != 1 {
		t.Errorf("TCP queries = %d, want 1 (the SRV query)", n)
	}
}

func TestLocateFailsWhenTruncatedAndTCPFails(t *testing.T) {
	s := startTruncatingServer(t, srvOnly, true, true)
	got, err := resolverFor(t, s.addr).Locate(t.Context(), "srvonly.example", 0, "")
	if err == nil {
		t.Fatalf("got %v, want an error: the truncated UDP answer is not a success", got)
	}
	if s.tcpQueries.Load() == 0 {
		t.Error("the resolver did not try TCP")
	}
}

func TestLocateUsesUDPAnswerWhenNotTruncated(t *testing.T) {
	s := startTruncatingServer(t, srvOnly, false, false)
	got, err := resolverFor(t, s.addr).Locate(t.Context(), "srvonly.example", 0, "")
	want := []string{"UDP 192.0.2.1:5080", "UDP 192.0.2.2:5070"}
	if g := targets(t, got, err); !slices.Equal(g, want) {
		t.Errorf("got %v, want %v", g, want)
	}
	if n := s.tcpQueries.Load(); n != 0 {
		t.Errorf("TCP queries = %d, want 0", n)
	}
}
