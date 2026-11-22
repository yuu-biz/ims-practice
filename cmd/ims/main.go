// Command ims is one IMS node. Its roles come from the configuration
// file, so the same binary and image serve as HSS, I-CSCF,
// S-CSCF, P-CSCF, or several of them at once.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/dashboard"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/hepagent"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/hssstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ibcfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ipsmgwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mmtelstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mrfcstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scstore"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

func main() {
	configPath := flag.String("config", "/etc/ims/node.yaml", "path to the node's YAML configuration")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(*configPath, log); err != nil {
		log.Error("node failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log = log.With("node", cfg.Node)
	for _, section := range cfg.Unread() {
		log.Warn("configuration section that no role reads; the node runs as if it were left out", "section", section)
	}
	if err := diameter.LoadDictionary(); err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	stores, closeStores := newRoleStores(cfg.Store)
	defer closeStores()

	// agent stays nil without a hep section, and then observes nothing.
	var agent *hepagent.Agent
	if h := cfg.HEP; h != nil {
		if agent, err = hepagent.New(h.Collector, h.AgentID, log); err != nil {
			return fmt.Errorf("hep: %w", err)
		}
		defer agent.Close()
	}

	var node *diameter.Node
	var server *diameter.Server
	var peers []*diameter.Peer
	var hssRole *hss.HSS
	var scscfRole atomic.Pointer[scscf.SCSCF]
	var epcRole *epc.EPC
	// The P-CSCF's Rx client takes the RARs and ASRs of the PCRF. Like
	// the S-CSCF it is made after its peer, so the handler finds it here.
	var policyControl atomic.Pointer[rx.Client]
	if d := cfg.Diameter; d != nil {
		node = diameter.NewNode(d.Identity, diameterApplications(cfg), d.Timers, log)
		node.Observe(agent.Diameter(diameterNodeName(cfg)))
		if cfg.HSS != nil {
			roleLog := log.With("role", config.RoleHSS)
			store, err := hssStore(ctx, *cfg.HSS, stores, roleLog)
			if err != nil {
				return err
			}
			hssConfig := *cfg.HSS
			hssConfig.UserLocks = stores.locks(config.RoleHSS, roleLog)
			hssRole = hss.New(hssConfig, store, node, roleLog)
		}
		if cfg.EPC != nil {
			epcRole = epc.New(cfg.EPC.Config, epc.NewMemoryStore(), node, log.With("role", config.RoleEPC))
		}
		node.Handle(diameterHandler(node, hssRole, &scscfRole, epcRole, &policyControl))
		if d.Listen != "" {
			server, err = node.Listen(d.Listen)
			if err != nil {
				return err
			}
		}
		if epcRole != nil {
			epcRole.SetAFs(server)
		}
		for _, p := range d.Peers {
			peers = append(peers, node.Connect(p))
		}
	}
	if hssRole != nil && cfg.HSS.ProvisioningListen != "" {
		api := &http.Server{Addr: cfg.HSS.ProvisioningListen, Handler: hssRole.Provisioning(server)}
		go func() {
			if err := api.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("hss provisioning API failed", "error", err.Error())
				cancel()
			}
		}()
		defer api.Close()
		log.Info("hss provisioning API listening", "address", cfg.HSS.ProvisioningListen)
	}

	var stacks []*sip.Stack
	defer func() {
		for _, s := range stacks {
			s.Close()
		}
	}()
	// startSIPWith starts a stack on c with stack, a sip.Config that
	// lacks only the sent-by, for the P-CSCF, whose stacks screen
	// requests.
	startSIPWith := func(role config.Role, c config.SIP, stack sip.Config) (*sip.Stack, error) {
		roleLog := log.With("role", role)
		transport, err := sip.ListenObserved(c.Listen.String(), roleLog, agent.SIP(string(role)))
		if err != nil {
			return nil, err
		}
		stack.SentBy = c.Host + ":" + strconv.Itoa(int(c.Listen.Port()))
		s, err := sip.NewStack(transport, stack, roleLog)
		if err != nil {
			transport.Close()
			return nil, err
		}
		stacks = append(stacks, s)
		return s, nil
	}
	startSIP := func(role config.Role, c config.SIP, onRequest func(*sip.ServerTransaction), onACK func(*sip.Message)) (*sip.Stack, error) {
		return startSIPWith(role, c, sip.Config{OnRequest: onRequest, OnACK: onACK})
	}
	var resolver *sipdns.Resolver
	if cfg.PCSCF != nil || cfg.ICSCF != nil || cfg.SCSCF != nil || cfg.IPSMGW != nil || cfg.MMTel != nil || cfg.MRFC != nil || cfg.IBCF != nil {
		resolver, err = sipdns.NewResolver(cfg.DNSServer, []sip.Transport{sip.UDP, sip.TCP}, log)
		if err != nil {
			return err
		}
	}

	if c := cfg.SCSCF; c != nil {
		roleLog := log.With("role", config.RoleSCSCF)
		s := scscf.New(scscf.Config{
			ServerName: c.SIP.URI(),
			Host:       c.SIP.Host,
			Port:       int(c.SIP.Listen.Port()),
			HomeDomain: cfg.HomeDomain,
			MinExpires: c.MinExpires,
			MaxExpires: c.MaxExpires,
			IBCF:       c.IBCF,
			UserLocks:  stores.locks(config.RoleSCSCF, roleLog),
		}, hssPeer(peers), node, scscfStore(stores, roleLog), resolver, roleLog)
		scscfRole.Store(s)
		stack, err := startSIP(config.RoleSCSCF, c.SIP, s.OnRequest, s.OnACK)
		if err != nil {
			return err
		}
		s.SetStack(stack)
	}
	if c := cfg.ICSCF; c != nil {
		var scscfs []icscf.SCSCF
		for _, e := range c.SCSCFs {
			scscfs = append(scscfs, icscf.SCSCF{Name: e.Name, Capabilities: e.Capabilities})
		}
		i := icscf.New(icscf.Config{HomeDomain: cfg.HomeDomain, SCSCFs: scscfs, Host: c.SIP.Host, Port: int(c.SIP.Listen.Port()),
			PSISubdomains: c.PSISubdomains},
			hssPeer(peers), node, resolver,
			log.With("role", config.RoleICSCF))
		if _, err := startSIP(config.RoleICSCF, c.SIP, i.OnRequest, nil); err != nil {
			return err
		}
	}
	if c := cfg.PCSCF; c != nil {
		pc := pcscf.Config{Host: c.SIP.Host, Port: int(c.SIP.Listen.Port()), ProtectedServerPort: int(c.ProtectedServerPort),
			VisitedNetworkID: c.VisitedNetworkID}
		if h := c.HostAccess; h != nil {
			pc.HostAccess = &pcscf.HostAccess{Gateway: h.Gateway, Address: h.Address}
		}
		var relay pcscf.Relay
		if c.MediaRelay != "" {
			relay = agw.NewClient(c.MediaRelay)
		}
		roleLog := log.With("role", config.RolePCSCF)
		p := pcscf.New(pc, resolver, pcscfStore(stores, roleLog), relay, roleLog)
		// Without a PCRF in the diameter section the P-CSCF asks no
		// policy control of the network. The PCRF serves
		// the network the P-CSCF is in, so its realm is the node's own.
		if peer := pcrfPeer(cfg, peers); peer != nil {
			client := rx.NewClient(node, peer, cfg.Diameter.Identity.OriginRealm, p.PolicyEvent, roleLog)
			policyControl.Store(client)
			p.SetPolicyControl(client)
			roleLog.Info("policy control over Rx")
		}
		stack, err := startSIPWith(config.RolePCSCF, c.SIP,
			sip.Config{Screen: p.Screen, Admit: p.AdmitUnprotected, OnRequest: p.OnRequest, OnACK: p.OnACK})
		if err != nil {
			return err
		}
		p.SetStack(stack)
		if port := c.ProtectedServerPort; port != 0 {
			// TS 33.203 7.1: the protected server port, on the same address.
			protected := config.SIP{Listen: netip.AddrPortFrom(c.SIP.Listen.Addr(), port), Host: c.SIP.Host}
			stack, err := startSIPWith(config.RolePCSCF, protected,
				sip.Config{Screen: p.Screen, OnRequest: p.OnProtectedRequest, OnACK: p.OnProtectedACK})
			if err != nil {
				return err
			}
			p.SetProtectedStack(stack)
		}
	}
	if c := cfg.IPSMGW; c != nil {
		roleLog := log.With("role", config.RoleIPSMGW)
		users := sh.NewClient(node, hssPeer(peers), cfg.HomeDomain)
		centreLog := roleLog.With("function", "sc")
		centre := sc.New(users, scStore(stores, centreLog), centreLog)
		g := ipsmgw.New(ipsmgw.Config{
			URI:           c.SIP.URI(),
			HomeDomain:    cfg.HomeDomain,
			Number:        c.Number,
			ServiceCentre: c.ServiceCentre,
			IBCF:          c.IBCF,
			UserLocks:     stores.locks(config.RoleIPSMGW, roleLog),
		}, centre, ipsmgwStore(stores, roleLog), users, resolver, roleLog)
		centre.SetIPSMGW(g)
		if c.IBCF.Host != "" {
			// A short message for a user of another network leaves
			// through the IBCF.
			centre.SetPeerNetworks(resolver, cfg.HomeDomain)
		}
		stack, err := startSIP(config.RoleIPSMGW, c.SIP, g.OnRequest, nil)
		if err != nil {
			return err
		}
		g.SetStack(stack)
	}
	if c := cfg.IBCF; c != nil {
		roleLog := log.With("role", config.RoleIBCF)
		listen := func(s config.SIP) (*sip.TransportLayer, error) {
			return sip.ListenObserved(s.Listen.String(), roleLog, agent.SIP(string(config.RoleIBCF)))
		}
		b, err := startIBCF(cfg.HomeDomain, *c, ibcfStore(stores, roleLog), resolver, listen, roleLog)
		if err != nil {
			return err
		}
		defer b.Close()
	}
	var mmtelRole *mmtel.AS
	if c := cfg.MMTel; c != nil {
		roleLog := log.With("role", config.RoleMMTel)
		// The AS is a UA on both legs of each call (TS 24.229 5.7.5), so
		// its stack belongs to a sip.UA rather than to startSIP.
		transport, err := sip.ListenObserved(c.SIP.Listen.String(), roleLog, agent.SIP(string(config.RoleMMTel)))
		if err != nil {
			return err
		}
		users := sh.NewClient(node, hssPeer(peers), cfg.HomeDomain)
		store := mmtelStore(stores, roleLog)
		mmtelRole = mmtel.New(mmtel.Config{
			Host:             c.SIP.Host,
			Port:             int(c.SIP.Listen.Port()),
			HomeDomain:       cfg.HomeDomain,
			HomeCountryCode:  c.HomeCountryCode,
			MRFC:             c.MRFC,
			Announcement:     c.Announcement,
			ConferenceDomain: c.ConferenceDomain,
		}, users, store, store, resolver, roleLog)
		if err := mmtelRole.Listen(transport); err != nil {
			transport.Close()
			return err
		}
		defer mmtelRole.Close()
		// Ut: the users change the same documents over XCAP that the AS
		// reads for each call, in the HSS over Sh.
		ut := &http.Server{Addr: c.XCAP.Listen.String(), Handler: xcap.NewHandler(users, c.XCAP.RootPath, c.XCAP.NonceKey, roleLog.With("function", "xcap"))}
		go func() {
			if err := ut.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				roleLog.Error("XCAP server failed", "error", err.Error())
				cancel()
			}
		}()
		defer ut.Close()
		roleLog.Info("XCAP server listening", "address", c.XCAP.Listen.String())
	}
	if c := cfg.MRFC; c != nil {
		roleLog := log.With("role", config.RoleMRFC)
		transport, err := sip.ListenObserved(c.SIP.Listen.String(), roleLog, agent.SIP(string(config.RoleMRFC)))
		if err != nil {
			return err
		}
		var processors []mrfc.Processor
		for _, url := range c.MRFPs {
			client := agw.NewClient(url)
			processors = append(processors, mrfc.Processor{Name: url, Control: client, Announcements: client})
		}
		m, err := mrfc.New(mrfc.Config{Host: c.SIP.Host, Port: int(c.SIP.Listen.Port()), NotifyURL: c.NotifyURL()},
			processors, mrfcStore(stores, roleLog), resolver, roleLog)
		if err != nil {
			transport.Close()
			return err
		}
		if err := m.Listen(transport); err != nil {
			transport.Close()
			return err
		}
		defer m.Close()
		// Mp: the MRFPs report the end of each announcement here.
		notify := &http.Server{Addr: c.Notify.String(), Handler: m.NotifyHandler()}
		go func() {
			if err := notify.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				roleLog.Error("Notify server failed", "error", err.Error())
				cancel()
			}
		}()
		defer notify.Close()
		roleLog.Info("Notify server listening", "address", c.Notify.String(), "mrfp", c.MRFPs)
	}
	if c := cfg.AGW; c != nil {
		control, err := startMediaNode(config.RoleAGW, *c, nil, log, cancel)
		if err != nil {
			return err
		}
		defer control.Close()
	}
	if c := cfg.MRFP; c != nil {
		announcements, err := agw.LoadAnnouncements(c.Announcements)
		if err != nil {
			return err
		}
		control, err := startMediaNode(config.RoleMRFP, c.AGW, announcements, log, cancel)
		if err != nil {
			return err
		}
		defer control.Close()
	}
	if epcRole != nil {
		// Close, not Shutdown, on the way out: a UE's event stream never
		// finishes by itself.
		api := &http.Server{Addr: cfg.EPC.API.String(), Handler: epcRole.Handler()}
		go func() {
			if err := api.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("epc API failed", "error", err.Error())
				cancel()
			}
		}()
		defer api.Close()
		log.Info("epc API listening", "address", cfg.EPC.API.String())
	}
	if c := cfg.Dashboard; c != nil {
		roleLog := log.With("role", config.RoleDashboard)
		var ues []dashboard.UESimulator
		for _, u := range c.UEs {
			ues = append(ues, dashboard.UESimulator{Name: u.Name, Control: u.Control})
		}
		d, err := dashboard.New(dashboard.Config{MaxMessages: dashboardMessages, HSSProvisioning: c.HSSProvisioning, EPC: c.EPC, UEs: ues}, roleLog)
		if err != nil {
			return err
		}
		addr, err := net.ResolveUDPAddr("udp", c.HEPListen)
		if err != nil {
			return err
		}
		collector, err := net.ListenUDP("udp", addr)
		if err != nil {
			return err
		}
		defer collector.Close()
		go func() {
			if err := d.ServeHEP(collector); err != nil {
				roleLog.Error("HEP collector failed", "error", err.Error())
				cancel()
			}
		}()
		// Close, not Shutdown, on the way out: an open /api/events stream
		// never finishes by itself.
		web := &http.Server{Addr: c.HTTPListen, Handler: d.Handler()}
		go func() {
			if err := web.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				roleLog.Error("dashboard HTTP server failed", "error", err.Error())
				cancel()
			}
		}()
		defer web.Close()
		roleLog.Info("dashboard listening", "hep", c.HEPListen, "http", c.HTTPListen)
	}
	log.Info("node started", "roles", cfg.Roles)

	<-ctx.Done()
	log.Info("node stopping")
	for _, s := range stacks {
		s.Close()
	}
	stacks = nil
	if mmtelRole != nil {
		mmtelRole.Close()
	}
	diameter.StopAll(peers)
	if server != nil {
		server.Stop()
	}
	log.Info("node stopped")
	return nil
}

// roleStores is where the roles of the node keep their state. With a
// store section of kind valkey that is the shared store: every role of
// the node takes its own adapter (internal/kvstore/<role>store) on one
// client to that server, so that a node recreated under the same name
// finds what its earlier process left. Without a store section, or with
// kind memory, each role keeps its state in the memory of the process,
// which goes with it.
type roleStores struct {
	// kv is nil when the roles keep their state in the process.
	kv      kvstore.Store
	section config.Store
}

// newRoleStores opens the client of the store section, one for the
// whole node: the roles share it, and it connects when the first
// operation of a role needs a connection. The caller calls the returned
// function when the node stops.
func newRoleStores(s *config.Store) (roleStores, func()) {
	if s == nil || s.Kind != config.StoreValkey {
		return roleStores{}, func() {}
	}
	kv := kvstore.NewValkey(kvstore.ValkeyConfig{Address: s.Address, Timeout: s.Timeout})
	return roleStores{kv: kv, section: *s}, kv.Close
}

// adapter gives the client and the configuration of the adapter of the
// role that logs to log, and false when that role keeps its state in
// the process.
func (r roleStores) adapter(log *slog.Logger) (kvstore.Store, kvjson.Config, bool) {
	if r.kv == nil {
		return nil, kvjson.Config{}, false
	}
	return r.kv, kvjson.Config{Prefix: r.section.Prefix, Timeout: r.section.Timeout, Log: log}, true
}

// locks is the lock per user of role: a lock in the shared store, under
// the prefix of the store section and the name of the role, so that the
// instances of the role exclude each other over one user and the roles
// do not. It is nil when the role keeps its state in the process, which
// gives the role the lock per user of its own process (ims.UserLocks).
func (r roleStores) locks(role config.Role, log *slog.Logger) ims.UserLocker {
	kv, cfg, ok := r.adapter(log)
	if !ok {
		return nil
	}
	return kvlock.New(kv, kvlock.Config{Prefix: cfg.Prefix, Role: string(role), Timeout: cfg.Timeout, Log: log})
}

// hssStore is where the HSS keeps its subscribers: the credentials,
// the user profile and the registration state of each one. The
// subscribers of the configuration outlive the process in the shared
// store, so an instance that starts adds the ones that are not there
// and leaves the stored ones as they are: the provisioning API and Cx
// have changed them since (TS 29.228 6.1.2), and a change of such a
// subscriber in the configuration does not reach the store. A public
// identity of the configuration that another subscriber holds in the
// store keeps the HSS from starting.
func hssStore(ctx context.Context, c hss.Config, stores roleStores, log *slog.Logger) (hss.Store, error) {
	kv, cfg, ok := stores.adapter(log)
	if !ok {
		return hss.NewMemoryStore(c.Subscriptions...)
	}
	store := hssstore.New(kv, cfg)
	for _, sub := range c.Subscriptions {
		err := store.Add(ctx, sub)
		if errors.Is(err, hss.ErrIdentityTaken) {
			_, stored, lookup := store.Subscriber(ctx, sub.PrivateIdentity)
			switch {
			case lookup != nil:
				err = lookup
			case stored:
				log.Info("subscriber of the configuration already stored; the stored one is kept", "private_identity", sub.PrivateIdentity)
				continue
			}
		}
		if err != nil {
			return nil, fmt.Errorf("hss: cannot store the subscriber %s of the configuration: %w", sub.PrivateIdentity, err)
		}
	}
	return store, nil
}

// mmtelStores is what the MMTel AS keeps outside the dialogs of its
// own process: the record of each two-party call, and of each
// conference whose focus it is. mmtel.MemoryStore and mmtelstore.Store
// are each both.
type mmtelStores interface {
	mmtel.Store
	mmtel.ConferenceStore
}

// mmtelStore is where the MMTel AS keeps those records. The AS is a
// UA on both legs of a call (TS 24.229 5.7.5), so an instance recreated
// under the same name needs the record to build the two dialogs again
// and go on with the call.
func mmtelStore(stores roleStores, log *slog.Logger) mmtelStores {
	if kv, cfg, ok := stores.adapter(log); ok {
		return mmtelstore.New(kv, cfg)
	}
	return mmtel.NewMemoryStore()
}

// mrfcStore is where the MRFC keeps which MRFP and which context hold
// the mix of each conference, so that a party who reaches any instance
// joins the same mix (TS 29.333 5.17.2.30).
func mrfcStore(stores roleStores, log *slog.Logger) mrfc.Store {
	if kv, cfg, ok := stores.adapter(log); ok {
		return mrfcstore.New(kv, cfg)
	}
	return mrfc.NewMemoryStore()
}

// ipsmgwStore is where the IP-SM-GW keeps the deliveries it is waiting
// for a report on, the RP-MR counter of each recipient and its
// subscriptions to the reg event of the users (TS 24.341 5.3.3).
// Whichever instance a report or a NOTIFY reaches then finds what
// another one stored.
func ipsmgwStore(stores roleStores, log *slog.Logger) ipsmgw.Store {
	if kv, cfg, ok := stores.adapter(log); ok {
		return ipsmgwstore.New(kv, cfg)
	}
	return ipsmgw.NewMemoryStore()
}

// scStore is where the SC stub keeps the status report that each
// delivery owes its sender, so that the instance which hears the
// outcome sends it (TS 23.040 3.2.6).
func scStore(stores roleStores, log *slog.Logger) sc.Store {
	if kv, cfg, ok := stores.adapter(log); ok {
		return scstore.New(kv, cfg)
	}
	return sc.NewMemoryStore()
}

// pcscfStore is where the P-CSCF keeps the IP association of each
// registered UE, the dialogs of its calls and its table of security
// associations (TS 24.229 5.2.2.1, 5.2.6.3, TS 33.203 7.1).
func pcscfStore(stores roleStores, log *slog.Logger) pcscf.Store {
	if kv, cfg, ok := stores.adapter(log); ok {
		return pcscfstore.New(kv, cfg)
	}
	return pcscf.NewMemoryStore()
}

// scscfStore is where the S-CSCF keeps its registrar state: the
// challenges it is waiting on, the registrations with their user
// profiles, the subscriptions to the reg event of each registration,
// and the original dialog identifiers of the requests it sent to an
// application server (TS 24.229 5.4.1.2.2A, 5.4.2.1.2, 5.4.3.3).
func scscfStore(stores roleStores, log *slog.Logger) scscf.Store {
	if kv, cfg, ok := stores.adapter(log); ok {
		return scscfstore.New(kv, cfg)
	}
	return scscf.NewMemoryStore()
}

// ibcfStore is where the IBCF keeps the records of its calls, so that a
// node that is recreated under the same name finds the calls that its
// earlier process left, and ends them (TS 24.229 5.10.2.4, 5.10.3.4).
func ibcfStore(stores roleStores, log *slog.Logger) ibcf.Store {
	if kv, cfg, ok := stores.adapter(log); ok {
		return ibcfstore.New(kv, cfg)
	}
	return ibcf.NewMemoryStore()
}

// startIBCF makes the IBCF and its two UAs: the internal one on Mx,
// towards the home network, and the external one on Ici, towards the
// peers (TS 29.165 3.2). The IBCF is a B2BUA (TS 24.229 5.10.5), so its
// transports belong to the UAs rather than to startSIP. The caller
// closes the IBCF when the node stops.
func startIBCF(homeDomain string, c config.IBCF, store ibcf.Store, locator ims.Locator,
	listen func(config.SIP) (*sip.TransportLayer, error), log *slog.Logger) (*ibcf.IBCF, error) {
	internal := sip.URI{Scheme: "sip", Host: c.SIP.Host, Port: int(c.SIP.Listen.Port())}
	external := sip.URI{Scheme: "sip", Host: c.ExternalSIP.Host, Port: int(c.ExternalSIP.Listen.Port())}
	peers, err := ibcf.NewPeerTable(c.Peers...)
	if err != nil {
		return nil, err
	}
	b, err := ibcf.New(ibcf.Config{
		Internal:         internal,
		External:         external,
		InternalPool:     c.SIP.Pool,
		ExternalPool:     c.ExternalSIP.Pool,
		HomeDomain:       homeDomain,
		HomeNumberFormat: c.HomeNumberFormat,
		ICSCF:            c.ICSCF,
		TrGWCoreAddress:  c.TrGW.CoreAddress,
		TrGWPeerAddress:  c.TrGW.PeerAddress,
	}, peers, agw.NewClient(c.TrGW.Control), store, locator, log)
	if err != nil {
		return nil, err
	}
	in, err := listen(c.SIP)
	if err != nil {
		return nil, err
	}
	out, err := listen(c.ExternalSIP)
	if err != nil {
		in.Close()
		return nil, err
	}
	if err := b.Listen(in, out); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	var names []string
	for _, p := range c.Peers {
		names = append(names, p.Profile.Name)
	}
	log.Info("IBCF ready", "internal", internal.String(), "external", external.String(), "peers", names, "trgw", c.TrGW.Control)
	return b, nil
}

// roleApplications are the Diameter applications each role speaks.
var roleApplications = map[config.Role][]uint32{
	config.RoleHSS:   {diameter.ApplicationIDCx, diameter.ApplicationIDSh},
	config.RoleICSCF: {diameter.ApplicationIDCx},
	config.RoleSCSCF: {diameter.ApplicationIDCx},
	// The IP-SM-GW registers itself and finds users over Sh.
	config.RoleIPSMGW: {diameter.ApplicationIDSh},
	// The MMTel AS reads the settings of the services over Sh.
	config.RoleMMTel: {diameter.ApplicationIDSh},
	// The P-CSCF has Diameter only to reach the PCRF, and the epc role's
	// PCRF serves it (TS 29.214).
	config.RolePCSCF: {diameter.ApplicationIDRx},
	config.RoleEPC:   {diameter.ApplicationIDRx},
}

// diameterApplications are the applications the node advertises in its
// CER and CEA: those of its roles, and no others.
// startMediaNode serves the control API of a media node: the IMS-AGW,
// or the MRFP with its announcements. Both are the same relay.
func startMediaNode(role config.Role, c config.AGW, announcements agw.Announcements, log *slog.Logger, cancel context.CancelFunc) (*http.Server, error) {
	roleLog := log.With("role", role)
	relay, err := agw.NewRelay(c.RTPAddress, c.MinPort, c.MaxPort, announcements, roleLog)
	if err != nil {
		return nil, err
	}
	control := &http.Server{Addr: c.Control.String(), Handler: agw.Handler(relay)}
	go func() {
		if err := control.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			roleLog.Error("control API failed", "error", err.Error())
			cancel()
		}
	}()
	roleLog.Info("control API listening", "address", c.Control.String(), "announcements", len(announcements))
	return control, nil
}

func diameterApplications(cfg config.Config) []uint32 {
	var apps []uint32
	for _, r := range cfg.Roles {
		for _, id := range roleApplications[r] {
			if !slices.Contains(apps, id) {
				apps = append(apps, id)
			}
		}
	}
	return apps
}

// diameterNodeName labels the HEP copies of the Diameter node. Every
// Diameter role of the node shares that one node, so a node with more
// than one is labeled with all of them, such as "icscf+scscf".
func diameterNodeName(cfg config.Config) string {
	var roles []string
	for _, r := range cfg.Roles {
		if len(roleApplications[r]) > 0 {
			roles = append(roles, string(r))
		}
	}
	return strings.Join(roles, "+")
}

// dashboardMessages bounds the messages the dashboard keeps. Each
// message counts once per node that sent or received it.
const dashboardMessages = 10000

// hssPeer is the Cx or Sh peer of the I-CSCF, the S-CSCF, the IP-SM-GW
// and the MMTel AS. The configuration gives them exactly one, the HSS.
func hssPeer(peers []*diameter.Peer) *diameter.Peer {
	if len(peers) == 0 {
		return nil
	}
	return peers[0]
}

// pcrfPeer is the Rx peer of the P-CSCF, or nil when the configuration
// names none. On a node whose other roles need the HSS, the HSS is the
// first peer and the PCRF the second.
func pcrfPeer(cfg config.Config, peers []*diameter.Peer) *diameter.Peer {
	if !cfg.HasRole(config.RolePCSCF) {
		return nil
	}
	i := 0
	if cfg.HasRole(config.RoleICSCF) || cfg.HasRole(config.RoleSCSCF) || cfg.HasRole(config.RoleIPSMGW) || cfg.HasRole(config.RoleMMTel) {
		i = 1
	}
	if i >= len(peers) {
		return nil
	}
	return peers[i]
}

// diameterHandler gives each request that reaches the node to the role
// of its application: Cx and Sh as cxHandler does, and Rx to the PCRF of
// the epc role, except the two requests that a PCRF sends, RAR and ASR
// (TS 29.214 4.4.6), which are for the P-CSCF's Rx client. epcRole is
// nil on a node without the epc role, and policyControl is empty on a
// node whose P-CSCF has no PCRF.
func diameterHandler(node *diameter.Node, hssRole *hss.HSS, scscfRole *atomic.Pointer[scscf.SCSCF], epcRole *epc.EPC, policyControl *atomic.Pointer[rx.Client]) diameter.Handler {
	cx := cxHandler(node, hssRole, scscfRole)
	return func(request *diam.Message) *diam.Message {
		if request.Header.ApplicationID != diameter.ApplicationIDRx {
			return cx(request)
		}
		fromPCRF := request.Header.CommandCode == rx.CommandReAuth || request.Header.CommandCode == rx.CommandAbortSession
		if client := policyControl.Load(); client != nil && fromPCRF {
			return client.Handle(request)
		}
		if epcRole != nil {
			return epcRole.HandleRx(request)
		}
		return diameter.CommandUnsupportedAnswer(node.Identity(), request)
	}
}

// cxHandler answers the Cx and Sh requests that reach the node. The HSS
// sends the S-CSCF the RTR (TS 29.228 6.1.3); every other request is
// for the HSS. A node with both roles gets both. The S-CSCF is made
// after its Cx peer, and the handler has to be set before that peer, so
// the handler finds the S-CSCF in scscfRole once it exists. hssRole is
// nil on a node without the hss role.
func cxHandler(node *diameter.Node, hssRole *hss.HSS, scscfRole *atomic.Pointer[scscf.SCSCF]) diameter.Handler {
	return func(request *diam.Message) *diam.Message {
		if s := scscfRole.Load(); s != nil && request.Header.CommandCode == cx.CommandRegistrationTermination {
			return s.Handle(request)
		}
		if hssRole != nil {
			return hssRole.Handle(request)
		}
		return diameter.CommandUnsupportedAnswer(node.Identity(), request)
	}
}
