package config

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func parseXCAP(t *testing.T, text string) (XCAP, error) {
	t.Helper()
	var raw rawXCAP
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatal(err)
	}
	return raw.validate()
}

func TestParseXCAP(t *testing.T) {
	got, err := parseXCAP(t, "listen: 0.0.0.0:80\nroot_path: /xcap/\nnonce_key: test-nonce-key-0123456789\n")
	if err != nil {
		t.Fatal(err)
	}
	want := XCAP{Listen: netip.MustParseAddrPort("0.0.0.0:80"), RootPath: "/xcap", NonceKey: []byte("test-nonce-key-0123456789")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got, err := parseXCAP(t, "listen: 0.0.0.0:80\nnonce_key: test-nonce-key-0123456789\n"); err != nil || got.RootPath != "" {
		t.Errorf("no root_path: %+v, %v", got, err)
	}
}

func TestParseRejectsXCAP(t *testing.T) {
	for _, tc := range []struct{ text, wantErr string }{
		{"listen: xcap:80\nnonce_key: test-nonce-key-0123456789\n", "listen:"},
		{"listen: 0.0.0.0:0\nnonce_key: test-nonce-key-0123456789\n", "listen: port 0"},
		{"listen: 0.0.0.0:80\nroot_path: xcap\nnonce_key: test-nonce-key-0123456789\n", "root_path:"},
		{"listen: 0.0.0.0:80\nroot_path: /xcap?a\nnonce_key: test-nonce-key-0123456789\n", "root_path:"},
		{"listen: 0.0.0.0:80\nnonce_key: short\n", "nonce_key:"},
	} {
		if _, err := parseXCAP(t, tc.text); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%q: error %v, want %q", tc.text, err, tc.wantErr)
		}
	}
}
