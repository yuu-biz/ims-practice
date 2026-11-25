package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// nodeConfigEnv names the configuration file that the test binary runs
// main with, when a test starts it again as the node under test.
const nodeConfigEnv = "IMS_TEST_NODE_CONFIG"

// TestANodeWhoseListenerCannotOpenFails: the HTTP listener of an OAM
// cannot open, because the port is taken. The process ends with a
// non-zero exit code, and its log says that the node failed
// and which listener on which address could not open, rather than that
// it started and stopped.
func TestANodeWhoseListenerCannotOpenFails(t *testing.T) {
	if path := os.Getenv(nodeConfigEnv); path != "" {
		os.Args = []string{"ims", "-config", path}
		main()
		return
	}
	taken := takenAddress(t)
	path := writeConfig(t, fmt.Sprintf(`
node: oam
roles: [oam]
home_domain: %s
oam:
  http_listen: %s
  nodes:
    - {name: pcscf-1, roles: [pcscf], maintenance: "http://127.0.0.1:1/"}
`, domain, taken))

	const deadline = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	node := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestANodeWhoseListenerCannotOpenFails$", "-test.timeout="+deadline.String())
	node.Env = append(os.Environ(), nodeConfigEnv+"="+path)
	out, err := node.Output()
	if ctx.Err() != nil {
		t.Fatalf("the node did not end within %v:\n%s", deadline, out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("the node ended with %v, want a non-zero exit code:\n%s", err, out)
	}

	var failed string
	lines := bufio.NewScanner(bytes.NewReader(out))
	for lines.Scan() {
		var line struct {
			Msg   string `json:"msg"`
			Error string `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &line) != nil {
			continue
		}
		if line.Msg == "node started" {
			t.Errorf("the node said it started:\n%s", out)
		}
		if line.Msg == "node failed" {
			failed = line.Error
		}
	}
	if !strings.Contains(failed, "oam") || !strings.Contains(failed, taken) {
		t.Errorf("node failed with %q, want it to name the oam listener on %s:\n%s", failed, taken, out)
	}
}
