package diameter

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// Result is the outcome of an answer: a base Result-Code (RFC 6733
// 7.1), or an Experimental-Result-Code when Experimental is true. A
// 3GPP application carries its own codes in Experimental-Result with
// Vendor-Id 10415 (RFC 6733 7.7), never in Result-Code, which is how a
// code such as 2001 of TS 29.229 6.2.1.1 differs from DIAMETER_SUCCESS.
type Result struct {
	Code         uint32
	Experimental bool
}

func (r Result) String() string {
	if r.Experimental {
		return fmt.Sprintf("Experimental-Result-Code %d", r.Code)
	}
	return fmt.Sprintf("Result-Code %d", r.Code)
}

// Success reports a 2xxx code of either kind.
func (r Result) Success() bool { return r.Code >= 2000 && r.Code < 3000 }

// Base returns a base protocol Result-Code.
func Base(code uint32) Result { return Result{Code: code} }

// Experimental returns an Experimental-Result-Code of a 3GPP
// application.
func Experimental(code uint32) Result { return Result{Code: code, Experimental: true} }

// AddTo writes the result as Result-Code, or as an Experimental-Result
// that holds the 3GPP Vendor-Id and the code. The answer format of each
// command decides where between the other AVPs it goes.
func (r Result) AddTo(m *diam.Message) {
	if r.Experimental {
		m.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(r.Code)),
		}})
		return
	}
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(r.Code))
}

// ResultAVPs reads the result of an answer from Result-Code or
// Experimental-Result. A parse function embeds it in the struct it
// unmarshals the answer into, so that one pass reads the result and the
// AVPs of the command.
type ResultAVPs struct {
	ResultCode         *uint32 `avp:"Result-Code,omitempty"`
	ExperimentalResult *struct {
		Code uint32 `avp:"Experimental-Result-Code"`
	} `avp:"Experimental-Result,omitempty"`
}

func (a ResultAVPs) Result() (Result, error) {
	switch {
	case a.ExperimentalResult != nil:
		return Experimental(a.ExperimentalResult.Code), nil
	case a.ResultCode != nil:
		return Base(*a.ResultCode), nil
	}
	return Result{}, fmt.Errorf("answer has neither Result-Code nor Experimental-Result")
}
