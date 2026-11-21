package k8s

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// A kustomization or a component, as far as the tests read it.
type kustomization struct {
	Resources    []string
	Components   []string
	Patches      []struct{ Path, Patch string }
	Replacements []struct {
		Source struct {
			Name      string
			FieldPath string `yaml:"fieldPath"`
		}
	}
}

func readKustomization(t *testing.T, dir string) kustomization {
	t.Helper()
	var k kustomization
	if err := yaml.Unmarshal(readFile(t, filepath.Join(dir, "kustomization.yaml")), &k); err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	return k
}

// inputs follows the resources and the components of a kustomization,
// and returns the environments it lists and the keys of ims-environment
// that its replacements read.
func inputs(t *testing.T, dir string) (envs, keys []string) {
	t.Helper()
	k := readKustomization(t, dir)
	for _, r := range k.Replacements {
		if r.Source.Name == EnvironmentConfigMap {
			key, ok := strings.CutPrefix(r.Source.FieldPath, "data.")
			if !ok {
				t.Errorf("%s reads %s of %s, not a key of its data", dir, r.Source.FieldPath, EnvironmentConfigMap)
			}
			keys = append(keys, key)
		}
	}
	for _, next := range slices.Concat(k.Resources, k.Components) {
		path := filepath.Clean(filepath.Join(dir, next))
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		if filepath.Dir(path) == "environments" {
			envs = append(envs, path)
			continue
		}
		e, k := inputs(t, path)
		envs, keys = append(envs, e...), append(keys, k...)
	}
	return envs, keys
}

// appliedKustomizations are the kustomizations that a person applies
// with kubectl apply -k. check-overlays.sh finds them by the same rule.
func appliedKustomizations(t *testing.T) []string {
	t.Helper()
	applied := []string{"capture"}
	for _, pattern := range []string{"overlays/*/kustomization.yaml", "overlays/*/capture/kustomization.yaml"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			applied = append(applied, filepath.ToSlash(filepath.Dir(file)))
		}
	}
	return applied
}

// TestEveryOverlayHasItsValues: each kustomization that a person
// applies lists one environment, and that environment defines every
// value its components read. kustomize would stop on a missing one too,
// but only once someone builds that overlay.
func TestEveryOverlayHasItsValues(t *testing.T) {
	for _, dir := range appliedKustomizations(t) {
		envs, keys := inputs(t, dir)
		slices.Sort(envs)
		envs = slices.Compact(envs)
		if len(envs) != 1 {
			t.Errorf("%s lists the environments %v, want one", dir, envs)
			continue
		}
		values, err := environmentValues(envs[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			if _, ok := values[key]; !ok {
				t.Errorf("%s reads %s, which %s does not define", dir, key, envs[0])
			}
		}
	}

	// The load balancer variant is left out of overlays/cloud-azure until
	// someone takes it in.
	values, err := environmentValues("environments/azure")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"components/azure-load-balancer-sip", "components/azure-load-balancer-rtp"} {
		_, keys := inputs(t, dir)
		for _, key := range keys {
			if _, ok := values[key]; !ok {
				t.Errorf("%s reads %s, which environments/azure does not define", dir, key)
			}
		}
	}
}

// imsImagePrefix starts the names of the images of the IMS, which
// components/image-registry replaces with image_prefix.
const imsImagePrefix = "ims-practice/"

// TestImageRegistryReachesEveryIMSImage: a cloud node pulls every image
// of the IMS from the registry of image_prefix. kustomize stops on a
// field path that a target lacks, so components/image-registry names
// each workload with init containers; this fails when one is missed, or
// when a cloud overlay leaves the component out.
func TestImageRegistryReachesEveryIMSImage(t *testing.T) {
	for _, dir := range appliedKustomizations(t) {
		envs, keys := inputs(t, dir)
		if slices.Contains(envs, KindEnvironment) {
			continue
		}
		if !slices.Contains(keys, "image_prefix") {
			t.Errorf("%s pulls the images by their names on kind; add components/image-registry", dir)
		}
	}

	var component struct {
		Replacements []struct {
			Source struct {
				FieldPath string `yaml:"fieldPath"`
			}
			Targets []struct {
				Select     struct{ Kind, Name string }
				FieldPaths []string `yaml:"fieldPaths"`
			}
		}
	}
	if err := yaml.Unmarshal(readFile(t, "components/image-registry/kustomization.yaml"), &component); err != nil {
		t.Fatal(err)
	}
	type target struct{ kind, name, fieldPath string }
	var targets []target
	for _, r := range component.Replacements {
		if r.Source.FieldPath != "data.image_prefix" {
			continue
		}
		for _, tg := range r.Targets {
			for _, path := range tg.FieldPaths {
				targets = append(targets, target{tg.Select.Kind, tg.Select.Name, path})
			}
		}
	}

	files, err := filepath.Glob("base/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "components/edge/pcscf.yaml", "components/capture/capture.yaml")
	for _, file := range files {
		decoder := yaml.NewDecoder(bytes.NewReader(readFile(t, file)))
		for {
			var workload struct {
				Kind     string
				Metadata struct{ Name string }
				Spec     struct {
					Template struct {
						Spec struct {
							Containers     []struct{ Image string }
							InitContainers []struct{ Image string } `yaml:"initContainers"`
						}
					}
				}
			}
			if err := decoder.Decode(&workload); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			spec := workload.Spec.Template.Spec
			// components/edge/pcscf.yaml is a patch, which names no image
			// of the main container.
			needs := map[target]bool{}
			for _, c := range spec.Containers {
				if strings.HasPrefix(c.Image, imsImagePrefix) {
					needs[target{workload.Kind, "", "spec.template.spec.containers.*.image"}] = true
				}
			}
			for _, c := range spec.InitContainers {
				if strings.HasPrefix(c.Image, imsImagePrefix) {
					needs[target{workload.Kind, workload.Metadata.Name, "spec.template.spec.initContainers.*.image"}] = true
				}
			}
			for need := range needs {
				if !slices.Contains(targets, need) {
					t.Errorf("%s: the %s %s runs an image of %s that components/image-registry does not reach (want a target %+v)", file, workload.Kind, workload.Metadata.Name, imsImagePrefix, need)
				}
			}
		}
	}
}

// rtpPorts is the range of rtp_ports in the relay's configuration.
func rtpPorts(t *testing.T) (low, high int) {
	t.Helper()
	var config struct {
		AGW struct {
			RTPPorts struct{ Min, Max int } `yaml:"rtp_ports"`
		}
	}
	if err := yaml.Unmarshal(readFile(t, ConfigDir+"/agw.yaml"), &config); err != nil {
		t.Fatal(err)
	}
	return config.AGW.RTPPorts.Min, config.AGW.RTPPorts.Max
}

type port struct {
	Name          string
	Port          int
	ContainerPort int `yaml:"containerPort"`
	HostPort      int `yaml:"hostPort"`
	Protocol      string
}

// TestEdgePortsAreTheRelaysPorts: a hostPort, a Service port and a
// capture filter cannot take a range, so the RTP ports are written out;
// they must be those that the relay uses (rtp_ports of agw.yaml) and
// that deploy/cloud opens.
func TestEdgePortsAreTheRelaysPorts(t *testing.T) {
	low, high := rtpPorts(t)
	var want []int
	for p := low; p <= high; p++ {
		want = append(want, p)
	}

	var agw struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct{ Ports []port }
				}
			}
		}
	}
	if err := yaml.Unmarshal(readFile(t, "components/edge/agw.yaml"), &agw); err != nil {
		t.Fatal(err)
	}
	var hostPorts []int
	for _, c := range agw.Spec.Template.Spec.Containers {
		for _, p := range c.Ports {
			if p.HostPort != p.ContainerPort || p.Protocol != "UDP" {
				t.Errorf("components/edge/agw.yaml: %+v must be UDP on the same port of the node", p)
			}
			hostPorts = append(hostPorts, p.HostPort)
		}
	}
	if !slices.Equal(hostPorts, want) {
		t.Errorf("components/edge/agw.yaml has the hostPorts %v, want rtp_ports %d-%d", hostPorts, low, high)
	}

	var service struct{ Spec struct{ Ports []port } }
	if err := yaml.Unmarshal(readFile(t, "components/azure-load-balancer-rtp/service.yaml"), &service); err != nil {
		t.Fatal(err)
	}
	var servicePorts []int
	for _, p := range service.Spec.Ports {
		servicePorts = append(servicePorts, p.Port)
	}
	if !slices.Equal(servicePorts, want) {
		t.Errorf("components/azure-load-balancer-rtp/service.yaml has the ports %v, want rtp_ports %d-%d", servicePorts, low, high)
	}

	capture := string(readFile(t, "components/edge-capture/kustomization.yaml"))
	if filter := fmt.Sprintf("or port 5060 or udp portrange %d-%d\n", low, high); !strings.Contains(capture, filter) {
		t.Errorf("components/edge-capture does not add the filter %q", filter)
	}

	variables := readFile(t, "../cloud/modules/ims-network/variables.tf")
	if def := fmt.Sprintf("default  = { min = %d, max = %d }", low, high); !bytes.Contains(variables, []byte(def)) {
		t.Errorf("deploy/cloud/modules/ims-network/variables.tf does not have rtp_ports %q", def)
	}
}

// TestEdgePodsTolerateTheEdgeTaint: the label and the taint are those
// that deploy/cloud gives the edge nodes.
func TestEdgePodsTolerateTheEdgeTaint(t *testing.T) {
	outputs := string(readFile(t, "../cloud/modules/ims-network/outputs.tf"))
	taint := regexp.MustCompile(`(?s)output "edge_node_taint".*?value\s*=\s*\{ key = "([^"]+)", value = "([^"]+)", effect = "([^"]+)" \}`).FindStringSubmatch(outputs)
	label := regexp.MustCompile(`(?s)output "edge_node_label".*?value\s*=\s*\{ key = "([^"]+)", value = "([^"]+)" \}`).FindStringSubmatch(outputs)
	if taint == nil || label == nil {
		t.Fatal("deploy/cloud/modules/ims-network/outputs.tf has no edge_node_taint or edge_node_label in the form this test reads")
	}
	type toleration struct{ Key, Operator, Value, Effect string }
	want := toleration{taint[1], "Equal", taint[2], taint[3]}

	var capture kustomization
	if err := yaml.Unmarshal(readFile(t, "components/edge-capture/kustomization.yaml"), &capture); err != nil {
		t.Fatal(err)
	}
	patches := map[string][]byte{"components/edge-capture": []byte(capture.Patches[0].Patch)}
	for _, name := range []string{"pcscf.yaml", "agw.yaml"} {
		patches["components/edge/"+name] = readFile(t, "components/edge/"+name)
	}
	for name, patch := range patches {
		var workload struct {
			Spec struct {
				Template struct {
					Spec struct {
						NodeSelector map[string]string `yaml:"nodeSelector"`
						Tolerations  []toleration
					}
				}
			}
		}
		if err := yaml.Unmarshal(patch, &workload); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		spec := workload.Spec.Template.Spec
		if !slices.Contains(spec.Tolerations, want) {
			t.Errorf("%s tolerates %v, want %v", name, spec.Tolerations, want)
		}
		if name == "components/edge-capture" {
			continue
		}
		if spec.NodeSelector[label[1]] != label[2] || len(spec.NodeSelector) != 1 {
			t.Errorf("%s selects the nodes %v, want %s=%s", name, spec.NodeSelector, label[1], label[2])
		}
	}
}

// TestPCSCFConfigurationTakesHostAccess: the init container of
// components/edge writes host_access under the line "pcscf:" of the
// P-CSCF's file, which then must have no host_access of its own.
func TestPCSCFConfigurationTakesHostAccess(t *testing.T) {
	config := readFile(t, ConfigDir+"/pcscf.yaml")
	if !slices.Contains(strings.Split(string(config), "\n"), "pcscf:") {
		t.Errorf("%s/pcscf.yaml has no line \"pcscf:\"", ConfigDir)
	}
	var doc struct{ PCSCF map[string]any }
	if err := yaml.Unmarshal(config, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.PCSCF["host_access"]; ok {
		t.Errorf("%s/pcscf.yaml has a host_access already", ConfigDir)
	}
	if !strings.Contains(string(readFile(t, "components/edge/pcscf.yaml")), `$0 == "pcscf:" { print "  host_access:"`) {
		t.Error("components/edge/pcscf.yaml no longer inserts host_access after \"pcscf:\"")
	}
}
