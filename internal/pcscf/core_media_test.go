package pcscf

import (
	"bytes"
	"net"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/openapitest"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/sdp"
)

func mediaSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func socketAddr(c *net.UDPConn) netip.AddrPort { return c.LocalAddr().(*net.UDPAddr).AddrPort() }

func sendRTP(t *testing.T, from *net.UDPConn, to netip.AddrPort, payload []byte) {
	t.Helper()
	p := rtp.Packet{PayloadType: sdp.PCMU, Payload: payload}
	if _, err := from.WriteToUDPAddrPort(p.Marshal(), to); err != nil {
		t.Fatal(err)
	}
}

// TestCoreTerminationSendsToTheNewPartyOfTheNetwork: on the network
// side the party of a call can change while the call goes on, as when
// the MMTel AS plays a customized alerting tone from an MRFP and then
// gives the caller the SDP of the callee who answered. The core
// termination sends to the address of the new SDP, also after it has
// received from the MRFP, so the caller's voice reaches the callee.
func TestCoreTerminationSendsToTheNewPartyOfTheNetwork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		relay func(t *testing.T) (*agw.Relay, Relay)
	}{
		{"HTTP API", func(t *testing.T) (*agw.Relay, Relay) {
			minPort, maxPort := rtptest.PortRange(t, 4)
			relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, nil, quiet())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, c := range relay.Contexts() {
					relay.Subtract(c.ID)
				}
				relay.Close()
			})
			api := httptest.NewServer(openapitest.Load(t, "../agw/openapi.yaml").Handler(t, agw.Handler(relay)))
			t.Cleanup(api.Close)
			return relay, agw.NewClient(api.URL)
		}},
		{"H.248", func(t *testing.T) (*agw.Relay, Relay) {
			relay, controller := iqOverUDP(t)
			return relay, controller
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay, control := tc.relay(t)
			ue, mrfp, callee := mediaSocket(t), mediaSocket(t), mediaSocket(t)

			c := newRawCallThrough(t, only(control), socketAddr(mrfp), nil)
			c.ack()
			contexts := relay.Contexts()
			if len(contexts) != 1 {
				t.Fatalf("%d relay contexts, want 1", len(contexts))
			}
			access, core := contexts[0].Terminations[agw.Access].Local, contexts[0].Terminations[agw.Core].Local

			// The UE talks, and the MRFP plays the tone to it.
			sendRTP(t, ue, access, []byte("caller"))
			if got := rtptest.Next(t, mrfp, 2*time.Second, "the caller's voice at the MRFP"); !bytes.Equal(got.Payload, []byte("caller")) {
				t.Fatalf("the MRFP got %q", got.Payload)
			}
			sendRTP(t, mrfp, core, []byte("tone"))
			if got := rtptest.Next(t, ue, 2*time.Second, "the tone at the UE"); !bytes.Equal(got.Payload, []byte("tone")) {
				t.Fatalf("the UE got %q, want the tone", got.Payload)
			}

			// The callee answers, and the AS hands the caller its SDP.
			c.send(c.far, update(c.invite, true, c.ueContact, "1", socketAddr(callee)))
			<-c.toUE

			sendRTP(t, ue, access, []byte("hello"))
			if got := rtptest.Next(t, callee, 2*time.Second, "the caller's voice at the callee"); !bytes.Equal(got.Payload, []byte("hello")) {
				t.Errorf("the callee got %q, want the caller's voice", got.Payload)
			}
			sendRTP(t, callee, core, []byte("hi"))
			if got := rtptest.Next(t, ue, 2*time.Second, "the callee's voice at the UE"); !bytes.Equal(got.Payload, []byte("hi")) {
				t.Errorf("the UE got %q, want the callee's voice", got.Payload)
			}
		})
	}
}
