package main

import (
	"encoding/hex"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/aka"
)

// TS 35.208 4.3.1, test set 1: K and OP give the OPc that the HSS of
// deploy/compose/hss.yaml has, so -op and -opc set up the same USIM.
func TestUSIMConfig(t *testing.T) {
	const k, op, opc = "465b5ce8b199b49faa5f0a2ee238a6bc", "cdc202d5123e20f62b6d676ac72cb318", "cd63cb71954a9f4e48a5994e37a02baf"
	fromOP, err := usimConfig(k, "", op, "000000000020")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(fromOP.Secret.OPc[:]); got != opc || fromOP.SQN != aka.SQN(0x20) {
		t.Errorf("from OP: OPc %s, SQN %d; want %s and 32", got, fromOP.SQN, opc)
	}
	fromOPc, err := usimConfig(k, opc, "", "000000000000")
	if err != nil || fromOPc.Secret != fromOP.Secret {
		t.Errorf("from OPc: %+v, %v", fromOPc, err)
	}
	for _, bad := range [][4]string{
		{k, "", "", "000000000000"},
		{k, opc, op, "000000000000"},
		{k[:30], opc, "", "000000000000"},
		{k, opc, "", "0020"},
	} {
		if _, err := usimConfig(bad[0], bad[1], bad[2], bad[3]); err == nil {
			t.Errorf("usimConfig%v: no error", bad)
		}
	}
}
