// Package ipsec sets up the IPsec ESP that protects the SIP between a
// UE and the P-CSCF once IMS AKA has agreed on the security
// associations (TS 33.203 6.2, 6.3, 7.1, 7.2).
//
// One authenticated registration agrees on two pairs of unidirectional
// security associations, four in all: one pair between the UE's
// protected client port and the P-CSCF's protected server port, the
// other between the P-CSCF's protected client port and the UE's
// protected server port (TS 33.203 6.3). Both ends set up the same
// four: the two an end sends on are outbound there and inbound at the
// other end. This package turns one Agreement into the security
// associations and the policies of the Linux kernel, and back out
// again when the registration ends.
//
// The kernel is reached through "ip xfrm" (iproute2) rather than
// through netlink directly, so that a learner can read the same
// commands in the log, type them by hand and see their result with "ip
// xfrm state" and "ip xfrm policy". The node needs CAP_NET_ADMIN for
// them.
package ipsec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/ims"
)

// End is the end of an agreement that a node is at.
type End string

const (
	UEEnd    End = "ue"
	PCSCFEnd End = "pcscf"
)

// Agreement is what the security mode set-up of one authenticated
// registration agreed on (TS 33.203 7.2): the addresses of the two
// ends, the SPIs and protected ports each of them chose, the transform
// both chose, and the keys of the authentication.
type Agreement struct {
	// UE and PCSCF are the IP addresses of the two ends, which the
	// security associations are bound to (TS 33.203 7.1).
	UE, PCSCF netip.Addr
	// UEMechanism and PCSCFMechanism carry the SPIs and the protected
	// ports of each end, and the transform both chose: spi_uc, spi_us,
	// port_uc and port_us in the first, spi_pc, spi_ps, port_pc and
	// port_ps in the second.
	UEMechanism, PCSCFMechanism ims.IPsec3GPP
	// IK and CK are IK_IM and CK_IM of the authentication, from which
	// Annex I expands the keys of ESP.
	IK aka.IK
	CK aka.CK
}

// SecurityAssociation is one of the four unidirectional security
// associations of an agreement: the address and port it carries
// packets from, those it carries them to, and the SPI, which is the one
// of the inbound SA at its destination (TS 33.203 7.1).
type SecurityAssociation struct {
	Source, Destination netip.AddrPort
	SPI                 uint32
	// From is the end that sends on it, where it is outbound; at the
	// other end it is inbound.
	From End
}

// SecurityAssociations are the four security associations of the
// agreement, pair by pair: the UE's protected client port and the
// P-CSCF's protected server port first, then the P-CSCF's protected
// client port and the UE's protected server port (TS 33.203 7.1 rule
// 1). Each pair carries the requests of one end and the responses that
// come back to them.
func (a Agreement) SecurityAssociations() []SecurityAssociation {
	ue, pcscf := a.UEMechanism, a.PCSCFMechanism
	uc := netip.AddrPortFrom(a.UE, ue.PortC)
	us := netip.AddrPortFrom(a.UE, ue.PortS)
	pc := netip.AddrPortFrom(a.PCSCF, pcscf.PortC)
	ps := netip.AddrPortFrom(a.PCSCF, pcscf.PortS)
	return []SecurityAssociation{
		{Source: uc, Destination: ps, SPI: pcscf.SPIS, From: UEEnd},
		{Source: ps, Destination: uc, SPI: ue.SPIC, From: PCSCFEnd},
		{Source: pc, Destination: us, SPI: ue.SPIS, From: PCSCFEnd},
		{Source: us, Destination: pc, SPI: pcscf.SPIC, From: UEEnd},
	}
}

// transform is how the kernel names a transform of TS 33.203 Annex H.
type transform struct {
	// integrity is the kernel's name for the integrity algorithm, and
	// truncation the number of bits of the integrity check value that
	// ESP carries.
	integrity  string
	truncation int
	// encryption is the kernel's name for the encryption algorithm.
	// Null encryption is a cipher of its own there, with an empty key.
	encryption string
}

// transforms are the transforms this package sets up, by the names that
// Annex H gives their algorithms. There is one, the one that
// ims.SupportedTransforms offers: HMAC-SHA2-256-128, which is
// AUTH_HMAC_SHA2_256_128 of RFC 4868 with its 256-bit key and its
// 128-bit integrity check value, and null encryption, which Annex H
// allows and TS 33.203 7.2 has the P-CSCF choose when its policy asks
// for no confidentiality.
var transforms = map[ims.Transform]transform{
	{Algorithm: "hmac-sha2-256-128", EncryptionAlgorithm: "null"}: {
		integrity: "hmac(sha256)", truncation: 128, encryption: "ecb(cipher_null)",
	},
}

// mechanism is the kernel's transform for the mechanism that the two
// ends agreed on. Only ESP in transport mode is set up here: TS 33.203
// 6 allows no other protocol, and transport mode is the one of 7.1 for
// a UE that is not behind a NAT. An absent prot or mod means esp and
// trans (Annex H).
func mechanism(m ims.IPsec3GPP) (transform, error) {
	if p := strings.ToLower(m.Protocol); p != "" && p != "esp" {
		return transform{}, fmt.Errorf("ipsec: no support for prot %q, only esp", m.Protocol)
	}
	if mode := strings.ToLower(m.Mode); mode != "" && mode != "trans" {
		return transform{}, fmt.Errorf("ipsec: no support for mod %q, only trans", m.Mode)
	}
	return lookup(m.Transform)
}

// lookup is the kernel's transform for t, whose empty encryption
// algorithm means null (TS 33.203 Annex H).
func lookup(t ims.Transform) (transform, error) {
	key := ims.Transform{Algorithm: strings.ToLower(t.Algorithm), EncryptionAlgorithm: strings.ToLower(t.EncryptionAlgorithm)}
	if key.EncryptionAlgorithm == "" {
		key.EncryptionAlgorithm = "null"
	}
	k, ok := transforms[key]
	if !ok {
		return transform{}, fmt.Errorf("ipsec: no ESP for alg %q with ealg %q", t.Algorithm, t.EncryptionAlgorithm)
	}
	return k, nil
}

// IKESP is the integrity key of the ESP security associations, the same
// for all four of them (TS 33.203 6.3). For HMAC-SHA2-256-128 Annex I
// derives it with the key derivation function of TS 33.220 Annex B.2.0,
// HMAC-SHA-256 over the string S, keyed with CK_IM || IK_IM.
func IKESP(t ims.Transform, ik aka.IK, ck aka.CK) ([]byte, error) {
	k, err := lookup(t)
	if err != nil {
		return nil, err
	}
	if k.integrity != "hmac(sha256)" {
		return nil, fmt.Errorf("ipsec: no key expansion for %q", t.Algorithm)
	}
	// Annex I: FC = 0x5A, P0 = "HMAC-SHA2-256-128", L0 = its length.
	const p0 = "HMAC-SHA2-256-128"
	s := append([]byte{0x5A}, p0...)
	s = append(s, byte(len(p0)>>8), byte(len(p0)))
	mac := hmac.New(sha256.New, append(ck[:], ik[:]...))
	mac.Write(s)
	return mac.Sum(nil), nil
}

// Command is one "ip" command, its arguments without the program.
type Command []string

// String is the command as a shell needs it, which is how the log shows
// it and how a learner types it: the words that a shell would take
// apart or swallow are quoted.
func (c Command) String() string {
	words := make([]string, len(c))
	for i, w := range c {
		if w == "" || strings.ContainsAny(w, " \t'\"()$&|<>") {
			words[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		} else {
			words[i] = w
		}
	}
	return "ip " + strings.Join(words, " ")
}

// Setup is the "ip xfrm" commands that set the agreement up at end.
// lifetime is how long the associations may live at most.
//
// Each security association becomes one entry of the kernel's security
// association database, in transport mode and with the anti-replay
// service that TS 33.203 6.3 asks for. TS 33.203 7.1 fixes the SA
// duration at 2^32-1 seconds and NOTE 4 leaves the lifetime to the SIP
// application; the kernel gets a hard lifetime all the same, so that
// the associations of a UE that stops answering go even though no
// registration ends to take them out (7.4.2a: the P-CSCF deletes any SA
// whose lifetime is exceeded). Refresh moves it when a registration is
// refreshed without a new authentication.
//
// Each association also becomes two policies, which bind it to the SIP
// between the two protected ports: one for UDP and one for TCP, since
// the two share the association (TS 33.203 7.1) and "ip xfrm" names the
// ports of a selector only under a transport protocol. The policy in
// the direction the node sends has the kernel protect those packets;
// the one in the direction it receives has the kernel discard the
// packets that arrive on those ports without ESP, which is what TS
// 33.203 7.1 items 3 and 4 ask of a protected port. The policy points
// at its association by reqid, which this package sets to the SPI: the
// two associations a node sends on run between the same two addresses
// and differ only in their ports and SPIs, so without it the kernel
// could protect a packet with either.
//
// Setting the same agreement up twice leaves the kernel as one set-up
// would: each state is added after a deleteall of its own SPI between
// the same two addresses, which succeeds whether or not one is there,
// and a policy is updated, which the kernel takes as add or replace. A
// state cannot be updated into being: the kernel answers ESRCH to an
// update of an SPI it does not hold.
func (a Agreement) Setup(end End, lifetime time.Duration) ([]Command, error) {
	return a.states(end, "add", lifetime, true)
}

// Refresh is the commands that give the associations of the agreement a
// new lifetime, for a registration that was refreshed without a new
// authentication (TS 33.203 7.4.2a raises the lifetime so that it ends
// shortly after the registration). It changes nothing else, and the
// policies stay as Setup left them.
func (a Agreement) Refresh(end End, lifetime time.Duration) ([]Command, error) {
	return a.states(end, "update", lifetime, false)
}

// states are the commands for the four security associations, with
// their policies when withPolicies.
func (a Agreement) states(end End, verb string, lifetime time.Duration, withPolicies bool) ([]Command, error) {
	k, err := mechanism(a.UEMechanism)
	if err != nil {
		return nil, err
	}
	key, err := IKESP(a.UEMechanism.Transform, a.IK, a.CK)
	if err != nil {
		return nil, err
	}
	seconds := int(lifetime.Round(time.Second) / time.Second)
	if seconds < 1 {
		return nil, fmt.Errorf("ipsec: the security associations have %s left to live", lifetime)
	}
	var out []Command
	for _, sa := range a.SecurityAssociations() {
		if verb == "add" {
			// A state of this SPI that an earlier set-up left behind
			// would make the add fail. deleteall succeeds with nothing
			// to delete, and names the SPI and both addresses, so it
			// touches no other UE's associations.
			out = append(out, sa.deleteState())
		}
		out = append(out, Command{"xfrm", "state", verb,
			"src", sa.Source.Addr().String(), "dst", sa.Destination.Addr().String(),
			"proto", "esp", "spi", spi(sa.SPI), "reqid", reqid(sa.SPI), "mode", "transport",
			"replay-window", "32", "limit", "time-hard", strconv.Itoa(seconds),
			// The key of null encryption is empty, which iproute2 reads
			// from an empty argument; "0x" alone it reads as the two
			// characters.
			"auth-trunc", k.integrity, "0x" + hex.EncodeToString(key), strconv.Itoa(k.truncation),
			"enc", k.encryption, ""})
		if withPolicies {
			out = append(out, sa.policies(end, "update", seconds)...)
		}
	}
	return out, nil
}

// Teardown is the commands that delete what Setup added. Each of them
// deletes what matches a selector of this agreement alone, and succeeds
// when there is nothing left to delete, so a teardown that runs twice,
// or one that follows a set-up that got only halfway, ends in the same
// state.
func (a Agreement) Teardown(end End) []Command {
	var out []Command
	for _, sa := range a.SecurityAssociations() {
		out = append(out, sa.deleteState())
		out = append(out, sa.policies(end, "deleteall", 0)...)
	}
	return out
}

// deleteState is the command that deletes this association alone: its
// SPI between its two addresses. "ip xfrm state deleteall" with a
// selector deletes what matches and is content with nothing, where
// "delete" answers that there is no such file.
func (sa SecurityAssociation) deleteState() Command {
	return Command{"xfrm", "state", "deleteall", "src", sa.Source.Addr().String(),
		"dst", sa.Destination.Addr().String(), "proto", "esp", "spi", spi(sa.SPI)}
}

// policies are the policy commands of one security association, one for
// UDP and one for TCP. The association is outbound at the end that
// sends on it and inbound at the other. A deleteall names the selector
// alone, which is what identifies a policy.
func (sa SecurityAssociation) policies(end End, verb string, seconds int) []Command {
	direction := "in"
	if sa.From == end {
		direction = "out"
	}
	var out []Command
	for _, protocol := range []string{"udp", "tcp"} {
		c := Command{"xfrm", "policy", verb,
			"src", prefix(sa.Source.Addr()), "dst", prefix(sa.Destination.Addr()),
			"proto", protocol, "sport", strconv.Itoa(int(sa.Source.Port())),
			"dport", strconv.Itoa(int(sa.Destination.Port())), "dir", direction}
		if verb != "deleteall" {
			c = append(c, "limit", "time-hard", strconv.Itoa(seconds),
				"tmpl", "src", sa.Source.Addr().String(), "dst", sa.Destination.Addr().String(),
				"proto", "esp", "reqid", reqid(sa.SPI), "mode", "transport")
		}
		out = append(out, c)
	}
	return out
}

// prefix is the address as a selector of one host.
func prefix(a netip.Addr) string { return a.String() + "/" + strconv.Itoa(a.BitLen()) }

func spi(v uint32) string   { return "0x" + strconv.FormatUint(uint64(v), 16) }
func reqid(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

// Kernel is the kernel's security association and policy databases,
// which it reaches by running "ip xfrm".
type Kernel struct {
	log *slog.Logger
	// Run takes the place of running the commands, for a test.
	Run func(ctx context.Context, cmds []Command) error
}

// NewKernel is the kernel of this host.
func NewKernel(log *slog.Logger) *Kernel { return &Kernel{log: log} }

// runFor bounds one run of ip, which talks to the kernel and to nothing
// else.
const runFor = 10 * time.Second

// Setup sets the agreement's security associations up at end, to live
// for at most lifetime.
func (k *Kernel) Setup(ctx context.Context, a Agreement, end End, lifetime time.Duration) error {
	cmds, err := a.Setup(end, lifetime)
	if err != nil {
		return err
	}
	if err := k.run(ctx, cmds, false); err != nil {
		return err
	}
	k.log.Info("ESP security associations set up", "end", string(end), "ue", a.UE.String(),
		"port_uc", a.UEMechanism.PortC, "port_us", a.UEMechanism.PortS,
		"port_pc", a.PCSCFMechanism.PortC, "port_ps", a.PCSCFMechanism.PortS,
		"lifetime", lifetime.Round(time.Second).String())
	return nil
}

// Refresh gives them a new lifetime.
func (k *Kernel) Refresh(ctx context.Context, a Agreement, end End, lifetime time.Duration) error {
	cmds, err := a.Refresh(end, lifetime)
	if err != nil {
		return err
	}
	return k.run(ctx, cmds, false)
}

// Teardown deletes them again. It gives the caller no error: the
// associations are going either way, the SIP layer has nothing to put
// in their place, and a response that waited for the kernel would reach
// the UE late or not at all. It runs every command even after one has
// failed, so that as little as possible is left behind, and what the
// kernel refused is in the log.
func (k *Kernel) Teardown(ctx context.Context, a Agreement, end End) {
	if err := k.run(ctx, a.Teardown(end), true); err != nil {
		k.log.Warn("ESP security associations may stay in the kernel", "end", string(end),
			"ue", a.UE.String(), "error", err.Error())
		return
	}
	k.log.Info("ESP security associations torn down", "end", string(end), "ue", a.UE.String())
}

// secret is a key in the text that ip wrote back, which the log is no
// place for.
var secret = regexp.MustCompile(`0x[0-9a-f]{32,}`)

// run runs each command with an ip of its own, rather than one "ip
// -batch": a batch stops at the first command that fails, and only the
// first deleteall of a batch takes effect, since ip reuses one netlink
// socket for the dump that deleteall walks. keepGoing runs the rest
// after a command has failed and returns the first error.
func (k *Kernel) run(ctx context.Context, cmds []Command, keepGoing bool) error {
	if k.Run != nil {
		return k.Run(ctx, cmds)
	}
	var first error
	for _, c := range cmds {
		if err := k.one(ctx, c); err != nil {
			if first == nil {
				first = err
			}
			if !keepGoing {
				return first
			}
		}
	}
	return first
}

func (k *Kernel) one(ctx context.Context, c Command) error {
	ctx, cancel := context.WithTimeout(ctx, runFor)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ip", c...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", c, err, secret.ReplaceAllString(strings.TrimSpace(string(out)), "0x..."))
	}
	return nil
}
