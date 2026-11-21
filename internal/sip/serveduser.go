package sip

import (
	"fmt"
	"strings"
)

// SessionCase is the session case of P-Served-User: which side of the
// call the S-CSCF serves the user on when it sends the request to an
// AS.
type SessionCase string

const (
	// SessionCaseNone is a P-Served-User without a session case.
	SessionCaseNone SessionCase = ""
	// SessionCaseOrig and SessionCaseTerm are the values of the sescase
	// parameter (RFC 5502 6).
	SessionCaseOrig SessionCase = "orig"
	SessionCaseTerm SessionCase = "term"
	// SessionCaseOrigCDIV is the originating session case after a
	// diversion. It is written as the orig-cdiv parameter, without
	// sescase (RFC 8498 6.2).
	SessionCaseOrigCDIV SessionCase = "orig-cdiv"
)

// Registration states of P-Served-User, the values of the regstate
// parameter (RFC 5502 6).
const (
	RegStateRegistered   = "reg"
	RegStateUnregistered = "unreg"
)

// PServedUser is the value of the P-Served-User header field (RFC 5502
// 6, RFC 8498 6.2). The S-CSCF adds it to a request that it sends to an
// AS, so that the AS knows whom it serves and in which session case,
// without guessing from the other header fields.
//
//	P-Served-User: <sip:user@example.com>;sescase=orig;regstate=reg
type PServedUser struct {
	// URI is the served user's public user identity.
	URI         string
	SessionCase SessionCase
	// RegState is RegStateRegistered or RegStateUnregistered, or ""
	// when the value has no regstate.
	RegState string
	// Params are the other parameters.
	Params Params
}

func ParsePServedUser(value string) (PServedUser, error) {
	na, err := ParseNameAddr(value)
	if err != nil {
		return PServedUser{}, fmt.Errorf("sip: P-Served-User: %w", err)
	}
	p := PServedUser{URI: na.URI}
	for _, param := range na.Params {
		switch strings.ToLower(param.Name) {
		case "sescase":
			switch sc := SessionCase(strings.ToLower(param.Value)); sc {
			case SessionCaseOrig, SessionCaseTerm:
				p.SessionCase = sc
			default:
				return PServedUser{}, fmt.Errorf("sip: P-Served-User: unknown sescase %q", param.Value)
			}
		case "orig-cdiv":
			p.SessionCase = SessionCaseOrigCDIV
		case "regstate":
			switch rs := strings.ToLower(param.Value); rs {
			case RegStateRegistered, RegStateUnregistered:
				p.RegState = rs
			default:
				return PServedUser{}, fmt.Errorf("sip: P-Served-User: unknown regstate %q", param.Value)
			}
		default:
			p.Params = append(p.Params, param)
		}
	}
	return p, nil
}

func (p PServedUser) String() string {
	var params Params
	switch p.SessionCase {
	case SessionCaseNone:
	case SessionCaseOrigCDIV:
		params.Set("orig-cdiv", "")
	default:
		params.Set("sescase", string(p.SessionCase))
	}
	if p.RegState != "" {
		params.Set("regstate", p.RegState)
	}
	return NameAddr{URI: p.URI, Params: append(params, p.Params...)}.String()
}

// Header is the P-Served-User header field with this value.
func (p PServedUser) Header() HeaderField {
	return HeaderField{Name: "P-Served-User", Value: p.String()}
}
