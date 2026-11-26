package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCtlSendsTheRequestOfEachAction: each action of "ue ctl" is one
// request of the control API, with the body that the API takes.
func TestCtlSendsTheRequestOfEachAction(t *testing.T) {
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(body))
		if r.URL.Path == "/merge" {
			http.Error(w, `{"error":"there are not two answered calls on the line"}`, http.StatusConflict)
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer server.Close()
	control := strings.TrimPrefix(server.URL, "http://")
	for _, args := range [][]string{{"status"}, {"call", "tel:+12025550102"}, {"hold"}, {"swap"}, {"hangup", "abc"}, {"conference"}} {
		if err := runCtl(control, args, io.Discard); err != nil {
			t.Errorf("ue ctl %v: %v", args, err)
		}
	}
	var out bytes.Buffer
	if err := runCtl(control, []string{"merge"}, &out); err == nil || !strings.Contains(out.String(), "not two answered calls") {
		t.Errorf("ue ctl merge after a 409: %v, printed %q; want the error and the answer", err, out.String())
	}
	// Every POST goes as JSON, one without a body too: the API takes
	// no other.
	want := []string{"GET /status  ", `POST /call application/json {"target":"tel:+12025550102"}`, "POST /hold application/json ",
		"POST /swap application/json ", `POST /hangup application/json {"call_id":"abc"}`, "GET /conference  ", "POST /merge application/json "}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, bad := range [][]string{nil, {"dance"}, {"call"}, {"status", "x"}} {
		if err := runCtl(control, bad, io.Discard); err != errCtlUsage {
			t.Errorf("ue ctl %v: %v, want the usage", bad, err)
		}
	}
}
