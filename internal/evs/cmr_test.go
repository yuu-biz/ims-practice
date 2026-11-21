package evs

import "testing"

// TestCMRTable walks all 128 CMR bytes and compares them with TS
// 26.445 Table A.3, written out here row by row.
func TestCMRTable(t *testing.T) {
	want := map[byte]string{
		0x80: "NB 5.9 (VBR)", 0x81: "NB 7.2", 0x82: "NB 8", 0x83: "NB 9.6",
		0x84: "NB 13.2", 0x85: "NB 16.4", 0x86: "NB 24.4",

		0x90: "IO 6.6", 0x91: "IO 8.85", 0x92: "IO 12.65", 0x93: "IO 14.25",
		0x94: "IO 15.85", 0x95: "IO 18.25", 0x96: "IO 19.85", 0x97: "IO 23.05",
		0x98: "IO 23.85",

		0xa0: "WB 5.9 (VBR)", 0xa1: "WB 7.2", 0xa2: "WB 8", 0xa3: "WB 9.6",
		0xa4: "WB 13.2", 0xa5: "WB 16.4", 0xa6: "WB 24.4", 0xa7: "WB 32",
		0xa8: "WB 48", 0xa9: "WB 64", 0xaa: "WB 96", 0xab: "WB 128",

		0xb3: "SWB 9.6", 0xb4: "SWB 13.2", 0xb5: "SWB 16.4", 0xb6: "SWB 24.4",
		0xb7: "SWB 32", 0xb8: "SWB 48", 0xb9: "SWB 64", 0xba: "SWB 96",
		0xbb: "SWB 128",

		0xc5: "FB 16.4", 0xc6: "FB 24.4", 0xc7: "FB 32", 0xc8: "FB 48",
		0xc9: "FB 64", 0xca: "FB 96", 0xcb: "FB 128",

		0xd0: "WB 13.2 CA-L-O2", 0xd1: "WB 13.2 CA-L-O3", 0xd2: "WB 13.2 CA-L-O5",
		0xd3: "WB 13.2 CA-L-O7", 0xd4: "WB 13.2 CA-H-O2", 0xd5: "WB 13.2 CA-H-O3",
		0xd6: "WB 13.2 CA-H-O5", 0xd7: "WB 13.2 CA-H-O7",

		0xe0: "SWB 13.2 CA-L-O2", 0xe1: "SWB 13.2 CA-L-O3", 0xe2: "SWB 13.2 CA-L-O5",
		0xe3: "SWB 13.2 CA-L-O7", 0xe4: "SWB 13.2 CA-H-O2", 0xe5: "SWB 13.2 CA-H-O3",
		0xe6: "SWB 13.2 CA-H-O5", 0xe7: "SWB 13.2 CA-H-O7",

		0xff: "NO_REQ",
	}
	for b := 0x80; b <= 0xff; b++ {
		c := CMR(b)
		name, defined := want[byte(b)]
		if c.Valid() != defined {
			t.Errorf("CMR 0x%02x: Valid() = %v, Table A.3 defines it: %v", b, c.Valid(), defined)
			continue
		}
		if defined && c.String() != name {
			t.Errorf("CMR 0x%02x: %q, want %q", b, c.String(), name)
		}
		if NewCMR(c.T(), c.D()) != c {
			t.Errorf("CMR 0x%02x: fields T=%03b D=%04b do not make it again", b, c.T(), c.D())
		}
	}
	if CMR(0x04).Valid() {
		t.Error("a byte with H = 0 is a valid CMR")
	}
	if NoRequest != NewCMR(RequestReserved, 0b1111) {
		t.Error("NO_REQ is not T=111 D=1111")
	}
}

// TestCompactCMR checks Table A.2, the 3-bit CMR of the Compact EVS
// AMR-WB IO payload, against the CMR bytes of Table A.3.
func TestCompactCMR(t *testing.T) {
	for code, want := range []string{"IO 6.6", "IO 8.85", "IO 12.65", "IO 15.85", "IO 18.25", "IO 23.05", "IO 23.85", "NO_REQ"} {
		c := cmrFromCompact(uint(code))
		if c.String() != want {
			t.Errorf("3-bit CMR %03b: %s, want %s", code, c, want)
		}
		if back, ok := c.compactCode(); !ok || back != uint(code) {
			t.Errorf("%s: 3-bit CMR %03b, %v; want %03b", c, back, ok, code)
		}
	}
	if code, ok := CMR(0).compactCode(); !ok || code != 0b111 {
		t.Errorf("no CMR: 3-bit CMR %03b, %v; want none (111)", code, ok)
	}
	// A.2.1.2.1: 14.25 and 19.85 kbit/s and the EVS Primary requests
	// need the CMR byte.
	for _, c := range []CMR{NewCMR(RequestIO, uint8(AMRWBIO14250)), NewCMR(RequestIO, uint8(AMRWBIO19850)), NewCMR(RequestSWB, uint8(Primary13200))} {
		if _, ok := c.compactCode(); ok {
			t.Errorf("%s fits the 3-bit CMR", c)
		}
	}
}
