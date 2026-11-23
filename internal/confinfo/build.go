package confinfo

import (
	"errors"
	"fmt"
	"math"
)

// Conference is what the conference focus knows about a conference, in
// the plain form the conference AS keeps in its Store.
type Conference struct {
	// URI is the conference URI the focus allocated (TS 24.147
	// 5.3.2.3.1), the entity of the document.
	URI         string
	DisplayText string
	// MaximumUserCount is how many users the conference takes.
	MaximumUserCount uint32
	Participants     []Participant
}

// Participant is one endpoint of one user in the conference, as the
// focus learns it from the dialog with that endpoint.
type Participant struct {
	// User is the user's identity, in IMS the P-Asserted-Identity of the
	// INVITE. It stays with the focus when AnonymousURI is set.
	User string
	// AnonymousURI, when it is set, names the user in the document in
	// place of User: the focus withholds the identity of a participant
	// that asked for privacy, and reveals it in no URI of the document
	// (RFC 4575 8.2). AnonymousName makes such a URI.
	AnonymousURI string
	// DisplayText is the user's name to show, such as the display name
	// of the P-Asserted-Identity.
	DisplayText string
	// Endpoint is the key of the endpoint among those of the user, which
	// the focus makes (RFC 4575 5.7): the endpoint's Contact or GRUU, or
	// a URI that the focus makes up.
	Endpoint      string
	Status        EndpointStatus
	JoiningMethod JoiningMethod
}

// Entity is the URI that names the user of the participant in the
// document (RFC 4575 5.6): the identity that the focus has for it, or
// the anonymous URI when the focus withholds that identity.
func (p Participant) Entity() string {
	if p.AnonymousURI != "" {
		return p.AnonymousURI
	}
	return p.User
}

// AnonymousName is how RFC 4575 5.6 has a focus name a participant
// whose identity it does not reveal: a URI whose host is in the
// .invalid top level domain, which RFC 2606 2 reserves and no DNS
// resolves, with a display text to match. The clause has the focus
// make the user part so that the URI is unique among the participants,
// so n counts the participants whose identity this focus has withheld.
// The form is the one the clause gives, which follows the anonymous
// URIs of RFC 3323 4.1.1.3.
func AnonymousName(n uint32) (uri, displayText string) {
	return fmt.Sprintf("sip:anonymous%d@anonymous.invalid", n), fmt.Sprintf("Anonymous%d", n)
}

// FullState builds the full conference document for one notification
// with the given version. It holds the elements GSMA IR.92 2.3.3 asks
// for as a minimum: the conference
// entity and maximum-user-count, and for each user its entity and
// display-text, and for each endpoint its entity and status.
//
// Participants with the same entity become endpoints of one user,
// because the entity of a user is unique in the document (RFC 4575
// 5.6). The user's display text is taken from its first participant.
// Each participant whose identity the focus withholds has an anonymous
// URI of its own, so two endpoints of one such user are two users here;
// the document then tells the subscribers one thing less about them.
//
// SIMPLIFIED: RFC 4575 3.2. The default policy is a full document in
// the NOTIFY that answers a SUBSCRIBE and only the changes after that.
// This package builds only full documents, which a subscriber must
// accept at any time (RFC 4575 4.6), so the focus sends the whole
// state every time. A conference of IR.92 has few users, so the
// documents stay small; a partial builder can come when a larger
// conference needs it.
func (c Conference) FullState(version uint32) Document {
	maximum := c.MaximumUserCount
	d := Document{
		Entity:  c.URI,
		State:   Full,
		Version: version,
		Description: &Description{
			DisplayText:      c.DisplayText,
			MaximumUserCount: &maximum,
		},
		Users: &Users{},
	}
	index := make(map[string]int)
	for _, p := range c.Participants {
		entity := p.Entity()
		i, ok := index[entity]
		if !ok {
			i = len(d.Users.Users)
			index[entity] = i
			d.Users.Users = append(d.Users.Users, User{Entity: entity, DisplayText: p.DisplayText})
		}
		d.Users.Users[i].Endpoints = append(d.Users.Users[i].Endpoints, Endpoint{
			Entity:        p.Endpoint,
			Status:        p.Status,
			JoiningMethod: p.JoiningMethod,
		})
	}
	return d
}

// NextVersion is the version of the next document sent within a
// subscription. RFC 4575 5.2 makes the version a 32-bit number that
// only goes up within a subscription. Wrapping around to zero would
// make the subscriber throw every later document away (RFC 4575 4.6),
// so the largest value is an error and the notifier has to end the
// subscription instead.
func NextVersion(v uint32) (uint32, error) {
	if v == math.MaxUint32 {
		return 0, errors.New("confinfo: version cannot go higher; end the subscription")
	}
	return v + 1, nil
}
