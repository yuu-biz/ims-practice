package ibcf_test

import (
	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
)

// Both clients of a TrGW carry out the procedures that the IBCF uses on
// Ix: the one of the HTTP API of the media relay, and the H.248
// Controller.
var (
	_ ibcf.TrGW = (*agw.Client)(nil)
	_ ibcf.TrGW = (*mediactl.Controller)(nil)
)
