package oam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
)

// The tests here put the OAM in front of the provisioning API of
// internal/hss itself rather than the fake of fakehss_test.go. A backup
// that goes out through the OAM and back through it has to leave the
// HSS as it stands, and only the two sides together can show that: the
// HSS writes the lines and reads them again, and the OAM carries each
// line between them without changing what it means.

// emptyMMTelServices is the MMTelServices document of the HSS
// configuration in these tests, which a subscription gets when its
// record leaves the repository data out. It has no rule of its own; the
// documents of the deployments are in deploy/compose/hss.yaml.
const emptyMMTelServices = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"/>`

// noPeers is the S-CSCFs of the HSS, which these tests never reach: the
// subscriptions they provision do not register, so the HSS neither
// pushes a profile nor deregisters a user.
type noPeers struct{}

func (noPeers) Request(context.Context, string, *diam.Message) (*diam.Message, error) {
	return nil, errors.New("no S-CSCF")
}

// newRealHSS serves the provisioning API of internal/hss on a store of
// its own. Its configuration holds an MMTelServices document, so that a
// record which leaves repository_data out asks for something other than
// one that carries an empty array.
func newRealHSS(t *testing.T) *httptest.Server {
	t.Helper()
	document, err := hss.MMTelServicesData([]byte(emptyMMTelServices))
	if err != nil {
		t.Fatal(err)
	}
	store, err := hss.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(&syncBuffer{}, nil))
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + testRealm, OriginRealm: testRealm},
		[]uint32{diameter.ApplicationIDCx}, diameter.Timers{}, log)
	h, err := hss.New(hss.Config{Realm: testRealm, MMTelServices: &document}, store, node, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	server := httptest.NewServer(h.Provisioning(noPeers{}))
	t.Cleanup(server.Close)
	return server
}

// TestABackupOfTheHSSGoesBackUnchanged: every line of a backup is the
// subscription as the HSS holds it, so a restore of a network that is
// still there answers unchanged for every line, down to the repository
// data. The OAM has to carry every field that the HSS writes and mean
// by it what the HSS means: a subscription with no repository data at
// all goes out as an empty array, which is not a line without the
// field, and one with a barred public identity goes out with it.
func TestABackupOfTheHSSGoesBackUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	backups, err := NewFileBackupStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, Config{HSSProvisioning: newRealHSS(t).URL, Backups: backups},
		newFakeNode(t, "hss-1", []string{"hss"}))
	ts.oam.exportPageSize = 2 // more than one page of the export
	lines := []string{
		// The repository data of the configuration, which the HSS gives a
		// record that leaves the field out.
		digestLine(1, "test-password-1"),
		// No repository data at all, which the empty array asks for.
		strings.Replace(digestLine(2, "test-password-2"), `"password"`, `"repository_data": [], "password"`, 1),
		// Repository data that the HSS makes of the two fields it holds.
		fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:00101%010d@%s"], "password": "test-password-3", "ut_password": "test-password-3", "mmtel_services": %q, "test_user": true}`,
			impiOf(3), 3, testRealm, emptyMMTelServices),
		akaLine(10),
		// A barred public identity, the temporary one that a UE without an
		// ISIM registers with.
		fmt.Sprintf(`{"private_identity": %q, "public_identities": ["sip:00101%010d@%s", "tel:+12025550104"], "barred_identities": ["sip:00101%010d@%s"], "password": "test-password-4"}`,
			impiOf(4), 4, testRealm, 4, testRealm),
	}
	if _, summary := ts.bulk(t, "", strings.Join(lines, "\n")); summary.Created != len(lines) {
		t.Fatalf("provisioning: %+v", summary)
	}

	_, backup := ts.streamCall(t, "/oam/v1/backups")
	if backup.manifest == nil || backup.manifest.State != JobCompleted || len(backup.errors) > 0 {
		t.Fatalf("backup: %+v", backup)
	}
	archived := archivedRecords(t, filepath.Join(dir, backup.manifest.ID+".jsonl"))
	if len(archived) != len(lines) {
		t.Fatalf("the archive holds %d records, want %d", len(archived), len(lines))
	}
	if got := archived[impiOf(2)].RepositoryData; got == nil || len(got) != 0 {
		t.Errorf("the line of a subscription with no repository data holds %v", got)
	}
	for _, impi := range []string{impiOf(1), impiOf(3)} {
		if got := archived[impi].RepositoryData; len(got) == 0 {
			t.Errorf("the line of %s holds no repository data", impi)
		}
	}
	if got, want := archived[impiOf(4)].BarredIdentities, []string{fmt.Sprintf("sip:00101%010d@%s", 4, testRealm)}; !slices.Equal(got, want) {
		t.Errorf("the barred identities of %s = %v, want %v", impiOf(4), got, want)
	}

	// The whole archive goes back into the HSS it came from.
	_, restore := ts.streamCall(t, "/oam/v1/backups/"+backup.manifest.ID+"/restore")
	if restore.summary == nil || restore.summary.Unchanged != len(lines) {
		t.Fatalf("restore: %+v", restore)
	}
	for _, o := range restore.outcomes {
		if o.Result != Unchanged {
			t.Errorf("%s restored as %s: %s", o.PrivateIdentity, o.Result, o.Reason)
		}
	}
}

// archivedRecords is each subscription of the archive by its private
// identity, read as the OAM reads a line that it restores: unknown
// fields refused, so a line that the HSS writes and the OAM cannot read
// fails here.
func archivedRecords(t *testing.T, path string) map[string]SubscriptionRecord {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records := map[string]SubscriptionRecord{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var entry archiveEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if entry.Record == nil {
			continue
		}
		var rec SubscriptionRecord
		if err := decodeRecord(entry.Record, &rec); err != nil {
			t.Fatalf("a line of the archive is no record the OAM reads: %v", err)
		}
		records[rec.PrivateIdentity] = rec
	}
	return records
}
