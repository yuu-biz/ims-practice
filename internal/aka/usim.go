package aka

import "crypto/subtle"

// USIM is the authentication function of the USIM, or of the ISIM for
// IMS (TS 33.203 6.1.1): the shared secret and the sequence numbers it
// has accepted. A USIM is not safe for concurrent use; the UE holds one
// per subscription and authenticates one challenge at a time.
type USIM struct {
	secret Secret
	// seqMS is the array SEQ_MS(0) .. SEQ_MS(a-1) of TS 33.102 C.2.2:
	// the highest SEQ accepted with each IND.
	seqMS [arraySize]uint64
}

// NewUSIM returns a USIM that has accepted sqnMS and nothing above it.
// A new card starts at 0 (TS 33.102 C.2.2 and C.3.2).
func NewUSIM(secret Secret, sqnMS SQN) *USIM {
	u := &USIM{secret: secret}
	u.seqMS[sqnMS.IND()] = sqnMS.SEQ()
	return u
}

// SQNMS is the highest sequence number the USIM has accepted: the
// highest SEQ_MS in the array with its index as IND (TS 33.102 C.2).
// It is what the USIM reports in an AUTS.
func (u *USIM) SQNMS() SQN {
	var ind uint
	for i, seq := range u.seqMS {
		if seq > u.seqMS[ind] {
			ind = uint(i)
		}
	}
	return NewSQN(u.seqMS[ind], ind)
}

// Response is what a USIM gives back for a challenge it accepts: RES
// for the network to compare with XRES, and the keys (TS 33.102 6.3.3).
type Response struct {
	RES []byte
	CK  CK
	IK  IK
}

// Authenticate is the user authentication function in the USIM, TS
// 33.102 6.3.3 (Figure 9). It fails with ErrMACFailure when the AUTN
// does not come from the home network, and with a
// SynchronisationFailure, which carries the AUTS, when its SQN is out of
// range. Only a challenge that passes both checks moves the USIM's
// sequence numbers forward.
//
// SIMPLIFIED: TS 33.102 C.2.2. The optional limit L on how far below
// SEQ_MS an accepted SEQ may be is not applied; Profile 2 suggests no
// value for it (C.3.2).
func (u *USIM) Authenticate(rand RAND, autn AUTN) (Response, error) {
	m := u.secret.milenage()
	concealed, amf, mac := autn.parts()
	sqn := reveal(concealed, m.F5(rand))
	xmac := m.F1(rand, sqn.octets(), amf)
	if subtle.ConstantTimeCompare(mac[:], xmac[:]) != 1 {
		return Response{}, ErrMACFailure
	}
	if !u.fresh(sqn) {
		return Response{}, SynchronisationFailure{AUTS: newAUTS(m, rand, u.SQNMS())}
	}
	u.seqMS[sqn.IND()] = sqn.SEQ()
	res := m.F2(rand)
	return Response{RES: res[:], CK: m.F3(rand), IK: m.F4(rand)}, nil
}

// fresh applies the checks of TS 33.102 C.2.1 (SEQ at most Δ above
// SEQ_MS) and C.2.2 (SEQ above the SEQ_MS kept for its IND).
func (u *USIM) fresh(sqn SQN) bool {
	highest := u.SQNMS().SEQ()
	if sqn.SEQ() > highest && sqn.SEQ()-highest > delta {
		return false
	}
	return sqn.SEQ() > u.seqMS[sqn.IND()]
}
