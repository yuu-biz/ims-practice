package oam

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// The customized alerting tone of a user is the operator's to
// provision: TS 24.182 4.5.2 gives it to the operator, and 4.8 leaves
// the user no standard way to set it. The OAM provisions it through the
// HSS provisioning API, which keeps it as the Sh repository data that
// the MMTel AS of the user reads for each call to it. It is a change of
// one subscription, not of a node's configuration, so it is in no
// configuration history.

// AlertingToneRequest is the body of PUT
// /oam/v1/subscriptions/{impi}/customized-alerting-tone.
type AlertingToneRequest struct {
	Tone     string `json:"tone"`
	Active   bool   `json:"active"`
	Operator string `json:"operator,omitempty"`
}

// SetAlertingTone provisions the tone that the callers of the user of
// impi hear while it rings, or turns a provisioned one off, and leaves
// the rest of the subscription as it is. It answers the tone as the HSS
// stored it.
func (s *Server) SetAlertingTone(ctx context.Context, impi string, req AlertingToneRequest) (AlertingTone, error) {
	if s.hss == nil {
		return AlertingTone{}, fmt.Errorf("%w: no HSS provisioning API", ErrUnavailable)
	}
	if err := checkToneName(req.Tone); err != nil {
		return AlertingTone{}, err
	}
	s.log.Info("alerting tone requested", "private_identity", impi, "tone", req.Tone, "active", req.Active, "operator", req.Operator)
	ctx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
	defer cancel()
	stored, err := s.hss.setAlertingTone(ctx, impi, AlertingTone{Tone: req.Tone, Active: req.Active})
	if err != nil {
		return AlertingTone{}, hssLookupError(err)
	}
	return stored, nil
}

// checkToneName holds the name to what an MRFC can ask an MRFP for: RFC
// 4240 3.3 has announcement-id = 1*(ALPHA / DIGIT). The HSS checks it
// too; checking it here names the field that is wrong instead of
// passing the HSS's refusal on as an upstream failure.
func checkToneName(tone string) error {
	if tone == "" {
		return fmt.Errorf("%w: tone: required", ErrInvalidRequest)
	}
	if strings.ContainsFunc(tone, func(c rune) bool {
		return !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9')
	}) {
		return fmt.Errorf("%w: tone: %q is no announcement of an MRFP: letters and digits only", ErrInvalidRequest, tone)
	}
	return nil
}

func (s *Server) alertingToneRoutes(rt *routes) {
	rt.handle(http.MethodPut, PathPrefix+"subscriptions/{impi}/customized-alerting-tone", func(w http.ResponseWriter, r *http.Request) {
		var req AlertingToneRequest
		if err := readJSON(w, r, &req, false); err != nil {
			writeError(w, err)
			return
		}
		tone, err := s.SetAlertingTone(r.Context(), r.PathValue("impi"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tone)
	})
}
