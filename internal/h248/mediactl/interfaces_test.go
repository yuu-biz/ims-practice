package mediactl_test

import (
	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248/mediactl"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
)

// The Gateway declares Relay and Processor itself, so that the media
// gateway side does not depend on the nodes that control media. Each
// copy converts to the interface of its node and back, so neither has
// a method that the other lacks, and the HTTP client of the media node
// can stand behind a Gateway of either profile.
var (
	_ mediactl.Relay     = pcscf.Relay(nil)
	_ pcscf.Relay        = mediactl.Relay(nil)
	_ mediactl.Processor = mrfc.MRFP(nil)
	_ mrfc.MRFP          = mediactl.Processor(nil)
	_ mediactl.Relay     = (*agw.Client)(nil)
	_ mediactl.Processor = (*agw.Client)(nil)
)
