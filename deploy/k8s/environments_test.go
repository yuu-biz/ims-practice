package k8s

import (
	"io/fs"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// commonValues are the values that every environment defines, since the
// components of every cluster read them.
var commonValues = []string{nameServerAddressKey, "pod_cidr"}

func environments(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob("environments/*")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(dirs, KindEnvironment) {
		t.Fatalf("no %s among %v", KindEnvironment, dirs)
	}
	return dirs
}

func TestEveryEnvironmentHasTheCommonValues(t *testing.T) {
	for _, dir := range environments(t) {
		values, err := environmentValues(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range commonValues {
			if _, ok := values[key]; !ok {
				t.Errorf("%s defines no %s", dir, key)
			}
		}
		if _, err := netip.ParsePrefix(values["pod_cidr"]); err != nil {
			t.Errorf("%s: pod_cidr: %v", dir, err)
		}
		if _, err := netip.ParseAddr(values[nameServerAddressKey]); err != nil {
			t.Errorf("%s: %s: %v", dir, nameServerAddressKey, err)
		}
	}
}

// localValuesFile is the file of a cloud environment that holds the
// person's own values, and that Git ignores (.gitignore).
const localValuesFile = "values.local.env"

// TestCloudValuesAreNotCommitted: the values of a cloud environment are
// the addresses of one person's line and nodes, and the name of their
// registry, which holds their account. A cloud environment reads them
// all from localValuesFile, which .gitignore lists, and the repository
// has only the example next to it. kind's values are those of
// kind-cluster.yaml, the same for everybody.
func TestCloudValuesAreNotCommitted(t *testing.T) {
	ignored := strings.Split(strings.ReplaceAll(string(readFile(t, ".gitignore")), "\r", ""), "\n")
	if !slices.Contains(ignored, localValuesFile) {
		t.Errorf(".gitignore does not list %s", localValuesFile)
	}
	for _, dir := range environments(t) {
		if dir == KindEnvironment {
			continue
		}
		var k struct {
			ConfigMapGenerator []struct {
				Name     string
				Literals []string
				Envs     []string
			} `yaml:"configMapGenerator"`
		}
		if err := yaml.Unmarshal(readFile(t, filepath.Join(dir, "kustomization.yaml")), &k); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, g := range k.ConfigMapGenerator {
			if len(g.Literals) > 0 {
				t.Errorf("%s writes %v into the repository; put them in %s and its example", dir, g.Literals, localValuesFile)
			}
			if !slices.Equal(g.Envs, []string{localValuesFile}) {
				t.Errorf("%s reads %v, want [%s]", dir, g.Envs, localValuesFile)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, localValuesFile+ExampleSuffix)); err != nil {
			t.Error(err)
		}
	}
}

// documentationRanges are the IPv4 ranges for examples (RFC 5737). The
// init container of components/edge/pcscf.yaml refuses them, so that a
// copy of the example that nobody edited stops the P-CSCF at start.
var documentationRanges = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
}

// TestExampleValuesReachNobody: an example holds made-up values only.
// A public address is one of documentationRanges, a private range is
// allowed (it is the cluster's inside, the same for everybody), and a
// name with a dot, such as a registry, ends in .invalid (RFC 2606), so
// that it never resolves.
func TestExampleValuesReachNobody(t *testing.T) {
	documentation := func(a netip.Addr) bool {
		return slices.ContainsFunc(documentationRanges, func(p netip.Prefix) bool { return p.Contains(a) })
	}
	for _, dir := range environments(t) {
		if dir == KindEnvironment {
			continue
		}
		values, err := environmentValues(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range slices.Sorted(maps.Keys(values)) {
			value := values[key]
			var addr netip.Addr
			if p, err := netip.ParsePrefix(value); err == nil {
				addr = p.Addr()
			} else if a, err := netip.ParseAddr(value); err == nil {
				addr = a
			}
			switch {
			case addr.IsValid():
				if !addr.IsPrivate() && !documentation(addr) {
					t.Errorf("%s/%s: %s=%s is neither private nor in %v", dir, localValuesFile+ExampleSuffix, key, value, documentationRanges)
				}
			case strings.Contains(value, "."):
				host, _, _ := strings.Cut(value, "/")
				if !strings.HasSuffix(host, ".invalid") {
					t.Errorf("%s/%s: %s=%s names a host that is not under .invalid", dir, localValuesFile+ExampleSuffix, key, value)
				}
			}
		}
	}

	// The examples of deploy/cloud, whose terraform.tfvars Git ignores
	// (deploy/cloud/.gitignore), follow the same rule for addresses.
	examples, err := filepath.Glob("../cloud/*/terraform.tfvars" + ExampleSuffix)
	if err != nil || len(examples) == 0 {
		t.Fatalf("no terraform.tfvars%s in deploy/cloud: %v", ExampleSuffix, err)
	}
	ipv4 := regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)
	for _, file := range examples {
		for _, found := range ipv4.FindAllString(string(readFile(t, file)), -1) {
			if addr, err := netip.ParseAddr(found); err != nil || !(addr.IsPrivate() || documentation(addr)) {
				t.Errorf("%s: %s is neither private nor in %v", file, found, documentationRanges)
			}
		}
	}

	// The case patterns of the init container, from documentationRanges.
	var patterns []string
	for _, p := range documentationRanges {
		patterns = append(patterns, strings.TrimSuffix(p.Addr().String(), "0")+"*")
	}
	refusal := strings.Join(patterns, " | ") + ")"
	if !strings.Contains(string(readFile(t, "components/edge/pcscf.yaml")), refusal) {
		t.Errorf("components/edge/pcscf.yaml does not refuse the example addresses with the case %q", refusal)
	}
}

// TestKindEnvironmentMatchesTheCluster: environments/kind repeats what
// kind-cluster.yaml makes, and must say the same.
func TestKindEnvironmentMatchesTheCluster(t *testing.T) {
	var cluster struct {
		Networking struct {
			PodSubnet     string `yaml:"podSubnet"`
			ServiceSubnet string `yaml:"serviceSubnet"`
		}
	}
	if err := yaml.Unmarshal(readFile(t, "kind-cluster.yaml"), &cluster); err != nil {
		t.Fatal(err)
	}
	values, err := environmentValues(KindEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if values["pod_cidr"] != cluster.Networking.PodSubnet {
		t.Errorf("pod_cidr is %s, kind-cluster.yaml has podSubnet %s", values["pod_cidr"], cluster.Networking.PodSubnet)
	}
	services, err := netip.ParsePrefix(cluster.Networking.ServiceSubnet)
	if err != nil {
		t.Fatal(err)
	}
	dns, err := netip.ParseAddr(values[nameServerAddressKey])
	if err != nil {
		t.Fatal(err)
	}
	// Kubernetes takes host 1 for the API server and host 10 for the
	// cluster DNS (deploy/cloud/modules/ims-network makes the same check).
	first := services.Masked().Addr()
	reserved := []netip.Addr{first, first.Next(), hostOf(first, 10)}
	if !services.Contains(dns) || slices.Contains(reserved, dns) {
		t.Errorf("%s %s must be inside serviceSubnet %s and none of %v", nameServerAddressKey, dns, services, reserved)
	}
}

func hostOf(first netip.Addr, n int) netip.Addr {
	for range n {
		first = first.Next()
	}
	return first
}

// TestKindValuesAreWrittenOnce: kind's values are in environments/kind,
// and in no manifest, so that another environment changes them all from
// one place. The zone takes the address from environments/kind when it
// is generated, and kind-cluster.yaml makes the Pod range.
func TestKindValuesAreWrittenOnce(t *testing.T) {
	values, err := environmentValues(KindEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string][]string{
		nameServerAddressKey: {ZoneDir + "/" + homeZone},
		"pod_cidr":           {"kind-cluster.yaml"},
	}
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// Another environment may have the same value.
		if strings.HasPrefix(path, "environments/") || slices.Contains([]string{".go", ".md"}, filepath.Ext(path)) {
			return nil
		}
		content := string(readFile(t, path))
		for _, key := range slices.Sorted(maps.Keys(values)) {
			if strings.Contains(content, values[key]) && !slices.Contains(allowed[key], path) {
				t.Errorf("deploy/k8s/%s has kind's %s %s; take it from the environment (environments/) with a replacement", path, key, values[key])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
