package pcscfstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.PCSCF(t, func(*testing.T) (pcscf.Store, pcscf.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return pcscfstore.New(kv, cfg), pcscfstore.New(kv, cfg)
			})
		})
	}
}

// TestIndexOfAPolicySessionGoesWithItsDialog: the index item that finds
// a dialog by its policy session lasts as long as the dialog, whether the
// dialog got its KeepUntil when it was put or by an update, so that no
// index item of a dialog that has gone stays for good.
func TestIndexOfAPolicySessionGoesWithItsDialog(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			s := pcscfstore.New(kv, cfg)
			keys := kvjson.New(kv, cfg, "pcscf")
			ctx := context.Background()

			put := pcscf.Dialog{Key: pcscf.DialogKey{CallID: "put@192.0.2.10", UETag: "ue"}, PolicySession: "pcscf;1;media",
				KeepUntil: time.Now().Add(100 * time.Millisecond)}
			if err := s.PutDialog(ctx, put); err != nil {
				t.Fatal(err)
			}
			updated := pcscf.Dialog{Key: pcscf.DialogKey{CallID: "updated@192.0.2.10", UETag: "ue"}, PolicySession: "pcscf;2;media"}
			if err := s.PutDialog(ctx, updated); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.UpdateDialog(ctx, updated.Key, func(d *pcscf.Dialog) {
				d.Released, d.KeepUntil = true, time.Now().Add(100*time.Millisecond)
			}); err != nil {
				t.Fatal(err)
			}

			for _, session := range []string{"pcscf;1;media", "pcscf;2;media"} {
				index := keys.Key("dialog-of-session", session)
				deadline := time.Now().Add(3 * time.Second)
				for {
					_, err := kv.Get(ctx, index)
					if errors.Is(err, kvstore.ErrNotFound) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if time.Now().After(deadline) {
						t.Fatalf("the index item %s is still there after its dialog has gone", index)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		})
	}
}
