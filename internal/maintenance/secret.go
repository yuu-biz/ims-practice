package maintenance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
)

// RedactedValue is what the API shows in place of a secret that is
// set.
const RedactedValue = "REDACTED"

// Secret stands for a secret in a configuration view: a password, or
// the K or OPc of an AKA subscription. It keeps only whether the secret
// is set, so a view that holds a Secret cannot hand out the secret
// itself, in JSON, in a log or in a print.
type Secret struct{ set bool }

// Redacted is the Secret for value: set unless value is empty.
func Redacted(value string) Secret { return Secret{set: value != ""} }

// IsSet says whether the secret had a value.
func (s Secret) IsSet() bool { return s.set }

// MarshalJSON writes RedactedValue for a secret that is set, and null
// for one that is not.
func (s Secret) MarshalJSON() ([]byte, error) {
	if !s.set {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(RedactedValue)), nil
}

func (s Secret) String() string {
	if !s.set {
		return ""
	}
	return RedactedValue
}

// LogValue makes slog write the secret as String does.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// secretNames are the field names, and the words of a field name, that
// hold a secret wherever they appear: the subscriber key K (Ki in GSM),
// the operator variant OP or OPc of Milenage (TS 35.206), and the
// derived CK and IK. So opc, OPc and last_opc are secret, and opcode is
// not.
var secretNames = []string{"k", "ki", "op", "opc", "ck", "ik"}

// secretWords mark a secret anywhere in a field name, such as password
// and ut_password, or nonce_key of the XCAP server.
var secretWords = []string{"password", "passwd", "secret", "token", "credential", "key"}

// isSecretName says whether a field of this name would hold a secret.
// Case does not matter, and _, -, . and space separate the words.
func isSecretName(name string) bool {
	lower := strings.ToLower(name)
	words := strings.FieldsFunc(lower, func(r rune) bool { return strings.ContainsRune("_-. ", r) })
	for _, w := range words {
		if slices.Contains(secretNames, w) {
			return true
		}
	}
	joined := strings.Join(words, "")
	for _, w := range secretWords {
		if strings.Contains(joined, w) {
			return true
		}
	}
	return false
}

// CheckRedacted marshals v to JSON and returns an error that names the
// first field with a secret's name (isSecretName) that holds a value:
// a string or a number anywhere under it, other than "" and
// RedactedValue. Booleans and null pass, so a view may say that a
// secret is set. A Secret passes, as it marshals to RedactedValue or
// null.
//
// The handler checks every configuration view and summary with it
// before it answers, and refuses to show one that fails; a role's test
// can call it on its view too. A secret under a field with an innocent
// name is not found: the view must hold a Secret for it.
func CheckRedacted(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return checkRedactedJSON(b)
}

func checkRedactedJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	return checkRedactedValue("$", doc, false)
}

// checkRedactedValue walks the decoded JSON. path is where v is, in the
// form $.hss.subscriptions[0].aka.k; secret says that a field above v
// has a secret's name.
func checkRedactedValue(path string, v any, secret bool) error {
	switch v := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if err := checkRedactedValue(path+"."+k, v[k], secret || isSecretName(k)); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range v {
			if err := checkRedactedValue(path+"["+strconv.Itoa(i)+"]", e, secret); err != nil {
				return err
			}
		}
	case string:
		if secret && v != "" && v != RedactedValue {
			return fmt.Errorf("%s holds a secret that is not redacted; use maintenance.Secret for it", path)
		}
	case json.Number:
		if secret {
			return fmt.Errorf("%s holds a secret that is not redacted; use maintenance.Secret for it", path)
		}
	}
	return nil
}
