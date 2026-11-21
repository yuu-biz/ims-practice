package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/ue"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

const xcapUsage = `usage: ue xcap [flags] get
       ue xcap [flags] set SERVICE on|off [TARGET]
       ue xcap [flags] oir restricted|not-restricted
       ue xcap [flags] noreply SECONDS

SERVICE is one of cfu, cfb, cfnr, cfnrc, cfnl (call forwarding, which
needs a TARGET such as tel:+12025550102 to switch on), baic, bic-roam,
acr, baoc, boic and boic-exhc (barring). noreply sets how long a call
rings before cfnr diverts it, 5 to 180 seconds. Each prints the
settings as JSON after the change.
`

// userAgent is the User-Agent of the UE's XCAP requests (GSMA IR.92
// 2.6).
//
// SIMPLIFIED: GSMA IR.92 2.6. The value should be built as GSMA PRD
// RCC.07 C.4.1 says, which this project has not read. It is a plain
// product token.
const userAgent = "ims-practice-ue"

// xcapClient is the XCAP client of the UE: the XCAP Root URI of
// -xcap-root, or the one TS 23.003 13.9.1.2 builds from the home
// domain, and xui as the XUI. GSMA IR.92 2.3.2 has the XUI be the
// default public identity of the P-Associated-URI of the last
// registration, which "ue serve" passes.
//
// SIMPLIFIED: GSMA IR.92 2.3.2. "ue xcap" does not register, so it has
// no P-Associated-URI, and passes -impu, which stands for the first
// record of EF_IMPU in the ISIM that IR.92 falls back to. For a
// softphone that is its configured identity. A UE without an ISIM has
// no such record: there -impu is the temporary public identity, so "ue
// xcap" is for the softphones.
func xcapClient(o options, xui string) *xcap.Client {
	root := o.xcapRoot
	if root == "" {
		root = "http://xcap." + o.cfg.HomeDomain
	}
	password := o.utPassword
	if password == "" {
		password = o.cfg.Password
	}
	return &xcap.Client{Root: root, XUI: xui, Username: o.cfg.PrivateIdentity, Password: password, UserAgent: userAgent}
}

// runXCAP is "ue xcap": one change of the supplementary services over
// Ut, or a read of them. It needs no registration (GSMA IR.92 2.3.2).
func runXCAP(o options, args []string) error {
	if len(args) == 0 {
		return errXCAPUsage
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	services := ue.NewSupplementaryServices(xcapClient(o, o.cfg.PublicIdentity))
	var settings ue.Settings
	var err error
	switch {
	case args[0] == "get" && len(args) == 1:
		settings, err = services.Settings(ctx)
	case args[0] == "set" && (len(args) == 3 || len(args) == 4) && (args[2] == "on" || args[2] == "off"):
		target := ""
		if len(args) == 4 {
			target = args[3]
		}
		settings, err = services.SetService(ctx, ue.Service(args[1]), args[2] == "on", target)
	case args[0] == "oir" && len(args) == 2 && (args[1] == "restricted" || args[1] == "not-restricted"):
		behaviour := simservs.PresentationNotRestricted
		if args[1] == "restricted" {
			behaviour = simservs.PresentationRestricted
		}
		settings, err = services.SetOIRDefault(ctx, behaviour)
	case args[0] == "noreply" && len(args) == 2:
		seconds, convErr := strconv.Atoi(args[1])
		if convErr != nil {
			return errXCAPUsage
		}
		settings, err = services.SetNoReplyTimer(ctx, seconds)
	default:
		return errXCAPUsage
	}
	if err != nil {
		return err
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	return out.Encode(settings)
}

// errXCAPUsage is a command line that "ue xcap" does not take.
var errXCAPUsage = errors.New("ue xcap: bad arguments")
