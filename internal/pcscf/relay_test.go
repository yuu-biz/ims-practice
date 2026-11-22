package pcscf_test

import (
	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
)

// Both clients of the media relay carry out the procedures of Iq: the
// one of the HTTP API, and the H.248 Controller.
var (
	_ pcscf.Relay = (*agw.Client)(nil)
	_ pcscf.Relay = (*mediactl.Controller)(nil)
)
