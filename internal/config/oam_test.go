package config

import (
	"reflect"
	"strings"
	"testing"
)

const oamYAML = `
node: oam
roles: [oam]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
oam:
  http_listen: ":8080"
  hss_provisioning: http://hss:8081
  data_dir: /var/lib/ims/oam
  subnetwork: ims
  mns_root: http://oam:8080/3GPPManagement
  max_ntf_subscriptions: 4
  nodes:
    - {name: hss, roles: [hss], maintenance: "http://hss:8085"}
    - {name: pcscf-1, roles: [pcscf, agw], maintenance: "http://172.28.0.40:8085"}
`

func TestParseOAM(t *testing.T) {
	cfg, err := Parse(strings.NewReader(oamYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := OAM{
		HTTPListen:          ":8080",
		HSSProvisioning:     "http://hss:8081",
		DataDir:             "/var/lib/ims/oam",
		SubNetwork:          "ims",
		MnSRoot:             "http://oam:8080/3GPPManagement",
		MaxNtfSubscriptions: 4,
		Nodes: []OAMNode{
			{Name: "hss", Roles: []string{"hss"}, Maintenance: "http://hss:8085"},
			{Name: "pcscf-1", Roles: []string{"pcscf", "agw"}, Maintenance: "http://172.28.0.40:8085"},
		},
	}
	if cfg.OAM == nil || !reflect.DeepEqual(*cfg.OAM, want) || cfg.Diameter != nil {
		t.Errorf("oam = %+v, want %+v", cfg.OAM, want)
	}

	// Without the HSS, the directory and the 3GPP management services
	// the role still starts: it then operates the nodes and leaves the
	// subscriptions, the history, the backups and the northbound
	// unavailable.
	optional := strings.Replace(oamYAML, "  hss_provisioning: http://hss:8081\n  data_dir: /var/lib/ims/oam\n  subnetwork: ims\n  mns_root: http://oam:8080/3GPPManagement\n  max_ntf_subscriptions: 4\n", "", 1)
	cfg, err = Parse(strings.NewReader(optional))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAM.HSSProvisioning != "" || cfg.OAM.DataDir != "" || cfg.OAM.SubNetwork != "" || cfg.OAM.MnSRoot != "" || len(cfg.OAM.Nodes) != 2 {
		t.Errorf("oam = %+v without hss_provisioning and data_dir", cfg.OAM)
	}

	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"http_listen without port", `http_listen: ":8080"`, `http_listen: "oam"`, "http_listen"},
		{"http_listen with port 0", `http_listen: ":8080"`, `http_listen: ":0"`, "http_listen"},
		{"provisioning not a URL", "http://hss:8081", "hss:8081", "hss_provisioning"},
		{"node without a name", "name: hss, ", "", "nodes[0].name"},
		{"node name with a slash", "name: hss,", "name: hss/1,", "nodes[0].name"},
		{"node name used twice", "name: pcscf-1", "name: hss", `nodes[1].name: "hss" is used twice`},
		{"node without a role", "roles: [hss], ", "", "nodes[0].roles"},
		{"role name with a slash", "roles: [pcscf, agw]", "roles: [pcscf, a/gw]", "nodes[1].roles[1]"},
		{"maintenance not a URL", `maintenance: "http://hss:8085"`, "maintenance: hss:8085", "nodes[0].maintenance"},
		{"maintenance with no host", `maintenance: "http://hss:8085"`, `maintenance: "http://"`, "names no host"},
		{"maintenance over https", `maintenance: "http://hss:8085"`, `maintenance: "https://hss:8085"`, "not an http:// URL"},
		{"provisioning with a query", "http://hss:8081", `"http://hss:8081/?x=1"`, "more than a host"},
		{"subnetwork without mns_root", "  mns_root: http://oam:8080/3GPPManagement\n", "", "mns_root: required"},
		{"mns_root without subnetwork", "  subnetwork: ims\n", "", "subnetwork: required"},
		{"subnetwork with a comma", "subnetwork: ims", "subnetwork: ims,1", "subnetwork"},
		{"mns_root with a query", "/3GPPManagement", "/3GPPManagement?x=1", "mns_root"},
		{"mns_root over https", "http://oam:8080/3GPP", "https://oam:8080/3GPP", "mns_root"},
		{"a negative bound of subscriptions", "max_ntf_subscriptions: 4", "max_ntf_subscriptions: -1", "max_ntf_subscriptions"},
		{"section without its role", "roles: [oam]", "roles: [lab]\nlab: {hep_listen: \":9060\", http_listen: \":8080\"}", "oam: only the oam role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(oamYAML, tt.old, tt.new, 1)
			if yaml == oamYAML {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
	// An OAM with no node has nothing to operate.
	empty, _, _ := strings.Cut(oamYAML, "  nodes:")
	if _, err := Parse(strings.NewReader(empty)); err == nil || !strings.Contains(err.Error(), "nodes: at least one node is required") {
		t.Errorf("oam without nodes: err = %v", err)
	}
	noSection := "node: oam\nroles: [oam]\nhome_domain: ims.mnc001.mcc001.3gppnetwork.org\n"
	if _, err := Parse(strings.NewReader(noSection)); err == nil || !strings.Contains(err.Error(), "oam: required for the oam role") {
		t.Errorf("oam role without its section: err = %v", err)
	}
}
