package pcscf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is what the P-CSCF, as an IMS-ALG, makes of the body of a
// message that may carry an SDP offer or answer (TS 24.229 6.7.2.1).

// errBody marks a body whose SDP the P-CSCF cannot point at the relay as
// it is, without asking the relay anything.
var errBody = errors.New("pcscf: the SDP of the body cannot be read")

var (
	// errMalformedSDP is an application/sdp body that does not parse.
	errMalformedSDP = fmt.Errorf("%w: the SDP does not parse", errBody)
	// errUnreadableBody is a body without a media type, which RFC 3261
	// 20.15 forbids, or one whose media type or multipart form does not
	// parse. It may be SDP, and the P-CSCF cannot tell.
	errUnreadableBody = fmt.Errorf("%w: the body has no readable media type", errBody)
	// errSDPInMultipart is a multipart body with an application/sdp part.
	errSDPInMultipart = fmt.Errorf("%w: the SDP is a part of a multipart body", errBody)
)

// sdpBodyError reports what keeps the P-CSCF from reading the SDP of m
// as it is: errUnreadableBody or errSDPInMultipart. It is nil for a
// message without a body, for an application/sdp body, and for a body
// of another type that carries no SDP. Requests and responses are read
// the same way: a request whose SDP the P-CSCF cannot read goes no
// further (refusalOf), and a response ends the call as one whose answer
// the relay did not take.
//
// SIMPLIFIED: RFC 5621 4.2 and TS 24.229 6.7.2.1. The P-CSCF does not
// point an SDP that is a part of a multipart body at the relay: it
// answers such a request 415 and ends the call of such a response. A UA
// is to read multipart bodies, and the SDP of an emergency call comes in
// one with the location of the UE. No node of this project sends a
// multipart body in an INVITE dialog.
func sdpBodyError(m *sip.Message) error {
	if len(m.Body) == 0 {
		return nil
	}
	value := m.Get("Content-Type")
	if strings.TrimSpace(value) == "" {
		return errUnreadableBody
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnreadableBody, err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return nil
	}
	has, err := multipartHasSDP(m.Body, params["boundary"], maxMultipartDepth)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %w", errUnreadableBody, err)
	case has:
		return errSDPInMultipart
	}
	return nil
}

// maxMultipartDepth is how deep multipartHasSDP looks into multipart
// parts within multipart parts. A body nested deeper is one that the
// P-CSCF does not read, so that a body cannot make it recurse without
// end; the bodies of SIP nest one or two levels (RFC 5621 4.2).
const maxMultipartDepth = 4

// multipartHasSDP reports whether the multipart body with boundary has
// an application/sdp part, also in a multipart part within it, down to
// depth levels (RFC 5621 4.2).
func multipartHasSDP(body []byte, boundary string, depth int) (bool, error) {
	if boundary == "" {
		return false, errors.New("the multipart body has no boundary")
	}
	if depth == 0 {
		return false, errors.New("the multipart body nests too deep")
	}
	r := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := r.NextPart()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		value := part.Header.Get("Content-Type")
		if value == "" {
			continue
		}
		mediaType, params, err := mime.ParseMediaType(value)
		if err != nil {
			return false, err
		}
		switch {
		case mediaType == "application/sdp":
			return true, nil
		case strings.HasPrefix(mediaType, "multipart/"):
			inner, err := io.ReadAll(part)
			if err != nil {
				return false, err
			}
			if has, err := multipartHasSDP(inner, params["boundary"], depth-1); has || err != nil {
				return has, err
			}
		}
	}
}

// carriesSDP reports whether the P-CSCF has to point the body of m at
// the relay context rc: an application/sdp body, or, when the call has
// a context, a body whose SDP it cannot read as it is (sdpBodyError) in
// a message that carries offers and answers. Those are INVITE and ACK
// (RFC 3261 13.2.1), UPDATE (RFC 3311) and PRACK (RFC 3262 5), and their
// responses. The body of any other message, such as an INFO or a BYE,
// goes on as it came, and so does such a body without a context.
func carriesSDP(m *sip.Message, rc *RelayContext) bool {
	if sip.HasSessionDescription(m) {
		return true
	}
	method := m.Method
	if m.StatusCode != 0 {
		cseq, _ := sip.ParseCSeq(m.Get("CSeq"))
		method = cseq.Method
	}
	switch method {
	case sip.INVITE, sip.ACK, sip.UPDATE, sip.PRACK:
		return rc != nil && sdpBodyError(m) != nil
	}
	return false
}

// withoutOffer reports whether m, a request within a dialog, is a
// re-INVITE that carries no offer, and so asks the party it reaches for
// one: the 2xx then carries the offer and the ACK the answer (RFC 3261
// 13.2.1, 14.1), and offerInResponse handles them. A body that the
// P-CSCF cannot read as it is may hold the offer, and carriesSDP has
// such a body handled as an offer when the call has a relay context.
func withoutOffer(m *sip.Message, rc *RelayContext) bool {
	return m.Method == sip.INVITE && !carriesSDP(m, rc)
}

// refusalOf is the response to req, a request whose SDP the P-CSCF
// could not point at the relay for err, with toTag in To for a request
// outside a dialog. The P-CSCF answers 400 (RFC 3261 21.4.1) to an SDP
// or a body that it cannot read, with a Reason-Phrase that says which,
// 415 with the type that it reads in Accept (RFC 3261 21.4.13) to an SDP
// in a multipart body, as a node that does not read multipart bodies
// answers (RFC 5621 4.2), and 500 (RFC 3261 21.5.1) when the relay
// failed.
func refusalOf(req *sip.Message, err error, toTag string) *sip.Message {
	switch {
	case errors.Is(err, errMalformedSDP):
		resp := sip.NewResponse(req, 400, toTag)
		resp.Reason = "Malformed SDP"
		return resp
	case errors.Is(err, errUnreadableBody):
		resp := sip.NewResponse(req, 400, toTag)
		resp.Reason = "Unreadable Message Body"
		return resp
	case errors.Is(err, errSDPInMultipart):
		resp := sip.NewResponse(req, 415, toTag)
		resp.Add("Accept", "application/sdp")
		return resp
	}
	return sip.NewResponse(req, 500, toTag)
}
