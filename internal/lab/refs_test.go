package lab

import "testing"

// TestEveryReferenceNamesAClause: the details view shows the clause of
// each reference, so a reference without one points nowhere.
func TestEveryReferenceNamesAClause(t *testing.T) {
	var refs []Ref
	for _, r := range sipRefTable {
		refs = append(refs, r...)
	}
	for _, r := range diameterRefTable {
		refs = append(refs, r...)
	}
	for _, r := range ownCommandRefTable {
		refs = append(refs, r...)
	}
	for _, r := range netannRefTable {
		refs = append(refs, r...)
	}
	if len(refs) == 0 {
		t.Fatal("the reference tables are empty")
	}
	for _, r := range refs {
		if r.Spec == "" || r.Clause == "" {
			t.Errorf("reference %+v names no specification or no clause", r)
		}
	}
}
