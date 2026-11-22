package k8s

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"go.yaml.in/yaml/v3"
)

// regenerate is said by every failure that the generator fixes.
var regenerate = fmt.Sprintf("run %q from the root of the repository and commit the result", GenerateCommand)

func copies(t *testing.T) map[string][]byte {
	t.Helper()
	c, err := Copies(ComposeDir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCopiesAreGenerated fails when deploy/compose or changes.go has
// changed and the copies have not been made again.
func TestCopiesAreGenerated(t *testing.T) {
	want := copies(t)
	for _, path := range slices.Sorted(maps.Keys(want)) {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("deploy/k8s/%s is missing: %s", path, regenerate)
			continue
		}
		if line, g, w, differ := firstDifference(got, want[path]); differ {
			t.Errorf("deploy/k8s/%s line %d is\n\t%q\nwant\n\t%q\n%s", path, line, g, w, regenerate)
		}
	}
	for _, pattern := range []string{ConfigDir + "/*.yaml", ZoneDir + "/*.zone"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if _, ok := want[path]; !ok {
				t.Errorf("deploy/k8s/%s has no original in deploy/compose: %s", path, regenerate)
			}
		}
	}
}

func firstDifference(got, want []byte) (line int, g, w string, differ bool) {
	gl := strings.Split(string(got), "\n")
	wl := strings.Split(string(want), "\n")
	for i := 0; i < max(len(gl), len(wl)); i++ {
		g, w = "", ""
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w || i >= len(gl) || i >= len(wl) {
			return i + 1, g, w, true
		}
	}
	return 0, "", "", false
}

// TestCopiesDifferOnlyByChanges checks the text editing of applyChanges
// from the other side: apart from the keys of changes.go, an original
// and its copy hold the same data.
func TestCopiesDifferOnlyByChanges(t *testing.T) {
	for path, content := range copies(t) {
		name, ok := strings.CutPrefix(path, ConfigDir+"/")
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			original := readYAML(t, filepath.Join(ComposeDir, name))
			var copied map[string]any
			if err := yaml.Unmarshal(content, &copied); err != nil {
				t.Fatal(err)
			}
			for _, c := range changes {
				if c.value != nil || c.remove {
					deleteKey(original, c.key)
					deleteKey(copied, c.key)
				}
			}
			if !reflect.DeepEqual(original, copied) {
				t.Errorf("apart from changes.go, the copy differs from deploy/compose/%s\noriginal: %v\ncopy:     %v", name, original, copied)
			}
		})
	}
}

// TestNoComposeAddresses: an address of the compose network left in a
// copy reaches nothing in the cluster. It means that deploy/compose has
// a new address that changes.go does not replace yet. The EPC's
// subscribers are the one known exception: Pods cannot ask for the
// static addresses that the stub hands out.
func TestNoComposeAddresses(t *testing.T) {
	const network = "172.28.0."
	for path, content := range copies(t) {
		if strings.HasSuffix(path, ".zone") {
			if bytes.Contains(content, []byte(network)) {
				t.Errorf("deploy/k8s/%s has an address of the compose network", path)
			}
			continue
		}
		var doc map[string]any
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatal(err)
		}
		deleteKey(doc, "epc.subscribers")
		walk(doc, func(s string) {
			if strings.Contains(s, network) {
				t.Errorf("deploy/k8s/%s: %q is an address of the compose network; add a change for it to deploy/k8s/changes.go", path, s)
			}
		})
	}
}

// TestEveryRoleHasAWorkload: a new role in deploy/compose gets its copy
// from the generator, but its workload and Service in base/ are written
// by hand, after the pattern of the others.
func TestEveryRoleHasAWorkload(t *testing.T) {
	var kustomization struct {
		Resources          []string
		ConfigMapGenerator []struct {
			Name  string
			Files []string
		} `yaml:"configMapGenerator"`
	}
	if err := yaml.Unmarshal(readFile(t, "base/kustomization.yaml"), &kustomization); err != nil {
		t.Fatal(err)
	}
	for path := range copies(t) {
		name, ok := strings.CutPrefix(path, ConfigDir+"/")
		if !ok {
			continue
		}
		role := strings.TrimSuffix(name, ".yaml")
		if !slices.Contains(kustomization.Resources, name) {
			t.Errorf("base/kustomization.yaml lists no resource %s: write the workload and Service of the role %s in deploy/k8s/base/%s", name, role, name)
		}
		generated := false
		for _, g := range kustomization.ConfigMapGenerator {
			generated = generated || (g.Name == role+"-config" && slices.Contains(g.Files, "node.yaml=config/"+name))
		}
		if !generated {
			t.Errorf("base/kustomization.yaml has no ConfigMap %s-config with node.yaml=config/%s", role, name)
		}
	}
}

// TestEveryNameHasAService: each CNAME of the home zone, and each SRV
// target, needs a Service of that name in base/. An SRV target that is
// a CNAME breaks RFC 2782.
func TestEveryNameHasAService(t *testing.T) {
	services := map[string]bool{}
	manifests, err := filepath.Glob("base/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range manifests {
		decoder := yaml.NewDecoder(bytes.NewReader(readFile(t, path)))
		for {
			var object struct {
				Kind     string
				Metadata struct{ Name string }
			}
			if decoder.Decode(&object) != nil {
				break
			}
			if object.Kind == "Service" {
				services[object.Metadata.Name+serviceDomain] = true
			}
		}
	}
	zone := ZoneDir + "/" + homeZone
	records := parseZone(t, zone, copies(t)[zone])
	aliases := map[string]bool{}
	for _, rr := range records {
		if cname, ok := rr.(*dns.CNAME); ok {
			aliases[cname.Hdr.Name] = true
		}
	}
	for _, rr := range records {
		if cname, ok := rr.(*dns.CNAME); ok && !services[cname.Target] {
			t.Errorf("%s points to %s, but deploy/k8s/base has no Service of that name", cname.Hdr.Name, cname.Target)
		}
		if srv, ok := rr.(*dns.SRV); ok && (aliases[srv.Target] || !services[srv.Target]) {
			t.Errorf("the SRV record %s points to %s, want a Service of deploy/k8s/base", srv.Hdr.Name, srv.Target)
		}
		if a, ok := rr.(*dns.A); ok && a.Hdr.Name != nameServer+"."+strings.TrimSuffix(homeZone, "zone") {
			t.Errorf("%s keeps an A record", a.Hdr.Name)
		}
	}
}

func TestEveryZoneIsInTheConfigMap(t *testing.T) {
	kustomization := string(readFile(t, ZoneDir+"/kustomization.yaml"))
	for path := range copies(t) {
		name, ok := strings.CutPrefix(path, ZoneDir+"/")
		if ok && !strings.Contains(kustomization, "- "+name+"\n") {
			t.Errorf("dns/kustomization.yaml does not put %s into the ConfigMap ims-zones", name)
		}
	}
}

func TestApplyChanges(t *testing.T) {
	const src = `# Top.
node: x
a:
  # Old comment
  # of b.
  b: 172.28.0.1
  c: [172.28.0.2]
  d:
    e: 1
  items:
    - name: one
      url: http://172.28.0.3
list:
- 1
- 2
# Head of z.
z: last
`
	tests := []struct {
		name    string
		changes []change
		want    string
	}{
		{"value of a scalar", []change{{key: "a.b", value: "b.svc"}},
			"  b: b.svc\n"},
		{"comment replaced", []change{{key: "a.b", comment: "New."}},
			"a:\n  # New.\n  b: 172.28.0.1\n"},
		{"comment inserted with a value", []change{{key: "a.c", comment: "Two\nlines.", value: []string{"${IP}"}}},
			"  b: 172.28.0.1\n  # Two\n  # lines.\n  c:\n    - ${IP}\n  d:\n"},
		{"block mapping removed", []change{{key: "a.d", remove: true, comment: "No d."}},
			"  c: [172.28.0.2]\n  # No d.\n  items:\n"},
		{"key in an item", []change{{key: "a.items.0.url", value: "http://one.svc"}},
			"    - name: one\n      url: http://one.svc\nlist:\n"},
		{"compact sequence removed", []change{{key: "list", remove: true}},
			"      url: http://172.28.0.3\n# Head of z.\nz: last\n"},
		{"two changes", []change{{key: "a.b", value: "b.svc"}, {key: "z", comment: "Z."}},
			"  b: b.svc\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := applyChanges([]byte(src), tt.changes)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("got\n%s\nwant it to contain\n%s", out, tt.want)
			}
			if !strings.HasPrefix(string(out), "# Top.\nnode: x\n") {
				t.Errorf("the lines before the change moved:\n%s", out)
			}
		})
	}

	for _, c := range []change{
		{key: "a.nothing", value: "x"},
		{key: "a.items.0", value: "x"},
		{key: "a.items.0.name", comment: "The first key of an item"},
		{key: "a.b", remove: true, value: "x", comment: "x"},
	} {
		if _, err := applyChanges([]byte(src), []change{c, {key: "a.b", value: "y"}}); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

func TestToServices(t *testing.T) {
	const src = `; Old header.
$ORIGIN example.
@         IN NS  dns
_sip._udp IN SRV   10 50 5060 hss
_x._udp   IN SRV   10 50 5060 dns
dns       IN A     172.28.0.2
hss       IN A     172.28.0.10
`
	out, err := toServices([]byte(src), "192.0.2.53")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"_sip._udp IN SRV   10 50 5060 hss.ims.svc.cluster.local.\n",
		"_x._udp   IN SRV   10 50 5060 dns\n",
		"hss       IN CNAME hss.ims.svc.cluster.local.\n",
		"so this record comes last.\ndns       IN A 192.0.2.53\n",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("got\n%s\nwant it to contain\n%s", out, want)
		}
	}
	if !strings.HasSuffix(string(out), "dns       IN A 192.0.2.53\n") {
		t.Errorf("the name server is not the last record:\n%s", out)
	}
	if strings.Contains(string(out), "Old header") {
		t.Errorf("the old header stayed:\n%s", out)
	}
	for _, bad := range []string{
		"a.b IN A 172.28.0.9\ndns IN A 172.28.0.2\n",
		"hss IN A 172.28.0.10\n",
		"dns IN A 172.28.0.2\ndns IN A 172.28.0.3\n",
	} {
		if _, err := toServices([]byte(bad), "192.0.2.53"); err == nil {
			t.Errorf("no error for\n%s", bad)
		}
	}
}

// TestNameServerAddressIsReplaced plays what components/name-server asks
// kustomize to do with the zone, and checks that the zone then gives the
// name server the other address and keeps every other record.
func TestNameServerAddressIsReplaced(t *testing.T) {
	var component struct {
		Replacements []struct {
			Source struct {
				Name      string
				FieldPath string `yaml:"fieldPath"`
			}
			Targets []struct {
				Select struct {
					Kind string
					Name string
				}
				FieldPaths []string `yaml:"fieldPaths"`
				Options    struct {
					Delimiter string
					Index     int
				}
			}
		}
	}
	if err := yaml.Unmarshal(readFile(t, "components/name-server/kustomization.yaml"), &component); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range component.Replacements {
		for _, target := range r.Targets {
			if target.Select.Name != "ims-zones" {
				continue
			}
			found = true
			if r.Source.Name != EnvironmentConfigMap || r.Source.FieldPath != "data."+nameServerAddressKey {
				t.Errorf("the zone takes %s %s, want data.%s of %s", r.Source.Name, r.Source.FieldPath, nameServerAddressKey, EnvironmentConfigMap)
			}
			if want := []string{"data.[" + homeZone + "]"}; !slices.Equal(target.FieldPaths, want) {
				t.Errorf("the replacement edits %v, want %v", target.FieldPaths, want)
			}
			if target.Options.Delimiter != NameServerDelimiter || target.Options.Index != 1 {
				t.Errorf("the replacement splits at %q and replaces part %d, want %q and 1", target.Options.Delimiter, target.Options.Index, NameServerDelimiter)
			}
		}
	}
	if !found {
		t.Fatal("components/name-server does not replace the address in the zone")
	}

	path := ZoneDir + "/" + homeZone
	zone := string(copies(t)[path])
	parts := strings.Split(zone, NameServerDelimiter)
	if len(parts) != 2 {
		t.Fatalf("%s has %q %d times, want once", path, NameServerDelimiter, len(parts)-1)
	}
	kind, err := environmentValue(KindEnvironment, nameServerAddressKey)
	if err != nil {
		t.Fatal(err)
	}
	if parts[1] != kind+"\n" {
		t.Fatalf("%s ends with %q after %q, want only kind's address %s", path, parts[1], NameServerDelimiter, kind)
	}

	const other = "10.100.0.53"
	before := parseZone(t, path, []byte(zone))
	after := parseZone(t, path, []byte(parts[0]+NameServerDelimiter+other))
	if len(after) != len(before) {
		t.Fatalf("%d records after the replacement, want %d", len(after), len(before))
	}
	for i, rr := range after {
		if a, ok := rr.(*dns.A); ok && a.Hdr.Name == nameServer+"."+strings.TrimSuffix(homeZone, "zone") {
			if a.A.String() != other {
				t.Errorf("the name server has %s after the replacement, want %s", a.A, other)
			}
			continue
		}
		if rr.String() != before[i].String() {
			t.Errorf("the replacement changed %s into %s", before[i], rr)
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(readFile(t, path), &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return doc
}

// deleteKey removes a dotted key such as "hep.collector", with the
// index of an item of a sequence as in lookup, if present.
func deleteKey(v any, key string) {
	first, rest, nested := strings.Cut(key, ".")
	switch v := v.(type) {
	case map[string]any:
		if !nested {
			delete(v, key)
			return
		}
		deleteKey(v[first], rest)
	case []any:
		var i int
		if _, err := fmt.Sscan(first, &i); err == nil && nested && i >= 0 && i < len(v) {
			deleteKey(v[i], rest)
		}
	}
}

// walk calls f with every string in the document.
func walk(v any, f func(string)) {
	switch v := v.(type) {
	case string:
		f(v)
	case map[string]any:
		for _, x := range v {
			walk(x, f)
		}
	case []any:
		for _, x := range v {
			walk(x, f)
		}
	}
}

func parseZone(t *testing.T, name string, content []byte) []dns.RR {
	t.Helper()
	zp := dns.NewZoneParser(bytes.NewReader(content), "", name)
	var rrs []dns.RR
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		rrs = append(rrs, rr)
	}
	if err := zp.Err(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return rrs
}
