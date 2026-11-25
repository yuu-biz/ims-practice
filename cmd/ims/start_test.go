package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// takenAddress holds a TCP port on the loopback address until the test
// ends, so that a listener the test gives that address cannot open.
func takenAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().String()
}

// freeAddress is a TCP address on the loopback address that nothing
// listens on when it returns, for a listener of the node under test.
func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestANodeThatFailsToStartStopsItsDiameter: the maintenance listener of
// an HSS cannot open, after its Diameter server has started. The node
// fails to start, and stops its Diameter server on the way out as it
// does when it is told to stop, so the Diameter port is free again.
func TestANodeThatFailsToStartStopsItsDiameter(t *testing.T) {
	diameterAddress := freeAddress(t)
	maintenanceAddress := takenAddress(t)
	path := writeConfig(t, fmt.Sprintf(`
node: hss
roles: [hss]
home_domain: %[1]s
hss:
  subscriptions:
    - private_identity: %[4]s
      public_identities: [%[5]s]
      password: test-password
diameter:
  origin_host: hss.%[1]s
  origin_realm: %[1]s
  listen: %[2]s
maintenance: {listen: %[3]s}
`, domain, diameterAddress, maintenanceAddress, impi, impu))

	err := run(path, quiet(), new(slog.LevelVar))
	if err == nil || !strings.Contains(err.Error(), maintenanceAddress) {
		t.Fatalf("run = %v, want the error of the maintenance listener on %s", err, maintenanceAddress)
	}
	l, err := net.Listen("tcp", diameterAddress)
	if err != nil {
		t.Fatalf("the Diameter server of the node that failed to start still listens: %v", err)
	}
	l.Close()
}
