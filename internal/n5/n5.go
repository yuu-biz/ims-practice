// Package n5 is the Npcf_PolicyAuthorization service of TS 29.514
// (Release 20, V20.1.0): the N5 reference point between an AF, such as
// the P-CSCF, and the PCF of a 5GC. It is the 5GS counterpart of Rx
// (package rx): the AF creates an Individual Application Session
// Context with the service information of an IMS session, updates it
// when the SDP changes, and deletes it when the session ends; the PCF
// notifies the AF of events and asks it to end a context that is no
// longer valid (TS 29.514 4.2.1).
//
// The JSON types carry the attribute names of the OpenAPI file of TS
// 29.514 Annex A.2 (TS29514_Npcf_PolicyAuthorization.yaml) and of the
// common data types of TS 29.571. A Go field is the attribute name with
// its first letter in upper case, so that "ascReqData" in a capture is
// AscReqData here.
//
// The package has three parts:
//   - Client: what the AF calls (Create, Update, Delete).
//   - NewNotificationHandler: what the AF serves at its notifUri for the
//     PCF's notifications and termination requests.
//   - NewHandler and Notifier: the skeleton of a PCF, which decodes the
//     requests, hands them to a PCF implementation and encodes the
//     answers, and sends the notifications to the AF.
//
// None of them keeps state between requests: an application session
// context is named by its resource URI, which either side keeps in its
// own store, so that the state can live outside the node and several
// instances of each side can run.
//
// SIMPLIFIED: TS 29.514 Annex A.2. Only the attributes that the
// P-CSCF procedures of Annex B and the signalling path status of
// 4.2.6.7 use are declared; the others (traffic routing, TSC, QoS
// monitoring, sponsored data, MPS, MCPTT and so on) are left out. The
// P-CSCF uses none of them, and decoding ignores attributes it does not
// know, as every SBI receiver must for an extensible API, so a fuller
// peer can still talk to this one.
package n5

import (
	"fmt"
	"strconv"
	"strings"
)

// APIRoot is the path that every resource of the API starts with,
// after the apiRoot of the PCF (TS 29.514 5.3.1): the API name and its
// major version.
const APIRoot = "/npcf-policyauthorization/v1"

// AppSessionsPath is the Application Sessions collection (TS 29.514
// 5.3.1).
const AppSessionsPath = APIRoot + "/app-sessions"

// Feature is the number of a feature of the API in the supported
// features list of TS 29.514 5.8. Only the features this project
// negotiates are named.
type Feature int

const (
	// IMSSBI: the AF is an IMS node talking SBI (TS 29.514 5.8, feature
	// 5, "IMS_SBI"). It enables servInfStatus, afChargId and the
	// subscription to the signalling path status (4.2.6.7).
	IMSSBI Feature = 5
	// ProvAFsignalFlow: the AF provides the IP flows of its SIP
	// signalling (TS 29.514 5.8, feature 7).
	ProvAFsignalFlow Feature = 7
	// PatchCorrection: PATCH carries AppSessionContextUpdateDataPatch
	// (TS 29.514 5.8, feature 28). TS 29.514 4.2.3.2 describes the
	// modification of service information only for this feature.
	PatchCorrection Feature = 28
)

// SupportedFeatures is the SupportedFeatures type of TS 29.571 5.2.2: a
// bit mask in hexadecimal, where the last character stands for
// features 1 to 4 and each character to its left for the next four.
type SupportedFeatures string

// NewSupportedFeatures returns the mask with the given features set.
func NewSupportedFeatures(features ...Feature) SupportedFeatures {
	highest := 0
	for _, f := range features {
		highest = max(highest, int(f))
	}
	if highest == 0 {
		return ""
	}
	nibbles := make([]byte, (highest+3)/4)
	for _, f := range features {
		i := (int(f) - 1) / 4
		nibbles[len(nibbles)-1-i] |= 1 << ((int(f) - 1) % 4)
	}
	var b strings.Builder
	for _, n := range nibbles {
		b.WriteString(strconv.FormatUint(uint64(n), 16))
	}
	return SupportedFeatures(b.String())
}

// Has reports whether feature f is set. Characters that are not
// hexadecimal digits count as no feature.
func (s SupportedFeatures) Has(f Feature) bool {
	if f < 1 {
		return false
	}
	i := len(s) - 1 - (int(f)-1)/4
	if i < 0 {
		return false
	}
	n, err := strconv.ParseUint(string(s[i]), 16, 8)
	return err == nil && n&(1<<((int(f)-1)%4)) != 0
}

// BitRate is the BitRate type of TS 29.571 5.2.2: a number, a space and
// a unit from bps to Tbps, such as "64 Kbps". The units are powers of
// 1000, as its example "125 Mbps", "0.125 Gbps", "125000 Kbps" shows.
type BitRate string

var bitRateUnits = []string{"bps", "Kbps", "Mbps", "Gbps", "Tbps"}

// BitRateFromBps writes a bit rate in the largest unit that keeps the
// number at 1 or more, such as "41.5 Kbps" for 41500 bit/s.
func BitRateFromBps(bps uint64) BitRate {
	unit, scale := 0, uint64(1)
	for unit < len(bitRateUnits)-1 && bps >= scale*1000 {
		unit++
		scale *= 1000
	}
	whole := strconv.FormatUint(bps/scale, 10)
	if frac := bps % scale; frac != 0 {
		digits := len(strconv.FormatUint(scale, 10)) - 1
		f := strings.TrimRight(fmt.Sprintf("%0*d", digits, frac), "0")
		whole += "." + f
	}
	return BitRate(whole + " " + bitRateUnits[unit])
}

// Bps reads a bit rate as bit/s. Fractions of a bit/s are dropped.
func (r BitRate) Bps() (uint64, error) {
	number, unit, ok := strings.Cut(string(r), " ")
	if !ok {
		return 0, fmt.Errorf("n5: bit rate %q has no unit", string(r))
	}
	scale := uint64(1)
	found := false
	for _, u := range bitRateUnits {
		if u == unit {
			found = true
			break
		}
		scale *= 1000
	}
	if !found {
		return 0, fmt.Errorf("n5: bit rate %q has an unknown unit", string(r))
	}
	whole, frac, _ := strings.Cut(number, ".")
	w, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("n5: bit rate %q: %w", string(r), err)
	}
	bps := w * scale
	for _, c := range frac {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("n5: bit rate %q is not a number", string(r))
		}
		scale /= 10
		bps += uint64(c-'0') * scale
	}
	return bps, nil
}
