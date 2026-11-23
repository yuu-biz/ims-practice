package hss

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

// The private identities of the subscriptions that the export tests
// add, beside the impi and otherImp of the configuration.
const (
	utImpi     = "001010000000003@" + realm
	catImpi    = "001010000000004@" + realm
	markedImpi = "001010000000005@" + realm
	emptyImpi  = "001010000000006@" + realm
	barredImpi = "001010000000007@" + realm
)

// exportLines is the body of a GET of the export at path: one line per
// subscription, and the path of the page that follows, if any.
func exportLines(t *testing.T, api http.Handler, path string) ([][]byte, string) {
	t.Helper()
	w := do(t, api, "GET", path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
	}
	if got := w.Header().Get("Content-Type"); got != ndjsonContentType {
		t.Errorf("Content-Type of the export = %q, want %q", got, ndjsonContentType)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control of the export = %q, want no-store", got)
	}
	var lines [][]byte
	for _, line := range bytes.Split(w.Body.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, nextPage(t, path, w.Header().Get("Link"))
}

// exportedLine is one line of the export, read back into the type of a
// PUT body, which is what the line is.
func exportedLine(t *testing.T, line []byte) subscriptionRequest {
	t.Helper()
	var req subscriptionRequest
	if err := json.Unmarshal(line, &req); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return req
}

// everyKindOfSubscription adds to h the kinds of subscription that an
// archive has to carry, beside the SIP Digest one of the configuration,
// and returns every private identity in the order the identities sort
// in. The HSS keeps a configured MMTelServices document and two service
// profiles, so that the subscriptions have supplementary service data
// and a profile name to carry.
func everyKindOfSubscription(t *testing.T, h *HSS, api http.Handler) []string {
	t.Helper()
	doc := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-waiting active="true"/></MMTelServices>`
	d, err := MMTelServicesData([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	h.config.MMTelServices = &d
	h.config.ServiceProfiles = map[string]ServiceProfile{"default": {}, "trial": {}}
	h.config.DefaultServiceProfile, h.config.TestUserServiceProfile = "default", "trial"

	// IMS-AKA, with a sequence number that the authentication moves on
	// from the one the operator provisioned.
	akaBody := `{"private_identity": "` + akaImpi + `", "public_identities": ["` + akaImpu + `"],
		"aka": {"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "opc": "cd63cb71954a9f4e48a5994e37a02baf", "amf": "8000", "sqn": "000000000020"}}`
	// A Ut password, which the HSS keeps as H(A1) in its repository
	// data, beside the configured supplementary service document.
	utBody := `{"private_identity": "` + utImpi + `", "public_identities": ["sip:001010000000003@` + realm + `"],
		"password": "test-password-3", "ut_password": "test-ut-password-3"}`
	// A customized alerting tone, which is repository data as well.
	catBody := `{"private_identity": "` + catImpi + `", "public_identities": ["sip:001010000000004@` + realm + `"],
		"password": "test-password-4", "customized_alerting_tone": {"tone": "cat1", "active": true}}`
	// The mark of a test user and a service profile name.
	markedBody := `{"private_identity": "` + markedImpi + `", "public_identities": ["sip:001010000000005@` + realm + `"],
		"password": "test-password-5", "test_user": true, "service_profile": "trial"}`
	// A barred identity, as a UE without an ISIM has: the temporary
	// public identity it makes from the IMSI may register and nothing
	// else.
	barredBody := `{"private_identity": "` + barredImpi + `", "password": "test-password-7",
		"public_identities": ["sip:001010000000007@` + realm + `", "tel:+12025550107"],
		"barred_identities": ["sip:001010000000007@` + realm + `"]}`
	for _, body := range []string{akaBody, utBody, catBody, markedBody, barredBody} {
		if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
			t.Fatalf("POST %s: %d %s", body, w.Code, w.Body)
		}
	}
	// No repository data at all, which an empty array asks for. The empty
	// barred_identities beside it bars nothing, as leaving that field out
	// does, so this line and one without it are the same subscription.
	empty := `{"private_identity": "` + emptyImpi + `", "public_identities": ["sip:001010000000006@` + realm + `"],
		"password": "test-password-6", "barred_identities": [], "repository_data": []}`
	if w, _ := putSubscription(t, api, emptyImpi, empty); w.Code != http.StatusCreated {
		t.Fatalf("PUT of a subscription with no repository data: %d %s", w.Code, w.Body)
	}
	if data, err := h.store.AllRepositoryData(context.Background(), emptyImpi); len(data) != 0 || err != nil {
		t.Fatalf("the subscription with no repository data holds %+v (%v)", data, err)
	}

	// Two vectors, so that SQN_HE is no longer the provisioned one.
	for range 2 {
		if a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil); a.AuthenticationVector == nil {
			t.Fatalf("MAA = %+v", a)
		}
	}
	return []string{impi, otherImp, utImpi, catImpi, markedImpi, emptyImpi, barredImpi, akaImpi}
}

// TestProvisioningExportsSubscriptionsThatGoBackUnchanged: every line
// of the export is the body of the PUT that puts that subscription
// back, so an operator who keeps the lines and sends them again gets
// unchanged for each one and leaves the HSS exactly as it stands, down
// to the sequence numbers of the repository data.
func TestProvisioningExportsSubscriptionsThatGoBackUnchanged(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	ids := everyKindOfSubscription(t, h, api)

	before := map[string][]byte{}
	for _, id := range ids {
		before[id] = storedState(t, h, id)
	}

	lines, next := exportLines(t, api, "/subscriptions/export")
	if next != "" {
		t.Errorf("an export that holds everything names a next page: %s", next)
	}
	if len(lines) != len(ids) {
		t.Fatalf("the export has %d lines, want %d:\n%s", len(lines), len(ids), bytes.Join(lines, []byte("\n")))
	}
	for i, line := range lines {
		id := ids[i]
		if got := exportedLine(t, line).PrivateIdentity; got != id {
			t.Fatalf("line %d is for %s, want %s", i+1, got, id)
		}
		w, got := putSubscription(t, api, id, string(line))
		if w.Code != http.StatusOK || got.Result != resultUnchanged {
			t.Errorf("the line of %s sent back: %d %s", id, w.Code, w.Body)
			continue
		}
		if after := storedState(t, h, id); !bytes.Equal(before[id], after) {
			t.Errorf("the line of %s changed the subscription:\nbefore %s\nafter  %s", id, before[id], after)
		}
	}
}

// TestTheExportCarriesTheCredentials: the export is the one answer of
// the API that hands out secrets. It carries the SIP Digest password,
// K and OPc, SQN_HE as it stands rather than the one provisioned, and
// the repository data, in which the Ut credential is the H(A1) of the
// Ut password and never the password itself.
func TestTheExportCarriesTheCredentials(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	everyKindOfSubscription(t, h, api)

	lines, _ := exportLines(t, api, "/subscriptions/export")
	byIdentity := map[string]subscriptionRequest{}
	textOf := map[string][]byte{}
	for _, line := range lines {
		req := exportedLine(t, line)
		byIdentity[req.PrivateIdentity], textOf[req.PrivateIdentity] = req, line
	}

	if got := byIdentity[impi].Password; got != "test-password-1" {
		t.Errorf("the password of %s = %q", impi, got)
	}
	stored := subscriberOrFail(t, h, akaImpi)
	akaLine := byIdentity[akaImpi]
	if akaLine.AKA == nil {
		t.Fatalf("the line of %s carries no AKA credentials", akaImpi)
	}
	if akaLine.AKA.K != "465b5ce8b199b49faa5f0a2ee238a6bc" || akaLine.AKA.OPc != "cd63cb71954a9f4e48a5994e37a02baf" || akaLine.AKA.AMF != "8000" {
		t.Errorf("the AKA credentials of %s = %+v", akaImpi, akaLine.AKA)
	}
	if want := fmt.Sprintf("%012x", uint64(stored.AKA.SQN)); akaLine.AKA.SQN != want {
		t.Errorf("the sqn of %s = %q, want the stored SQN_HE %q", akaImpi, akaLine.AKA.SQN, want)
	}
	if akaLine.AKA.SQN == "000000000020" {
		t.Errorf("the sqn of %s is the provisioned one, not the SQN_HE the authentications left", akaImpi)
	}
	// The Ut credential goes out as repository data: the HSS has no Ut
	// password to write, only its H(A1), so the line carries no
	// ut_password either.
	ut := byIdentity[utImpi]
	if ut.UtPassword != "" || strings.Contains(string(textOf[utImpi]), "test-ut-password-3") {
		t.Errorf("the line of %s holds the Ut password: %s", utImpi, textOf[utImpi])
	}
	indications := map[string]string{}
	for _, d := range ut.RepositoryData {
		indications[d.ServiceIndication] = d.ServiceData
	}
	credential := indications[xcap.CredentialServiceIndication]
	if !strings.Contains(credential, HA1(utImpi, realm, "test-ut-password-3")) {
		t.Errorf("the Ut credential of %s = %q", utImpi, credential)
	}
	if !strings.Contains(indications[simservs.ServiceIndication], "communication-waiting") {
		t.Errorf("the supplementary service data of %s = %q", utImpi, indications[simservs.ServiceIndication])
	}
	// The data stays as the operator reads it, rather than going out with
	// the angle brackets of the XML as escapes.
	if !bytes.Contains(textOf[utImpi], []byte("<MMTelServices")) {
		t.Errorf("the line of %s does not hold the document as it is: %s", utImpi, textOf[utImpi])
	}
	tones := map[string]string{}
	for _, d := range byIdentity[catImpi].RepositoryData {
		tones[d.ServiceIndication] = d.ServiceData
	}
	if !strings.Contains(tones[mmtel.CATServiceIndication], "cat1") {
		t.Errorf("the tone of %s = %q", catImpi, tones[mmtel.CATServiceIndication])
	}
	if marked := byIdentity[markedImpi]; !marked.TestUser || marked.ServiceProfile != "trial" {
		t.Errorf("the mark and the profile of %s: %+v", markedImpi, marked)
	}
	if data := byIdentity[emptyImpi].RepositoryData; data == nil || len(data) != 0 {
		t.Errorf("the repository data of %s = %+v, want the empty array that asks for none", emptyImpi, data)
	}
}

// TestTheExportPagesAsTheListingDoes: the export takes the after and
// limit of the listing and names the page that follows in the same Link
// header, and each line carries the credentials of its own subscriber
// and of no other.
func TestTheExportPagesAsTheListingDoes(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	ids := everyKindOfSubscription(t, h, api)
	// The password of each subscription that has one, so that a line
	// found with another's credentials is a failure rather than a
	// surprise.
	passwords := map[string]string{}
	for _, id := range ids {
		passwords[id] = subscriberOrFail(t, h, id).Password
	}

	var got []string
	path, pages := "/subscriptions/export?limit=2", 0
	for path != "" {
		lines, next := exportLines(t, api, path)
		for _, line := range lines {
			req := exportedLine(t, line)
			got = append(got, req.PrivateIdentity)
			if req.Password != passwords[req.PrivateIdentity] {
				t.Errorf("the line of %s carries the password %q, want %q", req.PrivateIdentity, req.Password, passwords[req.PrivateIdentity])
			}
		}
		if len(lines) > 2 {
			t.Errorf("a page of 2 holds %d lines", len(lines))
		}
		pages++
		if pages > len(ids)+1 {
			t.Fatalf("still paging after %d pages: %v", pages, got)
		}
		path = next
	}
	if strings.Join(got, " ") != strings.Join(ids, " ") {
		t.Errorf("the pages held %v, want %v", got, ids)
	}
	// The whole export has no page after it, a limit that is not a number
	// of subscriptions is refused, and a page after the last subscription
	// is empty.
	if w := do(t, api, "GET", "/subscriptions/export?limit=none", ""); w.Code != http.StatusBadRequest {
		t.Errorf("an export with a limit that is not a number: %d %s", w.Code, w.Body)
	}
	last, _ := exportLines(t, api, "/subscriptions/export?after="+ids[len(ids)-1])
	if len(last) != 0 {
		t.Errorf("the export after the last subscription holds %d lines", len(last))
	}
}

// TestTheExportPutsADeletedSubscriptionBack: an archive is for the
// subscription that is gone. Its line adds the subscription again, with
// the credentials and the repository data it had, and with the SQN_HE
// of the moment it was written: the user authenticates from there, and
// a USIM that is ahead of it resynchronises (internal/aka).
func TestTheExportPutsADeletedSubscriptionBack(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	everyKindOfSubscription(t, h, api)
	archive := map[string][]byte{}
	lines, _ := exportLines(t, api, "/subscriptions/export")
	for _, line := range lines {
		archive[exportedLine(t, line).PrivateIdentity] = line
	}
	archivedSQN := subscriberOrFail(t, h, akaImpi).AKA.SQN

	for _, id := range []string{akaImpi, utImpi} {
		if w := do(t, api, "DELETE", "/subscriptions/"+id, ""); w.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s: %d %s", id, w.Code, w.Body)
		}
		w, got := putSubscription(t, api, id, string(archive[id]))
		if w.Code != http.StatusCreated || got.Result != resultCreated {
			t.Fatalf("the archived line of %s: %d %s", id, w.Code, w.Body)
		}
	}
	if back := subscriberOrFail(t, h, akaImpi).AKA.SQN; back != archivedSQN {
		t.Errorf("SQN_HE of the subscription put back = %d, want the archived %d", back, archivedSQN)
	}
	// The next vector follows the restored SQN_HE, and the USIM of the
	// subscriber takes it.
	usim := aka.NewUSIM(akaSecret, archivedSQN)
	a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil)
	if a.AuthenticationVector == nil {
		t.Fatalf("MAA after the restore: %+v", a)
	}
	if _, err := usim.Authenticate(a.AuthenticationVector.RAND, a.AuthenticationVector.AUTN); err != nil {
		t.Errorf("the USIM refuses the vector after the restore: %v", err)
	}
	// The Ut credential and the supplementary service data came back with
	// the subscription, under the sequence number of new data.
	if d := storedData(t, h, utImpi, xcap.CredentialServiceIndication); d.SequenceNumber != 0 ||
		!strings.Contains(string(d.ServiceData.XML), HA1(utImpi, realm, "test-ut-password-3")) {
		t.Errorf("the Ut credential after the restore: %+v", d)
	}
	storedData(t, h, utImpi, simservs.ServiceIndication)
}

// TestTheExportAnswersBeforeASubscriptionCalledExport: the pattern of
// the export is the more specific of the two that GET /subscriptions/…
// has, so the export answers and a subscription whose private identity
// is that very word is the one that no GET of a single subscription
// reaches. Every other request still reaches it.
func TestTheExportAnswersBeforeASubscriptionCalledExport(t *testing.T) {
	h := newTestHSS()
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "export", "public_identities": ["sip:001010000000009@` + realm + `"], "password": "p"}`
	if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	lines, _ := exportLines(t, api, "/subscriptions/export")
	if len(lines) != 3 {
		t.Fatalf("the export has %d lines, want the 3 subscriptions", len(lines))
	}
	// The export answers, whether or not the word arrives as an escape.
	for _, path := range []string{"/subscriptions/export", "/subscriptions/ex%70ort"} {
		w := do(t, api, "GET", path, "")
		if ct := w.Header().Get("Content-Type"); ct != ndjsonContentType {
			t.Errorf("GET %s answered with Content-Type %q, want the export", path, ct)
		}
	}
	// The PATCH and the DELETE of that subscription reach it.
	if w := do(t, api, "PATCH", "/subscriptions/export", `{"test_user": true}`); w.Code != http.StatusOK {
		t.Errorf("PATCH: %d %s", w.Code, w.Body)
	}
	if w := do(t, api, "DELETE", "/subscriptions/export", ""); w.Code != http.StatusNoContent {
		t.Errorf("DELETE: %d %s", w.Code, w.Body)
	}
}

// changedDuringTheExport fails, or removes the subscription, when the
// export reads the repository data of privateID.
type changedDuringTheExport struct {
	Store
	privateID string
	fail      bool
}

func (s *changedDuringTheExport) AllRepositoryData(ctx context.Context, privateID string) ([]sh.TransparentData, error) {
	if privateID == s.privateID {
		if s.fail {
			return nil, errors.New("store unreachable")
		}
		if _, _, err := s.Store.Delete(ctx, privateID); err != nil {
			return nil, err
		}
	}
	return s.Store.AllRepositoryData(ctx, privateID)
}

func newChangingHSS(t *testing.T, store *changedDuringTheExport) http.Handler {
	t.Helper()
	memory, err := NewMemoryStore(
		Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"},
		Subscription{PrivateIdentity: otherImp, PublicIdentities: []string{"sip:other@" + realm}, Password: "test-password-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	store.Store = memory
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	return mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, node).Provisioning(&fakeSCSCFs{})
}

// TestAnExportThatCannotReadTheStoreSendsNoLine: a page whose repository
// data the store cannot give answers 500 with no line and no next page,
// rather than a body that breaks off, which a reader would take for the
// end of the page.
func TestAnExportThatCannotReadTheStoreSendsNoLine(t *testing.T) {
	api := newChangingHSS(t, &changedDuringTheExport{privateID: otherImp, fail: true})
	w := do(t, api, "GET", "/subscriptions/export", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GET: %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "test-password") || w.Header().Get("Link") != "" {
		t.Errorf("a failed export sent %q with Link %q", w.Body, w.Header().Get("Link"))
	}
}

// TestTheExportLeavesOutASubscriptionRemovedMeanwhile: its line would
// put back a subscription without the repository data it had.
func TestTheExportLeavesOutASubscriptionRemovedMeanwhile(t *testing.T) {
	api := newChangingHSS(t, &changedDuringTheExport{privateID: otherImp})
	lines, _ := exportLines(t, api, "/subscriptions/export")
	if len(lines) != 1 || !bytes.Contains(lines[0], []byte(impi)) {
		t.Errorf("export = %q, want the line of %s alone", lines, impi)
	}
}
