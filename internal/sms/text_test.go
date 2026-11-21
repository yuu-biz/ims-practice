package sms

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncodeText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		dcs    DataCodingScheme
		length uint8
		data   string
	}{
		{"empty", "", DCSGSM7, 0, ""},
		// The packing example used by many SMS tutorials.
		{"hellohello", "hellohello", DCSGSM7, 10, "E8329BFD4697D9EC37"},
		// Each extension character is an escape and a code: six septets.
		{"extension table", "{€}", DCSGSM7, 6, "1BD4A6BC4901"},
		{"Japanese", "こんにちは", DCSUCS2, 10, "30533093306B3061306F"},
		// U+1F600 is beyond U+FFFF: a surrogate pair.
		{"emoji", "😀", DCSUCS2, 4, "D83DDE00"},
		// 'ç' is not in the default alphabet (only 'Ç' is), so the
		// whole text goes as UCS2.
		{"one character outside the alphabet", "ça", DCSUCS2, 4, "00E70061"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dcs, ud, err := EncodeText(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if dcs != tt.dcs || ud.Length != tt.length || ud.HeaderIndicator || !bytes.Equal(ud.Data, mustHex(t, tt.data)) {
				t.Errorf("EncodeText(%q) = %#x, %+v, want %#x, length %d, data %s", tt.text, dcs, ud, tt.dcs, tt.length, tt.data)
			}
			got, err := DecodeText(dcs, ud)
			if err != nil || got != tt.text {
				t.Errorf("DecodeText = %q, %v, want %q", got, err, tt.text)
			}
		})
	}
}

// TestGSM7RoundTripEveryLength packs every length a short message can
// hold, using every character of the default alphabet in turn.
func TestGSM7RoundTripEveryLength(t *testing.T) {
	var alphabet []rune
	for code, r := range gsm7Default {
		if code != escape {
			alphabet = append(alphabet, r)
		}
	}
	for n := 0; n <= maxSeptets; n++ {
		text := make([]rune, n)
		for i := range text {
			text[i] = alphabet[(n+i)%len(alphabet)]
		}
		dcs, ud, err := EncodeText(string(text))
		if err != nil {
			t.Fatalf("length %d: %v", n, err)
		}
		if dcs != DCSGSM7 || int(ud.Length) != n || len(ud.Data) != (n*7+7)/8 {
			t.Errorf("length %d: dcs %#x, TP-UDL %d, %d octets", n, dcs, ud.Length, len(ud.Data))
		}
		got, err := DecodeText(dcs, ud)
		if err != nil || got != string(text) {
			t.Errorf("length %d: decoded %q, %v", n, got, err)
		}
	}
}

// TestGSM7TrailingAt shows why TP-UDL counts septets: seven characters
// and the same seven followed by '@' pack into the same seven octets.
func TestGSM7TrailingAt(t *testing.T) {
	_, seven, _ := EncodeText("1234567")
	_, eight, _ := EncodeText("1234567@")
	if !bytes.Equal(seven.Data, eight.Data) || seven.Length != 7 || eight.Length != 8 {
		t.Fatalf("got %x (TP-UDL %d) and %x (TP-UDL %d)", seven.Data, seven.Length, eight.Data, eight.Length)
	}
	for _, text := range []string{"1234567", "1234567@", "1234567\r"} {
		dcs, ud, _ := EncodeText(text)
		if got, _ := DecodeText(dcs, ud); got != text {
			t.Errorf("round trip of %q gave %q", text, got)
		}
	}
}

func TestEncodeTextTooLong(t *testing.T) {
	tests := []struct {
		text string
		fits bool
	}{
		{strings.Repeat("a", 160), true},
		{strings.Repeat("a", 161), false},
		{strings.Repeat("€", 80), true}, // 160 septets
		{strings.Repeat("€", 80) + "a", false},
		{strings.Repeat("あ", 70), true}, // 140 octets
		{strings.Repeat("あ", 71), false},
		{strings.Repeat("😀", 35), true}, // 140 octets
		{strings.Repeat("😀", 36), false},
	}
	for _, tt := range tests {
		_, _, err := EncodeText(tt.text)
		if tt.fits && err != nil {
			t.Errorf("%d runes: %v", len([]rune(tt.text)), err)
		}
		if !tt.fits && !errors.Is(err, ErrTextTooLong) {
			t.Errorf("%d runes: err = %v, want ErrTextTooLong", len([]rune(tt.text)), err)
		}
	}
}

func TestDecodeTextEscapes(t *testing.T) {
	tests := []struct {
		name    string
		septets []byte
		want    string
	}{
		{"code not in the extension table", []byte{escape, 0x41}, "A"},
		{"escape at the end", []byte{0x41, escape}, "A "},
		{"two escapes", []byte{escape, escape, 0x41}, " A"},
		{"page break", []byte{escape, 0x0a}, "\f"},
	}
	for _, tt := range tests {
		ud := UserData{Length: uint8(len(tt.septets)), Data: packSeptets(tt.septets)}
		got, err := DecodeText(DCSGSM7, ud)
		if err != nil || got != tt.want {
			t.Errorf("%s: got %q, %v, want %q", tt.name, got, err, tt.want)
		}
	}
}

func TestDecodeTextRefuses(t *testing.T) {
	_, ud, _ := EncodeText("hello")
	header := ud
	header.HeaderIndicator = true
	if _, err := DecodeText(DCSGSM7, header); !errors.Is(err, ErrUserDataHeader) {
		t.Errorf("with TP-UDHI: err = %v, want ErrUserDataHeader", err)
	}
	short := ud
	short.Length = 9
	if _, err := DecodeText(DCSGSM7, short); err == nil {
		t.Error("TP-UDL longer than the data: no error")
	}
	if _, err := DecodeText(DCSUCS2, UserData{Length: 3, Data: []byte{0, 0x41, 0}}); err == nil {
		t.Error("odd UCS2 length: no error")
	}
	if _, err := DecodeText(0x04, UserData{Length: 1, Data: []byte{1}}); err == nil {
		t.Error("8 bit data: no error")
	}
	if _, err := DecodeText(0x20, ud); err == nil {
		t.Error("compressed: no error")
	}
}

func TestDataCodingSchemeAlphabet(t *testing.T) {
	tests := []struct {
		dcs        DataCodingScheme
		alphabet   Alphabet
		compressed bool
	}{
		{0x00, AlphabetGSM7, false},
		{0x04, Alphabet8Bit, false},
		{0x08, AlphabetUCS2, false},
		{0x0c, AlphabetGSM7, false}, // reserved character set
		{0x18, AlphabetUCS2, false}, // UCS2, class 0
		{0x28, AlphabetUCS2, true},
		{0x48, AlphabetUCS2, false}, // marked for automatic deletion
		{0x60, AlphabetGSM7, true},
		{0x80, AlphabetGSM7, false}, // reserved coding group
		{0xc0, AlphabetGSM7, false}, // message waiting, discard
		{0xd8, AlphabetGSM7, false}, // message waiting, store
		{0xe8, AlphabetUCS2, false}, // message waiting, store, UCS2
		{0xf1, AlphabetGSM7, false}, // class 1
		{0xf5, Alphabet8Bit, false},
	}
	for _, tt := range tests {
		if got := tt.dcs.Alphabet(); got != tt.alphabet {
			t.Errorf("%#02x.Alphabet() = %d, want %d", uint8(tt.dcs), got, tt.alphabet)
		}
		if got := tt.dcs.Compressed(); got != tt.compressed {
			t.Errorf("%#02x.Compressed() = %v, want %v", uint8(tt.dcs), got, tt.compressed)
		}
	}
}
