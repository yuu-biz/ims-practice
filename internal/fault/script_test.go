package fault

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The commands that inject a fault are built twice: once here, for the
// API, and once in deploy/chaos/fault.sh, for a terminal. A learner
// reads the script to see what the API does, so the two must agree.
// This file runs the script against a docker of its own and compares
// what it asks docker to run with what this package asks of the Engine.

const faultScript = "../../deploy/chaos/fault.sh"

// stubDocker is a docker for the script: it answers the lookups of the
// containers of the test's project and writes down every command that
// changes something, one call per line, the arguments separated by "|".
// It starts no container, so the script runs without an Engine.
const stubDocker = `#!/bin/sh
set -eu

log() {
	out=
	for a in "$@"; do
		if [ -z "$out" ]; then out=$a; else out="$out|$a"; fi
	done
	printf '%s\n' "$out" >>"$FAULT_LOG"
}

# addresses NAME prints the IPv4 addresses of the container, in the
# order of its interfaces eth0, eth1 and so on.
addresses() {
	case $1 in
	lab-scscf-1) echo "172.28.0.30" ;;
	lab-icscf-1) echo "172.28.0.20" ;;
	lab-pcscf-1) echo "172.28.0.40" ;;
	lab-ibcf-a-1) echo "172.28.0.90 172.28.2.10" ;;
	esac
}

# on_network NAME NETWORK prints the address of the container there.
on_network() {
	case "$1 $2" in
	"lab-ibcf-a-1 lab_nni") echo "172.28.2.10" ;;
	"lab-ibcf-a-1 lab_ims") echo "172.28.0.90" ;;
	*) addresses "$1" ;;
	esac
}

cmd=$1
shift
case $cmd in
ps)
	# The containers of a service of the project, by the labels in the
	# filters. A lookup that does not name the project would reach the
	# containers of another stack, so it finds nothing here.
	service=
	project=
	for a in "$@"; do
		case $a in
		label=com.docker.compose.service=*) service=${a#label=com.docker.compose.service=} ;;
		label=com.docker.compose.project=*) project=${a#label=com.docker.compose.project=} ;;
		esac
	done
	[ "$project" = lab ] || exit 0
	echo "lab-$service-1"
	;;
inspect)
	# docker inspect -f FORMAT NAME...
	format=$2
	shift 2
	for name in "$@"; do
		case $format in
		*slice*Name*) echo "$name" ;;
		*HostConfig.NetworkMode*) echo "lab_ims" ;;
		*index*Networks*)
			network=$(printf '%s' "$format" | sed -n 's/.*Networks "\([^"]*\)".*/\1/p')
			on_network "$name" "$network"
			;;
		*Networks*) printf '%s ' $(addresses "$name") ;;
		*) echo "stub docker: unknown format $format" >&2; exit 1 ;;
		esac
	done
	;;
run)
	log "$cmd" "$@"
	# The last arguments are the command of the throw-away container.
	name=
	for a in "$@"; do
		case $a in container:*) name=${a#container:} ;; esac
	done
	case " $* " in
	*" ip "*)
		i=0
		for a in $(addresses "$name"); do
			echo "$((i + 2)): eth$i    inet $a/24 brd 172.28.0.255 scope global eth$i\\       valid_lft forever preferred_lft forever"
			i=$((i + 1))
		done
		;;
	esac
	;;
*) log "$cmd" "$@" ;;
esac
`

// equivalentLog keeps the docker commands that Docker logs as
// "equivalent": the command a learner can type for the call it made to
// the Engine.
type equivalentLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *equivalentLog) Enabled(context.Context, slog.Level) bool { return true }

func (l *equivalentLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "equivalent" {
			l.lines = append(l.lines, a.Value.String())
		}
		return true
	})
	return nil
}

func (l *equivalentLog) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *equivalentLog) WithGroup(string) slog.Handler      { return l }

func (l *equivalentLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.lines
	l.lines = nil
	return out
}

// runScript runs deploy/chaos/fault.sh with the stub docker and returns
// the commands it gave it, one per line, the arguments separated by
// "|".
func runScript(t *testing.T, args ...string) []string {
	t.Helper()
	dir := t.TempDir()
	stub := filepath.Join(dir, "docker")
	if err := os.WriteFile(stub, []byte(stubDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "commands")
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{faultScript}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAULT_LOG="+log,
		// The project and the image of the fake Engine below, so that
		// the two sides name the same containers.
		"COMPOSE_PROJECT_NAME=lab",
		"FAULT_TOOLS_IMAGE=lab/fault-tools:dev",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fault.sh %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestFaultScriptRunsTheCommandsOfThePackage: for every fault that both
// build the same commands for, the script runs the commands that the
// package gives the Engine. The nft and tc commands are compared
// argument by argument; the faults that are one docker command are
// compared to the "equivalent" that the package logs for its call.
//
// recreate is not among them: the script has Compose make the new
// container from its file, and the package, which has no Compose file,
// copies the configuration of the container it replaces. That
// difference is in deploy/chaos/README.md, and
// TestDockerRecreateKeepsTheNameAndTheAddress holds the package's side
// of it.
//
// The fault in each case takes the kind as its id, because the API
// names the nftables table after the id of the fault and the script
// after the kind: that is the one difference the comparison leaves out.
func TestFaultScriptRunsTheCommandsOfThePackage(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run deploy/chaos/fault.sh with")
	}
	for _, tc := range []struct {
		name string
		// script are the arguments of fault.sh.
		script []string
		// fault is the same fault as a request to the API; clear
		// compares the clearing of it rather than the applying.
		fault Request
		clear bool
	}{
		{"stop", []string{"stop", "scscf"}, Request{Target: "scscf", Kind: Stop}, false},
		{"start", []string{"start", "scscf"}, Request{Target: "scscf", Kind: Stop}, true},
		{"kill", []string{"kill", "scscf"}, Request{Target: "scscf", Kind: Kill}, false},
		{"pause", []string{"pause", "pcscf"}, Request{Target: "pcscf", Kind: Pause}, false},
		{"unpause", []string{"unpause", "pcscf"}, Request{Target: "pcscf", Kind: Pause}, true},
		{"crash", []string{"crash", "scscf"}, Request{Target: "scscf", Kind: Crash}, false},
		{"loss", []string{"loss", "scscf", "10"}, Request{Target: "scscf", Kind: Loss, LossPercent: 10}, false},
		{"loss of every packet", []string{"loss", "scscf", "100"}, Request{Target: "scscf", Kind: Loss, LossPercent: 100}, false},
		{"loss of one packet in a hundred", []string{"loss", "scscf", "1"}, Request{Target: "scscf", Kind: Loss, LossPercent: 1}, false},
		{"delay", []string{"delay", "pcscf", "200"}, Request{Target: "pcscf", Kind: Delay, Delay: Duration(200 * time.Millisecond)}, false},
		{"delay with jitter", []string{"delay", "pcscf", "200", "20"},
			Request{Target: "pcscf", Kind: Delay, Delay: Duration(200 * time.Millisecond), Jitter: Duration(20 * time.Millisecond)}, false},
		{"partition from everything", []string{"partition", "scscf"}, Request{Target: "scscf", Kind: Partition}, false},
		{"partition from a peer", []string{"partition", "scscf", "icscf"}, Request{Target: "scscf", Kind: Partition, Peers: []string{"icscf"}}, false},
		{"partition on one network", []string{"partition", "--network", "nni", "ibcf-a"},
			Request{Target: "ibcf-a", Kind: Partition, Network: "nni"}, false},
		{"delay on one network", []string{"delay", "--network", "nni", "ibcf-a", "50"},
			Request{Target: "ibcf-a", Kind: Delay, Network: "nni", Delay: Duration(50 * time.Millisecond)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &equivalentLog{}
			d, e := newTestDocker(t)
			d.log = slog.New(log)
			f := Fault{ID: string(tc.fault.Kind), Request: tc.fault}
			var err error
			if tc.clear {
				err = d.Clear(context.Background(), f)
			} else {
				err = d.Apply(context.Background(), f)
			}
			if err != nil {
				t.Fatalf("the API could not apply %+v: %v", tc.fault, err)
			}
			var want []string
			if helpers := e.Commands(); len(helpers) > 0 {
				// A network fault: the commands of the throw-away
				// containers, in the namespace of the target.
				prefix := strings.Join([]string{"run", "--rm", "--network", "container:lab-" + splitService(tc.fault.Target) + "-1", "--cap-add", "NET_ADMIN", "lab/fault-tools:dev"}, "|")
				for _, h := range helpers {
					want = append(want, prefix+"|"+h)
				}
			} else {
				for _, line := range log.take() {
					if strings.ContainsAny(line, `'"`) {
						t.Fatalf("the equivalent command %s is quoted; compare it argument by argument instead", line)
					}
					want = append(want, strings.Join(strings.Fields(strings.TrimPrefix(line, "docker ")), "|"))
				}
			}
			if got := runScript(t, tc.script...); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("fault.sh %s runs\n %v\nthe API runs\n %v", strings.Join(tc.script, " "), got, want)
			}
		})
	}
}

// splitService is the service of a target, without the number of one
// container of it.
func splitService(target string) string {
	service, _ := splitTarget(target)
	return service
}

// scriptCommands are the commands of deploy/chaos/fault.sh: the labels
// of the case that runs them.
func scriptCommands(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(faultScript)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	label := regexp.MustCompile(`(?m)^([a-z][a-z| ]*)\)$`)
	for _, m := range label.FindAllStringSubmatch(string(b), -1) {
		for _, c := range strings.Split(m[1], "|") {
			commands = append(commands, strings.TrimSpace(c))
		}
	}
	if len(commands) == 0 {
		t.Fatalf("%s has no command", faultScript)
	}
	return commands
}

// TestFaultScriptAndPackageAgree: every fault of the package is a
// command of the script, and every command of the script is a fault of
// the package or one of the few that only a terminal needs. A fault
// added to one side alone fails here.
func TestFaultScriptAndPackageAgree(t *testing.T) {
	// The script undoes a fault with a command of its own, because it
	// keeps nothing: the API clears the fault it holds instead. clear
	// removes every table of a fault, whichever fault made it, since the
	// script knows no fault's id; show and heal only read and tidy up.
	// Those five are no faults, so neither side is missing one.
	terminalOnly := []string{"start", "unpause", "clear", "show", "heal"}
	commands := scriptCommands(t)
	for _, kind := range Kinds {
		if !slices.Contains(commands, string(kind)) {
			t.Errorf("the kind %s is in the package and not in %s", kind, faultScript)
		}
	}
	for _, c := range commands {
		if !slices.Contains(Kinds, Kind(c)) && !slices.Contains(terminalOnly, c) {
			t.Errorf("%s has the command %s, which is no kind of the package", faultScript, c)
		}
	}
}
