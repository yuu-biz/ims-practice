// Package sipdns finds SIP servers in DNS the way RFC 3263 describes:
// NAPTR records choose the transport, SRV records choose the port and
// the host, and A records give the address. The P-CSCF uses it to find
// the I-CSCF of the home network, and the I-CSCF to find the S-CSCF.
package sipdns

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/miekg/dns"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// defaultPort is the SIP port of RFC 3261 19.1.2.
const defaultPort = 5060

// The NAPTR services and SRV prefixes of RFC 3263 4.1 for the
// transports this project speaks.
var transports = []struct {
	transport sip.Transport
	service   string // NAPTR service field
	srvPrefix string
}{
	{sip.UDP, "SIP+D2U", "_sip._udp."},
	{sip.TCP, "SIP+D2T", "_sip._tcp."},
}

// Resolver sends DNS queries to one server.
type Resolver struct {
	server     string // host:port
	client     *dns.Client
	transports []sip.Transport // the ones this node can use, in preference order
	log        *slog.Logger
}

// NewResolver queries server (host:port). An empty server means the
// first nameserver in /etc/resolv.conf, which in Docker Compose is the
// embedded DNS that forwards to the project's DNS container.
func NewResolver(server string, supported []sip.Transport, log *slog.Logger) (*Resolver, error) {
	if server == "" {
		conf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return nil, err
		}
		if len(conf.Servers) == 0 {
			return nil, errors.New("sipdns: no nameserver in /etc/resolv.conf")
		}
		server = net.JoinHostPort(conf.Servers[0], conf.Port)
	}
	return &Resolver{server: server, client: &dns.Client{}, transports: supported, log: log}, nil
}

// Locate returns the targets for a SIP URI's host, port and transport
// parameter, best first (RFC 3263 4). port is 0 and transport is empty
// when the URI has none.
func (r *Resolver) Locate(ctx context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error) {
	if transport != "" && !slices.Contains(r.transports, transport) {
		return nil, fmt.Errorf("sipdns: transport %s is not supported", transport)
	}
	// RFC 3263 4.1 and 4.2: a numeric address, or an explicit port, skips
	// NAPTR and SRV.
	if ip, err := netip.ParseAddr(host); err == nil {
		return []sip.Destination{{Transport: r.pick(transport), Addr: netip.AddrPortFrom(ip, portOr(port))}}, nil
	}
	if port != 0 {
		return r.addresses(ctx, host, r.pick(transport), port)
	}

	if transport == "" {
		targets, found, err := r.viaNAPTR(ctx, host)
		if err != nil || found {
			return targets, err
		}
	}
	// RFC 3263 4.1: without NAPTR records, try the SRV records of each
	// supported transport, or of the one the URI asked for.
	for _, t := range transports {
		if !slices.Contains(r.transports, t.transport) || (transport != "" && transport != t.transport) {
			continue
		}
		targets, found, err := r.viaSRV(ctx, t.srvPrefix+host, t.transport)
		if err != nil || found {
			return targets, err
		}
	}
	// RFC 3263 4.2: without SRV records, use the host's A records and
	// the default port.
	return r.addresses(ctx, host, r.pick(transport), defaultPort)
}

func (r *Resolver) pick(transport sip.Transport) sip.Transport {
	if transport != "" {
		return transport
	}
	// RFC 3263 4.1: UDP is the default for a sip URI.
	return sip.UDP
}

func portOr(port int) uint16 {
	if port == 0 {
		return defaultPort
	}
	return uint16(port)
}

// viaNAPTR is RFC 3263 4.1 with NAPTR records. Only records whose
// service this node supports are kept, in order and preference order.
// found is false when the domain has no usable NAPTR record.
func (r *Resolver) viaNAPTR(ctx context.Context, host string) (targets []sip.Destination, found bool, err error) {
	answers, err := r.query(ctx, host, dns.TypeNAPTR)
	if err != nil {
		return nil, false, err
	}
	var records []*dns.NAPTR
	for _, rr := range answers {
		if n, ok := rr.(*dns.NAPTR); ok {
			records = append(records, n)
		}
	}
	slices.SortStableFunc(records, func(a, b *dns.NAPTR) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.Preference, b.Preference))
	})
	for _, n := range records {
		// RFC 3263 4.1: the "s" flag says the replacement is an SRV name.
		if !strings.EqualFold(n.Flags, "s") {
			continue
		}
		for _, t := range transports {
			if !strings.EqualFold(n.Service, t.service) || !slices.Contains(r.transports, t.transport) {
				continue
			}
			targets, found, err := r.viaSRV(ctx, n.Replacement, t.transport)
			if err != nil || found {
				return targets, found, err
			}
		}
	}
	return nil, false, nil
}

// viaSRV is RFC 3263 4.2 with SRV records, ordered as RFC 2782 says:
// by priority, and within a priority by a weighted random choice.
func (r *Resolver) viaSRV(ctx context.Context, name string, transport sip.Transport) (targets []sip.Destination, found bool, err error) {
	answers, err := r.query(ctx, name, dns.TypeSRV)
	if err != nil {
		return nil, false, err
	}
	var records []*dns.SRV
	for _, rr := range answers {
		if s, ok := rr.(*dns.SRV); ok {
			records = append(records, s)
		}
	}
	if len(records) == 0 {
		return nil, false, nil
	}
	for _, s := range orderSRV(records) {
		addrs, err := r.addresses(ctx, s.Target, transport, int(s.Port))
		if err != nil {
			r.log.Info("dns srv target has no address", "target", s.Target, "error", err.Error())
			continue
		}
		targets = append(targets, addrs...)
	}
	if len(targets) == 0 {
		return nil, true, fmt.Errorf("sipdns: no SRV target of %s has an address", name)
	}
	return targets, true, nil
}

// orderSRV sorts by priority and, within one priority, picks records at
// random in proportion to their weight (RFC 2782, "Usage rules").
func orderSRV(records []*dns.SRV) []*dns.SRV {
	slices.SortStableFunc(records, func(a, b *dns.SRV) int { return cmp.Compare(a.Priority, b.Priority) })
	var out []*dns.SRV
	for len(records) > 0 {
		n := 1
		for n < len(records) && records[n].Priority == records[0].Priority {
			n++
		}
		group := slices.Clone(records[:n])
		records = records[n:]
		for len(group) > 0 {
			total := 0
			for _, s := range group {
				total += int(s.Weight)
			}
			i := 0
			if total > 0 {
				pick := rand.N(total + 1)
				for sum := 0; i < len(group); i++ {
					sum += int(group[i].Weight)
					if sum >= pick {
						break
					}
				}
				i = min(i, len(group)-1)
			}
			out = append(out, group[i])
			group = slices.Delete(group, i, i+1)
		}
	}
	return out
}

func (r *Resolver) addresses(ctx context.Context, host string, transport sip.Transport, port int) ([]sip.Destination, error) {
	answers, err := r.query(ctx, host, dns.TypeA)
	if err != nil {
		return nil, err
	}
	var targets []sip.Destination
	for _, rr := range answers {
		if a, ok := rr.(*dns.A); ok {
			ip, _ := netip.AddrFromSlice(a.A.To4())
			targets = append(targets, sip.Destination{Transport: transport, Addr: netip.AddrPortFrom(ip, uint16(port))})
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("sipdns: %s has no A record", host)
	}
	return targets, nil
}

func (r *Resolver) query(ctx context.Context, name string, qtype uint16) ([]dns.RR, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := r.client.ExchangeContext(ctx, m, r.server)
	if err != nil {
		return nil, fmt.Errorf("sipdns: %s %s: %w", dns.TypeToString[qtype], name, err)
	}
	var found []string
	for _, rr := range resp.Answer {
		found = append(found, strings.TrimPrefix(rr.String(), rr.Header().String()))
	}
	r.log.Info("dns lookup", "type", dns.TypeToString[qtype], "name", dns.Fqdn(name),
		"rcode", dns.RcodeToString[resp.Rcode], "answers", found)
	if resp.Rcode != dns.RcodeSuccess && resp.Rcode != dns.RcodeNameError {
		return nil, fmt.Errorf("sipdns: %s %s: %s", dns.TypeToString[qtype], name, dns.RcodeToString[resp.Rcode])
	}
	return resp.Answer, nil
}
