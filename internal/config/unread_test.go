package config

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hss"
)

// The node itself reads the maintenance section: it serves its metrics
// and its maintenance API there.
func TestUnreadIsEmptyForAConfigurationThatTheRolesRead(t *testing.T) {
	cfg, err := Parse(strings.NewReader(scscfYAML + "maintenance: {listen: 0.0.0.0:8085}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Unread(); len(got) != 0 {
		t.Errorf("Unread() = %v, want none", got)
	}
}

func TestUnreadNamesEverySectionThatNoRoleReads(t *testing.T) {
	yaml := strings.Replace(scscfYAML, "    host: scscf.ims.mnc001.mcc001.3gppnetwork.org\n",
		"    host: scscf-1.ims.mnc001.mcc001.3gppnetwork.org\n    pool: scscf.ims.mnc001.mcc001.3gppnetwork.org\n    transaction_timeout: 4s\n", 1)
	yaml = strings.Replace(yaml, "  max_expires: 600\n", "  max_expires: 600\n  restoration: true\n", 1)
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"scscf.sip.pool", "scscf.restoration"}
	if got := cfg.Unread(); !slices.Equal(got, want) {
		t.Errorf("Unread() = %v, want %v", got, want)
	}
}

// Each of these roles keeps its state in the store of the node, so the
// node reads the store section.
func TestUnreadKeepsTheStoreOfARoleThatReadsIt(t *testing.T) {
	for _, tt := range []struct {
		role Role
		cfg  Config
	}{
		{RoleIBCF, Config{IBCF: &IBCF{}}},
		{RoleSCSCF, Config{SCSCF: &SCSCF{}}},
		{RolePCSCF, Config{PCSCF: &PCSCF{}}},
		{RoleHSS, Config{HSS: &hss.Config{}}},
		{RoleIPSMGW, Config{IPSMGW: &IPSMGW{}}},
		{RoleMMTel, Config{MMTel: &MMTel{}}},
		{RoleMRFC, Config{MRFC: &MRFC{}}},
		{RoleAGW, Config{AGW: &AGW{}}},
		{RoleMRFP, Config{MRFP: &MRFP{}}},
	} {
		tt.cfg.Store = &Store{Kind: StoreValkey, Address: "valkey:6379"}
		if got := tt.cfg.Unread(); len(got) != 0 {
			t.Errorf("%s: Unread() = %v, want none", tt.role, got)
		}
	}
}

// The EPC stub keeps its state in the process, so a node of that role
// alone runs as it would without the store section.
func TestUnreadNamesTheStoreOfARoleThatDoesNotReadIt(t *testing.T) {
	cfg := Config{Store: &Store{Kind: StoreValkey, Address: "valkey:6379"}, EPC: &EPC{}}
	want := []string{"store"}
	if got := cfg.Unread(); !slices.Equal(got, want) {
		t.Errorf("Unread() = %v, want %v", got, want)
	}
}

// The IBCF reads the pool of each side, for its Record-Route.
func TestUnreadKeepsThePoolsOfTheIBCF(t *testing.T) {
	cfg := Config{IBCF: &IBCF{
		SIP:         SIP{Pool: "ibcf.ims.mnc001.mcc001.3gppnetwork.org"},
		ExternalSIP: SIP{Pool: "ibcf.nni.mnc001.example.net", TransactionTimeout: 4 * time.Second},
	}}
	if got := cfg.Unread(); len(got) != 0 {
		t.Errorf("Unread() = %v, want none", got)
	}
}
