package sipdns

import (
	"errors"
	"testing"
)

// The numbers are in the 555-0100 to 555-0199 range that the North
// American Numbering Plan keeps for fiction, so no real line is named.
var enumZone = []string{
	`1.0.1.0.5.5.5.2.0.2.1.e164.arpa. 60 IN NAPTR 10 100 "u" "E2U+sip" "!^.*$!sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org!" .`,
	`2.0.1.0.5.5.5.2.0.2.1.e164.arpa. 60 IN NAPTR 20 100 "u" "E2U+sip" "!^.*$!sip:second-choice@example.org!" .`,
	`2.0.1.0.5.5.5.2.0.2.1.e164.arpa. 60 IN NAPTR 10 100 "u" "E2U+sip" "!^\\+1(.*)$!sip:\\1@ims.mnc001.mcc001.3gppnetwork.org!" .`,
	`2.0.1.0.5.5.5.2.0.2.1.e164.arpa. 60 IN NAPTR 5 100 "u" "E2U+email:mailto" "!^.*$!mailto:x@example.org!" .`,
}

func TestApplicationUniqueString(t *testing.T) {
	if got, err := ApplicationUniqueString("+1-202-555-0101"); err != nil || got != "+12025550101" {
		t.Errorf("AUS = %q, %v", got, err)
	}
	for _, bad := range []string{"2025550101", "+", "+1-202-555-010a"} {
		if _, err := ApplicationUniqueString(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestENUMDomain(t *testing.T) {
	// RFC 6116 3.2 example.
	if got := ENUMDomain("+442079460148", ENUMSuffix); got != "8.4.1.0.6.4.9.7.0.2.4.4.e164.arpa." {
		t.Errorf("domain = %q", got)
	}
}

func TestENUM(t *testing.T) {
	r := newTestResolverWith(t, enumZone)
	tests := []struct {
		number string
		want   string
	}{
		{"+1-202-555-0101", "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"},
		// Order 5 is mailto and skipped; order 10 beats 20 and uses a
		// backreference.
		{"+12025550102", "sip:2025550102@ims.mnc001.mcc001.3gppnetwork.org"},
	}
	for _, tt := range tests {
		got, err := r.ENUM(t.Context(), tt.number, ENUMSuffix)
		if err != nil || got != tt.want {
			t.Errorf("ENUM(%s) = %q, %v; want %q", tt.number, got, err, tt.want)
		}
	}
	if _, err := r.ENUM(t.Context(), "+12025550198", ENUMSuffix); !errors.Is(err, ErrNoENUMRecord) {
		t.Errorf("number without records: err = %v, want ErrNoENUMRecord", err)
	}
}
