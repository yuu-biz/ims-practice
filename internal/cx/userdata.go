package cx

import (
	"encoding/xml"
	"fmt"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// UserProfile is the part of the User-Data AVP that this project uses:
// the IMS subscription of TS 29.228 Annex E, Table E.2, with one
// service profile.
//
// SIMPLIFIED: TS 29.228 Annex E. The profile has a single
// ServiceProfile, and leaves out the optional elements other than
// InitialFilterCriteria, and the schema's XML namespace.
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
}

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
// BarringIndication, then Identity. A BarringIndication that is absent
// is false, so only a barred identity carries one.
type publicIdentity struct {
	BarringIndication *bool  `xml:"BarringIndication"`
	Identity          string `xml:"Identity"`
}

// Marshal writes the profile as the XML document that User-Data holds.
func (p UserProfile) Marshal() []byte {
	s := imsSubscription{PrivateID: p.PrivateIdentity}
	s.ServiceProfile.InitialFilterCriteria = p.InitialFilterCriteria
	for _, id := range p.PublicIdentities {
		pi := publicIdentity{Identity: id}
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
	}
	if len(p.PublicIdentities) == 0 {
		return UserProfile{}, fmt.Errorf("User-Data: no PublicIdentity")
	}
	return p, nil
}
