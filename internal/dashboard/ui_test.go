package dashboard

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServesTheWebUI(t *testing.T) {
	h := newHarness(t, Config{})
	resp, err := http.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(body), "<title>IMS dashboard</title>") {
		t.Errorf("GET /: %s %q\n%s", resp.Status, resp.Header.Get("Content-Type"), body)
	}

	resp, err = http.Get(h.server.URL + "/missing.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /missing.js: %s", resp.Status)
	}
}
