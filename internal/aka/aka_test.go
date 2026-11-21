package aka

import (
	"encoding/hex"
	"errors"
	"testing"
)

// The inputs of MILENAGE test set 1, TS 35.208 4.3.1.
var (
	set1Secret = Secret{K: block("465b5ce8b199b49faa5f0a2ee238a6bc"), OPc: block("cd63cb71954a9f4e48a5994e37a02baf")}
	set1RAND   = RAND(block("23553cbe9637a89d218ae64dae47bf35"))
	set1SQN    = SQN(0xff9bb4d0b607)
	set1AMF    = AMF{0xb9, 0xb9}
)

func block(s string) [16]byte {
	var b [16]byte
	if n, err := hex.Decode(b[:], []byte(s)); err != nil || n != 16 {
		panic(s)
	}
	return b
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }

// TestAuthenticationVectorFromTestSet1 checks the vector against the
// outputs that TS 35.208 4.3.1 lists for the same inputs: AUTN is SQN ⊕
// f5 (ff9bb4d0b607 ⊕ aa689c648370), AMF and f1.
func TestAuthenticationVectorFromTestSet1(t *testing.T) {
	av := NewAuthenticationVector(set1Secret, set1RAND, set1SQN, set1AMF)
	if got, want := hexOf(av.AUTN[:]), "55f328b43577"+"b9b9"+"4a9ffac354dfafb3"; got != want {
		t.Errorf("AUTN = %s, want %s", got, want)
	}
	if got := hexOf(av.XRES); got != "a54211d5e3ba50bf" {
		t.Errorf("XRES = %s", got)
	}
	if got := hexOf(av.CK[:]); got != "b40ba9a3c58b2a05bbf0d987b21bf8cb" {
		t.Errorf("CK = %s", got)
	}
	if got := hexOf(av.IK[:]); got != "f769bcd751044604127672711c6d3441" {
		t.Errorf("IK = %s", got)
	}
	if av.RAND != set1RAND {
		t.Errorf("RAND = %x", av.RAND)
	}
}

// TestUSIMAcceptsTheNetworksVector is one authentication end to end: the
// USIM accepts the AUTN, and its RES and keys are those of the vector.
func TestUSIMAcceptsTheNetworksVector(t *testing.T) {
	sqnHE := NewSQN(0, 0)
	usim := NewUSIM(set1Secret, NewSQN(0, 0))
	for range 3 {
		sqnHE = sqnHE.Next()
		av := NewAuthenticationVector(set1Secret, set1RAND, sqnHE, set1AMF)
		r, err := usim.Authenticate(av.RAND, av.AUTN)
		if err != nil {
			t.Fatalf("SQN %d: %v", sqnHE, err)
		}
		if hexOf(r.RES) != hexOf(av.XRES) || r.CK != av.CK || r.IK != av.IK {
			t.Fatalf("response %+v does not match vector %+v", r, av)
		}
		if usim.SQNMS() != sqnHE {
			t.Errorf("SQN_MS = %d, want %d", usim.SQNMS(), sqnHE)
		}
	}
}

func TestUSIMRefusesAnotherKey(t *testing.T) {
	other := set1Secret
	other.K[0] ^= 1
	av := NewAuthenticationVector(other, set1RAND, NewSQN(1, 1), set1AMF)
	usim := NewUSIM(set1Secret, 0)
	if _, err := usim.Authenticate(av.RAND, av.AUTN); !errors.Is(err, ErrMACFailure) {
		t.Fatalf("err = %v, want ErrMACFailure", err)
	}
	if usim.SQNMS() != 0 {
		t.Errorf("a refused challenge moved SQN_MS to %d", usim.SQNMS())
	}
}

// TestUSIMSequenceNumberRange covers the freshness checks of TS 33.102
// C.2.1 and C.2.2.
func TestUSIMSequenceNumberRange(t *testing.T) {
	accepted := NewSQN(100, 7)
	for _, tc := range []struct {
		name  string
		sqn   SQN
		fresh bool
	}{
		{"replayed", accepted, false},
		{"older with the same IND", NewSQN(99, 7), false},
		{"older with an unused IND", NewSQN(99, 8), true},
		{"next", accepted.Next(), true},
		{"Δ ahead", NewSQN(100+delta, 7), true},
		{"more than Δ ahead", NewSQN(100+delta+1, 7), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usim := NewUSIM(set1Secret, accepted)
			av := NewAuthenticationVector(set1Secret, set1RAND, tc.sqn, set1AMF)
			_, err := usim.Authenticate(av.RAND, av.AUTN)
			var failure SynchronisationFailure
			switch {
			case tc.fresh && err != nil:
				t.Fatalf("refused: %v", err)
			case !tc.fresh && !errors.As(err, &failure):
				t.Fatalf("err = %v, want SynchronisationFailure", err)
			}
		})
	}
}

// TestResynchronisation runs TS 33.102 6.3.5 after the HSS lost track of
// the USIM: the USIM refuses the vector with an AUTS, the HSS takes
// SQN_MS from it, and the next vector is accepted.
func TestResynchronisation(t *testing.T) {
	sqnMS := NewSQN(5000, 3)
	usim := NewUSIM(set1Secret, sqnMS)
	sqnHE := NewSQN(10, 2)

	av := NewAuthenticationVector(set1Secret, set1RAND, sqnHE.Next(), set1AMF)
	_, err := usim.Authenticate(av.RAND, av.AUTN)
	var failure SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want SynchronisationFailure", err)
	}

	got, err := Resynchronise(set1Secret, sqnHE, av.RAND, failure.AUTS)
	if err != nil || got != sqnMS {
		t.Fatalf("Resynchronise = %d, %v; want SQN_MS %d", got, err, sqnMS)
	}
	av = NewAuthenticationVector(set1Secret, block("00112233445566778899aabbccddeeff"), got.Next(), set1AMF)
	if _, err := usim.Authenticate(av.RAND, av.AUTN); err != nil {
		t.Fatalf("after re-synchronisation: %v", err)
	}
}

// TestResynchronisationKeepsAGoodSQN: when the next SQN_HE would be
// accepted anyway, the AuC keeps SQN_HE (TS 33.102 6.3.5 step 3), as
// when the S-CSCF reused a vector that the USIM had already seen.
func TestResynchronisationKeepsAGoodSQN(t *testing.T) {
	sqnHE := NewSQN(20, 4)
	usim := NewUSIM(set1Secret, sqnHE)
	av := NewAuthenticationVector(set1Secret, set1RAND, sqnHE, set1AMF)
	_, err := usim.Authenticate(av.RAND, av.AUTN)
	var failure SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want SynchronisationFailure for a replay", err)
	}
	if got, err := Resynchronise(set1Secret, sqnHE, av.RAND, failure.AUTS); err != nil || got != sqnHE {
		t.Fatalf("Resynchronise = %d, %v; want SQN_HE %d kept", got, err, sqnHE)
	}
}

// TestAUTSConcealsSQNMSWithF5Star checks the first part of AUTS against
// test set 1: SQN_MS ⊕ f5* is ff9bb4d0b607 ⊕ 451e8beca43b.
func TestAUTSConcealsSQNMSWithF5Star(t *testing.T) {
	usim := NewUSIM(set1Secret, set1SQN)
	av := NewAuthenticationVector(set1Secret, set1RAND, set1SQN, set1AMF)
	_, err := usim.Authenticate(av.RAND, av.AUTN)
	var failure SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want SynchronisationFailure", err)
	}
	if got := hexOf(failure.AUTS[:6]); got != "ba853f3c123c" {
		t.Errorf("Conc(SQN_MS) = %s, want ba853f3c123c", got)
	}
}

func TestResynchronisationRefusesAForgedAUTS(t *testing.T) {
	sqnHE := NewSQN(10, 2)
	other := set1Secret
	other.K[15] ^= 1
	forged := newAUTS(other.milenage(), set1RAND, NewSQN(9000, 0))
	got, err := Resynchronise(set1Secret, sqnHE, set1RAND, forged)
	if !errors.Is(err, ErrInvalidAUTS) || got != sqnHE {
		t.Fatalf("Resynchronise = %d, %v; want SQN_HE %d and ErrInvalidAUTS", got, err, sqnHE)
	}
}

func TestSQNParts(t *testing.T) {
	s := NewSQN(0x123, 31)
	if s != 0x123<<5|31 || s.SEQ() != 0x123 || s.IND() != 31 {
		t.Fatalf("NewSQN(0x123, 31) = %#x, SEQ %#x, IND %d", uint64(s), s.SEQ(), s.IND())
	}
	if n := s.Next(); n.SEQ() != 0x124 || n.IND() != 0 {
		t.Errorf("Next = SEQ %#x IND %d, want SEQ 0x124 IND 0", n.SEQ(), n.IND())
	}
	if max := NewSQN(1<<43-1, 31); max.Next() != 0 {
		t.Errorf("the 48-bit SQN does not wrap: %#x", uint64(max.Next()))
	}
}
