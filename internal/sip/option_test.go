package sip

import (
	"reflect"
	"testing"
)

func TestOptionTags(t *testing.T) {
	m := &Message{Method: INVITE, RequestURI: calleeURI}
	m.Add("Supported", "100rel, timer")
	m.Add("Supported", "199")
	m.Add("Require", "precondition")

	for _, option := range []string{Option100rel, OptionTimer, Option199, OptionPrecondition} {
		if !m.Supports(option) {
			t.Errorf("Supports(%q) = false", option)
		}
	}
	if m.Supports(OptionReplaces) {
		t.Error("Supports(replaces) = true for a message that does not list it")
	}
	if !m.Requires(OptionPrecondition) || m.Requires(Option100rel) {
		t.Errorf("Requires: precondition %v, 100rel %v; want true, false",
			m.Requires(OptionPrecondition), m.Requires(Option100rel))
	}

	m.AddOption("Require", OptionPrecondition)
	m.AddOption("Require", Option100rel)
	if got := m.GetAll("Require"); !reflect.DeepEqual(got, []string{"precondition", "100rel"}) {
		t.Errorf("Require = %q, want precondition once and then 100rel", got)
	}
}
