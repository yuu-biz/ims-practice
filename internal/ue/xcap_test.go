package ue_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/ue"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

// The tests of this file run Ut end to end: the HSS stub with the
// subscribers and the first service documents of deploy/compose, the
// XCAP server of an MMTel AS reading them over Sh, and the UE's XCAP
// client.

const (
	utIMPI = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	utIMPU = "sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org"
)

// utNetwork starts the HSS of deploy/compose/hss.yaml and the XCAP
// server, and returns the AS's Sh client, the UE's services and the
// registry in which the AS counts the Sh messages it sends and
// receives.
func utNetwork(t *testing.T) (*sh.Client, *ue.SupplementaryServices, *metrics.Registry) {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("../../deploy/compose/hss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store, err := hss.NewMemoryStore(cfg.HSS.Subscriptions...)
	if err != nil {
		t.Fatal(err)
	}
	realm := cfg.HomeDomain
	timers := diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, timers, quiet())
	h, err := hss.New(*cfg.HSS, store, hssNode, quiet())
	if err != nil {
		t.Fatal(err)
	}
	hssNode.Handle(h.Handle)
	listener, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(listener.Stop)
	asNode := diameter.NewNode(diameter.Identity{OriginHost: "mmtel." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDSh}, timers, quiet())
	peer := asNode.Connect(diameter.PeerConfig{Address: listener.Addr(), OriginHost: "hss." + realm})
	t.Cleanup(peer.Stop)
	// The AS reaches the HSS through a peer group, as it does in Compose,
	// and the group counts the Sh messages of the role mmtel in the
	// registry of the node.
	reg := metrics.NewRegistry()
	hssFarm, err := diameter.NewPeerGroup(reg, "mmtel", peer)
	if err != nil {
		t.Fatal(err)
	}
	users := sh.NewClient(asNode, hssFarm, realm)
	// The connection opens in the background. Waiting for it here rather
	// than with a request of the client leaves the counters of the group
	// holding what the AS sent for the UE and nothing else.
	for deadline := time.Now().Add(5 * time.Second); !peer.Available(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the Sh connection to the HSS never opened")
		}
	}
	server := httptest.NewServer(xcap.NewHandler(users, "", realm, []byte("test-nonce-key-of-the-xcap-servers"), quiet()))
	t.Cleanup(server.Close)
	client := &xcap.Client{Root: server.URL, XUI: utIMPU, Username: utIMPI, Password: "test-password-1", UserAgent: "ims-practice-ue"}
	return users, ue.NewSupplementaryServices(client), reg
}

func setting(t *testing.T, s ue.Settings, name ue.Service) ue.ServiceSetting {
	t.Helper()
	for _, x := range s.Services {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("no setting for %s in %+v", name, s.Services)
	return ue.ServiceSetting{}
}

func storedServices(t *testing.T, users *sh.Client) simservs.MMTelServices {
	t.Helper()
	d, found, err := users.RepositoryData(context.Background(), utIMPU, simservs.ServiceIndication)
	if err != nil || !found {
		t.Fatalf("UDR: %v, %v", found, err)
	}
	m, err := simservs.ParseMMTelServices(d.ServiceData.XML)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSupplementaryServicesOverUt(t *testing.T) {
	users, services, _ := utNetwork(t)
	ctx := context.Background()

	settings, err := services.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.Services) != 11 || settings.OIRDefault != simservs.PresentationNotRestricted || settings.ETag != `"0"` {
		t.Errorf("first settings: %+v", settings)
	}
	for _, s := range settings.Services {
		if !s.Provisioned || s.Active || s.Target != "" {
			t.Errorf("first setting of %s: %+v", s.Name, s)
		}
	}

	settings, err = services.SetService(ctx, ue.CFU, true, "tel:+12025550102")
	if err != nil {
		t.Fatal(err)
	}
	if got := setting(t, settings, ue.CFU); !got.Active || got.Target != "tel:+12025550102" {
		t.Errorf("CFU after activation: %+v", got)
	}
	m := storedServices(t, users)
	call := simservs.CallInfo{CallingIdentity: "tel:+12025550103"}
	if r := simservs.EvaluateDiversion(&m.CompleteCommunicationDiversion.CommunicationDiversion, simservs.AtCommunicationSetup, call); !r.Diverts() || r.ForwardTo.Target != "tel:+12025550102" {
		t.Errorf("the AS diverts %+v", r)
	}

	// Deactivating keeps the registered target (GSMA IR.92 2.3.2).
	settings, err = services.SetService(ctx, ue.CFU, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := setting(t, settings, ue.CFU); got.Active || got.Target != "tel:+12025550102" {
		t.Errorf("CFU after deactivation: %+v", got)
	}

	if _, err := services.SetService(ctx, ue.BAOC, true, ""); err != nil {
		t.Fatal(err)
	}
	m = storedServices(t, users)
	if r := simservs.EvaluateOutgoingBarring(&m.CompleteCommunicationBarring.OutgoingCommunicationBarring, simservs.CallInfo{CalledIdentity: "tel:+12025550102"}); r.Allow {
		t.Errorf("BAOC active, but the AS lets the call out: %+v", r)
	}

	settings, err = services.SetOIRDefault(ctx, simservs.PresentationRestricted)
	if err != nil {
		t.Fatal(err)
	}
	if settings.OIRDefault != simservs.PresentationRestricted {
		t.Errorf("OIR default %q", settings.OIRDefault)
	}
	if _, err := services.SetService(ctx, ue.CFNR, true, ""); !errors.Is(err, ue.ErrInvalidSetting) {
		t.Errorf("CFNR without a target: %v", err)
	}
}

// TestNoReplyTimerOverUt: the UE shortens the time a call may ring
// before CFNR diverts it, and the AS reads the new time from the HSS
// (TS 24.604 4.9.1.1A). A time outside 5 to 180 s is not asked for.
func TestNoReplyTimerOverUt(t *testing.T) {
	users, services, _ := utNetwork(t)
	ctx := context.Background()
	settings, err := services.Settings(ctx)
	if err != nil || settings.NoReplyTimer != 20 {
		t.Fatalf("first settings have the no reply timer %d (%v), want the 20 s of hss.yaml", settings.NoReplyTimer, err)
	}
	settings, err = services.SetNoReplyTimer(ctx, 5)
	if err != nil || settings.NoReplyTimer != 5 || settings.ETag != `"1"` {
		t.Fatalf("after the PUT: %+v, %v; want 5 s in version 1", settings, err)
	}
	if got := storedServices(t, users).CompleteCommunicationDiversion.NoReplyTimer(); got != 5 {
		t.Errorf("the AS reads a no reply timer of %d s, want 5", got)
	}
	for _, seconds := range []int{4, 181} {
		if _, err := services.SetNoReplyTimer(ctx, seconds); !errors.Is(err, ue.ErrInvalidSetting) {
			t.Errorf("%d s: %v, want ErrInvalidSetting", seconds, err)
		}
	}
}

func TestSupplementaryServicesControlAPI(t *testing.T) {
	users, services, _ := utNetwork(t)
	api := httptest.NewServer(services.Handler())
	defer api.Close()
	put := func(path, body string) (*http.Response, ue.Settings) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, api.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var s ue.Settings
		json.NewDecoder(resp.Body).Decode(&s)
		return resp, s
	}

	resp, settings := put("/xcap/services/cfb", `{"active": true, "target": "tel:+12025550103"}`)
	if resp.StatusCode != http.StatusOK || !setting(t, settings, ue.CFB).Active {
		t.Errorf("PUT cfb: %d %+v", resp.StatusCode, settings)
	}
	m := storedServices(t, users)
	if r := simservs.EvaluateDiversion(&m.CompleteCommunicationDiversion.CommunicationDiversion, simservs.OnBusy, simservs.CallInfo{}); !r.Diverts() || r.ForwardTo.Target != "tel:+12025550103" {
		t.Errorf("busy: the AS diverts %+v", r)
	}
	for path, body := range map[string]string{
		"/xcap/services/cfu":   `{"active": true}`,
		"/xcap/services/cfx":   `{"active": true, "target": "tel:+12025550103"}`,
		"/xcap/services/cfnl":  `{"active": true, "target": "+12025550103"}`,
		"/xcap/oir":            `{"default_behaviour": "hidden"}`,
		"/xcap/services/baic/": `{"active": true}`,
	} {
		if resp, _ := put(path, body); resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("PUT %s %s: %d", path, body, resp.StatusCode)
		}
	}
	plain, _ := http.NewRequest(http.MethodPut, api.URL+"/xcap/oir", strings.NewReader(`{"default_behaviour": "presentation-restricted"}`))
	plain.Header.Set("Content-Type", "text/plain")
	if resp, err := http.DefaultClient.Do(plain); err != nil || resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("PUT oir as text/plain: %v, %v; want 415", resp, err)
	} else {
		resp.Body.Close()
	}
	resp, settings = put("/xcap/oir", `{"default_behaviour": "presentation-restricted"}`)
	if resp.StatusCode != http.StatusOK || settings.OIRDefault != simservs.PresentationRestricted {
		t.Errorf("PUT oir: %d %+v", resp.StatusCode, settings)
	}
	get, err := http.Get(api.URL + "/xcap")
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	var s ue.Settings
	if err := json.NewDecoder(get.Body).Decode(&s); err != nil || get.StatusCode != http.StatusOK || !strings.Contains(s.Document, "<simservs") {
		t.Errorf("GET /xcap: %d %+v %v", get.StatusCode, s, err)
	}
}
