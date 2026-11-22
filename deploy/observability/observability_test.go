// These tests fail when compose.yaml gains or loses a node and
// prometheus.yml has not been generated again, and when a dashboard
// points at a data source that is not provisioned.
package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	fragment      = "compose.observability.yaml"
	datasources   = "grafana/provisioning/datasources/prometheus.yaml"
	dashboardsDir = "grafana/dashboards"
)

type composeFile struct {
	Services map[string]struct {
		Networks map[string]struct {
			IPv4Address string `yaml:"ipv4_address"`
		} `yaml:"networks"`
	} `yaml:"services"`
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readYAML(t *testing.T, path string, v any) {
	t.Helper()
	b := readFile(t, path)
	if err := yaml.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// regenerate is said by every failure that the generator fixes.
var regenerate = fmt.Sprintf("run %q from the root of the repository and commit the result", GenerateCommand)

// TestPrometheusConfigIsGenerated fails when compose.yaml or
// MetricsPort has changed and prometheus.yml has not been made again.
func TestPrometheusConfigIsGenerated(t *testing.T) {
	compose := readFile(t, RootCompose)
	got := readFile(t, PrometheusYML)
	want, err := PrometheusConfig(got, compose)
	if err != nil {
		t.Fatalf("%s: %v", PrometheusYML, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("deploy/observability/%s does not match compose.yaml: %s", PrometheusYML, regenerate)
	}
}

// TestEveryNodeIsScraped reads the generated file as Prometheus does,
// and checks that the ims job lists exactly the nodes that "docker
// compose up" starts, each under its service name and on MetricsPort.
func TestEveryNodeIsScraped(t *testing.T) {
	var config struct {
		ScrapeConfigs []struct {
			JobName       string `yaml:"job_name"`
			StaticConfigs []struct {
				Targets []string          `yaml:"targets"`
				Labels  map[string]string `yaml:"labels"`
			} `yaml:"static_configs"`
		} `yaml:"scrape_configs"`
	}
	readYAML(t, PrometheusYML, &config)
	var got []string
	for _, job := range config.ScrapeConfigs {
		if job.JobName != "ims" {
			continue
		}
		for _, sc := range job.StaticConfigs {
			node := sc.Labels["node"]
			want := []string{fmt.Sprintf("%s:%d", node, MetricsPort)}
			if !slices.Equal(sc.Targets, want) {
				t.Errorf("node %q has targets %v, want %v", node, sc.Targets, want)
			}
			got = append(got, node)
		}
	}
	nodes, err := Nodes(readFile(t, RootCompose))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, nodes) {
		t.Errorf("the ims job scrapes %v, compose.yaml starts %v: %s", got, nodes, regenerate)
	}
}

// TestNodesHaveARole checks the rule of Nodes from another side: every
// service it picks mounts a role file, so it runs an IMS role and not,
// for example, a UE simulator.
func TestNodesHaveARole(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	readYAML(t, RootCompose, &compose)
	nodes, err := Nodes(readFile(t, RootCompose))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if !slices.ContainsFunc(compose.Services[n].Volumes, func(v string) bool {
			return strings.Contains(v, ":/etc/ims/node.yaml")
		}) {
			t.Errorf("service %s is taken as a node but mounts no /etc/ims/node.yaml", n)
		}
	}
}

func TestNodes(t *testing.T) {
	compose := []byte(`
x-node: &node
  image: ims-practice/ims:dev
  pull_policy: never
services:
  dns:
    image: coredns/coredns:1.12.1
  hss:
    <<: *node
    build: .
  pcscf:
    <<: *node
  long:
    build:
      context: .
      dockerfile: Dockerfile
  ue:
    <<: *node
    profiles: [ue]
  capture:
    build: images/capture
`)
	got, err := Nodes(compose)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hss", "pcscf", "long"}; !slices.Equal(got, want) {
		t.Errorf("Nodes = %v, want %v", got, want)
	}
	if _, err := Nodes([]byte("services:\n  dns:\n    image: coredns/coredns:1.12.1\n")); err == nil {
		t.Error("Nodes found nodes in a file without any")
	}
}

func TestPrometheusConfigKeepsTheHandWrittenLines(t *testing.T) {
	compose := []byte("services:\n  hss:\n    build: .\n")
	current := "global:\n  scrape_interval: 15s\nscrape_configs:\n  " + beginMarker +
		"\n  - job_name: stale\n  " + endMarker + "\n  - job_name: prometheus\n"
	got, err := PrometheusConfig([]byte(current), compose)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("global:\n  scrape_interval: 15s\nscrape_configs:\n  %s\n"+
		"  - job_name: ims\n    metrics_path: /metrics\n    static_configs:\n"+
		"      - targets: [\"hss:%d\"]\n        labels: {node: hss}\n"+
		"  %s\n  - job_name: prometheus\n", beginMarker, MetricsPort, endMarker)
	if string(got) != want {
		t.Errorf("PrometheusConfig =\n%s\nwant\n%s", got, want)
	}
	again, err := PrometheusConfig(got, compose)
	if err != nil || !bytes.Equal(again, got) {
		t.Errorf("a second run changed the file (%v)", err)
	}
	if _, err := PrometheusConfig([]byte("global: {}\n"), compose); err == nil {
		t.Error("PrometheusConfig accepted a file without the markers")
	}
}

func TestFragmentAddressesAreFree(t *testing.T) {
	var compose, extra composeFile
	readYAML(t, RootCompose, &compose)
	readYAML(t, fragment, &extra)
	used := map[string]string{}
	for name, s := range compose.Services {
		if n, ok := s.Networks["ims"]; ok && n.IPv4Address != "" {
			used[n.IPv4Address] = name
		}
	}
	for name, s := range extra.Services {
		if _, exists := compose.Services[name]; exists {
			t.Errorf("the fragment redefines service %q of compose.yaml", name)
		}
		addr := s.Networks["ims"].IPv4Address
		if other, taken := used[addr]; taken {
			t.Errorf("%s uses %s, which %s has in compose.yaml", name, addr, other)
		}
	}
}

func TestDashboardsUseTheProvisionedDataSource(t *testing.T) {
	var ds struct {
		Datasources []struct {
			UID string `yaml:"uid"`
		} `yaml:"datasources"`
	}
	readYAML(t, datasources, &ds)
	if len(ds.Datasources) != 1 {
		t.Fatalf("%d data sources, want 1", len(ds.Datasources))
	}
	uid := ds.Datasources[0].UID

	files, err := filepath.Glob(filepath.Join(dashboardsDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no dashboards in %s (%v)", dashboardsDir, err)
	}
	type datasource struct {
		UID string `json:"uid"`
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			UID    string `json:"uid"`
			Panels []struct {
				Title      string      `json:"title"`
				Type       string      `json:"type"`
				Datasource *datasource `json:"datasource"`
				Targets    []struct {
					Datasource *datasource `json:"datasource"`
					Expr       string      `json:"expr"`
				} `json:"targets"`
			} `json:"panels"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if d.UID == "" {
			t.Errorf("%s has no uid, so its URL would change on every start", file)
		}
		for _, p := range d.Panels {
			if p.Type == "row" {
				continue
			}
			if p.Datasource == nil || p.Datasource.UID != uid {
				t.Errorf("%s: panel %q does not use data source %q", file, p.Title, uid)
			}
			for _, target := range p.Targets {
				if target.Datasource == nil || target.Datasource.UID != uid || target.Expr == "" {
					t.Errorf("%s: panel %q has a query without data source %q or expression", file, p.Title, uid)
				}
			}
		}
	}
}
