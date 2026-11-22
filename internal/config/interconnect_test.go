package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The files of deploy/interconnect, the two interconnected networks.

// defaultProfiles are the profiles that deploy/interconnect/compose.yaml
// mounts as peer-profile.yaml next to each IBCF's file when neither
// IBCF_A_PROFILE nor IBCF_B_PROFILE names another.
var defaultProfiles = map[string]string{
	"a/ibcf.yaml": "network-b.yaml",
	"b/ibcf.yaml": "network-a.yaml",
}

func TestInterconnectConfigsLoad(t *testing.T) {
	files, err := filepath.Glob("../../deploy/interconnect/*/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var nodes int
	for _, f := range files {
		dir := filepath.Base(filepath.Dir(f))
		if dir == "profiles" {
			continue
		}
		nodes++
		name := dir + "/" + filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			path := f
			if profile, ok := defaultProfiles[name]; ok {
				path = withProfile(t, f, filepath.Join("../../deploy/interconnect/profiles", profile))
			}
			if _, err := Load(path); err != nil {
				t.Error(err)
			}
		})
	}
	if nodes == 0 {
		t.Fatal("no node files in deploy/interconnect")
	}
}

// withProfile copies the node's file into a directory of its own, with
// profile as its peer-profile.yaml, as Compose mounts them.
func withProfile(t *testing.T, node, profile string) string {
	t.Helper()
	dir := t.TempDir()
	for src, dst := range map[string]string{node: "node.yaml", profile: "peer-profile.yaml"} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dst), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "node.yaml")
}

// TestInterconnectProfilesLoad: every agreement in profiles/, those of
// the scenarios with mismatched profiles and the TTC example among them, is a valid
// profile.
func TestInterconnectProfilesLoad(t *testing.T) {
	files, err := filepath.Glob("../../deploy/interconnect/profiles/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("profiles: %v", files)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			if _, err := LoadProfile(f); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestInterconnectHomeIBCFIsInTheHomeDomain: the S-CSCF and the IP-SM-GW
// of network A name the IBCF of network A. They are generated from
// deploy/compose (deploy/interconnect/changes.go), which does not know
// the domain that their own file sets.
func TestInterconnectHomeIBCFIsInTheHomeDomain(t *testing.T) {
	for _, name := range []string{"scscf.yaml", "ipsmgw.yaml"} {
		t.Run(name, func(t *testing.T) {
			home, err := Load(filepath.Join("../../deploy/interconnect/a", name))
			if err != nil {
				t.Fatal(err)
			}
			var ibcf sip.URI
			switch {
			case home.SCSCF != nil:
				ibcf = home.SCSCF.IBCF
			case home.IPSMGW != nil:
				ibcf = home.IPSMGW.IBCF
			}
			if ibcf.Host != "ibcf."+home.HomeDomain || ibcf.Port != 5060 {
				t.Errorf("IBCF = %+v, want ibcf.%s:5060", ibcf, home.HomeDomain)
			}
		})
	}
}
