// Command ue is the UE simulator.
//
//	ue register   registers, then deregisters after a while or stays
//	              registered, refreshing as TS 24.229 5.1.1.4.1 says
//	ue call URI   registers, calls URI (a SIP or tel URI), plays audio,
//	              hangs up and deregisters
//	ue answer     registers, answers one call, records it, and
//	              deregisters after the caller hangs up
//	ue serve      registers and stays up, taking and making calls as a
//	              controller asks over HTTP (see ue.Phone.Handler and
//	              ue.SupplementaryServices.Handler)
//	ue sms NUMBER TEXT
//	              registers, sends TEXT as a short message to NUMBER (in
//	              international format, such as +12025550102), waits for
//	              the submit report, and for the status report with
//	              -status-report, then deregisters (TS 24.341 5.3.1)
//	ue xcap get | set SERVICE on|off [TARGET] | oir restricted|not-restricted | noreply SECONDS
//	              reads or changes the supplementary services over Ut
//	              with XCAP (TS 24.623, GSMA IR.92 2.3.2), without
//	              registering
//	ue ctl status | call URI | answer | hangup [CALL-ID] | hold | resume | swap | merge | conference
//	              sends one request to the control API of a "ue serve"
//	              at -control and prints the answer: a second call, call
//	              waiting, and the three-way session of TS 24.147
//	              5.3.1.3.3 are driven this way
//
// Any INVITE besides the one call of "ue answer" gets 486 (Busy Here).
// "ue serve" takes a second call while it has one, which waits (TS
// 24.615), and gives 486 to a third.
//
// With -k the UE registers with IMS AKA instead of SIP Digest: -k and
// -opc (or -op) are what its USIM holds, and -sqn the highest sequence
// number the USIM has accepted. The UE then sends its first REGISTER from
// -listen without protection, and everything after the challenge from
// its protected server port -port-s to the P-CSCF's (TS 33.203 7.1).
// With -port-c it binds the protected client port as well, sends its
// requests from there and protects everything after the challenge with
// IPsec ESP, which needs NET_ADMIN in the container.
//
// Every command takes short messages over IP while the UE is registered
// (TS 24.341 5.3.2): it logs each one as "sms received", with its sender
// and text, and answers with a delivery report.
//
// With -epc, the UE attaches to the EPC stub before it registers, takes
// the P-CSCF address from the PCO, accepts the voice bearer that the
// network sets up for a call, and detaches after it deregisters.
// When the network detaches it, the UE stops with an error.
//
// After it registers, the UE subscribes to its registration state (TS
// 24.229 5.1.1.3) and logs each NOTIFY. When a NOTIFY says the network
// ended the registration, register and serve register again if the
// event is deactivated, as 5.1.1.7 asks; otherwise register exits with
// an error and serve stays unregistered. Call and answer only log it.
//
// When a refresh of the registration fails, register and serve make an
// initial registration again, and after each failure in a row wait
// longer before the next attempt (TS 24.229 5.1.1.2.1, 5.1.1.4.1, RFC
// 5626 4.5). Serve does the same when its first registration fails;
// the other commands end with an error then.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/amr"
	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/hepagent"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsec"
	"github.com/yuu-biz/ims-practice/internal/milenage"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
	"github.com/yuu-biz/ims-practice/internal/wav"
)

const usage = `usage: ue register [flags]
       ue call [flags] URI
       ue answer [flags]
       ue serve [flags]
       ue sms [flags] NUMBER TEXT
       ue xcap [flags] get | set SERVICE on|off [TARGET] | oir restricted|not-restricted | noreply SECONDS
       ue ctl [-control host:port] ACTION (see ue ctl help)

"ue COMMAND -h" lists the flags of COMMAND.

Exit status 0 means that the registration, or the call, succeeded; for
serve, that it stopped and deregistered; for sms, that the SC took the
short message.
`

// defaultMediaListen is where the RTP socket of a command listens when
// the command has no -media-listen: "ue register" and "ue sms" open one
// because every command of run does, and they send no media.
const defaultMediaListen = "0.0.0.0:0"

type options struct {
	cfg ue.Config
	// The flags that are not copied into cfg as they are, but need to
	// be turned into something once they are parsed.
	domain, features, k, opc, op, sqn, codecs string
	pcscf, listen                             string
	mediaListen                               string
	hepCollector                              string
	deregisterAfter                           time.Duration
	// Audio and recording.
	wavIn, amrIn, wavOut, sentOut, receivedOut string
	toneFor                                    time.Duration
	// Playing what the call receives on the learner's own player.
	play   bool
	player string
	// Answering.
	ringFor time.Duration
	reject  int
	// Calling.
	giveUpAfter time.Duration
	// takesCall is set for "ue answer", the one command of run that
	// answers a call.
	takesCall bool
	// statusReport and reportWait are those of "ue sms".
	statusReport bool
	reportWait   time.Duration
	// control is the address of the HTTP API of "ue serve".
	control string
	// portS is the protected server port of IMS AKA, and portC the
	// protected client port, which only IPsec ESP binds.
	portS, portC int
	// xcapRoot and utPassword are those of the XCAP client of "ue
	// xcap" and "ue serve".
	xcapRoot, utPassword string
	// epc is the UE API of the EPC stub to attach to; empty means no
	// access network, with the P-CSCF that -pcscf names.
	epc string
}

// A flagGroup defines the flags of one concern on the flag set of a
// command. A command defines only the groups whose flags it reads, so
// that "ue COMMAND -h" lists what the command takes, and the flag package
// rejects a flag that the command would ignore.
type flagGroup func(o *options, flags *flag.FlagSet)

// commandGroups are the flag groups of each command. The commands that
// register (register, call, answer, sms and serve) all take the
// registration flags; the rest are the command's own.
var commandGroups = map[string][]flagGroup{
	"register": {registrationFlags, registerFlags},
	"call":     {registrationFlags, mediaFlags, audioFlags, recordingFlags, callFlags},
	"answer":   {registrationFlags, mediaFlags, audioFlags, recordingFlags, answerFlags},
	"serve":    {registrationFlags, mediaFlags, audioFlags, answerFlags, shortMessageCentreFlag, controlFlag, xcapFlags},
	"sms":      {registrationFlags, shortMessageCentreFlag, smsFlags},
	"xcap":     {identityFlags, xcapFlags},
	"ctl":      {controlFlag},
}

// newFlagSet is the flag set of command, with the defaults of its flags
// in o; ok is false for a command that does not exist.
func newFlagSet(command string, o *options, handling flag.ErrorHandling) (flags *flag.FlagSet, ok bool) {
	groups, ok := commandGroups[command]
	if !ok {
		return nil, false
	}
	flags = flag.NewFlagSet(command, handling)
	o.mediaListen = defaultMediaListen
	for _, define := range groups {
		define(o, flags)
	}
	commandUsage := usage
	switch command {
	case "xcap":
		commandUsage = xcapUsage
	case "ctl":
		commandUsage = ctlUsage
	}
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), commandUsage)
		fmt.Fprintf(flags.Output(), "\nFlags of ue %s:\n", command)
		flags.PrintDefaults()
	}
	return flags, true
}

// identityFlags are who the UE is, for the commands that sign in with
// it: the registration, and the HTTP Digest of Ut.
func identityFlags(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.domain, "domain", "ims.mnc001.mcc001.3gppnetwork.org", "home network domain")
	flags.StringVar(&o.cfg.PrivateIdentity, "impi", "001010000000001@ims.mnc001.mcc001.3gppnetwork.org", "private user identity")
	flags.StringVar(&o.cfg.PublicIdentity, "impu", "sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org", "public user identity")
	flags.StringVar(&o.cfg.Password, "password", "test-password-1", "SIP Digest password")
}

// registrationFlags are what every command that registers takes: the
// identity, IMS AKA, where the UE attaches and registers from, and the
// registration itself.
func registrationFlags(o *options, flags *flag.FlagSet) {
	identityFlags(o, flags)
	flags.StringVar(&o.k, "k", "", "IMS AKA instead of SIP Digest: the USIM's K, 32 hexadecimal digits")
	flags.StringVar(&o.opc, "opc", "", "IMS AKA: the USIM's OPc, 32 hexadecimal digits")
	flags.StringVar(&o.op, "op", "", "IMS AKA: the operator's OP, 32 hexadecimal digits, from which the UE derives OPc (TS 35.206 4.1)")
	flags.StringVar(&o.sqn, "sqn", "000000000000", "IMS AKA: SQN_MS, the highest sequence number the USIM has accepted, 12 hexadecimal digits")
	flags.IntVar(&o.portS, "port-s", 5062, "IMS AKA: the protected server port (TS 33.203 7.1), on the address of -listen")
	flags.IntVar(&o.portC, "port-c", 0, "IMS AKA with IPsec ESP: the protected client port (TS 33.203 7.1), which the UE binds, sends its requests from and sets up ESP on; 0 leaves ESP off and the signalling readable in a capture")
	flags.StringVar(&o.pcscf, "pcscf", "172.28.0.40:5060", "P-CSCF address, host:port; with -epc only the port counts, and the address comes from the PCO")
	flags.StringVar(&o.epc, "epc", "", "URL of the UE API of the EPC stub, such as http://172.28.0.80:8080, to attach to before registering; the IMSI is the user part of -impi")
	flags.StringVar(&o.cfg.PCSCFURI, "pcscf-uri", "sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060", "P-CSCF URI for the Route of requests")
	flags.StringVar(&o.listen, "listen", "0.0.0.0:5060", "local address for SIP over UDP and TCP")
	flags.StringVar(&o.hepCollector, "hep", os.Getenv("UE_HEP"), "HEP collector, host:port, to copy every SIP message to; empty copies nothing (default from $UE_HEP)")
	flags.DurationVar(&o.cfg.Expires, "expires", ue.DefaultExpires, "registration interval to ask for")
	flags.StringVar(&o.features, "features", ue.DefaultFeatures.String()[1:], "feature parameters of the registered Contact (RFC 3840), separated by ';'; empty registers none")
}

// registerFlags are those of "ue register".
func registerFlags(o *options, flags *flag.FlagSet) {
	flags.DurationVar(&o.deregisterAfter, "deregister-after", 0, "deregister after this long; 0 stays registered until stopped")
}

// mediaFlags are how a command that makes or takes calls sets up their
// media and their codecs.
func mediaFlags(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.mediaListen, "media-listen", defaultMediaListen, "local UDP address for RTP")
	flags.StringVar(&o.codecs, "codecs", "", "speech codecs to offer and accept, most preferred first, of "+strings.Join(ue.VoLTECodecNames, ",")+
		"; empty means all of them with -epc (IR.92) and PCMU without, as a softphone offers")
	flags.BoolVar(&o.cfg.Calls.NoPreconditions, "no-preconditions", false, "do not use the precondition mechanism (TS 24.229 5.1.5A)")
}

// audioFlags are what a call plays.
func audioFlags(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.wavIn, "wav-in", "", "WAV file to play in a PCMU call (8 kHz, 16-bit, mono); empty plays a tone")
	flags.StringVar(&o.amrIn, "amr-in", "", "AMR or AMR-WB file (RFC 4867 storage format) to play in a call in that codec; empty plays a tone")
	flags.DurationVar(&o.toneFor, "tone", 3*time.Second, "length of the tone when there is no -wav-in or -amr-in")
}

// recordingFlags are what the one call of a command saves, and how it
// is played while it runs.
func recordingFlags(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.wavOut, "wav-out", "", "WAV file to write what was heard in a PCMU call to")
	flags.StringVar(&o.sentOut, "sent-out", "", "file to write what was sent to: the PCMU payloads, or an AMR or AMR-WB file of the frames")
	flags.StringVar(&o.receivedOut, "received-out", "", "file to write what was received to, in the same formats; "+
		standardOutput+" writes it to standard output while the call runs, for a player to read")
	flags.BoolVar(&o.play, "play", false, "play what is received while the call runs, on the player of -player; the call goes on when there is none")
	flags.StringVar(&o.player, "player", "", "the player to run for -play: "+playerName+
		", or a program that takes its options; empty looks for "+playerName+" on PATH")
}

// standardOutput is the name that stands for standard output in
// -received-out. The UE writes the stream there as the packets arrive,
// so a player at the other end of the pipe plays the call as it
// happens.
const standardOutput = "-"

// logWriter keeps the JSON log out of a recording that goes to standard
// output. Scripts read the log on standard output, so it only moves to
// standard error when the recording takes the stream's place.
func logWriter(o options) io.Writer {
	if o.receivedOut == standardOutput {
		return os.Stderr
	}
	return os.Stdout
}

// checkRecording rejects what the recording flags cannot name.
func (o options) checkRecording() error {
	for _, f := range []struct{ name, value string }{{"sent-out", o.sentOut}, {"wav-out", o.wavOut}} {
		if f.value == standardOutput {
			return fmt.Errorf("-%s cannot write to standard output; only -received-out streams, since one call has one standard output", f.name)
		}
	}
	return nil
}

// answerFlags are the answer policy of a command that takes calls ("ue
// serve" keeps it until the controller sets another).
func answerFlags(o *options, flags *flag.FlagSet) {
	flags.DurationVar(&o.ringFor, "ring", ue.DefaultRing, "how long to ring before answering")
	flags.IntVar(&o.reject, "reject", 0, "reject the call with this status code, such as 486")
}

// callFlags are those of "ue call".
func callFlags(o *options, flags *flag.FlagSet) {
	flags.DurationVar(&o.giveUpAfter, "give-up-after", 0, "hang up with CANCEL when there is no answer by then; 0 waits")
}

// shortMessageCentreFlag is where the short messages of a command that
// sends them go.
func shortMessageCentreFlag(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.cfg.SCAddress, "sc", "+12025550100", "number of the SC (service centre) that short messages go to, in international format")
}

// smsFlags are those of "ue sms".
func smsFlags(o *options, flags *flag.FlagSet) {
	flags.BoolVar(&o.statusReport, "status-report", false, "ask for a status report, and wait for it too")
	flags.DurationVar(&o.reportWait, "report-wait", 30*time.Second, "how long to wait for the submit report, and for the status report")
}

// controlFlag is the address of the HTTP control API: "ue serve" serves
// it and "ue ctl" calls it.
func controlFlag(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.control, "control", ":8090", "address of the HTTP control API")
}

// xcapFlags are the XCAP client of a command that changes the
// supplementary services over Ut.
func xcapFlags(o *options, flags *flag.FlagSet) {
	flags.StringVar(&o.xcapRoot, "xcap-root", "", "XCAP Root URI; empty is http://xcap.<domain> (TS 23.003 13.9.1.2)")
	flags.StringVar(&o.utPassword, "ut-password", "", "password of HTTP Digest on Ut; empty is -password")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	command := os.Args[1]
	var o options
	flags, ok := newFlagSet(command, &o, flag.ExitOnError)
	if !ok {
		if command == "-h" || command == "-help" || command == "--help" {
			fmt.Print(usage)
			return
		}
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	flags.Parse(os.Args[2:])
	o.cfg.HomeDomain = o.domain
	o.cfg.Features = sip.ParseParams(o.features)
	if o.k != "" {
		usim, err := usimConfig(o.k, o.opc, o.op, o.sqn)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		o.cfg.AKA = usim
		if err := protectedPorts(o.portS, o.portC); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		// The card of -k is a USIM without an ISIM, so -impu is the
		// temporary public identity derived from the IMSI.
		o.cfg.TemporaryPublicIdentity = true
	}
	codecs, err := o.codecList()
	if err != nil {
		fmt.Fprintln(os.Stderr, "-codecs:", err)
		os.Exit(2)
	}
	o.cfg.Calls.Codecs = codecs
	if err := o.checkRecording(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	log := slog.New(slog.NewJSONHandler(logWriter(o), nil))
	switch command {
	case "register":
		err = run(log, o, registerOnly)
	case "call":
		if flags.NArg() != 1 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		err = run(log, o, func(ctx context.Context, s *session) error { return s.call(ctx, flags.Arg(0)) })
	case "answer":
		o.takesCall = true
		err = run(log, o, func(ctx context.Context, s *session) error { return s.answer(ctx) })
	case "serve":
		err = serve(log, o)
	case "sms":
		if flags.NArg() != 2 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		err = run(log, o, func(ctx context.Context, s *session) error { return s.sms(ctx, flags.Arg(0), flags.Arg(1)) })
	case "xcap":
		if err = runXCAP(o, flags.Args()); errors.Is(err, errXCAPUsage) {
			fmt.Fprint(os.Stderr, xcapUsage)
			os.Exit(2)
		}
	case "ctl":
		if err = runCtl(o.control, flags.Args(), os.Stdout); errors.Is(err, errCtlUsage) {
			fmt.Fprint(os.Stderr, ctlUsage)
			os.Exit(2)
		}
	}
	if err != nil {
		log.Error("ue failed", "error", err.Error())
		os.Exit(1)
	}
}

// session is a registered UE with its UA and its RTP socket.
type session struct {
	o     options
	ue    *ue.UE
	ua    *sip.UA
	media *ue.Media
	reg   ue.Registration
	// registered is false while the UE waits to register again after a
	// failure; it then has no registration to end.
	registered bool
	line       *ue.Line
	// regEvent is the subscription to the registration state, and
	// deregistered its Lost: the state of a NOTIFY that took the
	// registration away (TS 24.229 5.1.1.7). Both are nil without a
	// subscription.
	regEvent     *ue.RegEvent
	deregistered <-chan ue.RegState
	log          *slog.Logger
}

// errDeregistered is how the UE ends when the network ended its
// registration.
type errDeregistered struct{ state ue.RegState }

func (e *errDeregistered) Error() string {
	return fmt.Sprintf("deregistered by the network, event %s", e.state.Event)
}

// opened is a UE ready to register: its access network, when it has
// one, its transport and the UA on it.
type opened struct {
	ue *ue.UE
	ua *sip.UA
	// detached is closed when the network detaches the UE; nil without
	// an access network.
	detached <-chan struct{}
	close    func()
}

// openUE attaches the UE to its access network, when -epc names one,
// and opens the SIP side of the UE: its transport, with a HEP agent
// when -hep asks for one, and a UA on it whose INVITEs go to line.
// close detaches the UE and closes the UA and the agent.
func openUE(log *slog.Logger, o options, line *ue.Line) (u *opened, err error) {
	target, err := resolve(o.pcscf)
	if err != nil {
		return nil, fmt.Errorf("-pcscf: %w", err)
	}
	var access *ue.EPCAccess
	var detached <-chan struct{}
	var attached ue.Attachment
	if o.epc != "" {
		imsi, err := ue.IMSIOf(o.cfg.PrivateIdentity)
		if err != nil {
			return nil, fmt.Errorf("-epc: %w", err)
		}
		access = ue.NewEPCAccess(o.epc, imsi, log)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		attached, err = access.Attach(ctx)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("attach: %w", err)
		}
		// TS 24.229 L.2.2.1: the first P-CSCF of the PCO is the most
		// preferred. The PCO gives an address only, so the port is the
		// one of -pcscf, 5060 by default.
		target = netip.AddrPortFrom(attached.PCSCFs[0], target.Port())
		o.cfg.AccessNetworkInfo = attached.AccessNetworkInfo
		o.cfg.Calls.Resources, detached = ue.FollowAccess(access)
	}
	o.cfg.PCSCF = target
	var agent *hepagent.Agent
	defer func() {
		if err != nil {
			agent.Close()
			if access != nil {
				access.Detach(context.Background())
			}
		}
	}()
	if o.hepCollector != "" {
		if agent, err = hepagent.New(o.hepCollector, 0, log); err != nil {
			return nil, fmt.Errorf("-hep: %w", err)
		}
	}
	transport, err := sip.ListenObserved(o.listen, log, agent.SIP("ue"))
	if err != nil {
		return nil, err
	}
	closeUnprotected := func() {}
	if o.cfg.AKA != nil {
		// TS 33.203 7.1: -listen takes only the REGISTERs without
		// protection and their responses (item 4); the UA is on the
		// protected server port.
		unprotected, err := unprotectedStack(transport, target, log)
		if err != nil {
			return nil, err
		}
		o.cfg.AKA.Unprotected, closeUnprotected = unprotected, unprotected.Close
		on := transport.LocalAddr().Addr()
		if o.portC != 0 {
			// The protected ports and the security associations are
			// bound to the UE's own address (TS 33.203 7.1), which
			// -listen need not name: the UE learns it from the route to
			// the P-CSCF, as its contact does.
			own, err := contactAddress(target, transport.LocalAddr())
			if err != nil {
				unprotected.Close()
				return nil, fmt.Errorf("-port-c: %w", err)
			}
			on = own.Addr()
		}
		listen := netip.AddrPortFrom(on, uint16(o.portS))
		// With ESP the UE sends its requests from the protected client
		// port, which the security associations are bound to (TS
		// 33.203 7.1 item 2). It talks to its P-CSCF alone, so every
		// peer of this transport layer is one that ESP protects.
		if transport, err = sip.ListenProtected(listen.String(), uint16(o.portC), nil, log, agent.SIP("ue")); err != nil {
			unprotected.Close()
			return nil, fmt.Errorf("-port-s: %w", err)
		}
		if o.portC != 0 {
			o.cfg.AKA.ESP = &ue.ESP{ProtectedClientPort: uint16(o.portC), Kernel: ipsec.NewKernel(log)}
		}
	}
	addr, err := contactAddress(target, transport.LocalAddr())
	if err != nil {
		closeUnprotected()
		return nil, err
	}
	if access != nil && addr.Addr() != attached.Address {
		// The stub hands out the static address of the subscriber,
		// which the container must have, since no tunnel carries the
		// UE's packets.
		closeUnprotected()
		return nil, fmt.Errorf("the PDN address %s is not the address %s that this host reaches the P-CSCF from", attached.Address, addr.Addr())
	}
	contact := ue.NewContact(o.cfg, addr)
	// The UA takes the MESSAGEs of SMS over IP before the UE that
	// handles them exists; none comes before the UE registers.
	var receiver atomic.Pointer[ue.UE]
	ua, err := sip.NewUA(transport, ue.UAConfig(sip.UAConfig{
		SentBy:     addr.String(),
		Contact:    contact.URI(),
		OnCall:     line.Ring,
		OnOptions:  line.Options,
		OwnHeaders: ue.OwnHeaders(o.cfg.AccessNetworkInfo),
		OnMessage: func(st *sip.ServerTransaction) {
			if u := receiver.Load(); u != nil {
				u.ReceiveSMS(st)
				return
			}
			st.Respond(sip.NewResponse(st.Request, 480, sip.NewTag()))
		},
		// TS 24.229 5.1.2A.1.1: every request of the UE goes to its
		// P-CSCF, at the protected server port with IMS AKA.
		Locate: func(sip.URI) ([]sip.Destination, error) {
			if u := receiver.Load(); u != nil {
				return []sip.Destination{u.Outbound()}, nil
			}
			return []sip.Destination{{Transport: sip.UDP, Addr: target}}, nil
		},
	}, o.cfg.Calls), log)
	if err != nil {
		closeUnprotected()
		return nil, err
	}
	created := ue.New(o.cfg, ua.Stack(), contact, log)
	receiver.Store(created)
	return &opened{ue: created, ua: ua, detached: detached, close: func() {
		ua.Close()
		closeUnprotected()
		if access != nil {
			// The UE switches off: it detaches after it deregistered (TS
			// 24.301 5.5.2.2.1).
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := access.Detach(ctx); err != nil {
				log.Warn("detach failed", "error", err.Error())
			}
			cancel()
		}
		agent.Close()
	}}, nil
}

// unprotectedStack is the UE's stack for the REGISTERs that no security
// association protects. It refuses the requests that come to it, which
// TS 33.203 7.1 item 4 does not let an unprotected port take.
func unprotectedStack(transport *sip.TransportLayer, pcscf netip.AddrPort, log *slog.Logger) (*sip.Stack, error) {
	sentBy, err := contactAddress(pcscf, transport.LocalAddr())
	if err != nil {
		transport.Close()
		return nil, err
	}
	stack, err := sip.NewStack(transport, sip.Config{SentBy: sentBy.String(), OnRequest: func(st *sip.ServerTransaction) {
		st.Respond(sip.NewResponse(st.Request, 403, sip.NewTag()))
	}}, log)
	if err != nil {
		transport.Close()
	}
	return stack, err
}

// protectedPorts checks -port-s and -port-c: a port number, and never
// one of the standard SIP ports, which TS 33.203 7.1 keeps apart from
// the protected ones. A -port-c of 0 leaves ESP off.
func protectedPorts(portS, portC int) error {
	for _, p := range []struct {
		flag string
		port int
	}{{"-port-s", portS}, {"-port-c", portC}} {
		switch {
		case p.flag == "-port-c" && p.port == 0:
		case p.port < 1 || p.port > 65535:
			return fmt.Errorf("%s: %d is no port number", p.flag, p.port)
		case p.port == 5060 || p.port == 5061:
			return fmt.Errorf("%s: %d is a standard SIP port, and a protected port is one of its own (TS 33.203 7.1)", p.flag, p.port)
		}
	}
	if portC == portS {
		return fmt.Errorf("-port-c: %d is the protected server port, and the two are different ports (TS 33.203 7.1)", portC)
	}
	return nil
}

// usimConfig reads the flags of IMS AKA. OP gives OPc with MILENAGE
// (TS 35.206 4.1), as a client such as SIPp takes it.
func usimConfig(k, opc, op, sqn string) (*ue.AKA, error) {
	var c ue.AKA
	if err := hexKey(c.Secret.K[:], "-k", k); err != nil {
		return nil, err
	}
	switch {
	case (opc == "") == (op == ""):
		return nil, errors.New("-k needs one of -opc and -op")
	case opc != "":
		if err := hexKey(c.Secret.OPc[:], "-opc", opc); err != nil {
			return nil, err
		}
	default:
		var o [16]byte
		if err := hexKey(o[:], "-op", op); err != nil {
			return nil, err
		}
		c.Secret.OPc = milenage.OPc(c.Secret.K, o)
	}
	n, err := strconv.ParseUint(sqn, 16, 48)
	if err != nil || len(sqn) != 12 {
		return nil, fmt.Errorf("-sqn %q: want 12 hexadecimal digits", sqn)
	}
	c.SQN = aka.SQN(n)
	return &c, nil
}

func hexKey(dst []byte, flag, s string) error {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(dst) {
		return fmt.Errorf("%s %q: want %d hexadecimal digits", flag, s, 2*len(dst))
	}
	copy(dst, b)
	return nil
}

func run(log *slog.Logger, o options, do func(context.Context, *session) error) error {
	s := &session{o: o, line: ue.NewLine(o.takesCall), log: log}
	opened, err := openUE(log, o, s.line)
	if err != nil {
		return err
	}
	defer opened.close()
	s.ue, s.ua = opened.ue, opened.ua
	if s.media, err = ue.ListenMedia(o.mediaListen); err != nil {
		return err
	}
	defer s.media.Close()
	// What the call receives can go to standard output, to the
	// learner's own player, or to both.
	var ways []*way
	if o.receivedOut == standardOutput {
		// The player reading the pipe may stop before the call does.
		keepWritingOnABrokenPipe()
		ways = append(ways, &way{what: "on standard output", w: os.Stdout})
	}
	if pb := newPlayback(o, log, func() (sdp.Codec, bool) {
		stream, ok := s.media.Stream()
		return stream.Codec, ok
	}); pb != nil {
		defer pb.close()
		ways = append(ways, &way{what: "on the player", w: pb})
	}
	if len(ways) > 0 {
		s.media.StreamReceivedTo(newListeners(log, ways...))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	ctx, lost := whileAttached(ctx, opened.detached)
	if s.reg, err = s.ue.Register(ctx); err != nil {
		return err
	}
	s.registered = true
	log.Info("registered", "expires", int(s.reg.Expires/time.Second),
		"public_identities", s.reg.PublicIdentities, "service_route", s.reg.ServiceRoute)
	s.subscribe()

	doErr := do(ctx, s)
	var gone *errDeregistered
	if errors.As(doErr, &gone) {
		return doErr
	}
	if lost() {
		// SIMPLIFIED: TS 24.301 5.5.2.3.4 and IR.92 2.4.2.1. The stub
		// detaches the UE with "re-attach not required" and no EMM cause,
		// item b of 5.5.2.3.4: the UE should start T3402, attach again
		// when it expires (5.2.2.3.3), and then register anew. When the
		// event stream of the stub ends, the UE should set its PDN
		// connection up again. The simulator stops instead, without the
		// deregistration that no longer reaches the IMS. One run of it
		// holds one attach: openUE sets up its SIP transport, its contact
		// and the media resources of its calls once, for the PDN address
		// of that attach.
		return errors.Join(doErr, errDetached)
	}
	if !s.registered {
		return doErr
	}
	// Deregister even after a signal, with a fresh deadline. TS 24.229
	// 5.1.1.6.1 keeps the reg event subscription: the S-CSCF ends it.
	dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dcancel()
	if err := s.ue.Deregister(dctx); err != nil && !errors.Is(err, context.Canceled) {
		return errors.Join(doErr, fmt.Errorf("deregistration: %w", err))
	}
	log.Info("deregistered")
	// The UE need not wait for the NOTIFY that ends the subscription,
	// but answers it when it comes soon, rather than leave the network
	// retransmitting it to an address the UE no longer listens on.
	if s.regEvent != nil {
		select {
		case <-s.regEvent.Done():
		case <-time.After(time.Second):
		}
	}
	return doErr
}

// codecList is the codecs of -codecs. A UE on the EPC is a VoLTE UE
// and offers the codecs of IR.92 by default; one without an access
// network is a softphone and offers PCMU.
func (o options) codecList() ([]sdp.Codec, error) {
	names := strings.Split(o.codecs, ",")
	switch {
	case o.codecs != "":
	case o.epc != "":
		names = ue.VoLTECodecNames
	default:
		return nil, nil
	}
	return ue.CodecsNamed(names)
}

// errDetached is how the UE ends when the network detached it.
var errDetached = errors.New("detached by the network")

// whileAttached is ctx, which also ends when detached is closed; lost
// tells whether that happened.
func whileAttached(ctx context.Context, detached <-chan struct{}) (context.Context, func() bool) {
	if detached == nil {
		return ctx, func() bool { return false }
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-detached:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() bool {
		select {
		case <-detached:
			return true
		default:
			return false
		}
	}
}

// subscribe is TS 24.229 5.1.1.3 after an initial registration. A UE
// whose SUBSCRIBE fails stays registered, and only logs the failure.
//
// SIMPLIFIED: TS 24.229 5.1.1.7. With the event rejected, the UE should
// also release the dialogs of the identities it lost. "ue call" and "ue
// answer" only log the NOTIFY, and the call goes on until it ends. Only
// removing the subscription from the HSS rejects a registration, and a
// call of the simulator ends by itself once its audio is sent.
func (s *session) subscribe() {
	s.regEvent, s.deregistered = nil, nil
	ev, err := s.ue.SubscribeRegEvent(s.ua, s.reg)
	if err != nil {
		s.log.Warn("reg event SUBSCRIBE not sent", "error", err.Error())
		return
	}
	s.regEvent, s.deregistered = ev, ev.Lost()
}

func registerOnly(ctx context.Context, s *session) error {
	if s.o.deregisterAfter > 0 {
		select {
		case <-time.After(s.o.deregisterAfter):
		case state := <-s.deregistered:
			return &errDeregistered{state}
		case <-ctx.Done():
		}
		return nil
	}
	for {
		initial := false
		select {
		case <-time.After(ims.RefreshAfter(s.reg.Expires)):
		case state := <-s.deregistered:
			if state.Event != reginfo.Deactivated {
				return &errDeregistered{state}
			}
			// TS 24.229 5.1.1.7: deactivated asks for a new initial
			// registration, after which the UE subscribes again.
			s.log.Info("registering again after a network-initiated deregistration")
			initial = true
		case <-ctx.Done():
			return nil
		}
		if err := s.registerAgain(ctx, initial); err != nil {
			return nil
		}
	}
}

// registerAgain refreshes the registration, or makes an initial one.
// A refresh that fails is followed at once by an initial registration
// (TS 24.229 5.1.1.4.1), unless its response asked for a wait with
// Retry-After. After an initial registration that fails the
// UE waits before the next one, longer after each failure in a row
// (5.1.1.2.1), until one succeeds; only the end of ctx stops it.
func (s *session) registerAgain(ctx context.Context, initial bool) error {
	var backoff ue.RegistrationBackoff
	for {
		reg, err := s.ue.Register(ctx)
		if err == nil {
			s.reg, s.registered = reg, true
			if initial {
				s.log.Info("registered", "expires", int(reg.Expires/time.Second),
					"public_identities", reg.PublicIdentities, "service_route", reg.ServiceRoute)
				s.subscribe()
			} else {
				s.log.Info("re-registered", "expires", int(reg.Expires/time.Second))
			}
			return nil
		}
		if ctx.Err() != nil {
			// A refresh that a signal cut short leaves the registration
			// in place, and run still deregisters it.
			return ctx.Err()
		}
		s.registered = false
		if !initial && !ue.AsksToWait(err) {
			s.log.Warn("re-registration failed; registering again from the start", "error", err.Error())
			initial = true
			continue
		}
		wait := backoff.Failed(err)
		s.log.Warn("registration failed", "error", err.Error(), "retry_after_ms", wait.Milliseconds())
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *session) call(ctx context.Context, target string) error {
	speech, err := s.o.speech()
	if err != nil {
		return err
	}
	callErr := s.ue.Call(ctx, s.ua, s.reg, target, s.media, speech, ue.CallOptions{GiveUpAfter: s.o.giveUpAfter})
	return errors.Join(callErr, s.save())
}

func (s *session) answer(ctx context.Context) error {
	speech, err := s.o.speech()
	if err != nil {
		return err
	}
	s.log.Info("waiting for a call")
	var ic *sip.IncomingCall
	select {
	case ic = <-s.line.Calls():
	case <-ctx.Done():
		return ctx.Err()
	}
	s.log.Info("incoming call", "from", ic.Request.Get("From"), "p_asserted_identity", ic.Request.Get("P-Asserted-Identity"))
	answerErr := s.ue.Answer(ctx, ic, s.media, speech, ue.AnswerOptions{PickUp: time.After(s.o.ringFor), Reject: s.o.reject})
	return errors.Join(answerErr, s.save())
}

// errNotAccepted is how "ue sms" ends when the SC refused the short
// message.
type errNotAccepted struct{ sent ue.SentSMS }

func (e *errNotAccepted) Error() string {
	return fmt.Sprintf("the SC refused the short message: RP-Cause %d, TP-FCS %#x", e.sent.RPCause, e.sent.FailureCause)
}

// sms sends one short message and waits for what TS 24.341 5.3.1.2 and
// 5.3.1.3 bring back. The UE logs the submit report and the status
// report as they come ("submit report", "status report").
func (s *session) sms(ctx context.Context, to, text string) error {
	sent, err := s.ue.SendSMS(ctx, ue.OutgoingSMS{To: to, Text: text, StatusReport: s.o.statusReport})
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, s.o.reportWait)
	defer cancel()
	done := func(h ue.SMSHistory) bool {
		for _, m := range h.Sent {
			if m.CallID == sent.CallID {
				sent = m
			}
		}
		return sent.State == ue.SubmitFailed ||
			sent.State == ue.SubmitAccepted && (!sent.StatusReportRequested || sent.StatusReport != nil)
	}
	if _, err := s.ue.WaitSMS(wait, done); err != nil {
		return fmt.Errorf("short message %s is %s: %w", sent.CallID, sent.State, err)
	}
	if sent.State == ue.SubmitFailed {
		return &errNotAccepted{sent}
	}
	return nil
}

// speech is what the UE plays in a call: a WAV file or a tone for
// PCMU, and an AMR or AMR-WB file or the embedded tones for those codecs.
func (o options) speech() (ue.Speech, error) {
	sp := ue.ToneSpeech(o.toneFor)
	if o.wavIn != "" {
		f, err := os.Open(o.wavIn)
		if err != nil {
			return ue.Speech{}, err
		}
		defer f.Close()
		if sp.PCM, err = wav.Read(f); err != nil {
			return ue.Speech{}, fmt.Errorf("-wav-in: %w", err)
		}
	}
	if o.amrIn != "" {
		f, err := os.Open(o.amrIn)
		if err != nil {
			return ue.Speech{}, err
		}
		defer f.Close()
		if _, err := sp.ReadAMR(f); err != nil {
			return ue.Speech{}, fmt.Errorf("-amr-in: %w", err)
		}
	}
	return sp, nil
}

// save writes what was sent and heard, for the e2e test and for
// listening afterwards: the payloads of PCMU, or the frames of AMR and
// AMR-WB in their storage format (RFC 4867 5).
func (s *session) save() error {
	stream, ok := s.media.Stream()
	stats := s.media.Stats()
	attrs := []any{"packets_sent", stats.PacketsSent, "packets_received", stats.PacketsReceived,
		"rtcp_sent", stats.RTCPSent, "rtcp_received", stats.RTCPReceived}
	if !ok {
		s.log.Info("media", attrs...)
		return nil
	}
	codec, _ := stream.Codec.AMRFormat()
	amrCall := stream.Codec.Name == sdp.NameAMR || stream.Codec.Name == sdp.NameAMRWB
	received, _ := s.media.Received()
	if amrCall {
		attrs = append(attrs, "frames_sent", stats.FramesSent, "frames_received", stats.FramesReceived)
	} else {
		attrs = append(attrs, "bytes_sent", len(s.media.Sent()), "bytes_received", len(received))
	}
	sent, heard := s.media.DTMF()
	attrs = append(attrs, "dtmf_sent", sent, "dtmf_received", heard)
	info := stream.CodecInfo()
	s.log.Info("media", append([]any{"codec", info.Name, "payload_type", info.PayloadType, "clock_rate", info.ClockRate,
		"payload_format", info.PayloadFormat, "telephone_event", info.TelephoneEvent}, attrs...)...)
	write := func(name string, frames []amr.Frame, pcmu []byte) error {
		if name == "" || name == standardOutput {
			// The stream of StreamReceivedTo already went out, packet by
			// packet, while the call ran.
			return nil
		}
		if !amrCall {
			return os.WriteFile(name, pcmu, 0o644)
		}
		f, err := os.Create(name)
		if err != nil {
			return err
		}
		return errors.Join(amr.WriteStorage(f, codec.Codec, frames), f.Close())
	}
	errs := []error{
		write(s.o.sentOut, s.media.SentFrames(), s.media.Sent()),
		write(s.o.receivedOut, s.media.ReceivedFrames(), received),
	}
	if err := s.media.StreamError(); err != nil {
		// The listener stopped reading, which ends the stream but not
		// the call.
		s.log.Warn("the stream of what was received stopped", "error", err.Error())
	}
	switch {
	case s.o.wavOut == "":
	case amrCall:
		// The UE never decodes AMR; ffmpeg turns
		// the file of -received-out into WAV.
		s.log.Info("no WAV for a call in " + stream.Codec.Name + ": write the frames with -received-out")
	default:
		f, err := os.Create(s.o.wavOut)
		if err == nil {
			err = wav.Write(f, g711.Decode(received))
			f.Close()
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func resolve(hostport string) (netip.AddrPort, error) {
	addr, err := net.ResolveUDPAddr("udp4", hostport)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return addr.AddrPort(), nil
}

// contactAddress is the local address the UE uses towards the P-CSCF,
// which is the one to write in Contact and Via. A socket bound to
// 0.0.0.0 does not know it, so the routing table is asked.
func contactAddress(pcscf, local netip.AddrPort) (netip.AddrPort, error) {
	if !local.Addr().IsUnspecified() {
		return local, nil
	}
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(pcscf))
	if err != nil {
		return netip.AddrPort{}, err
	}
	defer c.Close()
	ip := c.LocalAddr().(*net.UDPAddr).AddrPort().Addr()
	return netip.AddrPortFrom(ip.Unmap(), local.Port()), nil
}
