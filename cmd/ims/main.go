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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/fault"
	"github.com/yuu-biz/ims-practice/internal/h248"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/hepagent"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsec"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/agwstore"
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
	"github.com/yuu-biz/ims-practice/internal/lab"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/oam"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

func main() {
	configPath := flag.String("config", "/etc/ims/node.yaml", "path to the node's YAML configuration")
	flag.Parse()

	// The maintenance API reads and sets the level of the log.
	level := new(slog.LevelVar)
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	if err := run(*configPath, log, level); err != nil {
		log.Error("node failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger, level *slog.LevelVar) error {
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
	applications := diameterApplications(cfg)
	if slices.Contains(applications, sgd.ApplicationIDS6c) || slices.Contains(applications, sgd.ApplicationIDSGd) {
		if err := sgd.LoadDictionary(); err != nil {
			return err
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()

	stores, closeStores := newRoleStores(cfg.Store)
	defer closeStores()
	// One registry for the node: each role registers its own metrics in
	// it, under its own role label, and the maintenance listener serves
	// them all at /metrics.
	registry := metrics.NewRegistry()
	// managed are the roles that the maintenance API of the node serves.
	var managed []managedRole

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
		node = diameter.NewNode(d.Identity, applications, d.Timers, log)
		node.Observe(agent.Diameter(diameterNodeName(cfg)))
		if cfg.HSS != nil {
			roleLog := log.With("role", config.RoleHSS)
			store, err := hssStore(ctx, *cfg.HSS, stores, roleLog)
			if err != nil {
				return err
			}
			hssConfig := *cfg.HSS
			hssConfig.UserLocks = stores.locks(config.RoleHSS, roleLog)
			hssConfig.Metrics = registry
			if hssRole, err = hss.New(hssConfig, store, node, roleLog); err != nil {
				return err
			}
			// The alerts to the service centres that are on their way
			// finish before the node stops.
			defer hssRole.Close()
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
		if hssRole != nil && server != nil {
			// What the operator takes out of service on an HSS is its
			// Diameter listener and the connections on it, so a node
			// that listens for none has nothing to drain.
			managed = append(managed, managedRole{function: hssRole.Function(server),
				manage: hssRole.Manage, settings: hssRole.Settings()})
		}
		if epcRole != nil {
			epcRole.SetAFs(server)
		}
		if hssRole != nil {
			// The S-CSCF and the service centres connect to the HSS, so
			// the RTR and the ALR go back over the server's connections.
			hssRole.SetPeers(server)
		}
		// One connection per entry of the diameter section, in its order,
		// so that peersOfKind finds each peer at the entry's index.
		for _, p := range d.Peers {
			peers = append(peers, node.Connect(p.PeerConfig))
		}
		// The gauge over the state of the connections belongs to the
		// node, not to a role: the roles of the node send over the same
		// connections.
		if err := diameter.RegisterPeerUp(registry, peers); err != nil {
			return err
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
	// startProtectedSIP starts a stack on c with stack, a sip.Config
	// that lacks only the sent-by, for the CSCFs, whose stacks forward
	// CANCELs statelessly and, at the P-CSCF, screen requests. With
	// clientPort it binds the protected client port of TS 33.203 7.1
	// beside the port it listens on, and the requests to a peer that
	// protected reports on leave from there.
	startProtectedSIP := func(role config.Role, c config.SIP, clientPort uint16, protected func(netip.AddrPort) bool, stack sip.Config) (*sip.Stack, error) {
		roleLog := log.With("role", role)
		transport, err := sip.ListenProtected(c.Listen.String(), clientPort, protected, roleLog, agent.SIP(string(role)))
		if err != nil {
			return nil, err
		}
		stack.SentBy = c.Host + ":" + strconv.Itoa(int(c.Listen.Port()))
		stack.Timers = c.Timers()
		s, err := sip.NewStack(transport, stack, roleLog)
		if err != nil {
			transport.Close()
			return nil, err
		}
		stacks = append(stacks, s)
		return s, nil
	}
	// startSIPWith is startProtectedSIP for the stacks that send from
	// the port they listen on, which is every stack but the protected
	// one of a P-CSCF with ESP.
	startSIPWith := func(role config.Role, c config.SIP, stack sip.Config) (*sip.Stack, error) {
		return startProtectedSIP(role, c, 0, nil, stack)
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
		hss, err := hssPeers(cfg, peers, registry, config.RoleSCSCF)
		if err != nil {
			return err
		}
		s, err := scscf.New(scscf.Config{
			ServerName: c.SIP.URI(),
			Host:       c.SIP.Host,
			Port:       int(c.SIP.Listen.Port()),
			HomeDomain: cfg.HomeDomain,
			MinExpires: c.MinExpires,
			MaxExpires: c.MaxExpires,
			IBCF:       c.IBCF,
			UserLocks:  stores.locks(config.RoleSCSCF, roleLog),
			Metrics:    registry,
		}, hss, node, scscfStore(stores, roleLog), resolver, roleLog)
		if err != nil {
			return err
		}
		// The sweep that handles the expiries of the users runs until the
		// node stops.
		defer s.Close()
		scscfRole.Store(s)
		managed = append(managed, managedRole{function: s.Function(hss.Available), manage: s.Manage})
		stack, err := startSIPWith(config.RoleSCSCF, c.SIP, sip.Config{OnRequest: s.OnRequest, OnACK: s.OnACK, OnCANCEL: s.OnCANCEL})
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
		hss, err := hssPeers(cfg, peers, registry, config.RoleICSCF)
		if err != nil {
			return err
		}
		i, err := icscf.New(icscf.Config{HomeDomain: cfg.HomeDomain, SCSCFs: scscfs, Host: c.SIP.Host, Port: int(c.SIP.Listen.Port()),
			PSISubdomains: c.PSISubdomains, Metrics: registry},
			hss, node, resolver,
			log.With("role", config.RoleICSCF))
		if err != nil {
			return err
		}
		managed = append(managed, managedRole{function: i.Function(hss.Available), manage: i.Manage})
		stack, err := startSIPWith(config.RoleICSCF, c.SIP, sip.Config{OnRequest: i.OnRequest, OnCANCEL: i.OnCANCEL})
		if err != nil {
			return err
		}
		i.SetStack(stack)
	}
	if c := cfg.PCSCF; c != nil {
		pc := pcscf.Config{Host: c.SIP.Host, Port: int(c.SIP.Listen.Port()), ProtectedServerPort: int(c.ProtectedServerPort),
			VisitedNetworkID: c.VisitedNetworkID, Metrics: registry}
		if port := c.ProtectedClientPort; port != 0 {
			// TS 33.203 6.3: IPsec ESP protects the signalling of the UEs
			// that register with IMS AKA. config has made sure that
			// sip.listen names this P-CSCF's own address, which the
			// security associations are bound to.
			pc.ESP = &pcscf.ESP{Address: c.SIP.Listen.Addr(), ProtectedClientPort: port,
				Kernel: ipsec.NewKernel(log.With("role", config.RolePCSCF))}
		}
		if h := c.HostAccess; h != nil {
			pc.HostAccess = &pcscf.HostAccess{Gateway: h.Gateway, Address: h.Address}
		}
		roleLog := log.With("role", config.RolePCSCF)
		var relays []pcscf.MediaRelay
		if m := c.MediaController; m != nil {
			controller, stop, err := startMediaController(*m, c.SIP.Host, roleLog, cancel)
			if err != nil {
				return err
			}
			defer stop()
			relays = []pcscf.MediaRelay{{Name: iqRelay, Control: controller}}
		}
		for _, u := range c.MediaRelays {
			relays = append(relays, pcscf.MediaRelay{Name: u, Control: agw.NewClient(u)})
		}
		p, err := pcscf.New(pc, resolver, pcscfStore(stores, roleLog), relays, roleLog)
		if err != nil {
			return err
		}
		// The sweep that handles the expiries of the reg event
		// subscriptions runs until the node stops.
		defer p.Close()
		managed = append(managed, managedRole{function: p.Function(), manage: p.Manage})
		// Without a PCRF in the diameter section the P-CSCF asks no
		// policy control of the network. The PCRF serves
		// the network the P-CSCF is in, so its realm is the node's own.
		pcrf, err := pcrfPeers(cfg, peers, registry)
		if err != nil {
			return err
		}
		if pcrf != nil {
			client := rx.NewClient(node, pcrf, cfg.Diameter.Identity.OriginRealm, p.PolicyEvent, roleLog)
			policyControl.Store(client)
			p.SetPolicyControl(client)
			roleLog.Info("policy control over Rx")
		}
		stack, err := startSIPWith(config.RolePCSCF, c.SIP,
			sip.Config{Screen: p.Screen, Admit: p.AdmitUnprotected, OnRequest: p.OnRequest, OnACK: p.OnACK, OnCANCEL: p.OnCANCEL})
		if err != nil {
			return err
		}
		p.SetStack(stack)
		if port := c.ProtectedServerPort; port != 0 {
			// TS 33.203 7.1: the protected server port, on the same address.
			// It sends only to UEs, each at the one address of its security
			// associations, so its transaction timeout stays 64*T1: there
			// is no other target to move on to. With ESP its requests
			// leave from the protected client port, which the security
			// associations of each UE are bound to.
			protected := config.SIP{Listen: netip.AddrPortFrom(c.SIP.Listen.Addr(), port), Host: c.SIP.Host}
			stack, err := startProtectedSIP(config.RolePCSCF, protected, c.ProtectedClientPort, p.ProtectedPeer,
				sip.Config{Screen: p.Screen, Admit: p.AdmitProtected, OnRequest: p.OnProtectedRequest,
					OnACK: p.OnProtectedACK, OnCANCEL: p.OnProtectedCANCEL})
			if err != nil {
				return err
			}
			p.SetProtectedStack(stack)
		}
	}
	if c := cfg.IPSMGW; c != nil {
		roleLog := log.With("role", config.RoleIPSMGW)
		hss, err := hssPeers(cfg, peers, registry, config.RoleIPSMGW)
		if err != nil {
			return err
		}
		users := sh.NewClient(node, hss, cfg.HomeDomain)
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
		// The sweep that ends the deliveries whose TR1N has run out runs
		// until the node stops.
		defer g.Close()
		managed = append(managed, managedRole{function: g.Function(hss.Available), manage: g.Manage})
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
		b, err := startIBCF(cfg.HomeDomain, *c, ibcfStore(stores, roleLog), resolver, registry, listen, roleLog)
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
		hss, err := hssPeers(cfg, peers, registry, config.RoleMMTel)
		if err != nil {
			return err
		}
		users := sh.NewClient(node, hss, cfg.HomeDomain)
		store := mmtelStore(stores, roleLog)
		mmtelRole = mmtel.New(mmtel.Config{
			Host:             c.SIP.Host,
			Port:             int(c.SIP.Listen.Port()),
			HomeDomain:       cfg.HomeDomain,
			HomeCountryCode:  c.HomeCountryCode,
			MRFC:             c.MRFC,
			Announcement:     c.Announcement,
			ConferenceDomain: c.ConferenceDomain,
			Timers:           c.SIP.Timers(),
		}, users, store, store, resolver, roleLog)
		managed = append(managed, managedRole{function: mmtelRole.Function(hss.Available), manage: mmtelRole.Manage})
		if err := mmtelRole.Listen(transport); err != nil {
			transport.Close()
			return err
		}
		defer mmtelRole.Close()
		// Ut: the users change the same documents over XCAP that the AS
		// reads for each call, in the HSS over Sh. AS.Ut puts it under the
		// administrative state of the AS, so that taking the AS out of
		// service takes Ut with it.
		ut := &http.Server{Addr: c.XCAP.Listen.String(),
			Handler: mmtelRole.Ut(xcap.NewHandler(users, c.XCAP.RootPath, c.XCAP.NonceKey, roleLog.With("function", "xcap")))}
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
		m, err := mrfc.New(mrfc.Config{Host: c.SIP.Host, Port: int(c.SIP.Listen.Port()), NotifyURL: c.NotifyURL(), Timers: c.SIP.Timers()},
			processors, mrfcStore(stores, roleLog), resolver, roleLog)
		if err != nil {
			transport.Close()
			return err
		}
		managed = append(managed, managedRole{function: m.Function(), manage: m.Manage})
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
		roleLog := log.With("role", config.RoleAGW)
		store, err := agwStore(stores, cfg.Node, config.RoleAGW, roleLog)
		if err != nil {
			return err
		}
		relay, stop, err := startMediaNode(*c, nil, store, roleLog, cancel)
		if err != nil {
			return err
		}
		managed = append(managed, managedRole{function: relay.Function(string(config.RoleAGW)), manage: relay.Manage})
		defer stop()
	}
	if c := cfg.MRFP; c != nil {
		roleLog := log.With("role", config.RoleMRFP)
		announcements, err := agw.LoadAnnouncements(c.Announcements)
		if err != nil {
			return err
		}
		store, err := agwStore(stores, cfg.Node, config.RoleMRFP, roleLog)
		if err != nil {
			return err
		}
		relay, stop, err := startMediaNode(c.AGW, announcements, store, roleLog, cancel)
		if err != nil {
			return err
		}
		managed = append(managed, managedRole{function: relay.Function(string(config.RoleMRFP)), manage: relay.Manage})
		defer stop()
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
	if c := cfg.Lab; c != nil {
		roleLog := log.With("role", config.RoleLab)
		var ues []lab.UESimulator
		for _, u := range c.UEs {
			ues = append(ues, lab.UESimulator{Name: u.Name, Control: u.Control})
		}
		l, err := lab.New(lab.Config{MaxMessages: labMessages, HSSProvisioning: c.HSSProvisioning, EPC: c.EPC, FaultAgent: c.FaultAgent, UEs: ues}, roleLog)
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
			if err := l.ServeHEP(collector); err != nil {
				roleLog.Error("HEP collector failed", "error", err.Error())
				cancel()
			}
		}()
		// Close, not Shutdown, on the way out: an open /api/events stream
		// never finishes by itself.
		web := &http.Server{Addr: c.HTTPListen, Handler: l.Handler()}
		go func() {
			if err := web.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				roleLog.Error("lab HTTP server failed", "error", err.Error())
				cancel()
			}
		}()
		defer web.Close()
		roleLog.Info("lab listening", "hep", c.HEPListen, "http", c.HTTPListen)
	}
	if c := cfg.OAM; c != nil {
		roleLog := log.With("role", config.RoleOAM)
		server, err := newOAM(*c, roleLog)
		if err != nil {
			return err
		}
		mux := http.NewServeMux()
		mux.Handle(oam.PathPrefix, server.Handler())
		// The screen calls the API at /oam/v1/..., so it is served
		// beside it, from the same origin.
		mux.Handle("/", oam.UIHandler())
		// Close, not Shutdown, on the way out: a bulk provisioning or a
		// backup streams its answer, and the node must not wait for one
		// to finish. A backup cut short leaves no archive, since the
		// store makes one readable only once it is whole.
		api := &http.Server{Addr: c.HTTPListen, Handler: mux}
		go func() {
			if err := api.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				roleLog.Error("oam HTTP server failed", "error", err.Error())
				cancel()
			}
		}()
		defer api.Close()
		roleLog.Info("oam listening", "http", c.HTTPListen, "nodes", len(c.Nodes))
	}
	if c := cfg.Fault; c != nil {
		roleLog := log.With("role", config.RoleFault)
		api, err := net.Listen("tcp", c.HTTPListen)
		if err != nil {
			return err
		}
		stop := startFaultAgent(ctx, api, *c, roleLog, cancel)
		defer stop()
	}
	if c := cfg.Maintenance; c != nil {
		l, err := net.Listen("tcp", c.Listen)
		if err != nil {
			return err
		}
		stop, err := startMaintenance(l, cfg, managed, registry, level, log, cancel)
		if err != nil {
			return err
		}
		defer stop()
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

// agwStore is where a media relay keeps its contexts: for each pass of
// a call, the ports it reserved and the address and mode of each
// termination. A node recreated under the same name opens those ports
// again, so that the media of the calls through it goes on.
//
// The contexts of a relay are under the name of its node and its role,
// and no other relay reads or writes them. The role is in the name
// because a node may have both media roles: the relay of its agw role
// and the relay of its mrfp role are two relays, each with its own
// ports, and one that opened the contexts of the other would hold ports
// outside its range and give those contexts up.
func agwStore(stores roleStores, node string, role config.Role, log *slog.Logger) (agw.ContextStore, error) {
	kv, cfg, ok := stores.adapter(log)
	if !ok {
		return agw.NewMemoryContextStore(), nil
	}
	return agwstore.New(kv, agwstore.Config{Config: cfg, Relay: node + "-" + string(role)})
}

// startIBCF makes the IBCF and its two UAs: the internal one on Mx,
// towards the home network, and the external one on Ici, towards the
// peers (TS 29.165 3.2). The IBCF is a B2BUA (TS 24.229 5.10.5), so its
// transports belong to the UAs rather than to startSIP. It counts in
// registry, the registry of the node. The caller closes the IBCF when
// the node stops.
func startIBCF(homeDomain string, c config.IBCF, store ibcf.Store, locator ims.Locator, registry *metrics.Registry,
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
		InternalTimers:   c.SIP.Timers(),
		ExternalTimers:   c.ExternalSIP.Timers(),
		Metrics:          registry,
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
	// The HSS answers the routing info query and the delivery status
	// report of the SMS central functions over S6c, and alerts them over
	// it.
	config.RoleHSS:   {diameter.ApplicationIDCx, diameter.ApplicationIDSh, sgd.ApplicationIDS6c},
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
// or the MRFP with its announcements. Both are the same relay. It opens
// the ports of the contexts that an earlier process of this relay left
// in store, so a store that cannot be read keeps the relay from
// starting. It returns the relay, which the caller gives the
// maintenance API of the node so that an operator can take it out of
// service, and the function that stops the control API and the relay
// and leaves its contexts in the store for the next process.
func startMediaNode(c config.AGW, announcements agw.Announcements, store agw.ContextStore, log *slog.Logger, cancel context.CancelFunc) (*agw.Relay, func(), error) {
	relay, err := agw.NewRelay(c.RTPAddress, c.MinPort, c.MaxPort, announcements, store, log)
	if err != nil {
		return nil, nil, err
	}
	if g := c.MediaGateway; g != nil {
		stop, err := startMediaGateway(*g, relay, log, cancel)
		if err != nil {
			relay.Close()
			return nil, nil, err
		}
		return relay, func() {
			stop()
			relay.Close()
		}, nil
	}
	control := &http.Server{Addr: c.Control.String(), Handler: agw.Handler(relay)}
	go func() {
		if err := control.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("control API failed", "error", err.Error())
			cancel()
		}
	}()
	log.Info("control API listening", "address", c.Control.String(), "announcements", len(announcements))
	return relay, func() {
		control.Close()
		relay.Close()
	}, nil
}

// faultExpiryInterval is how often the agent looks for the faults whose
// time is up, so that a fault ends within a second of its expires_at.
const faultExpiryInterval = time.Second

// startFaultAgent serves the fault API on l and ends each fault when
// its duration is up. The agent is not an IMS node: it breaks the
// others on purpose, so that a learner can watch what a failure does.
// It reaches their containers through the Engine's socket, which gives
// it the power of root on the host, so only deploy/chaos/agent.yaml
// hands that socket over and no IMS node gets it.
//
// The function it returns stops the API. A fault that is still in place
// then stays, as one does when the agent dies, until something removes
// it (deploy/chaos/fault.sh heal).
func startFaultAgent(ctx context.Context, l net.Listener, c config.Fault, log *slog.Logger, cancel context.CancelFunc) func() {
	manager := fault.NewManager(fault.Config{
		Executor: fault.NewDocker(c.DockerSocket, c.ComposeProject, c.ToolsImage, log),
		Logger:   log,
	})
	go manager.Run(ctx, faultExpiryInterval)
	// Close, not Shutdown, on the way out: a request that is waiting for
	// a node to stop would hold the agent back.
	api := &http.Server{Handler: manager.Handler()}
	go func() {
		if err := api.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("fault API failed", "error", err.Error())
			cancel()
		}
	}()
	log.Info("fault API listening", "address", l.Addr().String(), "project", c.ComposeProject, "tools_image", c.ToolsImage)
	return func() { api.Close() }
}

// newOAM makes the OAM server of the oam role. The inventory becomes
// the endpoints it operates, and the data directory the two stores it
// keeps itself: the history of the configuration changes it made, in
// one file, and the backups, each an archive and its manifest. Without
// a directory it keeps neither, and refuses those operations with 503.
func newOAM(c config.OAM, log *slog.Logger) (*oam.Server, error) {
	cfg := oam.Config{HSSProvisioning: c.HSSProvisioning, Log: log}
	for _, n := range c.Nodes {
		cfg.Nodes = append(cfg.Nodes, oam.Endpoint{Name: n.Name, Roles: n.Roles, URL: n.Maintenance})
	}
	if c.DataDir != "" {
		history, err := oam.NewFileChangeStore(filepath.Join(c.DataDir, "config-history.jsonl"))
		if err != nil {
			return nil, fmt.Errorf("oam: the configuration history: %w", err)
		}
		backups, err := oam.NewFileBackupStore(filepath.Join(c.DataDir, "backups"))
		if err != nil {
			return nil, fmt.Errorf("oam: the backups: %w", err)
		}
		cfg.History, cfg.Backups = history, backups
	}
	return oam.New(cfg)
}

// managedRole is a role that the maintenance API of the node serves:
// what the role tells the API, and how it learns the administrative
// state that the API keeps for it.
type managedRole struct {
	function maintenance.Function
	manage   func(*maintenance.ManagedFunction)
	// settings are the parts of the node's configuration that this role
	// changes while the node runs (maintenance.NewSetting). The rest of
	// its configuration changes only with the file and a restart.
	settings []maintenance.Setting
}

// startMaintenance serves the maintenance listener of the node on l: the
// metrics of its roles at /metrics, in the text format that Prometheus
// scrapes, and, when roles has any, the maintenance API of those roles
// under maintenance.PathPrefix. The function it returns stops the
// listener.
func startMaintenance(l net.Listener, cfg config.Config, roles []managedRole, registry *metrics.Registry,
	level *slog.LevelVar, log *slog.Logger, cancel context.CancelFunc) (func(), error) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.NewHandler(registry))
	if len(roles) > 0 {
		var functions []maintenance.Function
		var settings []maintenance.Setting
		for _, r := range roles {
			functions = append(functions, r.function)
			settings = append(settings, r.settings...)
		}
		// The host name tells apart the processes that run the same
		// configuration file.
		instance, _ := os.Hostname()
		api, err := maintenance.New(maintenance.Config{
			Node:          cfg.Node,
			Instance:      instance,
			Build:         maintenance.ReadBuild(),
			LogLevel:      level,
			Functions:     functions,
			Configuration: func() any { return newConfigurationView(cfg) },
			Settings:      settings,
			Log:           log,
		})
		if err != nil {
			l.Close()
			return nil, err
		}
		for _, r := range roles {
			r.manage(api.ManagedFunction(r.function.Role))
		}
		mux.Handle(maintenance.PathPrefix, api.Handler())
	}
	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("maintenance listener failed", "error", err.Error())
			cancel()
		}
	}()
	log.Info("maintenance listener listening", "address", l.Addr().String(), "maintenance_api", len(roles) > 0)
	return func() { server.Close() }, nil
}

// configurationView is the configuration of the node as its maintenance
// API shows it, under the names of the configuration file: the node's
// name, roles, home domain and maintenance listener, and the section of
// each role that the API serves. None of them holds a secret.
type configurationView struct {
	Node        string        `json:"node"`
	Roles       []config.Role `json:"roles"`
	HomeDomain  string        `json:"home_domain"`
	Maintenance struct {
		Listen string `json:"listen"`
	} `json:"maintenance"`
	HSS    *hssView    `json:"hss,omitempty"`
	PCSCF  *pcscfView  `json:"pcscf,omitempty"`
	ICSCF  *icscfView  `json:"icscf,omitempty"`
	SCSCF  *scscfView  `json:"scscf,omitempty"`
	MMTel  *mmtelView  `json:"mmtel,omitempty"`
	MRFC   *mrfcView   `json:"mrfc,omitempty"`
	IPSMGW *ipsmgwView `json:"ipsmgw,omitempty"`
	AGW    *agwView    `json:"agw,omitempty"`
	MRFP   *agwView    `json:"mrfp,omitempty"`
}

// hssView is the HSS of the configuration view: where its provisioning
// API listens, and the service profiles of the network by name, each
// with the features its criteria turn on. An operator reads the
// features to see which profile to give a subscription.
//
// It holds no subscriber. The credentials of one are secrets, and the
// subscribers are provisioning data that the provisioning API changes
// one at a time, not configuration. Nor does it hold the settings that
// the maintenance API changes while the HSS runs, the admission policy
// and the two profile names: those are in the changeable settings of
// the same answer, with the value in force, and this section is what
// the file alone decides.
type hssView struct {
	ProvisioningListen string                        `json:"provisioning_listen,omitempty"`
	ServiceProfiles    map[string]serviceProfileView `json:"service_profiles"`
}

// serviceProfileView is one service profile as the API shows it: its
// features, and not its criteria. The criteria are long, and what an
// operator decides is which profile a subscription gets, not what a
// profile holds (hss.ServiceProfile).
type serviceProfileView struct {
	Features []string `json:"features"`
}

type pcscfView struct {
	SIP              sipView         `json:"sip"`
	VisitedNetworkID string          `json:"visited_network_id"`
	MediaRelays      []string        `json:"media_relays,omitempty"`
	H248             *h248View       `json:"h248,omitempty"`
	HostAccess       *hostAccessView `json:"host_access,omitempty"`
	SecAgree         *secAgreeView   `json:"sec_agree,omitempty"`
}

type h248View struct {
	Listen string `json:"listen"`
}

type hostAccessView struct {
	Gateway string `json:"gateway"`
	Address string `json:"address"`
}

type secAgreeView struct {
	ProtectedServerPort int      `json:"protected_server_port"`
	ESP                 *espView `json:"esp,omitempty"`
}

type espView struct {
	ProtectedClientPort int `json:"protected_client_port"`
}

type icscfView struct {
	SIP           sipView      `json:"sip"`
	SCSCFs        []scscfEntry `json:"scscfs,omitempty"`
	PSISubdomains []string     `json:"psi_subdomains,omitempty"`
}

// scscfEntry is one S-CSCF that the I-CSCF may choose, with the
// capabilities it offers (TS 29.228 6.7).
type scscfEntry struct {
	Name         string   `json:"name"`
	Capabilities []uint32 `json:"capabilities,omitempty"`
}

type scscfView struct {
	SIP        sipView `json:"sip"`
	MinExpires int     `json:"min_expires"`
	MaxExpires int     `json:"max_expires"`
	IBCF       string  `json:"ibcf,omitempty"`
}

type mmtelView struct {
	SIP             sipView  `json:"sip"`
	HomeCountryCode string   `json:"home_country_code,omitempty"`
	XCAP            xcapView `json:"xcap"`
	MRFC            string   `json:"mrfc,omitempty"`
	Announcement    string   `json:"announcement,omitempty"`
	// ConferenceDomain is the subdomain whose public service identities
	// are the conferences of this instance (TS 23.003 13.5).
	ConferenceDomain string `json:"conference_domain,omitempty"`
}

// ipsmgwView is the IP-SM-GW with the SC stub beside it. None of its
// settings is a secret.
type ipsmgwView struct {
	SIP sipView `json:"sip"`
	// Number is the E.164 number that the IP-SM-GW registers in the HSS
	// for each user it serves, and ServiceCentre the address of the SC.
	Number        string `json:"number"`
	ServiceCentre string `json:"service_centre"`
	// IBCF is the IBCF through which a short message for a user of
	// another IMS network leaves.
	IBCF string `json:"ibcf,omitempty"`
}

type mrfcView struct {
	SIP sipView `json:"sip"`
	// Notify is where the MRFPs report the end of an announcement, and
	// MRFPs the control APIs of the MRFPs this instance may use.
	Notify string   `json:"notify"`
	MRFPs  []string `json:"mrfp,omitempty"`
}

// agwView is a media relay of the configuration view: where the
// controller reaches it, the address its terminations have, the range of
// RTP ports it hands out, and, for the mrfp role, the names of the
// announcements it can play. The same type serves both media roles,
// which the one relay carries out.
type agwView struct {
	// Control is where the HTTP control API listens, and H248 is set
	// instead when the relay takes its commands over H.248.
	Control       string            `json:"control,omitempty"`
	H248          *mediaGatewayView `json:"h248,omitempty"`
	RTPAddress    string            `json:"rtp_address"`
	RTPPorts      rtpPortsView      `json:"rtp_ports"`
	Announcements []string          `json:"announcements,omitempty"`
}

type mediaGatewayView struct {
	Listen     string `json:"listen"`
	Host       string `json:"host"`
	Controller string `json:"controller"`
}

type rtpPortsView struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// xcapView is the XCAP server of Ut. The key that signs its nonces is a
// secret, so the API shows only whether it is set.
type xcapView struct {
	Listen   string             `json:"listen"`
	RootPath string             `json:"root_path,omitempty"`
	NonceKey maintenance.Secret `json:"nonce_key"`
}

type sipView struct {
	Listen             string `json:"listen"`
	Host               string `json:"host"`
	Pool               string `json:"pool,omitempty"`
	TransactionTimeout string `json:"transaction_timeout,omitempty"`
}

func newConfigurationView(cfg config.Config) configurationView {
	v := configurationView{Node: cfg.Node, Roles: cfg.Roles, HomeDomain: cfg.HomeDomain}
	if m := cfg.Maintenance; m != nil {
		v.Maintenance.Listen = m.Listen
	}
	if c := cfg.HSS; c != nil {
		v.HSS = &hssView{ProvisioningListen: c.ProvisioningListen, ServiceProfiles: map[string]serviceProfileView{}}
		for name, p := range c.ServiceProfiles {
			// A profile whose criteria name no feature answers with an
			// empty list rather than null, so that every profile of the
			// answer reads the same way.
			features := p.Features
			if features == nil {
				features = []string{}
			}
			v.HSS.ServiceProfiles[name] = serviceProfileView{Features: features}
		}
	}
	if c := cfg.PCSCF; c != nil {
		v.PCSCF = &pcscfView{SIP: newSIPView(c.SIP), VisitedNetworkID: c.VisitedNetworkID, MediaRelays: c.MediaRelays}
		if m := c.MediaController; m != nil {
			v.PCSCF.H248 = &h248View{Listen: m.Listen.String()}
		}
		if h := c.HostAccess; h != nil {
			v.PCSCF.HostAccess = &hostAccessView{Gateway: h.Gateway.String(), Address: h.Address.String()}
		}
		if c.ProtectedServerPort != 0 {
			v.PCSCF.SecAgree = &secAgreeView{ProtectedServerPort: int(c.ProtectedServerPort)}
			if c.ProtectedClientPort != 0 {
				v.PCSCF.SecAgree.ESP = &espView{ProtectedClientPort: int(c.ProtectedClientPort)}
			}
		}
	}
	if c := cfg.ICSCF; c != nil {
		v.ICSCF = &icscfView{SIP: newSIPView(c.SIP), PSISubdomains: c.PSISubdomains}
		for _, e := range c.SCSCFs {
			v.ICSCF.SCSCFs = append(v.ICSCF.SCSCFs, scscfEntry{Name: e.Name, Capabilities: e.Capabilities})
		}
	}
	if c := cfg.SCSCF; c != nil {
		v.SCSCF = &scscfView{
			SIP:        newSIPView(c.SIP),
			MinExpires: int(c.MinExpires / time.Second),
			MaxExpires: int(c.MaxExpires / time.Second),
		}
		if c.IBCF.Host != "" {
			v.SCSCF.IBCF = c.IBCF.String()
		}
	}
	if c := cfg.MMTel; c != nil {
		v.MMTel = &mmtelView{
			SIP:             newSIPView(c.SIP),
			HomeCountryCode: c.HomeCountryCode,
			XCAP: xcapView{Listen: c.XCAP.Listen.String(), RootPath: c.XCAP.RootPath,
				NonceKey: maintenance.Redacted(string(c.XCAP.NonceKey))},
			MRFC:             c.MRFC,
			Announcement:     c.Announcement,
			ConferenceDomain: c.ConferenceDomain,
		}
	}
	if c := cfg.MRFC; c != nil {
		v.MRFC = &mrfcView{SIP: newSIPView(c.SIP), Notify: c.Notify.String(), MRFPs: c.MRFPs}
	}
	if c := cfg.IPSMGW; c != nil {
		v.IPSMGW = &ipsmgwView{SIP: newSIPView(c.SIP), Number: c.Number, ServiceCentre: c.ServiceCentre.String()}
		if c.IBCF.Host != "" {
			v.IPSMGW.IBCF = c.IBCF.String()
		}
	}
	if c := cfg.AGW; c != nil {
		v.AGW = newAGWView(*c)
	}
	if c := cfg.MRFP; c != nil {
		v.MRFP = newAGWView(c.AGW)
		for name := range c.Announcements {
			v.MRFP.Announcements = append(v.MRFP.Announcements, name)
		}
		slices.Sort(v.MRFP.Announcements)
	}
	return v
}

// newAGWView is the media relay of c as the maintenance API shows it.
// The paths of the announcement files are left out: what an MRFC asks
// for is the name of an announcement, which is what an operator reads
// here (RFC 4240 3.3).
func newAGWView(c config.AGW) *agwView {
	v := &agwView{
		RTPAddress: c.RTPAddress.String(),
		RTPPorts:   rtpPortsView{Min: int(c.MinPort), Max: int(c.MaxPort)},
	}
	if g := c.MediaGateway; g != nil {
		v.H248 = &mediaGatewayView{Listen: g.Listen.String(), Host: g.Host, Controller: g.Controller}
	} else {
		v.Control = c.Control.String()
	}
	return v
}

func newSIPView(c config.SIP) sipView {
	v := sipView{Listen: c.Listen.String(), Host: c.Host, Pool: c.Pool}
	if c.TransactionTimeout != 0 {
		v.TransactionTimeout = c.TransactionTimeout.String()
	}
	return v
}

// iqRelay is the name by which a dialog of the P-CSCF remembers the
// media relay that holds its context (pcscf.RelayContext) when the
// P-CSCF controls media over H.248. One controller stands for every
// media gateway that has registered with it, and the ID of a context
// names the gateway that holds it, so what the dialog has to remember
// is the reference point and not a gateway.
const iqRelay = "iq"

// startMediaController serves the controller side of Iq, the IMS-ALG of
// the P-CSCF (TS 29.334 5.17.2): the media gateways register with it,
// and the P-CSCF sends them the commands of a call through it in place
// of the HTTP client of a media node. The controller names itself by
// host, which is the name DNS resolves to this node, so that a gateway
// provisioned with that name reaches it.
//
// It passes no TerminationHeartbeat, so every context counts as in use:
// mediactl.ControllerConfig says what that leaves out, in the comment
// on that field. The function it returns stops the controller. The
// contexts stay in the gateways, which register again as soon as a
// controller answers at the same name.
//
// SIMPLIFIED: a node that stops sends no ServiceChange to say that it
// is going out of service. A controller and a gateway of a real network
// tell each other, so that the other side knows at once rather than
// after its timers run out; here the socket closes and the other side
// finds out from the keep-alive or the inactivity timer.
func startMediaController(c config.MediaController, host string, log *slog.Logger, cancel context.CancelFunc) (*mediactl.Controller, func(), error) {
	conn, err := net.ListenPacket("udp", c.Listen.String())
	if err != nil {
		return nil, nil, err
	}
	controller, err := mediactl.NewController(conn, mediactl.ControllerConfig{
		Endpoint: h248.EndpointConfig{MId: h248.MId{DomainName: host}, Logger: log},
	})
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	go func() {
		if err := controller.Serve(); err != nil {
			log.Error("H.248 controller failed", "error", err.Error())
			cancel()
		}
	}()
	log.Info("H.248 controller listening", "address", c.Listen.String(), "name", host)
	return controller, func() { controller.Close() }, nil
}

// startMediaGateway serves the media gateway side of Iq, the IMS-AGW
// (TS 29.334 5.17.3.5): it registers with the controller that c names,
// and carries out the commands it receives by calling relay, in place
// of the control API, which such a node does not serve. The relay is in
// this process, so the gateway reaches it through agw.Direct and
// nothing of the commands goes over HTTP.
//
// The controller's name is resolved again before each registration, to
// the one address that net.ResolveUDPAddr returns, so a gateway follows
// a controller whose address has changed. The function it returns stops
// the gateway, which sends no ServiceChange for it (see
// startMediaController); the caller closes the relay after it.
func startMediaGateway(c config.MediaGateway, relay *agw.Relay, log *slog.Logger, cancel context.CancelFunc) (func(), error) {
	conn, err := net.ListenPacket("udp", c.Listen.String())
	if err != nil {
		return nil, err
	}
	gateway, err := mediactl.NewGateway(conn, mediactl.GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: h248.MId{DomainName: c.Host}, Logger: log},
		LocateController: func() (net.Addr, error) { return net.ResolveUDPAddr("udp", c.Controller) },
		Relay:            agw.NewDirect(relay),
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	go func() {
		if err := gateway.Serve(); err != nil {
			log.Error("H.248 gateway failed", "error", err.Error())
			cancel()
		}
	}()
	log.Info("H.248 gateway started", "address", c.Listen.String(), "name", c.Host, "controller", c.Controller)
	return func() { gateway.Close() }, nil
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

// labMessages bounds the messages the Lab keeps. Each
// message counts once per node that sent or received it.
const labMessages = 10000

// hssPeers are the Cx and Sh peers of the I-CSCF, the S-CSCF, the
// IP-SM-GW and the MMTel AS: the instances of the HSS, in the order of
// the configuration. Every instance holds every subscriber, so a
// request that one of them does not answer goes to the next (TS 29.228
// 6.4).
//
// role is the role that sends through the group, which counts the
// requests and the answers under it in the registry of the node. Each
// role of the node has its own group, and they count in one pair of
// counters.
func hssPeers(cfg config.Config, peers []*diameter.Peer, registry *metrics.Registry, role config.Role) (*diameter.PeerGroup, error) {
	return diameter.NewPeerGroup(registry, string(role), peersOfKind(cfg, peers, config.PeerHSS)...)
}

// pcrfPeers is the Rx peer of the P-CSCF as a group of one, in which
// the P-CSCF counts the requests and the answers of its policy control,
// or nil when the configuration names no PCRF, which leaves the P-CSCF
// without policy control. The group holds the first PCRF of the
// configuration and no other: the P-CSCF sends to the one PCRF that
// serves its network (internal/rx), so there is no second one to send
// to when that one does not answer.
func pcrfPeers(cfg config.Config, peers []*diameter.Peer, registry *metrics.Registry) (*diameter.PeerGroup, error) {
	if !cfg.HasRole(config.RolePCSCF) {
		return nil, nil
	}
	pcrfs := peersOfKind(cfg, peers, config.PeerPCRF)
	if len(pcrfs) == 0 {
		return nil, nil
	}
	return diameter.NewPeerGroup(registry, string(config.RolePCSCF), pcrfs[0])
}

// peersOfKind are the connections to the peers that the diameter
// section calls kind, in its order. peers holds one connection per
// entry of that section, so the entry and the connection share an
// index.
func peersOfKind(cfg config.Config, peers []*diameter.Peer, kind config.PeerKind) []*diameter.Peer {
	if cfg.Diameter == nil {
		return nil
	}
	var found []*diameter.Peer
	for i, p := range cfg.Diameter.Peers {
		if p.Kind == kind && i < len(peers) {
			found = append(found, peers[i])
		}
	}
	return found
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
// sends the S-CSCF the RTR (TS 29.228 6.1.3) and the PPR (6.2.2); every
// other request is for the HSS. A node with both roles gets both. The S-CSCF is made
// after its Cx peer, and the handler has to be set before that peer, so
// the handler finds the S-CSCF in scscfRole once it exists. hssRole is
// nil on a node without the hss role.
func cxHandler(node *diameter.Node, hssRole *hss.HSS, scscfRole *atomic.Pointer[scscf.SCSCF]) diameter.Handler {
	return func(request *diam.Message) *diam.Message {
		if s := scscfRole.Load(); s != nil && fromHSS(request) {
			return s.Handle(request)
		}
		if hssRole != nil {
			return hssRole.Handle(request)
		}
		return diameter.CommandUnsupportedAnswer(node.Identity(), request)
	}
}

// fromHSS reports whether request is one that the HSS sends an S-CSCF.
func fromHSS(request *diam.Message) bool {
	switch request.Header.CommandCode {
	case cx.CommandRegistrationTermination, cx.CommandPushProfile:
		return request.Header.ApplicationID == diameter.ApplicationIDCx
	}
	return false
}
