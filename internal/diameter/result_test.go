package diameter

import (
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

func TestResultSuccess(t *testing.T) {
	for _, c := range []struct {
		result Result
		want   bool
		text   string
	}{
		{Base(DiameterSuccess), true, "Result-Code 2001"},
		{Base(DiameterUnableToComply), false, "Result-Code 5012"},
		// 2001 of TS 29.229 6.2.1.1, DIAMETER_FIRST_REGISTRATION.
		{Experimental(2001), true, "Experimental-Result-Code 2001"},
		{Experimental(5001), false, "Experimental-Result-Code 5001"},
	} {
		if got := c.result.Success(); got != c.want {
			t.Errorf("%v.Success() = %v, want %v", c.result, got, c.want)
		}
		if got := c.result.String(); got != c.text {
			t.Errorf("String() = %q, want %q", got, c.text)
		}
	}
}

// TestResultRoundTrip writes each kind of result and reads it back, as a
// parse function of an application does.
func TestResultRoundTrip(t *testing.T) {
	for _, want := range []Result{Base(DiameterSuccess), Experimental(5001)} {
		m := diam.NewMessage(diam.DeviceWatchdog, 0, 0, 1, 1, dict.Default)
		want.AddTo(m)
		var raw ResultAVPs
		if err := m.Unmarshal(&raw); err != nil {
			t.Fatal(err)
		}
		got, err := raw.Result()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("read back %v, want %v", got, want)
		}
	}
	m := diam.NewMessage(diam.DeviceWatchdog, 0, 0, 1, 1, dict.Default)
	var raw ResultAVPs
	if err := m.Unmarshal(&raw); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Result(); err == nil {
		t.Error("an answer with no result was read as one")
	}
}
