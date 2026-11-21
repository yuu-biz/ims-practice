package mmtel_test

import (
	"reflect"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

func cw(notify string) string {
	return `<complete-communication-waiting>
  <communication-waiting active="true"/>
  <operator-communication-waiting authorized="true"><calling-user-receives-notification-his-call-is-waiting>` + notify +
		`</calling-user-receives-notification-his-call-is-waiting></operator-communication-waiting>
</complete-communication-waiting>`
}

// TestCommunicationWaiting is the terminal based CW (TS 24.615
// 4.5.5.2.3): alice's UE is in a call and rings with the call-waiting
// Alert-Info, which reaches the caller unless alice's subscription says
// that callers do not learn of it.
func TestCommunicationWaiting(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  string
		want []string
	}{
		"by default":     {"", []string{"<urn:alert:service:call-waiting>"}},
		"notified":       {cw("true"), []string{"<urn:alert:service:call-waiting>"}},
		"not notified":   {cw("false"), nil},
		"other services": {cw("false") + oip("true", ""), nil},
	} {
		t.Run(name, func(t *testing.T) {
			hss := &documents{docs: map[string]string{aliceURI: mmtelServices(tc.doc)}}
			as := startAS(t, hss, mmtel.Config{})
			next := newUA(t, "alice", func(c *sip.IncomingCall) {
				c.Progress(180, sip.Body{}, sip.HeaderField{Name: "Alert-Info", Value: "<urn:alert:service:call-waiting>"})
				c.Accept(answer)
			})
			caller := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
			out, err := caller.Invite(toServedUser(t, as, next, "reg"))
			if err != nil {
				t.Fatal(err)
			}
			resps := readAll(t, out)
			out.Bye()
			if !reflect.DeepEqual(statuses(resps), []int{180, 200}) {
				t.Fatalf("caller got %v, want 180 and 200", statuses(resps))
			}
			if got := resps[0].GetAll("Alert-Info"); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("180 has Alert-Info %q, want %q", got, tc.want)
			}
		})
	}
}
