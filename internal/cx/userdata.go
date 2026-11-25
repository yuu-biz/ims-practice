package cx

import (
	"encoding/xml"
	"errors"
	"fmt"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// UserProfile is the part of the User-Data AVP that this project uses:
// the IMS subscription of TS 29.228 Annex E, Table E.2, with one
// service profile.
//
// SIMPLIFIED: TS 29.228 Annex E. The profile has a single
// ServiceProfile. Of the optional elements it holds BarringIndication,
// the IdentityType and WildcardedPSI of a public service identity, and
// InitialFilterCriteria only, and it leaves out the schema's XML
// namespace.
type UserProfile struct {
	PrivateIdentity  string
	PublicIdentities []string
	// BarredIdentities are those of PublicIdentities whose
	// BarringIndication is set: the S-CSCF lets them register, and in
	// nothing else (TS 29.228 B.2.1). The default public identity is the
	// first of PublicIdentities that is not barred (6.5.1.1).
	BarredIdentities []string
	// InitialFilterCriteria are those of the service profile, which all
	// of PublicIdentities share (TS 29.228 B.2).
	InitialFilterCriteria []ifc.InitialFilterCriteria
	// WildcardedPSI, when not empty, makes PublicIdentities public
	// service identities that match this wildcarded PSI, as the HSS
	// stores it, delimiters included (TS 29.228 B.2.1). Empty, they are
	// distinct public user identities. A User-Data holds one kind or the
	// other (TS 29.228 6.1.2.1).
	WildcardedPSI string
}

// identityTypeWildcardedPSI is the IdentityType of a public service
// identity that matches a wildcarded PSI (TS 29.228 Table E.1). A
// public identity without an IdentityType is a distinct public user
// identity (B.2.1).
const identityTypeWildcardedPSI = 2

type imsSubscription struct {
	XMLName        xml.Name       `xml:"IMSSubscription"`
	PrivateID      string         `xml:"PrivateID"`
	ServiceProfile serviceProfile `xml:"ServiceProfile"`
}

// serviceProfile keeps the order of tServiceProfile: the public
// identities, then the criteria.
type serviceProfile struct {
	PublicIdentity        []publicIdentity            `xml:"PublicIdentity"`
	InitialFilterCriteria []ifc.InitialFilterCriteria `xml:"InitialFilterCriteria"`
}

// publicIdentity keeps the order of tPublicIdentity (TS 29.228 Annex E):
// BarringIndication, then Identity, then Extension. A BarringIndication
// that is absent is false, so only a barred identity carries one.
type publicIdentity struct {
	BarringIndication *bool                    `xml:"BarringIndication"`
	Identity          string                   `xml:"Identity"`
	Extension         *publicIdentityExtension `xml:"Extension"`
}

// publicIdentityExtension is tPublicIdentityExtension (TS 29.228 Table
// E.2): the type of the identity, and the wildcarded PSI it matches.
type publicIdentityExtension struct {
	IdentityType  int    `xml:"IdentityType"`
	WildcardedPSI string `xml:"WildcardedPSI,omitempty"`
}

// DefaultIdentity is the default public identity of the profile: the
// first of PublicIdentities that is not barred (TS 29.228 6.5.1.1), or
// "" when every one of them is barred.
func (p UserProfile) DefaultIdentity() string {
	for _, id := range p.PublicIdentities {
		if !slices.Contains(p.BarredIdentities, id) {
			return id
		}
	}
	return ""
}

// ErrNoDefaultIdentity is the error of a profile that bars every one of
// its public identities, and so has no default public identity.
var ErrNoDefaultIdentity = errors.New("User-Data: every public identity is barred, so none is the default public identity")

// Registrable checks that the profile can be that of a registered user,
// whose default public identity the S-CSCF takes from it (TS 29.228
// 6.5.1.1) and keeps, as a PPR does not change it (6.5.2.1). A profile
// that bars every identity has none. The HSS refuses to register such a
// set (6.1.1.1 step 4), but the profile of a user who is not registered
// may be one, and its barred identities are what keep it out of every
// session.
func (p UserProfile) Registrable() error {
	if p.DefaultIdentity() == "" {
		return ErrNoDefaultIdentity
	}
	return nil
}

// Marshal writes the profile as the XML document that User-Data holds.
func (p UserProfile) Marshal() []byte {
	s := imsSubscription{PrivateID: p.PrivateIdentity}
	s.ServiceProfile.InitialFilterCriteria = p.InitialFilterCriteria
	for _, id := range p.PublicIdentities {
		pi := publicIdentity{Identity: id}
		if p.WildcardedPSI != "" {
			pi.Extension = &publicIdentityExtension{IdentityType: identityTypeWildcardedPSI, WildcardedPSI: p.WildcardedPSI}
		}
		if slices.Contains(p.BarredIdentities, id) {
			barred := true
			pi.BarringIndication = &barred
		}
		s.ServiceProfile.PublicIdentity = append(s.ServiceProfile.PublicIdentity, pi)
	}
	b, err := xml.Marshal(s)
	if err != nil {
		panic(err) // the struct above always marshals
	}
	return append([]byte(xml.Header), b...)
}

// ParseUserProfile reads the XML document of a User-Data AVP. Criteria
// that break the rules of the schema make the whole profile unusable.
func ParseUserProfile(data []byte) (UserProfile, error) {
	var s imsSubscription
	if err := xml.Unmarshal(data, &s); err != nil {
		return UserProfile{}, fmt.Errorf("User-Data: %w", err)
	}
	if err := ifc.Validate(s.ServiceProfile.InitialFilterCriteria); err != nil {
		return UserProfile{}, fmt.Errorf("User-Data: %w", err)
	}
	p := UserProfile{PrivateIdentity: s.PrivateID, InitialFilterCriteria: s.ServiceProfile.InitialFilterCriteria}
	for _, id := range s.ServiceProfile.PublicIdentity {
		p.PublicIdentities = append(p.PublicIdentities, id.Identity)
		if id.BarringIndication != nil && *id.BarringIndication {
			p.BarredIdentities = append(p.BarredIdentities, id.Identity)
		}
		if e := id.Extension; e != nil && e.IdentityType == identityTypeWildcardedPSI {
			p.WildcardedPSI = e.WildcardedPSI
		}
	}
	if len(p.PublicIdentities) == 0 {
		return UserProfile{}, fmt.Errorf("User-Data: no PublicIdentity")
	}
	return p, nil
}
