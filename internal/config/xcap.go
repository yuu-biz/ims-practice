package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// XCAP is the configuration of the XCAP server of the Ut reference
// point (TS 24.623), which the mmtel role runs (xcap.NewHandler).
//
// There is no realm: the server challenges with the home domain, in
// which the HSS derives the Ut credential of every user
// (hss.UtCredentialData), so the two cannot disagree.
type XCAP struct {
	// Listen is the TCP address of the HTTP server, such as 0.0.0.0:80.
	// XCAP is plain HTTP on the standard port 80 here.
	//
	// SIMPLIFIED: TS 24.623 5.2.3.1 and RFC 4825 8. Both ask an XCAP
	// server for HTTP over TLS, with the TLS profile of TS 33.310 Annex
	// E. TLS is left out because this project does not encrypt signalling
	// by default, so that captures can be read, for SIP and Diameter as
	// well.
	Listen netip.AddrPort
	// RootPath is the path of the XCAP Root URI: empty for
	// http://xcap.<home domain>, the form of TS 23.003 13.9.1.2 that a UE
	// builds when none is configured (GSMA IR.92 2.3.2).
	RootPath string
	// NonceKey signs the nonces of HTTP Digest. Every instance of the
	// mmtel role needs the same key, so that a nonce from one is good at
	// the others.
	NonceKey []byte
}

// rawXCAP is the xcap section, as the mmtel role's section holds it:
//
//	xcap:
//	  listen: 0.0.0.0:80
//	  root_path: ""
//	  nonce_key: <at least 16 characters, the same on every instance>
type rawXCAP struct {
	Listen   string `yaml:"listen"`
	RootPath string `yaml:"root_path"`
	NonceKey string `yaml:"nonce_key"`
}

// minNonceKey is the shortest nonce key taken: 128 bits, the size of
// the HMAC in a nonce.
const minNonceKey = 16

func (raw *rawXCAP) validate() (XCAP, error) {
	addr, err := netip.ParseAddrPort(raw.Listen)
	if err != nil {
		return XCAP{}, fmt.Errorf("listen: %w", err)
	}
	if addr.Port() == 0 {
		return XCAP{}, errors.New("listen: port 0")
	}
	// RFC 4825 6.1: the XCAP Root URI has no query component.
	if raw.RootPath != "" && (!strings.HasPrefix(raw.RootPath, "/") || strings.ContainsAny(raw.RootPath, "?#") || strings.Contains(raw.RootPath, "~~")) {
		return XCAP{}, fmt.Errorf("root_path: %q is not an absolute path without a query", raw.RootPath)
	}
	if len(raw.NonceKey) < minNonceKey {
		return XCAP{}, fmt.Errorf("nonce_key: at least %d characters are required", minNonceKey)
	}
	return XCAP{Listen: addr, RootPath: strings.TrimSuffix(raw.RootPath, "/"), NonceKey: []byte(raw.NonceKey)}, nil
}
