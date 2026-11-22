// These tests fail when compose.yaml or a node configuration gains or
// loses a service or a Diameter peer and the tables of the guides
// still say the old thing, and when a table names a service that
// compose.yaml does not define.
package compose

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	rootCompose = "../../compose.yaml"
	// The network of the IMS nodes in compose.yaml.
	imsNetwork = "ims"
	// Where Compose mounts the configuration of a node.
	nodeConfigMount = ":/etc/ims/node.yaml"
)

type composeFile struct {
	Services map[string]struct {
		Profiles    []string `yaml:"profiles"`
		NetworkMode string   `yaml:"network_mode"`
		Volumes     []string `yaml:"volumes"`
		Networks    map[string]struct {
			IPv4Address string `yaml:"ipv4_address"`
		} `yaml:"networks"`
	} `yaml:"services"`
}

type nodeConfig struct {
	Roles    []string `yaml:"roles"`
	Diameter struct {
		OriginHost string `yaml:"origin_host"`
		Peers      []struct {
			Address    string `yaml:"address"`
			OriginHost string `yaml:"origin_host"`
			Kind       string `yaml:"kind"`
		} `yaml:"peers"`
	} `yaml:"diameter"`
}

func readCompose(t *testing.T) composeFile {
	t.Helper()
	b, err := os.ReadFile(rootCompose)
	if err != nil {
		t.Fatal(err)
	}
	var c composeFile
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("%s: %v", rootCompose, err)
	}
	return c
}

// tableRows returns the rows of the Markdown table whose header row has
// exactly these cells. Backticks around a cell are removed.
func tableRows(t *testing.T, path string, header []string) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	cells := func(line string) []string {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
		var out []string
		for _, c := range strings.Split(line, "|") {
			out = append(out, strings.Trim(strings.TrimSpace(c), "`"))
		}
		return out
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "|") || !slices.Equal(cells(line), header) {
			continue
		}
		var rows [][]string
		for _, l := range lines[i+2:] { // skip the |---| line
			if !strings.HasPrefix(l, "|") {
				break
			}
			rows = append(rows, cells(l))
		}
		return rows
	}
	t.Fatalf("%s has no table with the header %v", path, header)
	return nil
}

// compare reports every difference between what the Compose files say
// (want) and what the table says (got), both keyed by the name of a
// service or a connection.
func compare(t *testing.T, table, what string, want, got map[string]string) {
	t.Helper()
	for _, key := range sortedKeys(want) {
		g, ok := got[key]
		switch {
		case !ok:
			t.Errorf("%s has no row for %s %q, which the Compose files have (%s)", table, what, key, want[key])
		case g != want[key]:
			t.Errorf("%s says %s %q has %q, the Compose files say %q", table, what, key, g, want[key])
		}
	}
	for _, key := range sortedKeys(got) {
		if _, ok := want[key]; !ok {
			t.Errorf("%s has a row for %s %q, which the Compose files do not have", table, what, key)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// byKey turns rows into key -> value, and fails on a key that appears
// twice.
func byKey(t *testing.T, table string, rows [][]string, key func([]string) string, value func([]string) string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, r := range rows {
		k := key(r)
		if _, dup := got[k]; dup {
			t.Errorf("%s has two rows for %q", table, k)
		}
		got[k] = value(r)
	}
	return got
}

type language struct {
	name         string
	capture      string
	readme       string
	addressTable []string
	diameter     []string
	services     []string
}

var languages = []language{
	{
		name:         "en",
		capture:      "../../docs/guide/en/capture.md",
		readme:       "../../docs/guide/en/README.md",
		addressTable: []string{"Node", "Service", "Address", "Profile"},
		diameter:     []string{"Client", "Server", "Application", "Port"},
		services:     []string{"Service", "Address", "What it is"},
	},
	{
		name:         "ja",
		capture:      "../../docs/guide/ja/capture.md",
		readme:       "../../docs/guide/ja/README.md",
		addressTable: []string{"ノード", "サービス", "アドレス", "プロファイル"},
		diameter:     []string{"クライアント", "サーバー", "アプリケーション", "ポート"},
		services:     []string{"サービス", "アドレス", "何か"},
	},
}

// TestCaptureGuideListsEveryAddress: the address table of the capture
// guide has a row for each service that has an address on the network
// of the IMS nodes, with that address and the profile that starts it.
// The capture service shares the host's network, so it has no row.
func TestCaptureGuideListsEveryAddress(t *testing.T) {
	compose := readCompose(t)
	want := map[string]string{}
	for name, s := range compose.Services {
		addr := s.Networks[imsNetwork].IPv4Address
		if addr == "" {
			continue
		}
		want[name] = fmt.Sprintf("%s, profile %q", addr, strings.Join(s.Profiles, ", "))
	}
	for _, lang := range languages {
		t.Run(lang.name, func(t *testing.T) {
			rows := tableRows(t, lang.capture, lang.addressTable)
			got := byKey(t, lang.capture, rows,
				func(r []string) string { return r[1] },
				func(r []string) string { return fmt.Sprintf("%s, profile %q", r[2], r[3]) })
			compare(t, "the address table of "+lang.capture, "service", want, got)
		})
	}
}

// TestGuideREADMEListsTheServicesOfUp: the table of the guides' README
// has a row for each service that "docker compose up" starts, that is,
// each service without a profile.
func TestGuideREADMEListsTheServicesOfUp(t *testing.T) {
	compose := readCompose(t)
	want := map[string]string{}
	for name, s := range compose.Services {
		if len(s.Profiles) > 0 {
			continue
		}
		addr := s.Networks[imsNetwork].IPv4Address
		if s.NetworkMode == "host" {
			addr = "host network"
		}
		want[name] = addr
	}
	for _, lang := range languages {
		t.Run(lang.name, func(t *testing.T) {
			rows := tableRows(t, lang.readme, lang.services)
			got := byKey(t, lang.readme, rows,
				func(r []string) string { return r[0] },
				func(r []string) string {
					// The capture service has words, in either language,
					// where the others have an address.
					if r[1] != "" && (r[1][0] < '0' || r[1][0] > '9') {
						return "host network"
					}
					return r[1]
				})
			compare(t, "the service table of "+lang.readme, "service", want, got)
		})
	}
}

// applications names the Diameter application of a connection from the
// role of the client and the kind of the peer in its configuration.
var applications = map[[2]string]string{
	{"icscf", "hss"}:  "Cx",
	{"scscf", "hss"}:  "Cx",
	{"ipsmgw", "hss"}: "Sh",
	{"mmtel", "hss"}:  "Sh",
	{"pcscf", "pcrf"}: "Rx",
}

// TestCaptureGuideListsEveryDiameterConnection: the Diameter table of
// the capture guide has a row for each peer in a node configuration,
// with the service of the client, the service that holds the peer's
// Origin-Host, the application and the port.
func TestCaptureGuideListsEveryDiameterConnection(t *testing.T) {
	compose := readCompose(t)
	configs := map[string]nodeConfig{}
	for name, s := range compose.Services {
		for _, v := range s.Volumes {
			path, _, ok := strings.Cut(v, nodeConfigMount) // the mount may end in :ro
			if !ok {
				continue
			}
			b, err := os.ReadFile("../../" + strings.TrimPrefix(path, "./"))
			if err != nil {
				t.Fatal(err)
			}
			var c nodeConfig
			if err := yaml.Unmarshal(b, &c); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			configs[name] = c
		}
	}
	serviceOf := map[string]string{} // Origin-Host -> service
	for name, c := range configs {
		if c.Diameter.OriginHost != "" {
			serviceOf[c.Diameter.OriginHost] = name
		}
	}
	want := map[string]string{}
	for client, c := range configs {
		for _, p := range c.Diameter.Peers {
			server, ok := serviceOf[p.OriginHost]
			if !ok {
				t.Errorf("the %s service has a Diameter peer %q that no node configuration has as its origin_host", client, p.OriginHost)
				continue
			}
			port := p.Address[strings.LastIndex(p.Address, ":")+1:]
			role := ""
			if len(c.Roles) > 0 {
				role = c.Roles[0]
			}
			app, ok := applications[[2]string{role, p.Kind}]
			if !ok {
				t.Errorf("the %s service (role %q) has a Diameter peer of kind %q: add its application to applications in this test", client, role, p.Kind)
			}
			want[client+" -> "+server] = app + ", port " + port
		}
	}
	for _, lang := range languages {
		t.Run(lang.name, func(t *testing.T) {
			rows := tableRows(t, lang.capture, lang.diameter)
			got := byKey(t, lang.capture, rows,
				func(r []string) string { return r[0] + " -> " + r[1] },
				func(r []string) string { return r[2] + ", port " + r[3] })
			compare(t, "the Diameter table of "+lang.capture, "connection", want, got)
		})
	}
}
