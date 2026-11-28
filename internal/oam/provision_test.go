package oam

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// Test data only: IMSIs of the test PLMN 001/01, fictional NANP numbers,
// the K and OPc of MILENAGE test set 1 (TS 35.208).
const (
	testK   = "465b5ce8b199b49faa5f0a2ee238a6bc"
	testOPc = "cd63cb71954a9f4e48a5994e37a02baf"
)

func impiOf(n int) string { return fmt.Sprintf("00101%010d@%s", n, testRealm) }

func digestLine(n int, password string) string {
	return fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:00101%010d@%s"], "tel": "tel:+1202555%04d", "password": %q}`,
		impiOf(n), n, testRealm, 100+n%100, password)
}

func akaLine(n int) string {
	return fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:00101%010d@%s"], "aka": {"k": %q, "opc": %q, "amf": "0000", "sqn": "000000000000"}}`,
		impiOf(n), n, testRealm, testK, testOPc)
}

func newProvisioningServer(t *testing.T, hss *fakeHSS, c Config) *testServer {
	t.Helper()
	c.HSSProvisioning = hss.server.URL
	return newTestServer(t, c, newFakeNode(t, "hss-1", []string{"hss"}))
}

// bulk sends a file to the bulk provisioning and reads the stream back.
func (ts *testServer) bulk(t *testing.T, query, file string) ([]Outcome, Summary) {
	t.Helper()
	resp, err := ts.server.Client().Post(ts.server.URL+"/oam/v1/provisioning"+query, "application/x-ndjson", strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("bulk: %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	return readStream(t, resp.Body)
}

func readStream(t *testing.T, r io.Reader) ([]Outcome, Summary) {
	t.Helper()
	var outcomes []Outcome
	var summary *Summary
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var l StreamLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("%s: %v", sc.Bytes(), err)
		}
		if err := maintenance.CheckRedacted(l); err != nil {
			t.Errorf("a report line holds a secret: %v", err)
		}
		switch {
		case l.Error != "":
			t.Errorf("the stream stopped: %s", l.Error)
		case l.Outcome != nil:
			outcomes = append(outcomes, *l.Outcome)
		case l.Summary != nil:
			summary = l.Summary
		}
	}
	if summary == nil {
		t.Fatal("no summary")
	}
	return outcomes, *summary
}

func results(os []Outcome) string {
	var s []string
	for _, o := range os {
		s = append(s, fmt.Sprintf("%d:%s", o.Line, o.Result))
	}
	return strings.Join(s, " ")
}

func TestBulkProvisioningIsIdempotent(t *testing.T) {
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{})
	file := strings.Join([]string{digestLine(1, "test-password-1"), digestLine(2, "test-password-2"), akaLine(10)}, "\n") + "\n"

	outcomes, summary := ts.bulk(t, "", file)
	if got := results(outcomes); got != "1:created 2:created 3:created" || summary.Created != 3 || summary.Lines != 3 {
		t.Fatalf("first run: %s %+v", got, summary)
	}
	if outcomes[2].PrivateIdentity != impiOf(10) {
		t.Errorf("outcome 3: %+v", outcomes[2])
	}
	if hss.count() != 3 {
		t.Fatalf("the HSS has %d subscriptions", hss.count())
	}

	puts := hss.puts.Load()
	outcomes, summary = ts.bulk(t, "", file)
	if got := results(outcomes); got != "1:unchanged 2:unchanged 3:unchanged" || summary.Unchanged != 3 {
		t.Errorf("the same file again: %s %+v", got, summary)
	}
	if hss.puts.Load() != puts+3 {
		t.Errorf("the second run sent %d records", hss.puts.Load()-puts)
	}

	// A changed password is an update; the AKA line with its SQN_HE of 0
	// stays unchanged, as the HSS keeps the SQN_HE that it has.
	changed := strings.Replace(file, "test-password-2", "test-password-2b", 1)
	outcomes, _ = ts.bulk(t, "", changed)
	if got := results(outcomes); got != "1:unchanged 2:updated 3:unchanged" {
		t.Errorf("one changed record: %s", got)
	}

	deletion := fmt.Sprintf(`{"private_identity": %q, "delete": true}`, impiOf(1))
	outcomes, summary = ts.bulk(t, "", deletion)
	if got := results(outcomes); got != "1:deleted" || summary.Deleted != 1 || hss.count() != 2 {
		t.Errorf("delete: %s %+v, %d left", got, summary, hss.count())
	}
	outcomes, _ = ts.bulk(t, "", deletion)
	if got := results(outcomes); got != "1:unchanged" || outcomes[0].Reason != "no such subscription" {
		t.Errorf("delete again: %+v", outcomes)
	}
}

func TestBulkDryRunChangesNothing(t *testing.T) {
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{})
	file := digestLine(1, "test-password-1") + "\n" + digestLine(2, "test-password-2") + "\n"

	outcomes, summary := ts.bulk(t, "?dry_run=true", file)
	if got := results(outcomes); got != "1:created 2:created" || !summary.DryRun || !outcomes[0].DryRun {
		t.Errorf("dry run: %s %+v", got, summary)
	}
	if hss.count() != 0 {
		t.Fatalf("a dry run stored %d subscriptions", hss.count())
	}

	ts.bulk(t, "", file)
	changed := strings.Replace(file, "test-password-1", "test-password-1b", 1)
	changed += fmt.Sprintf(`{"private_identity": %q, "delete": true}`, impiOf(2))
	outcomes, _ = ts.bulk(t, "?dry_run=true", changed)
	if got := results(outcomes); got != "1:updated 2:unchanged 3:deleted" {
		t.Errorf("dry run over stored data: %s", got)
	}
	if s, _ := hss.stored(impiOf(1)); s.Password != "test-password-1" || hss.count() != 2 {
		t.Error("the dry run changed the HSS")
	}
	if status, body := ts.call(t, "POST", "/oam/v1/provisioning?dry_run=maybe", "{}"); status != http.StatusBadRequest {
		t.Errorf("dry_run=maybe: %d %s", status, body)
	}
}

func TestBulkReportsEachBadLineAndGoesOn(t *testing.T) {
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{})
	// Stored before, so that line 10 meets it whatever the order in
	// which the records reach the HSS.
	ts.bulk(t, "", digestLine(1, "test-password-1"))
	taken := fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:00101%010d@%s"], "password": "test-password-9"}`, impiOf(9), 1, testRealm)
	lines := []string{
		digestLine(1, "test-password-1"),
		"",
		"not json",
		`{"private_identity": "` + impiOf(3) + `", "public_identities": ["sip:x@` + testRealm + `"], "password": "p", "imsi": "001010000000003"}`,
		`{"private_identity": "` + impiOf(4) + `", "public_identities": ["tel:+12025551234"], "password": "p"}`,
		`{"private_identity": "310150123456789@` + testRealm + `", "public_identities": ["sip:310150123456789@` + testRealm + `"], "password": "p"}`,
		`{"private_identity": "` + impiOf(5) + `", "public_identities": ["sip:00101` + "0000000005@" + testRealm + `"]}`,
		`{"private_identity": "` + impiOf(6) + `", "public_identities": ["sip:alice@` + testRealm + `"], "aka": {"k": "465b", "opc": "` + testOPc + `"}}`,
		`{"private_identity": "` + impiOf(7) + `", "public_identities": ["sip:alice7@` + testRealm + `"], "password": "p", "mmtel_services": "<oops/>"}`,
		taken,
		`{"private_identity": "` + impiOf(8) + `", "public_identities": ["` + strings.Repeat("x", maxRecordLine) + `"]}`,
		`{"private_identity": "` + impiOf(8) + `", "delete": true, "password": "p"}`,
		`{"private_identity": 7}`,
		digestLine(2, "test-password-2"),
		`{"private_identity": "` + impiOf(12) + `", "public_identities": ["sip:alice12@` + testRealm + `"], "barred_identities": ["tel:+12025551234"], "password": "p"}`,
	}
	outcomes, summary := ts.bulk(t, "", strings.Join(lines, "\n"))
	want := []struct {
		line   int
		result Result
		reason string
	}{
		{1, Unchanged, ""},
		{3, Rejected, "not a JSON object"},
		{4, Rejected, `unknown field "imsi"`},
		{5, Rejected, "test data"},
		{6, Rejected, "test PLMN 001/01"},
		{7, Rejected, "password or aka"},
		{8, Rejected, "aka.k: want 16 octets"},
		{9, Rejected, "mmtel_services"},
		{10, Rejected, "identity already belongs"},
		{11, Rejected, "line longer than"},
		{12, Rejected, "a line that deletes"},
		{13, Rejected, "private_identity: want a JSON string"},
		{14, Created, ""},
		{15, Rejected, "barred_identities[0]: test data"},
	}
	if len(outcomes) != len(want) {
		t.Fatalf("outcomes: %s", results(outcomes))
	}
	for i, w := range want {
		o := outcomes[i]
		if o.Line != w.line || o.Result != w.result || !strings.Contains(o.Reason, w.reason) {
			t.Errorf("line %d: %+v, want %s with %q", w.line, o, w.result, w.reason)
		}
	}
	if summary.Lines != 15 || summary.Unchanged != 1 || summary.Created != 1 || summary.Rejected != 12 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestSecretsNeverReachReportsLogsOrPrints(t *testing.T) {
	hss := newFakeHSS(t)
	hss.echo.Store(true)
	ts := newProvisioningServer(t, hss, Config{})
	secrets := []string{"test-secret-password-77", "test-secret-ut-77", testK, testOPc}
	line := fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:alice@%s"], "password": %q, "ut_password": %q, "aka": {"k": %q, "opc": %q}, "mmtel_services": "<oops/>"}`,
		impiOf(77), testRealm, secrets[0], secrets[1], secrets[2], secrets[3])

	outcomes, _ := ts.bulk(t, "", line)
	if len(outcomes) != 1 || outcomes[0].Result != Rejected || !strings.Contains(outcomes[0].Reason, "REDACTED") {
		t.Fatalf("outcome: %+v", outcomes)
	}
	status, body := ts.call(t, "PUT", "/oam/v1/subscriptions/"+impiOf(77), line)
	if status != http.StatusBadRequest {
		t.Errorf("PUT: %d %s", status, body)
	}

	var rec SubscriptionRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatal(err)
	}
	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("record", "record", rec, "aka", rec.AKA, "password", rec.Password)
	printed := fmt.Sprintf("%v %+v %#v %s %v", rec, rec, rec, rec.Password, *rec.AKA)
	for _, s := range secrets {
		for where, text := range map[string]string{
			"the report": results(outcomes) + outcomes[0].Reason, "the PUT answer": string(body),
			"the OAM log": ts.log.String(), "a log of the record": logged.String(), "a print of the record": printed,
		} {
			if strings.Contains(text, s) {
				t.Errorf("%s shows %s: %s", where, s, text)
			}
		}
	}
	// The value still reaches the HSS, which needs it.
	if b, _ := json.Marshal(rec.hssRequest()); !strings.Contains(string(b), testK) {
		t.Errorf("the request to the HSS lost K: %s", b)
	}
}

// TestBulkStreamsALargeFile sends a file that the OAM cannot have read
// whole when the first outcome comes back: the sender holds the rest of
// the file until it has read that outcome.
func TestBulkStreamsALargeFile(t *testing.T) {
	const lines, first = 3000, 50
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{ProvisioningConcurrency: 4})

	body, writer := io.Pipe()
	firstOutcome := make(chan struct{})
	go func() {
		w := bufio.NewWriter(writer)
		for n := range lines {
			if n == first {
				w.Flush()
				select {
				case <-firstOutcome:
				case <-time.After(10 * time.Second):
					writer.CloseWithError(fmt.Errorf("no outcome after %d lines: the OAM waits for the whole file", first))
					return
				}
			}
			fmt.Fprintf(w, `{"private_identity": %q, "public_identities": ["sip:00101%010d@%s"], "password": "test-password"}`+"\n", impiOf(n), n, testRealm)
		}
		w.Flush()
		writer.Close()
	}()
	resp, err := ts.server.Client().Post(ts.server.URL+"/oam/v1/provisioning", "application/x-ndjson", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	n := 0
	for sc.Scan() {
		var l StreamLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Outcome == nil {
			if l.Summary == nil || l.Summary.Created != lines {
				t.Errorf("last line: %s", sc.Bytes())
			}
			continue
		}
		n++
		if n == 1 {
			close(firstOutcome)
		}
		if l.Outcome.Line != n || l.Outcome.Result != Created {
			t.Fatalf("outcome %d: %+v", n, l.Outcome)
		}
	}
	if n != lines {
		t.Errorf("%d outcomes for %d lines", n, lines)
	}
	if m := hss.most.Load(); m > 4 {
		t.Errorf("%d records at once at the HSS, want at most 4", m)
	}
}

func TestSingleRecordOperations(t *testing.T) {
	hss := newFakeHSS(t)
	ts := newProvisioningServer(t, hss, Config{})
	path := "/oam/v1/subscriptions/" + impiOf(1)
	record := digestLine(1, "test-password-1")

	status, body := ts.call(t, "PUT", path, record)
	if o := decodeAs[Outcome](t, body); status != http.StatusCreated || o.Result != Created {
		t.Fatalf("PUT: %d %s", status, body)
	}
	status, body = ts.call(t, "PUT", path, record)
	if o := decodeAs[Outcome](t, body); status != http.StatusOK || o.Result != Unchanged {
		t.Errorf("PUT again: %d %s", status, body)
	}
	status, body = ts.call(t, "GET", path, "")
	if sub := decodeAs[Subscription](t, body); status != http.StatusOK || len(sub.PublicIdentities) != 2 || sub.AuthenticationSchemes[0] != "SIP Digest" {
		t.Errorf("GET: %d %s", status, body)
	}
	status, body = ts.call(t, "PUT", "/oam/v1/subscriptions/"+impiOf(3)+"?dry_run=true", digestLine(3, "test-password-3"))
	if o := decodeAs[Outcome](t, body); status != http.StatusOK || o.Result != Created || !o.DryRun || hss.count() != 1 {
		t.Errorf("PUT dry run: %d %s", status, body)
	}
	status, body = ts.call(t, "DELETE", path+"?dry_run=true", "")
	if o := decodeAs[Outcome](t, body); status != http.StatusOK || o.Result != Deleted || !o.DryRun || hss.count() != 1 {
		t.Errorf("DELETE dry run: %d %s", status, body)
	}
	if status, body = ts.call(t, "DELETE", path, ""); status != http.StatusOK || hss.count() != 0 {
		t.Errorf("DELETE: %d %s", status, body)
	}
	for _, tt := range []struct {
		method, path, body string
		want               int
	}{
		{"DELETE", path, "", http.StatusNotFound},
		{"GET", path, "", http.StatusNotFound},
		{"PUT", "/oam/v1/subscriptions/" + impiOf(2), record, http.StatusBadRequest},
		{"PUT", path, `{"private_identity": "` + impiOf(1) + `", "delete": true}`, http.StatusBadRequest},
		{"PUT", path, `{"public_identities": ["tel:+12025551234"], "password": "p"}`, http.StatusBadRequest},
		{"PUT", path, `{"password": `, http.StatusBadRequest},
	} {
		status, body := ts.call(t, tt.method, tt.path, tt.body)
		if status != tt.want || decodeAs[maintenance.ErrorBody](t, body).Error == "" {
			t.Errorf("%s %s %s: %d %s, want %d", tt.method, tt.path, tt.body, status, body, tt.want)
		}
	}
	ts.call(t, "PUT", path, record)
	other := `{"public_identities": ["tel:+12025550101"], "password": "p"}`
	if status, body := ts.call(t, "PUT", "/oam/v1/subscriptions/"+impiOf(2), other); status != http.StatusConflict {
		t.Errorf("PUT with an identity of another subscription: %d %s", status, body)
	}
	hss.stop()
	if status, body := ts.call(t, "GET", path, ""); status != http.StatusBadGateway {
		t.Errorf("GET with the HSS down: %d %s", status, body)
	}
	if status, body := ts.call(t, "PUT", path, record); status != http.StatusBadGateway {
		t.Errorf("PUT with the HSS down: %d %s", status, body)
	}
}

func TestSubscriberOperationsWithoutAnHSS(t *testing.T) {
	ts := newTestServer(t, Config{}, newFakeNode(t, "pcscf-1", []string{"pcscf"}))
	for _, path := range []string{"/oam/v1/provisioning", "/oam/v1/subscriptions/x@" + testRealm} {
		method := "POST"
		if !strings.HasSuffix(path, "provisioning") {
			method = "GET"
		}
		if status, body := ts.call(t, method, path, ""); status != http.StatusServiceUnavailable {
			t.Errorf("%s %s: %d %s", method, path, status, body)
		}
	}
}

func TestOnlyTestDataPasses(t *testing.T) {
	for id, ok := range map[string]bool{
		"tel:+12025550100":                     true,
		"tel:+1-202-555-0199":                  true,
		"tel:+12025550199;phone-context=x":     true,
		"tel:+12025550200":                     false,
		"tel:+12025551234":                     false,
		"tel:+11025550100":                     false,
		"tel:+442079460000":                    false,
		"tel:5550100":                          false,
		"sip:+12025550123@" + testRealm:        true,
		"sip:+12025559999@" + testRealm:        false,
		"sip:001010000000001@" + testRealm:     true,
		"sip:310150000000001@" + testRealm:     false,
		"sip:12025550123@" + testRealm:         true,
		"sip:5550123@" + testRealm:             false,
		"sip:alice@" + testRealm:               true,
		"sip:+alice@" + testRealm:              false,
		"sip:pcscf." + testRealm:               true,
		"mailto:alice@" + testRealm:            false,
		"sip:001010000000001;x=y@" + testRealm: true,
	} {
		if err := checkPublicIdentity(id); (err == nil) != ok {
			t.Errorf("%s: %v, want ok %v", id, err, ok)
		}
	}
	if checkPrivateIdentity("310150123456789@"+testRealm) == nil || checkPrivateIdentity("no-realm") == nil || checkPrivateIdentity(impiOf(1)) != nil {
		t.Error("private identities")
	}
}
