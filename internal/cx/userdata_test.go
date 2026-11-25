package cx

import (
	"errors"
	"testing"
)

// TestDefaultIdentityComparesCanonicalForms: a barred identity written
// in another form of the same URI is still the barred one, so the
// default public identity is the first identity that is not barred in
// any form (TS 29.228 6.5.1.1).
func TestDefaultIdentityComparesCanonicalForms(t *testing.T) {
	p := UserProfile{PublicIdentities: []string{"sip:+12025550101@ims.example.org;user=phone", "tel:+1-202-555-0101", "sip:alice@ims.example.org"},
		BarredIdentities: []string{"sip:+12025550101@IMS.EXAMPLE.ORG;user=phone", "tel:+12025550101"}}
	if got := p.DefaultIdentity(); got != "sip:alice@ims.example.org" {
		t.Errorf("default identity = %q, want sip:alice@ims.example.org", got)
	}
	p.PublicIdentities = p.PublicIdentities[:2]
	if err := p.Registrable(); !errors.Is(err, ErrNoDefaultIdentity) {
		t.Errorf("Registrable of a profile that bars both identities in other forms = %v, want ErrNoDefaultIdentity", err)
	}
}
