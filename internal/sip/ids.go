package sip

import "crypto/rand"

// rand.Text gives 26 base32 characters (130 random bits), which are all
// token characters, so the values below need no quoting or escaping.

// NewBranch returns a branch parameter for a new client transaction. It
// starts with the magic cookie and is unique across space and time (RFC
// 3261 8.1.1.7).
func NewBranch() string { return BranchMagicCookie + rand.Text() }

// NewTag returns a From or To tag. RFC 3261 19.3 asks for at least 32
// random bits; 10 characters carry 50.
func NewTag() string { return rand.Text()[:10] }

// NewCallID returns a Call-ID for a new call or registration (RFC 3261
// 8.1.1.4).
func NewCallID() string { return rand.Text() }
