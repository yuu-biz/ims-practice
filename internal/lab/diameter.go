package lab

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// diameterMessage is the part of the DIAMETER (JSON) payload that the
// summary needs. The nodes write the whole message: every AVP with its
// name, code, vendor and value.
type diameterMessage struct {
	Command       string `json:"command"`
	Code          uint32 `json:"code"`
	ApplicationID uint32 `json:"application_id"`
	// Request is false for an answer, also when an encoder leaves the
	// false value out.
	Request bool          `json:"request"`
	AVPs    []diameterAVP `json:"avps"`
}

type diameterAVP struct {
	Code   uint32          `json:"code"`
	Vendor uint32          `json:"vendor"`
	Value  json.RawMessage `json:"value"`
	AVPs   []diameterAVP   `json:"avps"`
}

func parseDiameter(payload []byte) (diameterMessage, error) {
	var d diameterMessage
	if err := json.Unmarshal(payload, &d); err != nil {
		return diameterMessage{}, err
	}
	if d.Code == 0 {
		return diameterMessage{}, errors.New("diameter JSON without a command code")
	}
	return d, nil
}

// abbreviation is "UAR" or "UAA", from the short name that the
// dictionary gives the command, as in the logs of the nodes, or "" for
// a command the dictionary does not have.
func (d diameterMessage) abbreviation() string {
	short, ok := diameter.CommandShortName(d.ApplicationID, d.Code)
	if !ok {
		return ""
	}
	if d.Request {
		return short + "R"
	}
	return short + "A"
}

// summary is the abbreviation, followed for an answer by its result
// code: "UAR", "UAA 2001".
func (d diameterMessage) summary() string {
	s := d.abbreviation()
	if s == "" {
		s = d.Command + " Request"
		if !d.Request {
			s = d.Command + " Answer"
		}
	}
	if code, ok := d.resultCode(); ok && !d.Request {
		s += " " + strconv.FormatUint(uint64(code), 10)
	}
	return s
}

// AVP codes of the result. An answer carries either Result-Code (RFC
// 6733 7.1) or, for the codes of TS 29.229 6.2, Experimental-Result
// with an Experimental-Result-Code inside.
const (
	avpResultCode             = 268
	avpExperimentalResult     = 297
	avpExperimentalResultCode = 298
)

func (d diameterMessage) resultCode() (uint32, bool) {
	if code, ok := findUnsigned(d.AVPs, avpResultCode); ok {
		return code, true
	}
	for _, a := range d.AVPs {
		if a.Code == avpExperimentalResult && a.Vendor == 0 {
			return findUnsigned(a.AVPs, avpExperimentalResultCode)
		}
	}
	return 0, false
}

func findUnsigned(avps []diameterAVP, code uint32) (uint32, bool) {
	for _, a := range avps {
		if a.Code != code || a.Vendor != 0 {
			continue
		}
		var v uint32
		if err := json.Unmarshal(a.Value, &v); err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}
