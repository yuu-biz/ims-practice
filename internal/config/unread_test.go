package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUnreadIsEmptyForAConfigurationThatTheRolesRead(t *testing.T) {
	cfg, err := Parse(strings.NewReader(scscfYAML))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Unread(); len(got) != 0 {
		t.Errorf("Unread() = %v, want none", got)
	}
}

func TestUnreadNamesEverySectionThatNoRoleReads(t *testing.T) {
	yaml := scscfYAML + "store: {kind: valkey, address: valkey:6379}\nmaintenance: {listen: 0.0.0.0:9090}\n"
	yaml = strings.Replace(yaml, "    host: scscf.ims.mnc001.mcc001.3gppnetwork.org\n",
		"    host: scscf-1.ims.mnc001.mcc001.3gppnetwork.org\n    pool: scscf.ims.mnc001.mcc001.3gppnetwork.org\n    transaction_timeout: 4s\n", 1)
	yaml = strings.Replace(yaml, "  max_expires: 600\n", "  max_expires: 600\n  restoration: true\n", 1)
	cfg, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"store", "maintenance", "scscf.sip.pool", "scscf.sip.transaction_timeout", "scscf.restoration"}
	if got := cfg.Unread(); !slices.Equal(got, want) {
		t.Errorf("Unread() = %v, want %v", got, want)
	}
}

// The IBCF keeps the records of its calls in the store of the node.
func TestUnreadKeepsTheStoreOfAnIBCF(t *testing.T) {
	cfg := Config{Store: &Store{Kind: StoreValkey, Address: "valkey:6379"}, IBCF: &IBCF{}}
	if got := cfg.Unread(); len(got) != 0 {
		t.Errorf("Unread() = %v, want none", got)
	}
}

// The IBCF reads the pool of each side, for its Record-Route, but no role
// reads transaction_timeout.
func TestUnreadKeepsThePoolsOfTheIBCF(t *testing.T) {
	cfg := Config{IBCF: &IBCF{
		SIP:         SIP{Pool: "ibcf.ims.mnc001.mcc001.3gppnetwork.org"},
		ExternalSIP: SIP{Pool: "ibcf.nni.mnc001.example.net", TransactionTimeout: 4 * time.Second},
	}}
	want := []string{"ibcf.external_sip.transaction_timeout"}
	if got := cfg.Unread(); !slices.Equal(got, want) {
		t.Errorf("Unread() = %v, want %v", got, want)
	}
}
