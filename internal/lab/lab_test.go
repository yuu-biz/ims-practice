package lab

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hep"
	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

type node struct {
	name string
	addr netip.AddrPort
}

var (
	ue1   = node{"ue", netip.MustParseAddrPort("172.28.0.100:5060")}
	ue2   = node{"ue", netip.MustParseAddrPort("172.28.0.101:5060")}
	pcscf = node{"pcscf", netip.MustParseAddrPort("172.28.0.40:5060")}
	icscf = node{"icscf", netip.MustParseAddrPort("172.28.0.20:5060")}
	scscf = node{"scscf", netip.MustParseAddrPort("172.28.0.30:5060")}
	hss   = node{"hss", netip.MustParseAddrPort("172.28.0.10:3868")}
)

var start = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

// harness feeds HEP packets to a Lab and reads its API.
type harness struct {
	t *testing.T
	l *Lab
	// api is openapi.yaml, which every exchange of server is checked
	// against.
	api    *openapitest.Document
	server *httptest.Server
	clock  time.Time
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	if cfg.MaxMessages == 0 {
		cfg.MaxMessages = 1000
	}
	l, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	api := openapitest.Load(t, "openapi.yaml")
	server := httptest.NewServer(api.Handler(t, l.Handler()))
	t.Cleanup(server.Close)
	return &harness{t: t, l: l, api: api, server: server, clock: start}
}

// capture is one node's HEP copy of a message from src to dst.
func (h *harness) capture(captor node, src, dst node, proto hep.ProtocolType, correlationID, payload string) {
	h.clock = h.clock.Add(time.Millisecond)
	h.l.ingest(hep.Packet{
		IPProtocol:    hep.IPProtocolUDP,
		Source:        src.addr,
		Destination:   dst.addr,
		Time:          h.clock,
		ProtocolType:  proto,
		CorrelationID: correlationID,
		NodeName:      captor.name,
		Payload:       []byte(payload),
	}, captor.addr.Addr())
}

// hop is a SIP message going from one node to the next, captured by both.
func (h *harness) hop(from, to node, text string) {
	callID := header(text, "Call-ID")
	h.capture(from, from, to, hep.ProtocolTypeSIP, callID, text)
	h.capture(to, from, to, hep.ProtocolTypeSIP, callID, text)
}

func (h *harness) get(path string, v any) {
	h.t.Helper()
	resp, err := http.Get(h.server.URL + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("GET %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		h.t.Fatalf("GET %s: %v", path, err)
	}
}

func header(text, name string) string {
	for _, line := range strings.Split(text, "\r\n") {
		if v, ok := strings.CutPrefix(line, name+": "); ok {
			return v
		}
	}
	return ""
}

// sipText joins a start line, header rows and a body into a message as
// it goes on the wire.
func sipText(startLine, body string, rows ...string) string {
	return startLine + "\r\n" + strings.Join(rows, "\r\n") + "\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
}
