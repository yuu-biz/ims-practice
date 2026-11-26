package config

import (
	"slices"
	"strings"
	"testing"

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
		{RoleSC, Config{SC: &SC{}}},
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
