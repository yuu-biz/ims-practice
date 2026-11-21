package ims

import (
	"testing"
	"time"
)

func TestRefreshAfter(t *testing.T) {
	if got := RefreshAfter(3600 * time.Second); got != 3000*time.Second {
		t.Errorf("3600 s: refresh after %v, want 3000 s", got)
	}
	if got := RefreshAfter(600 * time.Second); got != 300*time.Second {
		t.Errorf("600 s: refresh after %v, want 300 s", got)
	}
}
