package fault

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DockerAPIVersion is the Docker Engine API version that Docker speaks.
// 1.44 is Docker Engine 25.0, the oldest that Docker Engine 29 still
// takes.
const DockerAPIVersion = "1.44"

// The labels that Docker Compose puts on the containers of a project.
// A fault finds its target by them, so that every service of every
// override file (deploy/interconnect, deploy/redundancy) is a target
// without a list of names here.
const (
	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
	labelNumber  = "com.docker.compose.container-number"
	labelOneOff  = "com.docker.compose.oneoff"
)

// Docker applies faults through the Docker Engine API, to the
// containers of one Compose project. Each operation is the one that a
// docker command does; the log line of every call carries that command
// as "equivalent", so that a learner can type it.
//
// It needs the Engine's socket, which gives the process the power of
// root on the host. Only the fault agent of deploy/chaos/agent.yaml has
// it, an override file that a person names to start the agent;
// compose.yaml never mounts it (TestOnlyTheAgentGetsTheDockerSocket).
type Docker struct {
	// Project is the Compose project, such as "ims-practice".
	Project string
	// ToolsImage holds nft, tc and ip (deploy/chaos/tools).
	ToolsImage string

	client  *http.Client
	baseURL string
	log     *slog.Logger
}

// NewDocker talks to the Engine at the Unix socket, usually
// /var/run/docker.sock. log may be nil.
func NewDocker(socket, project, toolsImage string, log *slog.Logger) *Docker {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	// The host part is not used; the socket is the address.
	return newDocker(&http.Client{Transport: transport}, "http://docker", project, toolsImage, log)
}

func newDocker(c *http.Client, base, project, toolsImage string, log *slog.Logger) *Docker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Docker{
		Project:    project,
		ToolsImage: toolsImage,
		client:     c,
		baseURL:    base + "/v" + DockerAPIVersion,
		log:        log,
	}
}

func (d *Docker) Apply(ctx context.Context, f Fault) error {
	c, err := d.container(ctx, f.Target)
	if err != nil {
		return err
	}
	if c.HoldsDockerSocket {
		return fmt.Errorf("%s holds the Docker socket: it is the fault agent, or a tool like it, and a fault there would stop the faults from ending", c.Name)
	}
	switch f.Kind {
	case Stop:
		return d.post(ctx, "/containers/"+c.ID+"/stop", "docker stop "+c.Name)
	case Kill:
		return d.post(ctx, "/containers/"+c.ID+"/kill", "docker kill "+c.Name)
	case Pause:
		return d.post(ctx, "/containers/"+c.ID+"/pause", "docker pause "+c.Name)
	case Crash:
		return d.exec(ctx, c, crashCommand())
	case Recreate:
		return d.recreate(ctx, c, f.ID)
	}
	devices, err := d.devices(ctx, c, f.Network)
	if err != nil {
		return err
	}
	switch f.Kind {
	case Delay:
		for i, dev := range devices {
			if err := d.inNamespace(ctx, c, netemCommand(dev, time.Duration(f.Delay), time.Duration(f.Jitter))); err != nil {
				// A failed fault leaves nothing behind: take back the
				// interfaces that were delayed already.
				for _, done := range devices[:i] {
					d.clearInNamespace(ctx, c, netemDeleteCommand(done))
				}
				return err
			}
		}
		return nil
	case Loss:
		return d.inNamespace(ctx, c, lossCommand(tableName(f.ID), devices, f.LossPercent))
	case Partition:
		peers, err := d.addresses(ctx, f.Peers)
		if err != nil {
			return err
		}
		return d.inNamespace(ctx, c, partitionCommand(tableName(f.ID), devices, peers))
	}
	return fmt.Errorf("unknown kind %q", f.Kind)
}

func (d *Docker) Clear(ctx context.Context, f Fault) error {
	c, err := d.container(ctx, f.Target)
	if err != nil {
		return err
	}
	switch f.Kind {
	case Stop, Kill:
		return d.post(ctx, "/containers/"+c.ID+"/start", "docker start "+c.Name)
	case Pause:
		return d.post(ctx, "/containers/"+c.ID+"/unpause", "docker unpause "+c.Name)
	case Delay:
		devices, err := d.devices(ctx, c, f.Network)
		if err != nil {
			return err
		}
		var errs []error
		for _, dev := range devices {
			errs = append(errs, d.clearInNamespace(ctx, c, netemDeleteCommand(dev)))
		}
		return errors.Join(errs...)
	case Loss, Partition:
		return d.clearInNamespace(ctx, c, nftDeleteCommand(tableName(f.ID)))
	case Crash, Recreate:
		return fmt.Errorf("a %s is over as soon as it has happened; there is nothing to clear", f.Kind)
	}
	return fmt.Errorf("unknown kind %q", f.Kind)
}

// exec runs cmd inside the container, as docker exec does: the Engine
// makes an exec instance and then starts it. Detached, so that the
// answer does not wait for a process that dies with its container.
func (d *Docker) exec(ctx context.Context, c container, cmd []string) error {
	d.log.Info("docker", "equivalent", "docker exec "+c.Name+" "+shellQuote(cmd))
	var created struct {
		ID string `json:"Id"`
	}
	if err := d.call(ctx, http.MethodPost, "/containers/"+c.ID+"/exec", map[string]any{"Cmd": cmd}, &created); err != nil {
		return err
	}
	return d.call(ctx, http.MethodPost, "/exec/"+created.ID+"/start", map[string]any{"Detach": true}, nil)
}

// recreate replaces the container with a new one that has the same
// configuration, name and address, as an orchestrator replaces a node
// that died. The node says no goodbye, loses what it kept in its
// memory, and gets a new network namespace, so the network faults on it
// are gone with it; its volumes stay, so a store with persistence reads
// its data again.
//
// deploy/chaos/fault.sh does this with "docker compose up -d --no-deps",
// which reads the Compose file again. The agent has no Compose file, so
// it copies the configuration of the container it replaces, which is
// the one Compose gave it.
//
// The new container is made before the old one goes, under a name of
// its own, so that a create that fails leaves the node as it was. Two
// containers may hold the same address and the same aliases in the
// meantime, because the Engine takes them when a container starts, not
// when it is made. id, the id of the fault, keeps the temporary name
// free of the one that another recreate of the same node may be using.
func (d *Docker) recreate(ctx context.Context, c container, id string) error {
	var old struct {
		Config     map[string]any
		HostConfig map[string]any
		// NetworkSettings holds, for each Docker network, the fixed
		// address of the Compose file and the aliases it set, which are
		// the names the other containers reach the node by.
		NetworkSettings struct {
			Networks map[string]struct {
				IPAMConfig *struct{ IPv4Address string }
				Aliases    []string
			}
		}
	}
	if err := d.get(ctx, "/containers/"+c.ID+"/json", &old); err != nil {
		return err
	}
	if len(old.Config) == 0 {
		return fmt.Errorf("the Engine reports no configuration of %s", c.Name)
	}
	create := maps.Clone(old.Config)
	// Compose names the host of each IMS node in compose.yaml; where it
	// names none, the Engine makes the Hostname the first 12 characters
	// of the container's id. The new container is another container, so
	// it is left to get a Hostname of its own instead of the name of the
	// one that is gone.
	if len(c.ID) >= shortIDLength && create["Hostname"] == c.ID[:shortIDLength] {
		delete(create, "Hostname")
	}
	create["HostConfig"] = old.HostConfig
	endpoints := map[string]any{}
	for network, n := range old.NetworkSettings.Networks {
		endpoint := map[string]any{}
		if n.IPAMConfig != nil && n.IPAMConfig.IPv4Address != "" {
			endpoint["IPAMConfig"] = map[string]any{"IPv4Address": n.IPAMConfig.IPv4Address}
		}
		if len(n.Aliases) > 0 {
			endpoint["Aliases"] = n.Aliases
		}
		endpoints[network] = endpoint
	}
	create["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}

	temporary := c.Name + "-replacing-" + id
	var made struct {
		ID string `json:"Id"`
	}
	d.log.Info("docker", "equivalent", "docker create --name "+temporary+" (the configuration of "+c.Name+")")
	if err := d.call(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(temporary), create, &made); err != nil {
		return fmt.Errorf("%s is still running; the Engine could not make the container that replaces it: %w", c.Name, err)
	}
	// force kills the old container and removes it in one call, whether
	// it is running, stopped or on its way back up after a crash.
	d.log.Info("docker", "equivalent", "docker rm -f "+c.Name)
	if err := d.call(ctx, http.MethodDelete, "/containers/"+c.ID+"?force=1", nil, nil); err != nil {
		// Nothing of the fault is left behind: the node is as it was,
		// and the container that would have replaced it goes.
		d.call(context.WithoutCancel(ctx), http.MethodDelete, "/containers/"+made.ID+"?force=1", nil, nil)
		return err
	}
	d.log.Info("docker", "equivalent", "docker rename "+temporary+" "+c.Name)
	if err := d.call(ctx, http.MethodPost, "/containers/"+made.ID+"/rename?name="+url.QueryEscape(c.Name), nil, nil); err != nil {
		return fmt.Errorf("%s is removed and the container that replaces it is still called %s: %w", c.Name, temporary, err)
	}
	return d.post(ctx, "/containers/"+made.ID+"/start", "docker start "+c.Name)
}

// container is what Docker reports of a container of the project.
type container struct {
	ID      string
	Name    string
	Service string
	// Number is the com.docker.compose.container-number label: 1 for
	// the only container of a service, 1 to n with --scale.
	Number int
	// OneOff is a container of "docker compose run".
	OneOff bool
	State  string
	// NetworkMode is "host" for the capture containers, which share the
	// host's network namespace to record the bridges.
	NetworkMode string
	// Networks maps the Docker networks, such as "ims-practice_nni",
	// to the container's IPv4 address there.
	Networks map[string]netip.Addr
	// Addresses are the addresses of Networks, in order.
	Addresses []netip.Addr
	// HoldsDockerSocket is true for a container with the Docker socket
	// mounted, such as the fault agent of deploy/chaos/agent.yaml.
	HoldsDockerSocket bool
}

// dockerSocket is the file name of the Engine's socket, wherever it is
// mounted.
const dockerSocket = "docker.sock"

// shortIDLength is how much of a container's id the Engine shows as its
// short id, and makes the host name of a container that was given none.
const shortIDLength = 12

// target writes the container the way a request names it.
func (c container) target() string { return c.Service + ":" + strconv.Itoa(c.Number) }

// list returns the containers of the project that carry the labels,
// stopped ones included.
func (d *Docker) list(ctx context.Context, labels ...string) ([]container, error) {
	filters, _ := json.Marshal(map[string][]string{"label": append([]string{labelProject + "=" + d.Project}, labels...)})
	var list []struct {
		ID              string `json:"Id"`
		Names           []string
		Labels          map[string]string
		State           string
		HostConfig      struct{ NetworkMode string }
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
		Mounts []struct{ Source, Destination string }
	}
	p := "/containers/json?all=1&filters=" + url.QueryEscape(string(filters))
	if err := d.get(ctx, p, &list); err != nil {
		return nil, err
	}
	out := make([]container, 0, len(list))
	for _, l := range list {
		number, _ := strconv.Atoi(l.Labels[labelNumber])
		c := container{
			ID:          l.ID,
			Name:        strings.TrimPrefix(l.Names[0], "/"),
			Service:     l.Labels[labelService],
			Number:      number,
			OneOff:      l.Labels[labelOneOff] == "True",
			State:       l.State,
			NetworkMode: l.HostConfig.NetworkMode,
			Networks:    map[string]netip.Addr{},
		}
		for _, m := range l.Mounts {
			if path.Base(m.Source) == dockerSocket || path.Base(m.Destination) == dockerSocket {
				c.HoldsDockerSocket = true
			}
		}
		for name, n := range l.NetworkSettings.Networks {
			if a, err := netip.ParseAddr(n.IPAddress); err == nil && a.Is4() {
				c.Networks[name] = a
				c.Addresses = append(c.Addresses, a)
			}
		}
		slices.SortFunc(c.Addresses, netip.Addr.Compare)
		out = append(out, c)
	}
	return out, nil
}

// containers returns the containers of the service, or of every
// service that a path.Match pattern matches, and of only the one
// numbered so when number is not empty. For each service, they are the
// containers that "docker compose up" made; a service that has none,
// such as the UEs ue to ue3 of compose.yaml, gives its running one-off
// containers of "docker compose run" instead.
func (d *Docker) containers(ctx context.Context, service, number string) ([]container, error) {
	var labels []string
	if !strings.ContainsAny(service, "*?") {
		labels = append(labels, labelService+"="+service)
	}
	if number != "" {
		labels = append(labels, labelNumber+"="+number)
	}
	all, err := d.list(ctx, labels...)
	if err != nil {
		return nil, err
	}
	up := map[string][]container{}
	run := map[string][]container{}
	for _, c := range all {
		// The helpers of runHelper have no service.
		if ok, _ := path.Match(service, c.Service); !ok || c.Service == "" {
			continue
		}
		if !c.OneOff {
			up[c.Service] = append(up[c.Service], c)
		} else if c.State == "running" {
			run[c.Service] = append(run[c.Service], c)
		}
	}
	var out []container
	for s, cs := range run {
		if len(up[s]) == 0 {
			up[s] = cs
		}
	}
	for _, cs := range up {
		out = append(out, cs...)
	}
	slices.SortFunc(out, func(a, b container) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Number, b.Number), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}

// container finds the one container of a target, "service" or
// "service:number".
func (d *Docker) container(ctx context.Context, target string) (container, error) {
	service, number := splitTarget(target)
	cs, err := d.containers(ctx, service, number)
	if err != nil {
		return container{}, err
	}
	switch len(cs) {
	case 0:
		if number != "" {
			return container{}, fmt.Errorf("no container %s of the service %q in the project %q", number, service, d.Project)
		}
		return container{}, fmt.Errorf("no container of the service %q in the project %q", service, d.Project)
	case 1:
		return cs[0], nil
	}
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.target()
	}
	return container{}, fmt.Errorf("the service %q has %d containers (%s); name one", service, len(cs), strings.Join(names, ", "))
}

// addresses turns the peers into addresses: an address stays as it is,
// a service gives the addresses of its containers on every network.
func (d *Docker) addresses(ctx context.Context, peers []string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, p := range peers {
		if a, err := netip.ParseAddr(p); err == nil {
			out = append(out, a)
			continue
		}
		service, number := splitTarget(p)
		cs, err := d.containers(ctx, service, number)
		if err != nil {
			return nil, fmt.Errorf("peer: %w", err)
		}
		if len(cs) == 0 {
			return nil, fmt.Errorf("peer: no container of the service %q in the project %q", p, d.Project)
		}
		n := len(out)
		for _, c := range cs {
			out = append(out, c.Addresses...)
		}
		if len(out) == n {
			return nil, fmt.Errorf("peer %q has no IPv4 address; is it running?", p)
		}
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return slices.Compact(out), nil
}

// devices finds the target's interfaces that a network fault sits on:
// the one on the Compose network, or all of them. Compose names a
// network <project>_<name>, such as ims-practice_nni.
func (d *Docker) devices(ctx context.Context, c container, network string) ([]string, error) {
	if err := sharesTheHost(c); err != nil {
		return nil, err
	}
	addresses := c.Addresses
	if network != "" {
		a, ok := c.Networks[d.Project+"_"+network]
		if !ok {
			a, ok = c.Networks[network]
		}
		if !ok {
			return nil, fmt.Errorf("%s is not on the network %q", c.Name, network)
		}
		addresses = []netip.Addr{a}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%s has no IPv4 address; is it running?", c.Name)
	}
	out, err := d.runHelper(ctx, c, addressesCommand())
	if err != nil {
		return nil, err
	}
	devices := devicesOf(out, addresses)
	if len(devices) == 0 {
		return nil, fmt.Errorf("no interface of %s holds %v: %s", c.Name, addresses, out)
	}
	return devices, nil
}

// sharesTheHost refuses a network fault on a container in the host's
// network namespace, such as capture: the rules would change the host.
func sharesTheHost(c container) error {
	if c.NetworkMode == "host" {
		return fmt.Errorf("%s uses the host's network; a network fault there would change the host", c.Name)
	}
	return nil
}

// inNamespace runs cmd in a helper container that joins the target's
// network namespace with NET_ADMIN, like
// docker run --rm --network container:<target> --cap-add NET_ADMIN.
// The rules stay in the namespace after the helper has gone. The IMS
// image needs neither the tools nor the capability.
func (d *Docker) inNamespace(ctx context.Context, c container, cmd []string) error {
	out, err := d.runHelper(ctx, c, cmd)
	if err != nil {
		return err
	}
	if out != "" {
		d.log.Info("fault helper", "target", c.Name, "output", out)
	}
	return nil
}

func (d *Docker) clearInNamespace(ctx context.Context, c container, cmd []string) error {
	out, err := d.runHelper(ctx, c, cmd)
	if err != nil && alreadyGone(out) {
		d.log.Info("fault helper", "target", c.Name, "event", "already gone", "output", out)
		return nil
	}
	return err
}

func (d *Docker) runHelper(ctx context.Context, c container, cmd []string) (string, error) {
	if err := sharesTheHost(c); err != nil {
		return "", err
	}
	equivalent := "docker run --rm --network container:" + c.Name + " --cap-add NET_ADMIN " + d.ToolsImage + " " + shellQuote(cmd)
	create := map[string]any{
		"Image": d.ToolsImage,
		"Cmd":   cmd,
		"Labels": map[string]string{
			// Marks the helper as part of the project for "docker
			// compose ps -a" and for the clean up, but not as one of
			// its services.
			"com.docker.compose.project": d.Project,
			"ims-practice.fault-helper":  c.Name,
		},
		"HostConfig": map[string]any{
			"NetworkMode": "container:" + c.ID,
			"CapAdd":      []string{"NET_ADMIN"},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	d.log.Info("docker", "equivalent", equivalent)
	if err := d.call(ctx, http.MethodPost, "/containers/create", create, &created); err != nil {
		if strings.Contains(err.Error(), "No such image") {
			return "", fmt.Errorf("%w; build it with: docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools", err)
		}
		return "", err
	}
	defer func() {
		// The helper is removed even when ctx has ended.
		ctx := context.WithoutCancel(ctx)
		d.call(ctx, http.MethodDelete, "/containers/"+created.ID+"?force=1", nil, nil)
	}()
	if err := d.call(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil); err != nil {
		return "", err
	}
	var waited struct{ StatusCode int }
	if err := d.call(ctx, http.MethodPost, "/containers/"+created.ID+"/wait", nil, &waited); err != nil {
		return "", err
	}
	out, err := d.logs(ctx, created.ID)
	if err != nil {
		return "", err
	}
	if waited.StatusCode != 0 {
		return out, fmt.Errorf("%s exited with %d: %s", cmd[0], waited.StatusCode, out)
	}
	return out, nil
}

// logs reads what the helper wrote. Without a TTY, the Engine frames
// each piece with 8 bytes: the stream, three zeros, and the length.
func (d *Docker) logs(ctx context.Context, id string) (string, error) {
	resp, err := d.do(ctx, http.MethodGet, "/containers/"+id+"/logs?stdout=1&stderr=1", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	var header [8]byte
	for {
		if _, err := io.ReadFull(resp.Body, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("logs of the helper: %w", err)
		}
		n := binary.BigEndian.Uint32(header[4:])
		if _, err := io.CopyN(&out, resp.Body, int64(n)); err != nil {
			return "", fmt.Errorf("logs of the helper: %w", err)
		}
	}
	return strings.TrimSpace(out.String()), nil
}

// post calls an operation that has no body either way, such as
// /containers/{id}/stop.
func (d *Docker) post(ctx context.Context, path, equivalent string) error {
	d.log.Info("docker", "equivalent", equivalent)
	return d.call(ctx, http.MethodPost, path, nil, nil)
}

func (d *Docker) get(ctx context.Context, path string, out any) error {
	return d.call(ctx, http.MethodGet, path, nil, out)
}

// call sends in as JSON, if any, and decodes the answer into out, if
// any. 304, which stop and start give for a container that is already
// so, is a success.
func (d *Docker) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	resp, err := d.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode != http.StatusNotModified {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("docker %s %s: %w", method, path, err)
		}
	}
	return nil
}

func (d *Docker) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker engine: %w", err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		defer resp.Body.Close()
		var e struct{ Message string }
		json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		return nil, fmt.Errorf("docker engine: %s %s: %s: %s", method, strings.SplitN(path, "?", 2)[0], resp.Status, e.Message)
	}
	return resp, nil
}

// shellQuote writes cmd as a shell would take it, for the log.
func shellQuote(cmd []string) string {
	out := make([]string, len(cmd))
	for i, a := range cmd {
		if a != "" && !strings.ContainsAny(a, " \t\n\"'\\$`{}<>;|&*?![]()#~") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
