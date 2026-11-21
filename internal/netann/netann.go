// Package netann reads the Request-URI of an INVITE to a media server by
// the conventions of RFC 4240, which an AS uses to drive the MRFC over
// Mr' (TS 24.229 10.2.2.2, 10.2.3.2). The user part names the service:
//
//	sip:annc@mrfc.example.net;play=/provisioned/notice;repeat=2;delay=500
//	sip:conf=4f1a@mrfc.example.net
//
// Parse is a pure function. Where the request does not stand, its error
// is the response that RFC 4240 asks the media server to answer with.
package netann

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The service names of RFC 4240 that this MRFC offers.
const (
	ServiceAnnouncement = "annc" // RFC 4240 3
	ServiceConference   = "conf" // RFC 4240 5
)

// Request is what the Request-URI asks for: exactly one of the fields is
// set.
type Request struct {
	Announcement *Announcement
	Conference   *Conference
}

// RepeatForever is the Repeat of repeat=forever.
const RepeatForever = -1

// Announcement is a request of the announcement service (RFC 4240 3).
type Announcement struct {
	// Prompt is the prompt-url of play=, as written: a URL, or
	// /provisioned/<id> for an announcement provisioned in the media
	// server (3.3).
	Prompt string
	// Repeat is the number of plays in all, or RepeatForever. RFC 4240 3
	// does not say whether repeat counts the first play; this package
	// takes repeat=n as n plays, and 0, like no repeat at all, as one.
	Repeat int
	// Delay is the silence between two plays, and Duration the most the
	// announcement may last, zero for no limit. The URI gives both in
	// milliseconds.
	Delay, Duration time.Duration
}

// Provisioned returns the announcement-id of a prompt-url of the form
// /provisioned/<id> (RFC 4240 3.3).
func (a Announcement) Provisioned() (string, bool) {
	id, ok := strings.CutPrefix(a.Prompt, "/provisioned/")
	if !ok || id == "" || strings.IndexFunc(id, notAlphanumeric) >= 0 {
		return "", false
	}
	return id, true
}

// Conference is a request of the conference service (RFC 4240 5): the
// first INVITE for an ID creates the mix, and each later one joins it.
type Conference struct {
	// ID is the instance-id after "conf=", in lower case: the service
	// indicator, and so the ID, is case-insensitive (RFC 4240 2).
	ID string
}

// Error is a request that the media server refuses, with the response
// code and reason phrase that RFC 4240 gives for it.
type Error struct {
	Status int
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("netann: %d %s", e.Status, e.Reason) }

// Service is the service name of the user part of u, in lower case:
// the part before "=" (RFC 4240 2), such as "annc" or "conf".
func Service(u sip.URI) string {
	name, _, _ := strings.Cut(unescape(u.User), "=")
	return strings.ToLower(name)
}

// Parse reads the Request-URI of an INVITE. Parameters that this
// package does not know are ignored, as RFC 4240 3 asks of extension
// parameters.
//
// SIMPLIFIED: RFC 4240 3. The locale, content-type and param1 to param9
// parameters are read past: each announcement has one variant, and
// announcement sequences with variables are not offered.
func Parse(u sip.URI) (Request, error) {
	name, instance, hasInstance := strings.Cut(unescape(u.User), "=")
	switch strings.ToLower(name) {
	case ServiceAnnouncement:
		if hasInstance {
			// annc-ind is "annc" alone (RFC 4240 3.3).
			return Request{}, &Error{488, "Not Acceptable Here"}
		}
		a, err := announcement(u.Params)
		if err != nil {
			return Request{}, err
		}
		return Request{Announcement: &a}, nil
	case ServiceConference:
		if instance == "" {
			// RFC 4240 5: conf without a conf-id.
			return Request{}, &Error{404, "Not Found"}
		}
		return Request{Conference: &Conference{ID: strings.ToLower(instance)}}, nil
	}
	// RFC 4240 2: a service this media server does not offer, such as
	// the dialog service of RFC 4240 4.
	return Request{}, &Error{488, "Not Acceptable Here"}
}

func announcement(params sip.Params) (Announcement, error) {
	var a Announcement
	play, ok := params.Get("play")
	if !ok || play == "" {
		return a, &Error{400, "Mandatory play parameter missing"}
	}
	a.Prompt = unescape(play)
	if v, ok := params.Get("repeat"); ok {
		if strings.EqualFold(v, "forever") {
			a.Repeat = RepeatForever
		} else if n, err := digits(v); err != nil {
			return a, &Error{400, "Invalid repeat parameter"}
		} else {
			a.Repeat = n
		}
	}
	for _, p := range []struct {
		name string
		to   *time.Duration
	}{{"delay", &a.Delay}, {"duration", &a.Duration}} {
		v, ok := params.Get(p.name)
		if !ok {
			continue
		}
		ms, err := digits(v)
		if err != nil {
			return a, &Error{400, "Invalid " + p.name + " parameter"}
		}
		*p.to = time.Duration(ms) * time.Millisecond
	}
	return a, nil
}

// digits reads 1*DIGIT (RFC 4240 3.3).
func digits(v string) (int, error) {
	if v == "" || strings.IndexFunc(v, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
		return 0, fmt.Errorf("netann: %q is not a number", v)
	}
	return strconv.Atoi(v)
}

func notAlphanumeric(c rune) bool {
	return !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9')
}

// unescape undoes the escapes of a URI part (RFC 3261 19.1.2), which
// compare equal to the characters they stand for (19.1.4). A part with
// a broken escape is kept as written.
func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}
