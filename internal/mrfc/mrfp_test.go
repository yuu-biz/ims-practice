package mrfc_test

import (
	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// Both clients of an MRFP carry out the procedures of Mp: the one of
// the HTTP API, and the H.248 Controller. The HTTP client also tells
// which announcements the MRFP holds.
var (
	_ mrfc.MRFP          = (*agw.Client)(nil)
	_ mrfc.MRFP          = (*mediactl.Controller)(nil)
	_ mrfc.Announcements = (*agw.Client)(nil)
)
