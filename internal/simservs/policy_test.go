package simservs

import "testing"

func TestValidateSimservsDocumentChecksTheTopLevel(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		want      ConflictCondition
	}{
		{"foreign element outside extensions",
			`<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><x:a xmlns:x="urn:example:vendor"/></simservs>`,
			SchemaValidationError},
		{"service after extensions",
			`<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><extensions/><communication-waiting/></simservs>`,
			SchemaValidationError},
		{"two extensions",
			`<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><extensions/><extensions/></simservs>`,
			SchemaValidationError},
		{"services of other specifications",
			`<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap"><communication-diversion-serv-cap/><communication-waiting/><extensions><x:a xmlns:x="urn:example:vendor"/></extensions></simservs>`,
			""},
	} {
		if got := conflictOf(ValidateSimservsDocument(mustDocument(t, tc.doc))); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
