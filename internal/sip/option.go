package sip

import "slices"

// Option tags name the extensions that a UA lists in Supported, asks
// for in Require and refuses in Unsupported (RFC 3261 19.2).
const (
	// Option100rel is reliable provisional responses (RFC 3262 8.1).
	Option100rel = "100rel"
	// OptionPrecondition is preconditions in SDP (RFC 3312 11).
	OptionPrecondition = "precondition"
	// OptionTimer is the session timer (RFC 4028 12.3).
	OptionTimer = "timer"
	// Option199 is the 199 (Early Dialog Terminated) response (RFC 6228
	// 11.2).
	Option199 = "199"
	// OptionReplaces is the Replaces header field (RFC 3891 6.2).
	OptionReplaces = "replaces"
)

// Supports reports whether the sender of m understands the extension:
// its option tag is in Supported or in Require. A request that requires
// an extension also supports it, even where its Supported leaves the
// tag out.
func (m *Message) Supports(option string) bool {
	return slices.Contains(m.GetAll("Supported"), option) || m.Requires(option)
}

// Requires reports whether m lists the option tag in Require: the
// receiver has to understand the extension to process m (RFC 3261
// 20.32).
func (m *Message) Requires(option string) bool {
	return slices.Contains(m.GetAll("Require"), option)
}

// AddOption lists the option tag in the named header field, Supported
// or Require, unless it is there already.
func (m *Message) AddOption(name, option string) {
	if !slices.Contains(m.GetAll(name), option) {
		m.Add(name, option)
	}
}
