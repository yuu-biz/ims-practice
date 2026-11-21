package ims

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// MechanismIPsec3GPP is the sec-mechanism of IMS AKA: IPsec keyed with
// IK and CK (TS 33.203 Annex H, TS 24.229 5.1.1.2.2 item d).
const MechanismIPsec3GPP = "ipsec-3gpp"

// IPsec3GPP is one ipsec-3gpp sec-mechanism: the parameters of the two
// pairs of security associations that a UE offers in Security-Client
// and a P-CSCF in Security-Server (TS 33.203 7.2, Annex H).
type IPsec3GPP struct {
	// Preference is q, which only a Security-Server carries (RFC 3329
	// 2.3.1). Empty leaves it out.
	Preference string
	Transform
	// Protocol and Mode are prot and mod. Without them, as this project
	// writes them, they are esp and trans, the only ones IMS uses (Annex
	// H, TS 33.203 6).
	Protocol, Mode string
	// SPIC and SPIS are spi-c and spi-s, the SPIs of the inbound SAs at
	// the sender's protected client port and protected server port.
	SPIC, SPIS uint32
	// PortC and PortS are port-c and port-s, those ports (TS 33.203 7.1).
	PortC, PortS uint16
}

// Transform is a pair of integrity and encryption algorithm, alg and
// ealg, from which TS 33.203 7.2 has the P-CSCF and the UE choose.
type Transform struct {
	Algorithm string
	// EncryptionAlgorithm empty means null (Annex H).
	EncryptionAlgorithm string
}

func (t Transform) same(o Transform) bool {
	encryption := func(t Transform) string {
		if t.EncryptionAlgorithm == "" {
			return "null"
		}
		return t.EncryptionAlgorithm
	}
	return strings.EqualFold(t.Algorithm, o.Algorithm) && strings.EqualFold(encryption(t), encryption(o))
}

// SupportedTransforms are those that the P-CSCF and the UE simulator
// offer, most preferred first. There is one: HMAC-SHA2-256-128, which
// Annex H recommends over HMAC-SHA-1-96, with encryption null, which the
// UE has to accept (TS 33.203 7.2). A longer list makes the REGISTER
// that answers a challenge, with Security-Client and Security-Verify,
// longer than the 1300 bytes above which RFC 3261 18.1.1 sends it over
// TCP, from a port that the P-CSCF does not know (see its Association).
var SupportedTransforms = []Transform{
	{Algorithm: "hmac-sha2-256-128", EncryptionAlgorithm: "null"},
}

// ParseIPsec3GPP reads an ipsec-3gpp sec-mechanism. It needs alg, which
// Annex H makes mandatory, and the SPIs and ports, without which no
// security association can be set up (TS 24.229 5.1.1.5.1 item 3).
func ParseIPsec3GPP(m sip.SecurityMechanism) (IPsec3GPP, error) {
	if !strings.EqualFold(m.Name, MechanismIPsec3GPP) {
		return IPsec3GPP{}, fmt.Errorf("ims: sec-mechanism %q is not %s", m.Name, MechanismIPsec3GPP)
	}
	x := IPsec3GPP{}
	x.Preference, _ = m.Params.Get("q")
	x.Protocol, _ = m.Params.Get("prot")
	x.Mode, _ = m.Params.Get("mod")
	x.EncryptionAlgorithm, _ = m.Params.Get("ealg")
	var ok bool
	if x.Algorithm, ok = m.Params.Get("alg"); !ok || x.Algorithm == "" {
		return IPsec3GPP{}, fmt.Errorf("ims: %s without alg", m)
	}
	for _, n := range []struct {
		name string
		bits int
		set  func(uint64)
	}{
		{"spi-c", 32, func(v uint64) { x.SPIC = uint32(v) }},
		{"spi-s", 32, func(v uint64) { x.SPIS = uint32(v) }},
		{"port-c", 16, func(v uint64) { x.PortC = uint16(v) }},
		{"port-s", 16, func(v uint64) { x.PortS = uint16(v) }},
	} {
		s, _ := m.Params.Get(n.name)
		v, err := strconv.ParseUint(s, 10, n.bits)
		if err != nil || v == 0 {
			return IPsec3GPP{}, fmt.Errorf("ims: %s without a usable %s", m, n.name)
		}
		n.set(v)
	}
	return x, nil
}

// SecurityMechanism writes x in the order of the mech-parameters of
// Annex H.
func (x IPsec3GPP) SecurityMechanism() sip.SecurityMechanism {
	m := sip.SecurityMechanism{Name: MechanismIPsec3GPP}
	add := func(name, value string) {
		if value != "" {
			m.Params = append(m.Params, sip.Param{Name: name, Value: value})
		}
	}
	add("q", x.Preference)
	add("alg", x.Algorithm)
	add("prot", x.Protocol)
	add("mod", x.Mode)
	add("ealg", x.EncryptionAlgorithm)
	add("spi-c", strconv.FormatUint(uint64(x.SPIC), 10))
	add("spi-s", strconv.FormatUint(uint64(x.SPIS), 10))
	add("port-c", strconv.Itoa(int(x.PortC)))
	add("port-s", strconv.Itoa(int(x.PortS)))
	return m
}

// IPsec3GPPMechanisms reads the ipsec-3gpp sec-mechanisms of the named
// header field of m and skips those of other mechanisms, such as tls.
func IPsec3GPPMechanisms(m *sip.Message, header string) ([]IPsec3GPP, error) {
	mechanisms, err := sip.SecurityMechanisms(m, header)
	if err != nil {
		return nil, err
	}
	var out []IPsec3GPP
	for _, sm := range mechanisms {
		if !strings.EqualFold(sm.Name, MechanismIPsec3GPP) {
			continue
		}
		x, err := ParseIPsec3GPP(sm)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// ChooseTransform is the choice of TS 33.203 7.2, which the P-CSCF and
// the UE make alike: the first of the P-CSCF's mechanisms whose
// algorithms the UE offers too. It returns that mechanism of each side.
func ChooseTransform(pcscf, ue []IPsec3GPP) (fromPCSCF, fromUE IPsec3GPP, ok bool) {
	for _, p := range pcscf {
		if i := slices.IndexFunc(ue, func(u IPsec3GPP) bool { return u.Transform.same(p.Transform) }); i >= 0 {
			return p, ue[i], true
		}
	}
	return IPsec3GPP{}, IPsec3GPP{}, false
}

// NewSPI is an SPI for a new inbound SA that is none of taken (TS 33.203
// 7.1). 0 to 255 are not used: RFC 4303 2.1 reserves them.
func NewSPI(taken ...uint32) uint32 {
	for {
		var b [4]byte
		rand.Read(b[:])
		if spi := binary.BigEndian.Uint32(b[:]); spi > 255 && !slices.Contains(taken, spi) {
			return spi
		}
	}
}

// NewProtectedPort is a port number for a protected client port that is
// none of taken: random, as TS 33.203 7.1 NOTE 16 recommends for the
// UE, from the dynamic ports of RFC 6335 6, so never 5060 or 5061.
func NewProtectedPort(taken ...uint16) uint16 {
	for {
		var b [2]byte
		rand.Read(b[:])
		if port := 49152 + binary.BigEndian.Uint16(b[:])%16384; !slices.Contains(taken, port) {
			return port
		}
	}
}
