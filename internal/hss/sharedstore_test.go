package hss_test

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// TestAnInstanceMadeAgainOnTheStoreTakesOnTheSettings: the settings
// that an operator changed through one instance are in the store of the
// role, so an instance made in place of it, from a configuration file
// whose policy is all, answers on the policy in force from its start.
// The store is internal/kvstore/hssstore, on each backend of
// storetest.Backends.
func TestAnInstanceMadeAgainOnTheStoreTakesOnTheSettings(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			first := newHSSOn(t, kv, cfg, nil)
			t.Cleanup(first.Close)
			api, err := maintenance.New(maintenance.Config{Node: "hss", LogLevel: new(slog.LevelVar),
				Functions: []maintenance.Function{{Role: "hss"}}, Configuration: func() any { return struct{}{} },
				Settings: first.Settings()})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := api.ChangeConfiguration(maintenance.ConfigurationChangeRequest{
				Settings: map[string]json.RawMessage{"hss.admission": json.RawMessage(`"test_users"`)}}); err != nil {
				t.Fatal(err)
			}
			first.Close()

			again := newHSSOn(t, kv, cfg, nil)
			t.Cleanup(again.Close)
			if got := again.Admission(); got != hss.AdmitTestUsers {
				t.Errorf("the instance made again answers on %q, want test_users", got)
			}
		})
	}
}
