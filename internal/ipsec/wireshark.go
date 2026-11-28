package ipsec

import (
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
)

// This file writes the security associations of an agreement the way
// Wireshark takes them, so that a reader of a capture sees the SIP that
// ESP carries. Wireshark keeps them in a table of its own settings,
// esp_sa, whose rows name the two addresses, the SPI and each
// algorithm with its key; it reads a packet with the first row that
// matches its addresses and SPI.
//
// The keys of the four associations are the same (TS 33.203 6.3), and
// neither of them is in the capture: the 401 that carries IK_IM and
// CK_IM goes no further than the P-CSCF, which takes them out before
// the response reaches the UE, and Annex I expands IK_ESP from them
// with a key derivation function. A node that sets the associations up
// knows them all, so it is the one that can write the rows.

// WiresharkSAs are the four security associations of the agreement as
// rows of Wireshark's ESP SAs table, in the order of
// SecurityAssociations. A row holds no comma and no quotation mark of
// its own: the addresses, the SPI and the keys are numbers, and the
// names of the algorithms are words of Wireshark's own lists.
func (a Agreement) WiresharkSAs() ([]string, error) {
	k, err := mechanism(a.UEMechanism)
	if err != nil {
		return nil, err
	}
	integrityKey, err := IKESP(a.UEMechanism.Transform, a.IK, a.CK)
	if err != nil {
		return nil, err
	}
	encryptionKey, err := CKESP(a.UEMechanism.Transform, a.CK)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, sa := range a.SecurityAssociations() {
		rows = append(rows, row(protocolOf(sa.Source.Addr()),
			sa.Source.Addr().String(), sa.Destination.Addr().String(), spi(sa.SPI),
			k.readBy.encryption, hexKey(encryptionKey),
			k.readBy.integrity, hexKey(integrityKey)))
	}
	return rows, nil
}

// row is one record of the table: each field in quotation marks,
// separated by commas.
func row(fields ...string) string {
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = `"` + f + `"`
	}
	return strings.Join(quoted, ",")
}

// protocolOf is the protocol of a row, the first field of the table.
func protocolOf(a netip.Addr) string {
	if a.Unmap().Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// SAFile is a file of such rows, which a node appends to for every set
// of security associations it sets up. A reader gives it to Wireshark
// or to tshark to see the SIP inside the ESP of the capture beside it.
//
// Nothing is ever removed from it: a capture outlives the associations
// it holds, and a reader opens it once the registrations it recorded
// have ended, so the rows of every set that was set up have to stay.
// Two sets of the same SPIs, which only a set-up that ran twice makes,
// leave the same row twice; Wireshark reads a packet with the first row
// that matches, so the second changes nothing.
//
// The keys are in the clear, which is the point of the file: it is a
// deployment's way of handing a learner the keys of its own test
// subscribers. Only a deployment whose configuration names such a file
// writes one.
type SAFile struct {
	path string
	log  *slog.Logger
	// mu keeps the rows of one agreement together, which several
	// registrations at once would otherwise interleave.
	mu sync.Mutex
}

// NewSAFile is the file at path.
func NewSAFile(path string, log *slog.Logger) *SAFile {
	return &SAFile{path: path, log: log}
}

// Write appends the rows of the agreement. It gives the caller no
// error: the file is there for a reader of the capture, and a
// registration that the security associations are set up for goes on
// without it. What went wrong is in the log.
func (f *SAFile) Write(a Agreement) {
	rows, err := a.WiresharkSAs()
	if err == nil {
		err = f.append(rows)
	}
	if err != nil {
		f.log.Warn("the security associations are not in the file that Wireshark reads",
			"file", f.path, "ue", a.UE.String(), "error", err.Error())
		return
	}
	f.log.Info("wrote the security associations for Wireshark", "file", f.path, "ue", a.UE.String())
}

func (f *SAFile) append(rows []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := os.OpenFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(file, strings.Join(rows, "\n"))
	if closed := file.Close(); err == nil {
		err = closed
	}
	return err
}
