package maintenance

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// akaView is how a role would show an AKA subscription: the keys are
// Secrets, and SQN and AMF, which are not secret, are shown.
type akaView struct {
	K   Secret `json:"k"`
	OPc Secret `json:"opc"`
	AMF string `json:"amf"`
	SQN string `json:"sqn"`
}

type subscriptionView struct {
	PrivateIdentity string   `json:"private_identity"`
	Password        Secret   `json:"password"`
	UtPassword      Secret   `json:"ut_password,omitzero"`
	AKA             *akaView `json:"aka,omitempty"`
}

func TestSecretMarshalsRedacted(t *testing.T) {
	v := subscriptionView{
		PrivateIdentity: "user0123@ims.mnc001.mcc001.3gppnetwork.org",
		Password:        Redacted("secret-test-password"),
		AKA: &akaView{
			K:   Redacted("465b5ce8b199b49faa5f0a2ee238a6bc"),
			OPc: Redacted("cd63cb71954a9f4e48a5994e37a02baf"),
			AMF: "b9b9",
			SQN: "ff9bb4d0b607",
		},
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-test-password", "465b5ce8", "cd63cb71"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("JSON %s holds %s", b, secret)
		}
	}
	want := `{"private_identity":"user0123@ims.mnc001.mcc001.3gppnetwork.org","password":"REDACTED","aka":{"k":"REDACTED","opc":"REDACTED","amf":"b9b9","sqn":"ff9bb4d0b607"}}`
	if string(b) != want {
		t.Errorf("JSON = %s, want %s", b, want)
	}
	if err := CheckRedacted(v); err != nil {
		t.Errorf("CheckRedacted: %v", err)
	}
	if s := fmt.Sprint(v.AKA.K); s != RedactedValue {
		t.Errorf("printed as %q", s)
	}
	var log strings.Builder
	slog.New(slog.NewTextHandler(&log, nil)).Info("x", "k", v.AKA.K)
	if strings.Contains(log.String(), "465b5ce8") || !strings.Contains(log.String(), "k="+RedactedValue) {
		t.Errorf("logged as %s", log.String())
	}
}

func TestUnsetSecretIsNull(t *testing.T) {
	b, err := json.Marshal(struct {
		Password Secret `json:"password"`
	}{Redacted("")})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"password":null}` {
		t.Errorf("JSON = %s", b)
	}
}

func TestCheckRedactedFindsSecrets(t *testing.T) {
	for _, tc := range []struct {
		name string
		view any
		path string // in the error; "" for a view that passes
	}{
		{"plain password", map[string]any{"password": "x"}, "$.password"},
		{"nested AKA key", map[string]any{"hss": map[string]any{"subscriptions": []any{
			map[string]any{"aka": map[string]any{"k": "465b5ce8b199b49faa5f0a2ee238a6bc", "amf": "b9b9"}},
		}}}, "$.hss.subscriptions[0].aka.k"},
		{"Go field names", struct{ OPc string }{"cd63cb71954a9f4e48a5994e37a02baf"}, "$.OPc"},
		{"suffix", map[string]any{"ut_password": "x"}, "$.ut_password"},
		{"word inside", map[string]any{"xcap": map[string]any{"nonce_key": "x"}}, "$.xcap.nonce_key"},
		{"key name as a word", map[string]any{"last_opc": "x"}, "$.last_opc"},
		{"list under a secret name", map[string]any{"secrets": []any{"a"}}, "$.secrets[0]"},
		{"object under a secret name", map[string]any{"credentials": map[string]any{"user": "alice"}}, "$.credentials.user"},
		{"number", map[string]any{"token": 1234}, "$.token"},
		{"redacted", map[string]any{"password": RedactedValue, "k": Redacted("x")}, ""},
		{"empty and null", map[string]any{"password": "", "opc": nil}, ""},
		{"flag", map[string]any{"password_set": true}, ""},
		{"not a secret", map[string]any{"sqn": "ff9bb4d0b607", "amf": "b9b9", "kind": "x", "opcode": 3, "peers": []any{"hss"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckRedacted(tc.view)
			if tc.path == "" {
				if err != nil {
					t.Errorf("CheckRedacted: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.path+" ") {
				t.Errorf("CheckRedacted = %v, want an error at %s", err, tc.path)
			}
		})
	}
}

func TestCheckRedactedReportsMarshalErrors(t *testing.T) {
	if err := CheckRedacted(map[string]any{"f": func() {}}); err == nil {
		t.Error("no error for a value that does not marshal")
	}
}
