package ipsec

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ims"
)

// TestWiresharkSAsWithEncryption: the four rows of the agreement name
// each association by its two addresses and its SPI, with the two
// algorithms of the transform as Wireshark names them and the keys of
// TS 33.203 6.3, the same in every row.
func TestWiresharkSAsWithEncryption(t *testing.T) {
	a := agreement(t)
	a.UEMechanism.Transform, a.PCSCFMechanism.Transform = encrypted, encrypted
	rows, err := a.WiresharkSAs()
	if err != nil {
		t.Fatalf("WiresharkSAs: %v", err)
	}
	const ik = "0x9e98fd635ce304d820de064bea152b645b2fa171ba99416e3a3e90f5f9687c9e"
	const ck = "0xb40ba9a3c58b2a05bbf0d987b21bf8cb"
	want := []string{
		`"IPv4","172.28.0.120","172.28.0.20","0x138a","AES-CBC [RFC3602]","` + ck + `","HMAC-SHA-256-128 [RFC4868]","` + ik + `"`,
		`"IPv4","172.28.0.20","172.28.0.120","0xfa1","AES-CBC [RFC3602]","` + ck + `","HMAC-SHA-256-128 [RFC4868]","` + ik + `"`,
		`"IPv4","172.28.0.20","172.28.0.120","0xfa2","AES-CBC [RFC3602]","` + ck + `","HMAC-SHA-256-128 [RFC4868]","` + ik + `"`,
		`"IPv4","172.28.0.120","172.28.0.20","0x1389","AES-CBC [RFC3602]","` + ck + `","HMAC-SHA-256-128 [RFC4868]","` + ik + `"`,
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Errorf("the rows are\n%s\nwant\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}

// TestWiresharkSAsWithNullEncryption: with null encryption the rows
// name NULL and leave the encryption key empty, which is the row that
// lets Wireshark read the SIP that ESP carries in the clear.
func TestWiresharkSAsWithNullEncryption(t *testing.T) {
	rows, err := agreement(t).WiresharkSAs()
	if err != nil {
		t.Fatalf("WiresharkSAs: %v", err)
	}
	const want = `"IPv4","172.28.0.120","172.28.0.20","0x138a","NULL","",` +
		`"HMAC-SHA-256-128 [RFC4868]","0x9e98fd635ce304d820de064bea152b645b2fa171ba99416e3a3e90f5f9687c9e"`
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want the four associations", len(rows))
	}
	if rows[0] != want {
		t.Errorf("the first row is\n%s\nwant\n%s", rows[0], want)
	}
}

// TestWiresharkSAsRefuseAnotherTransform: an agreement whose transform
// this package does not set up has no rows, rather than rows that name
// another algorithm.
func TestWiresharkSAsRefuseAnotherTransform(t *testing.T) {
	a := agreement(t)
	a.UEMechanism.Transform = ims.Transform{Algorithm: "hmac-sha-1-96"}
	if rows, err := a.WiresharkSAs(); err == nil {
		t.Errorf("WiresharkSAs wrote rows for hmac-sha-1-96: %v", rows)
	}
}

// TestSAFileKeepsTheRowsOfEverySet: a capture outlives the security
// associations it holds, so the rows of a second set follow those of
// the first instead of replacing them.
func TestSAFileKeepsTheRowsOfEverySet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "esp_sa")
	f := NewSAFile(path, slog.New(slog.DiscardHandler))
	first := agreement(t)
	f.Write(first)
	second := agreement(t)
	second.UEMechanism.SPIC, second.PCSCFMechanism.SPIS = 6001, 6002
	f.Write(second)
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("the file holds %d rows, want the four of each set:\n%s", len(lines), text)
	}
	rows, err := second.WiresharkSAs()
	if err != nil {
		t.Fatalf("WiresharkSAs: %v", err)
	}
	if got := strings.Join(lines[4:], "\n"); got != strings.Join(rows, "\n") {
		t.Errorf("the last rows are\n%s\nwant those of the second set\n%s", got, strings.Join(rows, "\n"))
	}
	// Every row is a line of its own, so that a reader can hand them to
	// tshark one by one.
	for i, line := range lines {
		if strings.Count(line, `"`) != 16 {
			t.Errorf("row %d is not one record: %q", i, line)
		}
	}
}

// TestSAFileSurvivesAFileItCannotWrite: the file is there for a reader
// of the capture, so a path the node cannot write leaves the
// registration alone and says so in the log.
func TestSAFileSurvivesAFileItCannotWrite(t *testing.T) {
	var logged strings.Builder
	f := NewSAFile(filepath.Join(t.TempDir(), "no-such-directory", "esp_sa"),
		slog.New(slog.NewTextHandler(&logged, nil)))
	f.Write(agreement(t))
	if !strings.Contains(logged.String(), "level=WARN") {
		t.Errorf("the log does not warn about the file: %q", logged.String())
	}
}
