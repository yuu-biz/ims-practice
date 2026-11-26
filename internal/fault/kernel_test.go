package fault

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// inNamespace runs the commands one after another in a network
// namespace of their own, which unshare makes and drops when they end,
// so that the rules change nothing of the host. It skips the test
// where that cannot be done: not as root, or without unshare or the
// first command's tool.
func inNamespace(t *testing.T, commands ...[]string) (string, error) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("a network namespace of its own needs root")
	}
	for _, tool := range []string{"unshare", commands[0][0]} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	var script []string
	for _, c := range commands {
		script = append(script, shellQuote(c))
	}
	out, err := exec.Command("unshare", "--net", "sh", "-ec", strings.Join(script, "\n")).CombinedOutput()
	return string(out), err
}

// TestTheKernelTakesTheRules has nft and tc apply each rule that a
// network fault puts in place, at the bounds of what a request may ask,
// and take it away again, on the loopback interface of a namespace of
// its own. The other tests compare the text of the rules; this one
// shows that nft and tc accept it, as a statement they refuse would
// fail the fault on a real node.
func TestTheKernelTakesTheRules(t *testing.T) {
	lo := []string{"lo"}
	peers := []netip.Addr{netip.MustParseAddr("172.28.0.20"), netip.MustParseAddr("172.28.0.30")}
	for _, tc := range []struct {
		name  string
		apply []string
	}{
		{"loss of one packet in a hundred", lossCommand("ims_fault_1", lo, 1)},
		{"loss of 99 packets in a hundred", lossCommand("ims_fault_1", lo, 99)},
		{"loss of every packet", lossCommand("ims_fault_1", lo, 100)},
		{"partition from everything", partitionCommand("ims_fault_1", lo, nil)},
		{"partition from two peers", partitionCommand("ims_fault_1", lo, peers)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := inNamespace(t, tc.apply, []string{"nft", "list", "table", "netdev", "ims_fault_1"}, nftDeleteCommand("ims_fault_1")); err != nil {
				t.Errorf("%v: %s", err, out)
			}
		})
	}
	for _, tc := range []struct {
		name          string
		delay, jitter time.Duration
	}{
		{"the shortest delay", time.Microsecond, 0},
		{"the longest delay, with as much jitter", 10 * time.Second, 10 * time.Second},
		{"a delay with jitter", 200 * time.Millisecond, 1500 * time.Microsecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := inNamespace(t, netemCommand("lo", tc.delay, tc.jitter), netemDeleteCommand("lo"))
			switch {
			case err != nil && strings.Contains(out, "Specified qdisc kind is unknown"):
				// tc reads the arguments of netem before it asks the
				// kernel, and answers "Illegal ..." for one it cannot
				// read: these it read, and the kernel has no netem.
				t.Skipf("the kernel has no netem: %s", out)
			case err != nil:
				t.Errorf("%v: %s", err, out)
			}
		})
	}
}
