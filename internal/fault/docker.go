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
	}
	return fmt.Errorf("unknown kind %q", f.Kind)
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
