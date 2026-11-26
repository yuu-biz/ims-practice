package lab

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeUE stands in for the control API of "ue serve". It records each
// request and answers what reply says.
type fakeUE struct {
	server   *httptest.Server
	requests chan fakeRequest
}

type fakeRequest struct{ method, path, body string }

func newFakeUE(t *testing.T, reply http.HandlerFunc) *fakeUE {
	t.Helper()
	f := &fakeUE{requests: make(chan fakeRequest, 10)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.requests <- fakeRequest{r.Method, r.URL.Path, string(body)}
		reply(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

const ueStatus = `{"impu":"sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org","registered":true,"call":null,"answer_policy":{"mode":"auto","ring_seconds":1}}`

func answerJSON(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

// unreachable is the URL of a server that has stopped.
func unreachable() string {
	s := httptest.NewServer(http.NotFoundHandler())
	s.Close()
	return s.URL
}

func TestUEListShowsEachUEOrWhyNot(t *testing.T) {
	up := newFakeUE(t, answerJSON(http.StatusOK, ueStatus+"\n"))
	broken := newFakeUE(t, answerJSON(http.StatusOK, "not json"))
	h := newHarness(t, Config{UEs: []UESimulator{
		{"phone1", up.server.URL},
		{"phone2", unreachable()},
		{"phone3", broken.server.URL},
	}})

	var list []struct {
		Name      string          `json:"name"`
		Reachable bool            `json:"reachable"`
		Status    json.RawMessage `json:"status"`
		Error     string          `json:"error"`
	}
	h.get("/api/ues", &list)
	if len(list) != 3 || list[0].Name != "phone1" || list[1].Name != "phone2" || list[2].Name != "phone3" {
		t.Fatalf("list = %+v, want phone1, phone2, phone3 in config order", list)
	}
	if !list[0].Reachable || string(list[0].Status) != ueStatus || list[0].Error != "" {
		t.Errorf("phone1 = %+v, want its status as the UE gave it", list[0])
	}
	if got := <-up.requests; got.method != http.MethodGet || got.path != "/status" {
		t.Errorf("phone1 was asked %s %s, want GET /status", got.method, got.path)
	}
	for _, e := range list[1:] {
		if e.Reachable || string(e.Status) != "null" || e.Error == "" {
			t.Errorf("%s = %+v, want unreachable with a reason", e.Name, e)
		}
	}
}

// TestUEListNamesAStatusTooLongToRead: a status longer than the Lab reads
// is cut, and the cut JSON is no JSON; the reason says it was too long,
// not that the UE answered something else.
func TestUEListNamesAStatusTooLongToRead(t *testing.T) {
	long := `{"impu":"` + strings.Repeat("a", 1<<16) + `"}`
	ue := newFakeUE(t, answerJSON(http.StatusOK, long))
	h := newHarness(t, Config{UEs: []UESimulator{{"phone1", ue.server.URL}}})
	var list []struct {
		Reachable bool   `json:"reachable"`
		Error     string `json:"error"`
	}
	h.get("/api/ues", &list)
	if len(list) != 1 || list[0].Reachable || !strings.Contains(list[0].Error, "longer than") {
		t.Errorf("list = %+v, want phone1 unreachable for a status longer than the Lab reads", list)
	}
}

func TestUEListWithoutUEsIsEmpty(t *testing.T) {
	h := newHarness(t, Config{})
	var list []any
	h.get("/api/ues", &list)
	if list == nil || len(list) != 0 {
		t.Errorf("list = %#v, want []", list)
	}
}

func TestUERequestsReachTheUE(t *testing.T) {
	ue := newFakeUE(t, answerJSON(http.StatusConflict, `{"error":"there is a call on the line already"}`))
	h := newHarness(t, Config{UEs: []UESimulator{{"phone1", ue.server.URL}}})

	tests := []struct{ method, path, body, wantPath string }{
		{http.MethodPost, "/api/ues/phone1/register", "", "/register"},
		{http.MethodPost, "/api/ues/phone1/deregister", "", "/deregister"},
		{http.MethodPost, "/api/ues/phone1/call", `{"target":"tel:+12025550102"}`, "/call"},
		{http.MethodPost, "/api/ues/phone1/answer", "", "/answer"},
		{http.MethodPost, "/api/ues/phone1/hangup", `{"reject_status":486}`, "/hangup"},
		{http.MethodPost, "/api/ues/phone1/hold", "", "/hold"},
		{http.MethodPost, "/api/ues/phone1/resume", "", "/resume"},
		{http.MethodPost, "/api/ues/phone1/dtmf", `{"digits":"12#"}`, "/dtmf"},
		{http.MethodPost, "/api/ues/phone1/swap", "", "/swap"},
		{http.MethodPost, "/api/ues/phone1/merge", "", "/merge"},
		{http.MethodPut, "/api/ues/phone1/answer-policy", `{"mode":"manual"}`, "/answer-policy"},
		{http.MethodPost, "/api/ues/phone1/sms", `{"to":"+12025550102","text":"Hello, Bob!","status_report":true}`, "/sms"},
		{http.MethodPut, "/api/ues/phone1/sms-memory", `{"full":true}`, "/sms-memory"},
		{http.MethodPost, "/api/ues/phone1/sms-memory/notify", "", "/sms-memory/notify"},
		{http.MethodPut, "/api/ues/phone1/sms-availability", `{"available":false}`, "/sms-availability"},
		{http.MethodGet, "/api/ues/phone1/xcap", "", "/xcap"},
		{http.MethodPut, "/api/ues/phone1/xcap/services/cfu", `{"active":true,"target":"tel:+12025550102"}`, "/xcap/services/cfu"},
		{http.MethodPut, "/api/ues/phone1/xcap/oir", `{"default_behaviour":"presentation-restricted"}`, "/xcap/oir"},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, h.server.URL+tt.path, strings.NewReader(tt.body))
		if tt.method != http.MethodGet {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "a call on the line") {
			t.Errorf("%s %s answered %d %s, want the UE's 409", tt.method, tt.path, resp.StatusCode, body)
		}
		if got := <-ue.requests; got != (fakeRequest{tt.method, tt.wantPath, tt.body}) {
			t.Errorf("%s %s reached the UE as %+v", tt.method, tt.path, got)
		}
	}
}

func TestUERequestsOutsideTheControlAPI(t *testing.T) {
	ue := newFakeUE(t, answerJSON(http.StatusOK, ueStatus))
	h := newHarness(t, Config{UEs: []UESimulator{{"phone1", ue.server.URL}, {"phone2", unreachable()}}})

	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/ues/ue-x/register", http.StatusNotFound},
		{http.MethodPut, "/api/ues/phone1/register", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/ues/phone1/status", http.StatusNotFound},
		{http.MethodGet, "/api/ues/phone1/sms", http.StatusNotFound},
		{http.MethodDelete, "/api/ues/phone1/xcap", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/ues/phone2/register", http.StatusBadGateway},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, h.server.URL+tt.path, nil)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.want)
		}
	}
	select {
	case got := <-ue.requests:
		t.Errorf("the UE got %+v, want nothing", got)
	default:
	}
}

// The UE sends an event and keeps the stream open; the browser must get
// that event before the stream ends.
func TestUEEventsStreamThrough(t *testing.T) {
	ue := newFakeUE(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: "+ueStatus+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	h := newHarness(t, Config{UEs: []UESimulator{{"phone1", ue.server.URL}}})

	resp, err := http.Get(h.server.URL + "/api/ues/phone1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- s
	}()
	select {
	case got := <-line:
		if got != "data: "+ueStatus+"\n" {
			t.Errorf("first line = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the event did not come through while the stream was open")
	}
	if got := <-ue.requests; got.method != http.MethodGet || got.path != "/events" {
		t.Errorf("the UE was asked %s %s, want GET /events", got.method, got.path)
	}
}
