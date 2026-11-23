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
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"

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
		{Source: uc, Destination: ps, SPI: pcscf.SPIS},
		{Source: ps, Destination: uc, SPI: ue.SPIC},
		{Source: pc, Destination: us, SPI: ue.SPIS},
		{Source: us, Destination: pc, SPI: pcscf.SPIC},
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

// Setup is the "ip xfrm" commands that set the agreement up at end, one
// to a line, for "ip -batch".
//
// Each security association becomes one entry of the kernel's security
// association database, in transport mode and with the anti-replay
// service that TS 33.203 6.3 asks for. It carries no lifetime of its
// own: TS 33.203 7.1 fixes the SA duration at 2^32-1 seconds and NOTE 4
// leaves the lifetime to the SIP application, which deletes the
// associations with Teardown.
//
// Each of them also becomes two policies, which bind it to the SIP
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
// The commands update rather than add, so that setting the same
// agreement up twice, or setting a new one up over the ports of the one
// it replaces, leaves the kernel in the same state as setting it up
// once (TS 33.203 7.4.0: an authenticated re-registration replaces the
// associations).
func (a Agreement) Setup(end End) (string, error) {
	k, err := lookup(a.UEMechanism.Transform)
	if err != nil {
		return "", err
	}
	key, err := IKESP(a.UEMechanism.Transform, a.IK, a.CK)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, sa := range a.SecurityAssociations() {
		fmt.Fprintf(&b, "state update src %s dst %s proto esp spi %s reqid %s mode transport replay-window 32"+
			" auth-trunc %s 0x%x %d enc %s 0x\n",
			sa.Source.Addr(), sa.Destination.Addr(), spi(sa.SPI), reqid(sa.SPI),
			k.integrity, key, k.truncation, k.encryption)
		for _, p := range sa.policies(a.own(end), "update") {
			b.WriteString(p)
		}
	}
	return b.String(), nil
}

// Teardown is the "ip xfrm" commands that delete what Setup added. The
// states go by address pair, since the four associations of one UE are
// the only ESP between those two addresses.
func (a Agreement) Teardown(end End) string {
	var b strings.Builder
	for i, sa := range a.SecurityAssociations() {
		if i%2 == 0 {
			// Both associations of a pair run between the same two
			// addresses as the other pair's, so one deleteall for each
			// direction is enough.
			fmt.Fprintf(&b, "state deleteall src %s dst %s proto esp\n", sa.Source.Addr(), sa.Destination.Addr())
		}
		for _, p := range sa.policies(a.own(end), "delete") {
			b.WriteString(p)
		}
	}
	return b.String()
}

// policies are the policy commands of one security association, one for
// UDP and one for TCP. own is the address of the end the commands are
// for, which tells its outbound policies from its inbound ones. A
// delete names the selector alone, which is what identifies a policy.
func (sa SecurityAssociation) policies(own netip.Addr, verb string) []string {
	direction := "in"
	if sa.Source.Addr() == own {
		direction = "out"
	}
	var out []string
	for _, protocol := range []string{"udp", "tcp"} {
		line := fmt.Sprintf("policy %s src %s/%d dst %s/%d proto %s sport %d dport %d dir %s",
			verb, sa.Source.Addr(), sa.Source.Addr().BitLen(), sa.Destination.Addr(), sa.Destination.Addr().BitLen(),
			protocol, sa.Source.Port(), sa.Destination.Port(), direction)
		if verb != "delete" {
			line += fmt.Sprintf(" tmpl src %s dst %s proto esp reqid %s mode transport",
				sa.Source.Addr(), sa.Destination.Addr(), reqid(sa.SPI))
		}
		out = append(out, line+"\n")
	}
	return out
}

// own is the address of end, this node's own in the commands it sets up.
func (a Agreement) own(end End) netip.Addr {
	if end == UEEnd {
		return a.UE
	}
	return a.PCSCF
}

func spi(v uint32) string   { return "0x" + strconv.FormatUint(uint64(v), 16) }
func reqid(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

// Kernel is the kernel's security association and policy databases,
// which it reaches by running "ip xfrm" in batch.
type Kernel struct {
	log *slog.Logger
	// Run takes the place of running ip, for a test. force asks it to
	// carry on past an error, as a teardown does.
	Run func(ctx context.Context, force bool, batch string) error
}

// NewKernel is the kernel of this host.
func NewKernel(log *slog.Logger) *Kernel { return &Kernel{log: log} }

// Setup sets the agreement's security associations up at end.
func (k *Kernel) Setup(ctx context.Context, a Agreement, end End) error {
	batch, err := a.Setup(end)
	if err != nil {
		return err
	}
	if err := k.run(ctx, false, batch); err != nil {
		return err
	}
	k.log.Info("ESP security associations set up", "end", string(end), "ue", a.UE.String(),
		"port_uc", a.UEMechanism.PortC, "port_us", a.UEMechanism.PortS,
		"port_pc", a.PCSCFMechanism.PortC, "port_ps", a.PCSCFMechanism.PortS)
	return nil
}

// Teardown deletes them again. It carries on past the ones that are
// gone already, so that the end state is the same however often it
// runs.
func (k *Kernel) Teardown(ctx context.Context, a Agreement, end End) error {
	if err := k.run(ctx, true, a.Teardown(end)); err != nil {
		return err
	}
	k.log.Info("ESP security associations torn down", "end", string(end), "ue", a.UE.String())
	return nil
}

func (k *Kernel) run(ctx context.Context, force bool, batch string) error {
	if k.Run != nil {
		return k.Run(ctx, force, batch)
	}
	args := []string{"-batch", "-"}
	if force {
		args = append([]string{"-force"}, args...)
	}
	cmd := exec.CommandContext(ctx, "ip", args...)
	cmd.Stdin = strings.NewReader(batch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
