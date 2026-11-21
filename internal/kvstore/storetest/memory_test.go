package storetest_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
)

// The suites run on the MemoryStores of the role packages too: what they
// check is the behaviour of a Store, not of the adapters. Two instances
// of a role share a MemoryStore by sharing the one value.

func TestSCSCFMemoryStore(t *testing.T) {
	storetest.SCSCF(t, func(*testing.T) (scscf.Store, scscf.Store) {
		s := scscf.NewMemoryStore()
		return s, s
	})
}

func TestPCSCFMemoryStore(t *testing.T) {
	storetest.PCSCF(t, func(*testing.T) (pcscf.Store, pcscf.Store) {
		s := pcscf.NewMemoryStore()
		return s, s
	})
}

func TestHSSMemoryStore(t *testing.T) {
	newStore := func(t *testing.T) hss.Store {
		s, err := hss.NewMemoryStore()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	storetest.HSS(t, func(t *testing.T) (hss.Store, hss.Store) {
		s := newStore(t)
		return s, s
	})
}

func TestEPCMemoryStore(t *testing.T) {
	storetest.EPC(t, func(*testing.T) (epc.Store, epc.Store) {
		s := epc.NewMemoryStore()
		return s, s
	})
}

func TestMMTelMemoryStore(t *testing.T) {
	storetest.MMTel(t, func(*testing.T) (mmtel.Store, mmtel.Store) {
		s := mmtel.NewMemoryStore()
		return s, s
	})
}

func TestSCMemoryStore(t *testing.T) {
	storetest.SC(t, func(*testing.T) (sc.Store, sc.Store) {
		s := sc.NewMemoryStore()
		return s, s
	})
}

func TestIPSMGWMemoryStore(t *testing.T) {
	storetest.IPSMGW(t, func(*testing.T) (ipsmgw.Store, ipsmgw.Store) {
		s := ipsmgw.NewMemoryStore()
		return s, s
	})
}
