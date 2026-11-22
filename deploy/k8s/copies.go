// Package k8s holds the Kubernetes manifests of the IMS. Its
// role configuration files and DNS zones are copies of those in
// deploy/compose, made by go generate, because kustomize does not read
// files outside its own directory. A copy differs from its original
// only by the table in changes.go.
package k8s

//go:generate go run ./gencopies

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/yuu-biz/ims-practice/internal/yamledit"
)

// The directories of the originals and the copies, relative to
// deploy/k8s, where go generate runs.
const (
	ComposeDir = "../compose"
	ConfigDir  = "base/config"
	ZoneDir    = "dns"
)

// KindEnvironment holds the values of the kind cluster that the
// manifests depend on (environments/kind/kustomization.yaml). The zone
// takes the name server's address from it.
const KindEnvironment = "environments/kind"

// EnvironmentConfigMap is the name of the ConfigMap of values that each
// environment defines and the components read.
const EnvironmentConfigMap = "ims-environment"

// GenerateCommand is what a person runs, from the root of the
// repository, after changing deploy/compose or changes.go.
const GenerateCommand = "go generate ./deploy/k8s/..."

// Copies returns every copy, by its path relative to deploy/k8s, made
// from the role files and the zones in composeDir.
func Copies(composeDir string) (map[string][]byte, error) {
	nameServerAddress, err := environmentValue(KindEnvironment, nameServerAddressKey)
	if err != nil {
		return nil, err
	}
	copies := map[string][]byte{}
	used := make([]bool, len(changes))

	roleFiles, err := filepath.Glob(filepath.Join(composeDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, path := range roleFiles {
		name := filepath.Base(path)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var mine []yamledit.Change
		for i, c := range changes {
			if c.File == name || (c.File == "" && yamledit.HasKey(src, c.Key)) {
				mine = append(mine, c)
				used[i] = true
			}
		}
		out, err := yamledit.Apply(src, mine)
		if err != nil {
			return nil, fmt.Errorf("deploy/compose/%s: %w", name, err)
		}
		copies[ConfigDir+"/"+name] = append(yamledit.Header("#", GenerateCommand, "deploy/compose/"+name, "deploy/k8s/changes.go"), out...)
	}
	for i, c := range changes {
		if !used[i] {
			return nil, fmt.Errorf("changes.go: %s %s matches no role file in deploy/compose", c.File, c.Key)
		}
	}

	zones, err := filepath.Glob(filepath.Join(composeDir, "dns", "*.zone"))
	if err != nil {
		return nil, err
	}
	for _, path := range zones {
		name := filepath.Base(path)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out := src
		if name == homeZone {
			if out, err = toServices(src, nameServerAddress); err != nil {
				return nil, fmt.Errorf("deploy/compose/dns/%s: %w", name, err)
			}
		}
		copies[ZoneDir+"/"+name] = append(yamledit.Header(";", GenerateCommand, "deploy/compose/dns/"+name, "deploy/k8s/changes.go"), out...)
	}
	return copies, nil
}

// aRecord is an A record as deploy/compose/dns writes them, with the
// columns lined up: "hss       IN A     172.28.0.10".
var aRecord = regexp.MustCompile(`^(\S+)(\s+IN\s+)A(\s+)(\S+)\s*$`)

// srvRecord is an SRV record as deploy/compose/dns writes them, with
// the target last: "_sip._udp IN SRV   10 50 5060 icscf".
var srvRecord = regexp.MustCompile(`^(\S+\s+IN\s+SRV\s+\d+\s+\d+\s+\d+\s+)(\S+)\s*$`)

// toServices makes the home network zone of Kubernetes from that of
// Docker Compose (changes.go).
func toServices(src []byte, nameServerAddress string) ([]byte, error) {
	lines := strings.SplitAfter(string(src), "\n")
	first := 0
	for first < len(lines) && strings.HasPrefix(lines[first], ";") {
		first++
	}
	// The names that become CNAMEs, which may come after the SRV records
	// that point to them.
	aliases := map[string]bool{}
	for _, line := range lines[first:] {
		if m := aRecord.FindStringSubmatch(strings.TrimRight(line, "\n")); m != nil && m[1] != nameServer {
			aliases[m[1]] = true
		}
	}
	out := yamledit.CommentLines("", ";", homeZoneHeader)
	var last []string
	for _, line := range lines[first:] {
		m := aRecord.FindStringSubmatch(strings.TrimRight(line, "\n"))
		switch {
		case m == nil:
			// The target of an SRV record must not be an alias (RFC 2782),
			// so it names the node's Service, which has the address.
			if srv := srvRecord.FindStringSubmatch(strings.TrimRight(line, "\n")); srv != nil && aliases[srv[2]] {
				line = srv[1] + srv[2] + serviceDomain + "\n"
			}
			out = append(out, line)
		case m[1] == nameServer:
			if last != nil {
				return nil, fmt.Errorf("%q has two A records", m[1])
			}
			// One space before and after the type, as components/name-server
			// writes it with the address of another environment.
			last = append(yamledit.CommentLines("", ";", nameServerComment),
				strings.TrimRight(m[1]+m[2], " ")+" A "+nameServerAddress+"\n")
		case strings.Contains(m[1], ".") || m[1] == "@":
			return nil, fmt.Errorf("%q: only the A record of a single label can become a CNAME to a Service", m[1])
		default:
			// The target starts in the column of the address.
			pad := strings.Repeat(" ", max(1, len("A")+len(m[3])-len("CNAME")))
			out = append(out, m[1]+m[2]+"CNAME"+pad+m[1]+serviceDomain+"\n")
		}
	}
	if last == nil {
		return nil, fmt.Errorf("no A record for the name server %q", nameServer)
	}
	out = append(out, "\n")
	out = append(out, last...)
	return []byte(strings.Join(out, "")), nil
}

// environmentValues reads the ConfigMap ims-environment from the
// kustomization of an environment (environments/<name>): its literals,
// and the files of its envs. A cloud environment keeps its values in
// such a file, values.local.env, which Git ignores because the values
// are the person's own (.gitignore). For it this reads the committed
// example next to it, values.local.env.example, so that go generate and
// the tests give the same result on every machine.
func environmentValues(dir string) (map[string]string, error) {
	path := filepath.Join(dir, "kustomization.yaml")
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k struct {
		ConfigMapGenerator []struct {
			Name     string
			Literals []string
			Envs     []string
		} `yaml:"configMapGenerator"`
	}
	if err := yaml.Unmarshal(src, &k); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, g := range k.ConfigMapGenerator {
		if g.Name != EnvironmentConfigMap {
			continue
		}
		values := map[string]string{}
		for _, l := range g.Literals {
			key, value, ok := strings.Cut(l, "=")
			if !ok {
				return nil, fmt.Errorf("%s: %q is not key=value", path, l)
			}
			values[key] = value
		}
		for _, env := range g.Envs {
			if err := readEnvFile(filepath.Join(dir, env+ExampleSuffix), values); err != nil {
				return nil, err
			}
		}
		return values, nil
	}
	return nil, fmt.Errorf("%s: no ConfigMap %s", path, EnvironmentConfigMap)
}

// ExampleSuffix ends the name of the committed example of a file that
// Git ignores, such as environments/aws/values.local.env.example.
const ExampleSuffix = ".example"

// readEnvFile adds the values of an env file of kustomize to values. As
// kustomize reads it, a line is KEY=value, and a line that starts with
// "#" after any spaces is a comment; a "#" after a value belongs to the
// value.
func readEnvFile(path string, values map[string]string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(src), "\n") {
		line = strings.TrimLeft(strings.TrimSuffix(line, "\r"), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: %q is not KEY=value", path, i+1, line)
		}
		// kustomize refuses a key that two sources define.
		if _, defined := values[key]; defined {
			return fmt.Errorf("%s:%d: %s is defined twice", path, i+1, key)
		}
		values[key] = value
	}
	return nil
}

// environmentValue is one value of environmentValues.
func environmentValue(dir, key string) (string, error) {
	values, err := environmentValues(dir)
	if err != nil {
		return "", err
	}
	v, ok := values[key]
	if !ok {
		return "", fmt.Errorf("%s: the ConfigMap %s has no %s", dir, EnvironmentConfigMap, key)
	}
	return v, nil
}
