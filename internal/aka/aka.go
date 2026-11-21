// Package aka is the Authentication and Key Agreement of TS 33.102
// (Release 19) 6.3 as IMS-AKA uses it (TS 33.203 6.1): the
// authentication vector that the HSS builds, the checks of the USIM
// (or ISIM) and the re-synchronisation of the sequence numbers, with
// the MILENAGE functions of TS 35.206. It also has the parts of RFC 3310
// that carry AKA in SIP Digest: the nonce, the auts and the password.
//
// The HSS uses NewAuthenticationVector and Resynchronise; the UE uses
// USIM. The package knows nothing of SIP or Diameter, so that both
// sides, and the tests, share one implementation.
package aka

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"

	"github.com/yuu-biz/ims-practice/internal/milenage"
)

// The lengths of TS 33.102 6.3.7.
type (
	// RAND is the random challenge.
	RAND [16]byte
	// AMF is the authentication management field.
	AMF [2]byte
	// CK is the cipher key.
	CK [16]byte
	// IK is the integrity key.
	IK [16]byte
	// AUTN is the authentication token SQN ⊕ AK || AMF || MAC.
	AUTN [16]byte
	// AUTS is the re-synchronisation token SQN_MS ⊕ AK || MAC-S, where
	// AK is the output of f5*.
	AUTS [14]byte
)

// Secret is what the USIM and the AuC of the home network share: the
// subscriber key K and OPc, which MILENAGE derives from the operator
// variant OP (TS 35.206 4.1).
type Secret struct {
	K   [16]byte
	OPc [16]byte
}

func (s Secret) milenage() milenage.Milenage { return milenage.New(s.K, s.OPc) }

// AuthenticationVector is one authentication vector of TS 33.102 6.3.2.
// Each is good for one authentication (TS 33.203 6.1.1).
type AuthenticationVector struct {
	RAND RAND
	// XRES is the expected response, 4 to 16 octets. MILENAGE makes 8.
	XRES []byte
	CK   CK
	IK   IK
	AUTN AUTN
}

// NewAuthenticationVector is the HE/AuC side of TS 33.102 6.3.2 (Figure
// 7): MAC = f1(SQN || RAND || AMF), XRES = f2(RAND), CK = f3(RAND),
// IK = f4(RAND), AK = f5(RAND) and AUTN = SQN ⊕ AK || AMF || MAC. The
// caller picks a fresh, unpredictable rand and the next SQN of the
// subscriber (SQN.Next).
func NewAuthenticationVector(secret Secret, rand RAND, sqn SQN, amf AMF) AuthenticationVector {
	m := secret.milenage()
	mac := m.F1(rand, sqn.octets(), amf)
	res := m.F2(rand)
	return AuthenticationVector{
		RAND: rand,
		XRES: res[:],
		CK:   m.F3(rand),
		IK:   m.F4(rand),
		AUTN: newAUTN(conceal(sqn, m.F5(rand)), amf, mac),
	}
}

func newAUTN(concealedSQN [6]byte, amf AMF, mac [8]byte) AUTN {
	var a AUTN
	copy(a[0:6], concealedSQN[:])
	copy(a[6:8], amf[:])
	copy(a[8:16], mac[:])
	return a
}

func (a AUTN) parts() (concealedSQN [6]byte, amf AMF, mac [8]byte) {
	copy(concealedSQN[:], a[0:6])
	copy(amf[:], a[6:8])
	copy(mac[:], a[8:16])
	return concealedSQN, amf, mac
}

// resynchronisationAMF is the AMF that MAC-S is computed with: all
// zeros, so that the AUTS need not carry it (TS 33.102 6.3.3).
var resynchronisationAMF AMF

func newAUTS(m milenage.Milenage, rand RAND, sqnMS SQN) AUTS {
	var a AUTS
	concealed := conceal(sqnMS, m.F5Star(rand))
	macS := m.F1Star(rand, sqnMS.octets(), resynchronisationAMF)
	copy(a[0:6], concealed[:])
	copy(a[6:14], macS[:])
	return a
}

func (a AUTS) parts() (concealedSQN [6]byte, macS [8]byte) {
	copy(concealedSQN[:], a[0:6])
	copy(macS[:], a[6:14])
	return concealedSQN, macS
}

// ErrMACFailure means that the MAC in an AUTN is not the one the USIM
// computes: the challenge does not come from the home network (TS
// 33.102 6.3.3). The UE answers without a RES and without an AUTS (TS
// 24.229 5.1.1.5.3).
var ErrMACFailure = errors.New("aka: MAC failure")

// SynchronisationFailure means that the SQN in an AUTN is not in the
// range the USIM accepts (TS 33.102 6.3.3). The UE sends AUTS so that
// the HSS can re-synchronise (TS 33.102 6.3.5).
type SynchronisationFailure struct {
	AUTS AUTS
}

func (SynchronisationFailure) Error() string { return "aka: synchronisation failure" }

// ErrInvalidAUTS means that the MAC-S in an AUTS is not the one the AuC
// computes, so the AuC keeps its SQN_HE (TS 33.102 6.3.5 steps 4 and
// 5).
var ErrInvalidAUTS = errors.New("aka: AUTS fails verification")

// Resynchronise is the HE/AuC side of the re-synchronisation, TS 33.102
// 6.3.5 steps 1 to 5. rand is the RAND sent with the AUTN that the USIM
// refused and auts is its answer. It returns the SQN_HE to generate the
// next authentication vector from (step 6): sqnHE itself when the next
// SQN it gives would already be accepted (step 3), otherwise SQN_MS of
// the USIM. When the AUTS fails verification, it returns sqnHE and
// ErrInvalidAUTS; the AuC still sends a new vector (step 6).
func Resynchronise(secret Secret, sqnHE SQN, rand RAND, auts AUTS) (SQN, error) {
	m := secret.milenage()
	concealed, macS := auts.parts()
	// Step 1: Conc(SQN_MS) ⊕ f5*(RAND).
	sqnMS := reveal(concealed, m.F5Star(rand))
	// Steps 2 and 3. The AuC knows only the highest SQN that the USIM
	// accepted, so it checks the next SQN against that one.
	if sqnHE.Next().acceptableAfter(sqnMS) {
		return sqnHE, nil
	}
	// Step 4.
	want := m.F1Star(rand, sqnMS.octets(), resynchronisationAMF)
	if subtle.ConstantTimeCompare(macS[:], want[:]) != 1 {
		return sqnHE, ErrInvalidAUTS
	}
	// Step 5.
	return sqnMS, nil
}

// SQN is a sequence number, 48 bits (TS 33.102 6.3.7). It follows
// Profile 2 of TS 33.102 Annex C.3.2, the profile that is not
// time-based: SQN = SEQ || IND, with IND the last 5 bits.
type SQN uint64

// The parameters of Profile 2, TS 33.102 C.3.2.
const (
	sqnBits = 48
	indBits = 5
	// arraySize is a, the number of SEQ_MS the USIM keeps (C.2.2).
	arraySize = 1 << indBits
	// delta is Δ, the largest step forward in SEQ that the USIM accepts
	// (C.2.1).
	delta = 1 << 28
)

// NewSQN returns SEQ || IND. ind is taken modulo the array size and
// seq is cut to the bits that remain.
func NewSQN(seq uint64, ind uint) SQN {
	return SQN((seq<<indBits | uint64(ind%arraySize)) & (1<<sqnBits - 1))
}

// SEQ is the counter part of the SQN.
func (s SQN) SEQ() uint64 { return uint64(s) >> indBits }

// IND is the index part of the SQN, which selects an entry of the
// USIM's array (C.1.2).
func (s SQN) IND() uint { return uint(s) % arraySize }

// Next is the SQN of the next authentication vector after SQN_HE s:
// SEQ goes up by 1 (C.1.1.2) and IND by 1 modulo the array size, the
// general rule of C.3.4.
//
// SIMPLIFIED: TS 33.102 C.3.4. The guidelines allow IND to stay the same
// for vectors to the same serving node, and to keep separate IND ranges
// per domain. IMS is one domain and the HSS sends one vector per MAR,
// so the general rule alone applies.
func (s SQN) Next() SQN { return NewSQN(s.SEQ()+1, s.IND()+1) }

// acceptableAfter reports whether a USIM whose highest accepted SQN is
// sqnMS would take s: SEQ must be above every SEQ_MS (C.2.2) and at
// most Δ above SEQ_MS (C.2.1).
func (s SQN) acceptableAfter(sqnMS SQN) bool {
	return s.SEQ() > sqnMS.SEQ() && s.SEQ()-sqnMS.SEQ() <= delta
}

func (s SQN) octets() [6]byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(s))
	var o [6]byte
	copy(o[:], b[2:])
	return o
}

func sqnOf(o [6]byte) SQN {
	var b [8]byte
	copy(b[2:], o[:])
	return SQN(binary.BigEndian.Uint64(b[:]))
}

// conceal is SQN ⊕ AK, which hides the SQN from a passive observer
// (TS 33.102 6.3.2).
func conceal(sqn SQN, ak [6]byte) [6]byte {
	o := sqn.octets()
	for i := range o {
		o[i] ^= ak[i]
	}
	return o
}

// reveal is (SQN ⊕ AK) ⊕ AK.
func reveal(concealed, ak [6]byte) SQN {
	for i := range concealed {
		concealed[i] ^= ak[i]
	}
	return sqnOf(concealed)
}
