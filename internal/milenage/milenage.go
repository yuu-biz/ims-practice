// Package milenage is the MILENAGE example algorithm set of TS 35.206
// (Release 19) chapter 4: the authentication and key generation
// functions f1, f1*, f2, f3, f4, f5 and f5* of TS 33.102 6.3, built on
// AES (Rijndael with a 128-bit key and block, TS 35.206 4.2).
//
// The functions follow the framework of TS 35.206 4.1 one by one, so
// that each output can be read against the specification. Each call
// computes TEMP again: the code is for reading, not for speed.
package milenage

import (
	"crypto/aes"
	"crypto/cipher"
)

// The constants c1 to c5 of TS 35.206 4.1, as the last byte of a
// 128-bit block whose other bytes are zero: c1 is all zero, and c2 to
// c5 set bit 127, 126, 125 and 124.
var (
	c1 = constant(0x00)
	c2 = constant(0x01)
	c3 = constant(0x02)
	c4 = constant(0x04)
	c5 = constant(0x08)
)

// The rotations r1 to r5 of TS 35.206 4.1, in bits.
const (
	r1 = 64
	r2 = 0
	r3 = 32
	r4 = 64
	r5 = 96
)

func constant(last byte) [16]byte {
	var c [16]byte
	c[15] = last
	return c
}

// Milenage holds the subscriber key K, as an AES cipher, and OPc, the
// two values every function of the set needs.
type Milenage struct {
	k   cipher.Block
	opc [16]byte
}

// New returns the functions for the subscriber key k and the value opc
// derived from the operator variant OP (see OPc).
func New(k, opc [16]byte) Milenage {
	return Milenage{k: newCipher(k), opc: opc}
}

// OPc derives OPc = OP ⊕ E[OP]K (TS 35.206 4.1). An operator may keep
// only OPc on the USIM and in the AuC, so that OP itself is never
// stored there (TS 35.206 5.1).
func OPc(k, op [16]byte) [16]byte {
	return xor(op, encrypt(newCipher(k), op))
}

func newCipher(k [16]byte) cipher.Block {
	// aes.NewCipher fails only for a key length other than 16, 24 or 32
	// bytes, which the array type rules out.
	block, err := aes.NewCipher(k[:])
	if err != nil {
		panic(err)
	}
	return block
}

// F1 is the network authentication function: MAC-A, the first 64 bits
// of OUT1.
func (m Milenage) F1(rand [16]byte, sqn [6]byte, amf [2]byte) (macA [8]byte) {
	out1 := m.out1(rand, sqn, amf)
	copy(macA[:], out1[0:8])
	return macA
}

// F1Star is the re-synchronisation message authentication function:
// MAC-S, the last 64 bits of OUT1.
func (m Milenage) F1Star(rand [16]byte, sqn [6]byte, amf [2]byte) (macS [8]byte) {
	out1 := m.out1(rand, sqn, amf)
	copy(macS[:], out1[8:16])
	return macS
}

// F2 is the user authentication function: RES, the last 64 bits of
// OUT2.
func (m Milenage) F2(rand [16]byte) (res [8]byte) {
	out2 := m.out(rand, r2, c2)
	copy(res[:], out2[8:16])
	return res
}

// F3 is the cipher key generating function: CK is OUT3.
func (m Milenage) F3(rand [16]byte) (ck [16]byte) {
	return m.out(rand, r3, c3)
}

// F4 is the integrity key generating function: IK is OUT4.
func (m Milenage) F4(rand [16]byte) (ik [16]byte) {
	return m.out(rand, r4, c4)
}

// F5 is the anonymity key generating function for normal operation: AK,
// the first 48 bits of OUT2.
func (m Milenage) F5(rand [16]byte) (ak [6]byte) {
	out2 := m.out(rand, r2, c2)
	copy(ak[:], out2[0:6])
	return ak
}

// F5Star is the anonymity key generating function for
// re-synchronisation: AK, the first 48 bits of OUT5.
func (m Milenage) F5Star(rand [16]byte) (ak [6]byte) {
	out5 := m.out(rand, r5, c5)
	copy(ak[:], out5[0:6])
	return ak
}

// temp is TEMP = E[RAND ⊕ OPc]K.
func (m Milenage) temp(rand [16]byte) [16]byte {
	return encrypt(m.k, xor(rand, m.opc))
}

// out1 is OUT1 = E[TEMP ⊕ rot(IN1 ⊕ OPc, r1) ⊕ c1]K ⊕ OPc, where IN1 is
// SQN || AMF || SQN || AMF.
func (m Milenage) out1(rand [16]byte, sqn [6]byte, amf [2]byte) [16]byte {
	var in1 [16]byte
	copy(in1[0:6], sqn[:])
	copy(in1[6:8], amf[:])
	copy(in1[8:14], sqn[:])
	copy(in1[14:16], amf[:])
	x := xor(xor(m.temp(rand), rotate(xor(in1, m.opc), r1)), c1)
	return xor(encrypt(m.k, x), m.opc)
}

// out is OUT2 to OUT5: E[rot(TEMP ⊕ OPc, r) ⊕ c]K ⊕ OPc.
func (m Milenage) out(rand [16]byte, r int, c [16]byte) [16]byte {
	x := xor(rotate(xor(m.temp(rand), m.opc), r), c)
	return xor(encrypt(m.k, x), m.opc)
}

func encrypt(k cipher.Block, x [16]byte) [16]byte {
	var y [16]byte
	k.Encrypt(y[:], x[:])
	return y
}

func xor(a, b [16]byte) [16]byte {
	var y [16]byte
	for i := range y {
		y[i] = a[i] ^ b[i]
	}
	return y
}

// rotate is rot(x, r): x cyclically rotated by r bits towards the most
// significant bit (TS 35.206 2.2.4). Bit 0 is the most significant bit
// of the first byte (2.2.3), so the bytes move towards the start. Every
// r of TS 35.206 is a multiple of 8.
func rotate(x [16]byte, r int) [16]byte {
	var y [16]byte
	for i := range y {
		y[i] = x[(i+r/8)%16]
	}
	return y
}
