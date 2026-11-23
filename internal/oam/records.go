package oam

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// SecretString is a credential of a subscription record: a SIP Digest
// or Ut password, or the K or OPc of AKA. JSON carries its value, as the
// HSS and the backup file need it; printing it with fmt or slog gives
// maintenance.RedactedValue instead, so a log line, an error or a
// report that takes a record by mistake does not show it.
type SecretString string

func (s SecretString) String() string {
	if s == "" {
		return ""
	}
	return "REDACTED"
}

func (s SecretString) GoString() string { return `"` + s.String() + `"` }

func (s SecretString) LogValue() slog.Value { return slog.StringValue(s.String()) }

// SubscriptionRecord is one line of a bulk provisioning file, and one
// subscription of a backup: the subscriptionRequest of the HSS
// provisioning API (internal/hss), with the Delete field that the OAM
// reads itself. The names are those of the HSS API and of the
// configuration file.
type SubscriptionRecord struct {
	PrivateIdentity string `json:"private_identity"`
	// PublicIdentities are SIP and tel URIs; the first that is not
	// barred is the default public identity.
	PublicIdentities []string `json:"public_identities,omitempty"`
	// BarredIdentities are those of the public identities that may
	// register and do nothing else, such as the temporary public
	// identity of a UE without an ISIM. The HSS refuses one that is not
	// among the public identities.
	BarredIdentities []string     `json:"barred_identities,omitempty"`
	Password         SecretString `json:"password,omitempty"`
	AKA              *AKARecord   `json:"aka,omitempty"`
	// Tel joins the public identities after the others, as in the HSS
	// API.
	Tel        string       `json:"tel,omitempty"`
	UtPassword SecretString `json:"ut_password,omitempty"`
	// MMTelServices is the first MMTelServices document, as XML text.
	MMTelServices string `json:"mmtel_services,omitempty"`
	// RepositoryData is the Sh repository data of the subscription as
	// the HSS holds it now, which a backup has in place of
	// MMTelServices and UtPassword. It carries no omitempty, as the
	// field of the request does not (hssSubscriptionRequest): an empty
	// array asks for a subscription with no repository data at all, and
	// a line of an archive that holds one goes back in as one.
	RepositoryData []RepositoryDataRecord `json:"repository_data"`
	// TestUser marks a test user.
	TestUser bool `json:"test_user,omitempty"`
	// ServiceProfile names the service profile of the subscription;
	// empty lets the HSS choose by TestUser.
	ServiceProfile string `json:"service_profile,omitempty"`
	// Delete removes the subscription of PrivateIdentity; the line then
	// has no other field. It is the OAM's, not sent to the HSS.
	Delete bool `json:"delete,omitempty"`
}

// AKARecord is the AKA credentials, in hexadecimal like the test data
// of TS 35.208, as the HSS API takes them.
type AKARecord struct {
	K   SecretString `json:"k"`
	OPc SecretString `json:"opc"`
	AMF string       `json:"amf,omitempty"`
	// SQN is SQN_HE. The HSS takes it only for a new subscription, so
	// that a provisioning file sent again never takes SQN_HE back,
	// which would let a vector be used twice.
	SQN string `json:"sqn,omitempty"`
}

// RepositoryDataRecord is one piece of the Sh repository data of a
// subscription: the Service-Indication and the data as XML text, which
// the HSS stores without reading it.
type RepositoryDataRecord struct {
	ServiceIndication string `json:"service_indication"`
	ServiceData       string `json:"service_data"`
}

// LogValue gives only the identity, whatever the record holds.
func (r SubscriptionRecord) LogValue() slog.Value {
	return slog.StringValue(r.PrivateIdentity)
}

// LogValue leaves out K and OPc, which a JSON log handler would
// otherwise marshal.
func (a AKARecord) LogValue() slog.Value {
	return slog.GroupValue(slog.String("k", a.K.String()), slog.String("opc", a.OPc.String()),
		slog.String("amf", a.AMF), slog.String("sqn", a.SQN))
}

// secrets are the credentials of the record, which no report or error
// may show.
func (r SubscriptionRecord) secrets() []string {
	s := []string{string(r.Password), string(r.UtPassword)}
	if r.AKA != nil {
		s = append(s, string(r.AKA.K), string(r.AKA.OPc))
	}
	return s
}

// validate checks a record before it goes to the HSS: the form of each
// field as the HSS API checks it, and that every identity is test data
// so that no real subscriber's data enters the network. The error names
// the field, never a credential.
func (r SubscriptionRecord) validate() error {
	if r.PrivateIdentity == "" {
		return errors.New("private_identity: required")
	}
	if err := checkPrivateIdentity(r.PrivateIdentity); err != nil {
		return err
	}
	if r.Delete {
		if r.PublicIdentities != nil || r.BarredIdentities != nil || r.Password != "" || r.AKA != nil || r.Tel != "" ||
			r.UtPassword != "" || r.MMTelServices != "" || r.RepositoryData != nil || r.TestUser || r.ServiceProfile != "" {
			return errors.New("delete: a line that deletes has no field but private_identity")
		}
		return nil
	}
	switch {
	case len(r.PublicIdentities) == 0:
		return errors.New("public_identities: at least one is required")
	case r.Password == "" && r.AKA == nil:
		return errors.New("password or aka: at least one is required")
	case r.RepositoryData != nil && (r.MMTelServices != "" || r.UtPassword != ""):
		return errors.New("repository_data: goes without mmtel_services and ut_password, which it holds")
	}
	for i, id := range r.PublicIdentities {
		if err := checkPublicIdentity(id); err != nil {
			return fmt.Errorf("public_identities[%d]: %w", i, err)
		}
	}
	// Whether each barred identity is among the public ones is for the
	// HSS to say; the OAM checks that it is test data, as it does for
	// every identity of a record.
	for i, id := range r.BarredIdentities {
		if err := checkPublicIdentity(id); err != nil {
			return fmt.Errorf("barred_identities[%d]: %w", i, err)
		}
	}
	if r.Tel != "" {
		if !strings.HasPrefix(r.Tel, "tel:") {
			return errors.New("tel: not a tel URI")
		}
		if err := checkPublicIdentity(r.Tel); err != nil {
			return fmt.Errorf("tel: %w", err)
		}
	}
	if r.AKA != nil {
		if err := r.AKA.validate(); err != nil {
			return err
		}
	}
	for i, d := range r.RepositoryData {
		if d.ServiceIndication == "" || d.ServiceData == "" {
			return fmt.Errorf("repository_data[%d]: service_indication and service_data are required", i)
		}
	}
	if r.ServiceProfile != "" && !maintenance.IsName(r.ServiceProfile) {
		return errors.New("service_profile: not a name of lowercase letters, digits and hyphens")
	}
	return nil
}

func (a AKARecord) validate() error {
	fields := []struct {
		name, value string
		octets      int
		optional    bool
	}{
		{"k", string(a.K), 16, false},
		{"opc", string(a.OPc), 16, false},
		{"amf", a.AMF, 2, true},
		{"sqn", a.SQN, 6, true},
	}
	for _, f := range fields {
		if f.value == "" && f.optional {
			continue
		}
		if b, err := hex.DecodeString(f.value); err != nil || len(b) != f.octets {
			return fmt.Errorf("aka.%s: want %d octets in hexadecimal", f.name, f.octets)
		}
	}
	return nil
}

// testIMSIPrefix is the MCC and MNC of the test PLMN 001/01 that all the
// test data of the project uses.
const testIMSIPrefix = "00101"

// checkPrivateIdentity checks a NAI of the form user@realm (TS 23.003
// derives it from the IMSI); a user part of digits must be a test IMSI.
func checkPrivateIdentity(id string) error {
	user, realm, ok := strings.Cut(id, "@")
	if !ok || user == "" || realm == "" {
		return errors.New("private_identity: not of the form user@realm")
	}
	if err := checkTestNumber(user); err != nil {
		return fmt.Errorf("private_identity: %w", err)
	}
	return nil
}

// checkPublicIdentity checks a SIP or tel URI as the HSS does, and that
// a number in it is test data.
func checkPublicIdentity(id string) error {
	switch {
	case strings.HasPrefix(id, "tel:"):
		number, _, _ := strings.Cut(strings.TrimPrefix(id, "tel:"), ";")
		if !strings.HasPrefix(number, "+") {
			return errors.New("test data: a tel URI must be a global number, a fictional +1 NXX 555 01XX one")
		}
		return checkTestNumber(stripVisualSeparators(number))
	case strings.HasPrefix(id, "sip:"):
		user, _, ok := strings.Cut(strings.TrimPrefix(id, "sip:"), "@")
		if !ok {
			return nil
		}
		user, _, _ = strings.Cut(user, ";")
		return checkTestNumber(stripVisualSeparators(user))
	}
	return errors.New("not a SIP or tel URI")
}

// stripVisualSeparators removes the visual separators that a telephone
// number may have in a tel URI, which do not change the number (RFC
// 3966 3 and 5.1.1).
func stripVisualSeparators(s string) string {
	return strings.NewReplacer("-", "", ".", "", "(", "", ")", "").Replace(s)
}

// checkTestNumber refuses a number that could belong to a real
// subscriber. A user part that is not a number, such as alice, passes.
// A number is a test IMSI of 15 digits that starts with
// testIMSIPrefix, or a NANP number +1 NXX 555 01XX, of the range
// 555-0100 to 555-0199 that NANPA keeps for fictional use. Digits
// without the "+" are read as the same number.
func checkTestNumber(user string) error {
	digits := strings.TrimPrefix(user, "+")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		if strings.HasPrefix(user, "+") {
			return errors.New("test data: not a fictional +1 NXX 555 01XX number")
		}
		return nil
	}
	switch {
	case len(digits) == 15 && !strings.HasPrefix(user, "+"):
		if !strings.HasPrefix(digits, testIMSIPrefix) {
			return errors.New("test data: an IMSI must be of the test PLMN 001/01")
		}
	case len(digits) == 11 && digits[0] == '1' && digits[1] >= '2' && digits[4:9] == "55501":
	default:
		return errors.New("test data: not a fictional +1 NXX 555 01XX number or a test IMSI")
	}
	return nil
}
