package fault

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeContainer is one container of the fake Engine.
type fakeContainer struct {
	service string
	number  int
	oneOff  bool
	state   string
	host    bool
	// socket mounts the Docker socket, as the fault agent does.
	socket bool
	// networks maps the Docker network to the address there, in the
	// order of the interfaces eth0, eth1 and so on.
	networks [][2]string
}

func (c fakeContainer) id() string {
	if c.oneOff {
		return fmt.Sprintf("%s-run-%d", c.service, c.number)
	}
	return fmt.Sprintf("%s-id-%d", c.service, c.number)
}

// ipOutput is what "ip -o -4 addr show" prints in the container.
func (c fakeContainer) ipOutput() string {
	lines := []string{"1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever"}
	for i, n := range c.networks {
		lines = append(lines, fmt.Sprintf("%d: eth%d    inet %s/24 brd 172.28.0.255 scope global eth%d\\       valid_lft forever preferred_lft forever", i+2, i, n[1], i))
	}
	return strings.Join(lines, "\n")
}

// fakeEngine answers the part of the Docker Engine API that Docker
// uses, for the containers of the project "lab".
type fakeEngine struct {
	mu         sync.Mutex
	containers []fakeContainer
	calls      []string
	created    []map[string]any
	// helper is the container that the last helper joined.
	helper fakeContainer
	// failCmd, when set, makes the helpers that run it exit with 1 and
	// print output.
	failCmd string
	output  string
}

func (e *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	path, ok := strings.CutPrefix(r.URL.Path, "/v"+DockerAPIVersion)
	if !ok {
		http.Error(w, `{"message":"no version"}`, http.StatusBadRequest)
		return
	}
	e.calls = append(e.calls, r.Method+" "+path)
	switch {
	case r.Method == "GET" && path == "/containers/json":
		var f map[string][]string
		json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
		list := []map[string]any{}
		if !slices.Contains(f["label"], "com.docker.compose.project=lab") || r.URL.Query().Get("all") != "1" {
			json.NewEncoder(w).Encode(list)
			return
		}
		for _, c := range e.containers {
			labels := map[string]string{
				"com.docker.compose.project":          "lab",
				"com.docker.compose.service":          c.service,
				"com.docker.compose.container-number": fmt.Sprint(c.number),
				"com.docker.compose.oneoff":           map[bool]string{false: "False", true: "True"}[c.oneOff],
			}
			match := true
			for _, l := range f["label"] {
				k, v, _ := strings.Cut(l, "=")
				if labels[k] != v {
					match = false
				}
			}
			if !match {
				continue
			}
			mode, networks := "lab_ims", map[string]any{}
			if c.host {
				mode = "host"
			}
			for _, n := range c.networks {
				networks[n[0]] = map[string]any{"IPAddress": n[1]}
			}
			state := c.state
			if state == "" {
				state = "running"
			}
			name := fmt.Sprintf("/lab-%s-%d", c.service, c.number)
			if c.oneOff {
				name = fmt.Sprintf("/lab-%s-run-%d", c.service, c.number)
			}
			mounts := []map[string]any{}
			if c.socket {
				mounts = append(mounts, map[string]any{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock"})
			}
			list = append(list, map[string]any{
				"Mounts":          mounts,
				"Id":              c.id(),
				"Names":           []string{name},
				"Labels":          labels,
				"State":           state,
				"HostConfig":      map[string]any{"NetworkMode": mode},
				"NetworkSettings": map[string]any{"Networks": networks},
			})
		}
		json.NewEncoder(w).Encode(list)
	case r.Method == "POST" && path == "/containers/create":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		e.created = append(e.created, body)
		if body["Image"] != "lab/fault-tools:dev" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"message":"No such image: %v"}`, body["Image"])
			return
		}
		mode := body["HostConfig"].(map[string]any)["NetworkMode"].(string)
		for _, c := range e.containers {
			if "container:"+c.id() == mode {
				e.helper = c
			}
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"Id":"helper"}`)
	case path == "/containers/helper/wait":
		code := 0
		if e.failCmd != "" && e.lastCmd()[0] == e.failCmd {
			code = 1
		}
		fmt.Fprintf(w, `{"StatusCode":%d}`, code)
	case path == "/containers/helper/logs":
		out := ""
		switch {
		case e.failCmd != "" && e.lastCmd()[0] == e.failCmd:
			out = e.output
		case e.lastCmd()[0] == "ip":
			out = e.helper.ipOutput()
		}
		// Two frames, as the Engine sends stdout and stderr.
		for i, part := range []string{out, "\n"} {
			var h [8]byte
			h[0] = byte(1 + i)
			binary.BigEndian.PutUint32(h[4:], uint32(len(part)))
			w.Write(h[:])
			w.Write([]byte(part))
		}
	case r.Method == "POST" && strings.HasSuffix(path, "/start") && strings.Contains(path, "already"):
		w.WriteHeader(http.StatusNotModified)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (e *fakeEngine) lastCmd() []string {
	var cmd []string
	for _, a := range e.created[len(e.created)-1]["Cmd"].([]any) {
		cmd = append(cmd, a.(string))
	}
	return cmd
}

func (e *fakeEngine) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.calls
	e.calls = nil
	return out
}

// Commands returns the commands of the helpers so far, each joined
// with "|".
func (e *fakeEngine) Commands() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, c := range e.created {
		var cmd []string
		for _, a := range c["Cmd"].([]any) {
			cmd = append(cmd, a.(string))
		}
		out = append(out, strings.Join(cmd, "|"))
	}
	e.created = nil
	return out
}

func on(addr string) [][2]string { return [][2]string{{"lab_ims", addr}} }

func newTestDocker(t *testing.T) (*Docker, *fakeEngine) {
	e := &fakeEngine{containers: []fakeContainer{
		{service: "pcscf", number: 1, networks: on("172.28.0.40")},
		{service: "icscf", number: 1, networks: on("172.28.0.20")},
		{service: "scscf", number: 1, networks: on("172.28.0.30")},
		{service: "mmtel", number: 1, networks: on("172.28.0.75")},
		// A service with two containers (--scale).
		{service: "agw", number: 1, networks: on("172.28.0.51")},
		{service: "agw", number: 2, networks: on("172.28.0.52")},
		// Instances that are services of their own (deploy/redundancy).
		{service: "scscf-1", number: 1, networks: on("172.28.0.31")},
		{service: "scscf-2", number: 1, networks: on("172.28.0.32")},
		{service: "valkey", number: 1, networks: on("172.28.0.5")},
		// An IBCF of deploy/interconnect, on its network and the NNI.
		{service: "ibcf-a", number: 1, networks: [][2]string{{"lab_ims", "172.28.0.90"}, {"lab_nni", "172.28.2.10"}}},
		{service: "ibcf-b", number: 1, networks: [][2]string{{"lab_peer", "172.28.1.90"}, {"lab_nni", "172.28.2.20"}}},
		// A UE of "docker compose run", and one that has exited.
		{service: "ue3", number: 1, oneOff: true, networks: on("172.28.0.103")},
		{service: "ue2", number: 1, oneOff: true, state: "exited"},
		// A service with a container of "up" and one of "run".
		{service: "epc", number: 1, networks: on("172.28.0.80")},
		{service: "epc", number: 2, oneOff: true, networks: on("172.28.0.99")},
		// The capture container shares the host's network.
		{service: "capture", number: 1, host: true},
		// The fault agent has the Docker socket.
		{service: "fault-agent", number: 1, socket: true, networks: [][2]string{{"lab_chaos", "172.28.9.10"}}},
	}}
	s := httptest.NewServer(e)
	t.Cleanup(s.Close)
	return newDocker(s.Client(), s.URL, "lab", "lab/fault-tools:dev", nil), e
}

func TestDockerNodeFaults(t *testing.T) {
	d, e := newTestDocker(t)
	ctx := context.Background()
	for _, tc := range []struct {
		target       string
		kind         Kind
		apply, clear string
	}{
		{"pcscf", Stop, "POST /containers/pcscf-id-1/stop", "POST /containers/pcscf-id-1/start"},
		{"pcscf", Kill, "POST /containers/pcscf-id-1/kill", "POST /containers/pcscf-id-1/start"},
		{"pcscf", Pause, "POST /containers/pcscf-id-1/pause", "POST /containers/pcscf-id-1/unpause"},
		{"mmtel", Pause, "POST /containers/mmtel-id-1/pause", "POST /containers/mmtel-id-1/unpause"},
		{"agw:2", Kill, "POST /containers/agw-id-2/kill", "POST /containers/agw-id-2/start"},
		{"scscf-2", Stop, "POST /containers/scscf-2-id-1/stop", "POST /containers/scscf-2-id-1/start"},
		{"valkey", Kill, "POST /containers/valkey-id-1/kill", "POST /containers/valkey-id-1/start"},
		{"ibcf-a", Kill, "POST /containers/ibcf-a-id-1/kill", "POST /containers/ibcf-a-id-1/start"},
		{"ue3", Pause, "POST /containers/ue3-run-1/pause", "POST /containers/ue3-run-1/unpause"},
		// The container of "up" is the node, not the one of "run".
		{"epc", Stop, "POST /containers/epc-id-1/stop", "POST /containers/epc-id-1/start"},
	} {
		f := Fault{ID: "0a0b0c0d", Request: Request{Target: tc.target, Kind: tc.kind}}
		if err := d.Apply(ctx, f); err != nil {
			t.Fatalf("Apply %s %s: %v", tc.kind, tc.target, err)
		}
		if got := e.Calls(); len(got) != 2 || got[1] != tc.apply {
			t.Errorf("Apply %s %s called %v, want the lookup and %s", tc.kind, tc.target, got, tc.apply)
		}
		if err := d.Clear(ctx, f); err != nil {
			t.Fatalf("Clear %s %s: %v", tc.kind, tc.target, err)
		}
		if got := e.Calls(); len(got) != 2 || got[1] != tc.clear {
			t.Errorf("Clear %s %s called %v, want the lookup and %s", tc.kind, tc.target, got, tc.clear)
		}
	}
}

func TestDockerStartOfARunningContainer(t *testing.T) {
	d, e := newTestDocker(t)
	e.containers = append(e.containers, fakeContainer{service: "already", number: 1, networks: on("172.28.0.90")})
	if err := d.Clear(context.Background(), Fault{Request: Request{Target: "already", Kind: Stop}}); err != nil {
		t.Fatalf("Clear with 304 from start: %v", err)
	}
}

func TestDockerLossRunsHelpersInTheNamespace(t *testing.T) {
	d, e := newTestDocker(t)
	f := Fault{ID: "0a0b0c0d", Request: Request{Target: "pcscf", Kind: Loss, LossPercent: 20}}
	if err := d.Apply(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	helper := []string{
		"POST /containers/create",
		"POST /containers/helper/start",
		"POST /containers/helper/wait",
		"GET /containers/helper/logs",
		"DELETE /containers/helper",
	}
	// One helper finds the interface, the other puts the rules there.
	want := append(append([]string{"GET /containers/json"}, helper...), helper...)
	if got := e.Calls(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for _, c := range e.created {
		host := c["HostConfig"].(map[string]any)
		if host["NetworkMode"] != "container:pcscf-id-1" || fmt.Sprint(host["CapAdd"]) != "[NET_ADMIN]" {
			t.Fatalf("HostConfig = %v", host)
		}
	}
	got := e.Commands()
	if want := []string{"ip|-o|-4|addr|show", strings.Join(lossCommand("ims_fault_0a0b0c0d", []string{"eth0"}, 20), "|")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}

	if err := d.Clear(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if got := e.Commands(); fmt.Sprint(got) != "[nft|delete table netdev ims_fault_0a0b0c0d]" {
		t.Fatalf("clear commands = %v", got)
	}
}

func TestDockerFaultOnOneNetworkOfTwo(t *testing.T) {
	d, e := newTestDocker(t)
	ctx := context.Background()

	// Without a network, the fault sits on both interfaces of the IBCF.
	f := Fault{ID: "0a0b0c0d", Request: Request{Target: "ibcf-a", Kind: Loss, LossPercent: 5}}
	if err := d.Apply(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got := e.Commands(); len(got) != 2 || got[1] != strings.Join(lossCommand("ims_fault_0a0b0c0d", []string{"eth0", "eth1"}, 5), "|") {
		t.Fatalf("commands = %q", got)
	}

	// Cutting the NNI link: everything on the interface of the network
	// nni, and nothing on the IBCF's own network.
	f = Fault{ID: "0a0b0c0e", Request: Request{Target: "ibcf-a", Kind: Partition, Network: "nni"}}
	if err := d.Apply(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got := e.Commands(); len(got) != 2 || got[1] != strings.Join(partitionCommand("ims_fault_0a0b0c0e", []string{"eth1"}, nil), "|") {
		t.Fatalf("commands = %q", got)
	}

	f.Network = "peer"
	if err := d.Apply(ctx, f); err == nil || !strings.Contains(err.Error(), `lab-ibcf-a-1 is not on the network "peer"`) {
		t.Fatalf("Apply on a network that the target is not on: %v", err)
	}
	// The full name of the Docker network works too.
	f.Network = "lab_nni"
	e.Commands()
	if err := d.Apply(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got := e.Commands(); len(got) != 2 || !strings.Contains(got[1], `egress_eth1 { type filter hook egress device "eth1"`) || strings.Contains(got[1], "eth0") {
		t.Fatalf("commands = %q", got)
	}
}

func TestDockerPartitionResolvesThePeers(t *testing.T) {
	d, e := newTestDocker(t)
	ctx := context.Background()
	for _, tc := range []struct {
		peers []string
		set   string
	}{
		{[]string{"scscf", "172.28.0.2", "icscf"}, "172.28.0.2, 172.28.0.20, 172.28.0.30"},
		// Every container of a scaled service, or one of them.
		{[]string{"agw"}, "172.28.0.51, 172.28.0.52"},
		{[]string{"agw:1"}, "172.28.0.51"},
		// A pattern for every instance of a role: the store cut off from
		// the S-CSCFs, scscf-1 and scscf-2 but not scscf.
		{[]string{"scscf-*"}, "172.28.0.31, 172.28.0.32"},
		// A peer on two networks gives both addresses.
		{[]string{"ibcf-b"}, "172.28.1.90, 172.28.2.20"},
		{[]string{"ue3"}, "172.28.0.103"},
	} {
		f := Fault{ID: "0a0b0c0d", Request: Request{Target: "valkey", Kind: Partition, Peers: tc.peers}}
		if err := d.Apply(ctx, f); err != nil {
			t.Fatalf("peers %v: %v", tc.peers, err)
		}
		cmd := e.Commands()[1]
		if !strings.Contains(cmd, "egress_eth0 ip daddr { "+tc.set+" } counter drop") ||
			!strings.Contains(cmd, "ingress_eth0 ip saddr { "+tc.set+" } counter drop") {
			t.Errorf("peers %v: Cmd = %s, want the set %s", tc.peers, cmd, tc.set)
		}
	}

	for peer, want := range map[string]string{
		"mrf":     `no container of the service "mrf"`,
		"mrfc-*":  `no container of the service "mrfc-*"`,
		"agw:3":   `no container of the service "agw:3"`,
		"ue2":     `no container of the service "ue2"`,
		"capture": `peer "capture" has no IPv4 address`,
	} {
		f := Fault{ID: "0a0b0c0d", Request: Request{Target: "pcscf", Kind: Partition, Peers: []string{peer}}}
		if err := d.Apply(ctx, f); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Apply with the peer %s: %v, want %s", peer, err, want)
		}
	}
}

func TestDockerDelayOnEveryInterface(t *testing.T) {
	d, e := newTestDocker(t)
	f := Fault{ID: "0a0b0c0d", Request: Request{Target: "ibcf-a", Kind: Delay, Delay: Duration(200 * time.Millisecond)}}
	if err := d.Apply(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	want := "[ip|-o|-4|addr|show tc|qdisc|add|dev|eth0|root|netem|delay|200ms tc|qdisc|add|dev|eth1|root|netem|delay|200ms]"
	if got := e.Commands(); fmt.Sprint(got) != want {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if err := d.Clear(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	want = "[ip|-o|-4|addr|show tc|qdisc|del|dev|eth0|root tc|qdisc|del|dev|eth1|root]"
	if got := e.Commands(); fmt.Sprint(got) != want {
		t.Fatalf("clear commands = %v, want %v", got, want)
	}
}

func TestDockerHelperFailure(t *testing.T) {
	d, e := newTestDocker(t)
	e.failCmd = "tc"
	e.output = "Error: Specified qdisc kind is unknown."
	f := Fault{ID: "0a0b0c0d", Request: Request{Target: "pcscf", Kind: Delay, Delay: Duration(200 * time.Millisecond)}}
	err := d.Apply(context.Background(), f)
	if err == nil || err.Error() != "tc exited with 1: Error: Specified qdisc kind is unknown." {
		t.Fatalf("Apply = %v", err)
	}
	if got := e.Calls(); got[len(got)-1] != "DELETE /containers/helper" {
		t.Fatalf("the helper was not removed: %v", got)
	}

	// A rule that went with the old namespace counts as cleared.
	e.failCmd = "nft"
	e.output = "Error: Could not process rule: No such file or directory"
	if err := d.Clear(context.Background(), Fault{ID: "0a0b0c0d", Request: Request{Target: "pcscf", Kind: Loss, LossPercent: 5}}); err != nil {
		t.Fatalf("Clear of a table that is gone: %v", err)
	}
	e.failCmd = "tc"
	e.output = "Error: Cannot delete qdisc with handle of zero."
	if err := d.Clear(context.Background(), f); err != nil {
		t.Fatalf("Clear of a qdisc that is gone: %v", err)
	}
	e.output = "Error: Operation not permitted"
	if err := d.Clear(context.Background(), f); err == nil {
		t.Fatal("Clear succeeded on another error")
	}
}

func TestDockerErrors(t *testing.T) {
	d, _ := newTestDocker(t)
	ctx := context.Background()
	for target, want := range map[string]string{
		"mrf":   `no container of the service "mrf" in the project "lab"`,
		"agw":   `the service "agw" has 2 containers (agw:1, agw:2); name one`,
		"agw:3": `no container 3 of the service "agw" in the project "lab"`,
		// An exited one-off container is not a node.
		"ue2": `no container of the service "ue2"`,
	} {
		if err := d.Apply(ctx, Fault{Request: Request{Target: target, Kind: Stop}}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Apply on %s: %v, want %s", target, err, want)
		}
	}
	if err := d.Apply(ctx, Fault{ID: "0a0b0c0d", Request: Request{Target: "capture", Kind: Loss, LossPercent: 5}}); err == nil || !strings.Contains(err.Error(), "uses the host's network") {
		t.Errorf("network fault on the host's namespace: %v", err)
	}
	for _, kind := range []Kind{Stop, Kill, Pause, Loss} {
		f := Fault{ID: "0a0b0c0d", Request: Request{Target: "fault-agent", Kind: kind, LossPercent: map[Kind]int{Loss: 5}[kind]}}
		if err := d.Apply(ctx, f); err == nil || !strings.Contains(err.Error(), "holds the Docker socket") {
			t.Errorf("%s on the fault agent: %v, want a refusal", kind, err)
		}
	}
	d.ToolsImage = "lab/missing:dev"
	err := d.Apply(ctx, Fault{ID: "0a0b0c0d", Request: Request{Target: "pcscf", Kind: Loss, LossPercent: 5}})
	if err == nil || !strings.Contains(err.Error(), "No such image: lab/missing:dev") || !strings.Contains(err.Error(), "build fault-tools") {
		t.Errorf("missing image: %v", err)
	}
}

func TestDevicesOf(t *testing.T) {
	out := "1: lo    inet 127.0.0.1/8 scope host lo\\       valid_lft forever preferred_lft forever\n" +
		"2: eth0    inet 172.28.0.90/24 brd 172.28.0.255 scope global eth0\\       valid_lft forever preferred_lft forever\n" +
		"3: eth1@if12    inet 172.28.2.10/24 brd 172.28.2.255 scope global eth1\\       valid_lft forever preferred_lft forever\n"
	for _, tc := range []struct {
		addresses []string
		want      string
	}{
		{[]string{"172.28.2.10"}, "[eth1]"},
		{[]string{"172.28.2.10", "172.28.0.90"}, "[eth0 eth1]"},
		{[]string{"172.28.0.91"}, "[]"},
	} {
		var as []netip.Addr
		for _, a := range tc.addresses {
			as = append(as, netip.MustParseAddr(a))
		}
		if got := fmt.Sprint(devicesOf(out, as)); got != tc.want {
			t.Errorf("devicesOf(%v) = %s, want %s", tc.addresses, got, tc.want)
		}
	}
}

func TestRules(t *testing.T) {
	eth0 := []string{"eth0"}
	for _, tc := range []struct {
		got  []string
		want string
	}{
		{lossCommand("ims_fault_1", eth0, 20), `nft|add table netdev ims_fault_1; ` +
			`add chain netdev ims_fault_1 egress_eth0 { type filter hook egress device "eth0" priority filter; policy accept; }; ` +
			`add chain netdev ims_fault_1 ingress_eth0 { type filter hook ingress device "eth0" priority filter; policy accept; }; ` +
			`add rule netdev ims_fault_1 egress_eth0 udp dport 9060 accept; ` +
			`add rule netdev ims_fault_1 egress_eth0 meta protocol ip numgen random mod 100 < 20 counter drop; ` +
			`add rule netdev ims_fault_1 ingress_eth0 meta protocol ip numgen random mod 100 < 20 counter drop`},
		{partitionCommand("ims_fault_1", eth0, nil), `nft|add table netdev ims_fault_1; ` +
			`add chain netdev ims_fault_1 egress_eth0 { type filter hook egress device "eth0" priority filter; policy accept; }; ` +
			`add chain netdev ims_fault_1 ingress_eth0 { type filter hook ingress device "eth0" priority filter; policy accept; }; ` +
			`add rule netdev ims_fault_1 egress_eth0 udp dport 9060 accept; ` +
			`add rule netdev ims_fault_1 egress_eth0 meta protocol ip counter drop; ` +
			`add rule netdev ims_fault_1 ingress_eth0 meta protocol ip counter drop`},
		{partitionCommand("ims_fault_1", []string{"eth0", "eth1"}, []netip.Addr{netip.MustParseAddr("172.28.2.20")}), `nft|add table netdev ims_fault_1; ` +
			`add chain netdev ims_fault_1 egress_eth0 { type filter hook egress device "eth0" priority filter; policy accept; }; ` +
			`add chain netdev ims_fault_1 ingress_eth0 { type filter hook ingress device "eth0" priority filter; policy accept; }; ` +
			`add rule netdev ims_fault_1 egress_eth0 udp dport 9060 accept; ` +
			`add chain netdev ims_fault_1 egress_eth1 { type filter hook egress device "eth1" priority filter; policy accept; }; ` +
			`add chain netdev ims_fault_1 ingress_eth1 { type filter hook ingress device "eth1" priority filter; policy accept; }; ` +
			`add rule netdev ims_fault_1 egress_eth1 udp dport 9060 accept; ` +
			`add rule netdev ims_fault_1 egress_eth0 ip daddr { 172.28.2.20 } counter drop; ` +
			`add rule netdev ims_fault_1 ingress_eth0 ip saddr { 172.28.2.20 } counter drop; ` +
			`add rule netdev ims_fault_1 egress_eth1 ip daddr { 172.28.2.20 } counter drop; ` +
			`add rule netdev ims_fault_1 ingress_eth1 ip saddr { 172.28.2.20 } counter drop`},
		{netemCommand("eth0", 200*time.Millisecond, 0), "tc|qdisc|add|dev|eth0|root|netem|delay|200ms"},
		{netemCommand("eth0", 200*time.Millisecond, 1500*time.Microsecond), "tc|qdisc|add|dev|eth0|root|netem|delay|200ms|1500us"},
		{netemDeleteCommand("eth0"), "tc|qdisc|del|dev|eth0|root"},
	} {
		if got := strings.Join(tc.got, "|"); got != tc.want {
			t.Errorf("command\n got %s\nwant %s", got, tc.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote([]string{"nft", `add chain t egress { device "eth0"; }`, "it's"})
	want := `nft 'add chain t egress { device "eth0"; }' 'it'\''s'`
	if got != want {
		t.Fatalf("shellQuote = %s, want %s", got, want)
	}
}
