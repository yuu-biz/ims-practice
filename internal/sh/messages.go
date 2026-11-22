package sh

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// UserDataRequest is the UDR of TS 29.329 6.1.1. An application server
// reads user data from the HSS with it (Sh-Pull, TS 29.328 6.1.1).
type UserDataRequest struct {
	diameter.Header
	UserIdentity UserIdentity
	// ServerName is the SIP URI of the AS. TS 29.328 6.1.1.1 needs it
	// only to read initial filter criteria, which the HSS stub does not
	// serve, so it is usually empty.
	ServerName string
	// ServiceIndications name the repository data to read; a read of
	// RepositoryData has one (TS 29.328 6.1.1.1, 7.4).
	ServiceIndications []string
	DataReferences     []DataReference
	// IdentitySets narrow a read of IMSPublicIdentity. None means
	// ALL_IDENTITIES (TS 29.328 7.6.2).
	IdentitySets []IdentitySet
}

func (r UserDataRequest) Message() *diam.Message {
	m := diameter.NewRequest(diameter.ApplicationIDSh, CommandUserData)
	begin(m, r.Header)
	m.AddAVP(r.UserIdentity.avp())
	if r.ServerName != "" {
		m.AddAVP(diameter.VendorAVP(avpServerName, mv, datatype.UTF8String(r.ServerName)))
	}
	for _, s := range r.ServiceIndications {
		m.AddAVP(diameter.VendorAVP(avpServiceIndication, mv, datatype.OctetString(s)))
	}
	for _, d := range r.DataReferences {
		m.AddAVP(diameter.VendorAVP(avpDataReference, mv, datatype.Enumerated(d)))
	}
	for _, s := range r.IdentitySets {
		m.AddAVP(diameter.VendorAVP(avpIdentitySet, v, datatype.Enumerated(s)))
	}
	return m
}

func ParseUserDataRequest(m *diam.Message) (UserDataRequest, error) {
	var raw struct {
		diameter.Header
		UserIdentity       userIdentityAVP `avp:"User-Identity"`
		ServerName         string          `avp:"Server-Name,omitempty"`
		ServiceIndications []string        `avp:"Service-Indication,omitempty"`
		DataReferences     []int32         `avp:"Data-Reference,omitempty"`
		IdentitySets       []int32         `avp:"Identity-Set,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return UserDataRequest{}, fmt.Errorf("UDR: %w", err)
	}
	id, err := raw.UserIdentity.userIdentity()
	if err != nil {
		return UserDataRequest{}, fmt.Errorf("UDR: User-Identity: %w", err)
	}
	r := UserDataRequest{Header: raw.Header, UserIdentity: id, ServerName: raw.ServerName, ServiceIndications: raw.ServiceIndications}
	for _, d := range raw.DataReferences {
		r.DataReferences = append(r.DataReferences, DataReference(d))
	}
	for _, s := range raw.IdentitySets {
		r.IdentitySets = append(r.IdentitySets, IdentitySet(s))
	}
	return r, nil
}

// UserDataAnswer is the UDA of TS 29.329 6.1.2. UserData is the Sh-Data
// document (see ShData), or empty when the HSS has none of the data
// asked for (TS 29.328 6.1.1.1 step 5).
type UserDataAnswer struct {
	Result   diameter.Result
	UserData []byte
}

func (a UserDataAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	m := NewAnswer(request, local, a.Result)
	if len(a.UserData) > 0 {
		m.AddAVP(diameter.VendorAVP(avpUserData, mv, datatype.OctetString(a.UserData)))
	}
	return m
}

func ParseUserDataAnswer(m *diam.Message) (UserDataAnswer, error) {
	var raw struct {
		diameter.ResultAVPs
		UserData []byte `avp:"User-Data,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return UserDataAnswer{}, fmt.Errorf("UDA: %w", err)
	}
	result, err := raw.Result()
	if err != nil {
		return UserDataAnswer{}, fmt.Errorf("UDA: %w", err)
	}
	return UserDataAnswer{Result: result, UserData: raw.UserData}, nil
}

// ProfileUpdateRequest is the PUR of TS 29.329 6.1.3. An application
// server updates user data in the HSS with it (Sh-Update, TS 29.328
// 6.1.2). UserData holds the new data as an Sh-Data document.
type ProfileUpdateRequest struct {
	diameter.Header
	UserIdentity   UserIdentity
	DataReferences []DataReference
	UserData       []byte
}

func (r ProfileUpdateRequest) Message() *diam.Message {
	m := diameter.NewRequest(diameter.ApplicationIDSh, CommandProfileUpdate)
	begin(m, r.Header)
	m.AddAVP(r.UserIdentity.avp())
	for _, d := range r.DataReferences {
		m.AddAVP(diameter.VendorAVP(avpDataReference, mv, datatype.Enumerated(d)))
	}
	m.AddAVP(diameter.VendorAVP(avpUserData, mv, datatype.OctetString(r.UserData)))
	return m
}

func ParseProfileUpdateRequest(m *diam.Message) (ProfileUpdateRequest, error) {
	var raw struct {
		diameter.Header
		UserIdentity   userIdentityAVP `avp:"User-Identity"`
		DataReferences []int32         `avp:"Data-Reference,omitempty"`
		UserData       []byte          `avp:"User-Data"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		return ProfileUpdateRequest{}, fmt.Errorf("PUR: %w", err)
	}
	id, err := raw.UserIdentity.userIdentity()
	if err != nil {
		return ProfileUpdateRequest{}, fmt.Errorf("PUR: User-Identity: %w", err)
	}
	r := ProfileUpdateRequest{Header: raw.Header, UserIdentity: id, UserData: raw.UserData}
	for _, d := range raw.DataReferences {
		r.DataReferences = append(r.DataReferences, DataReference(d))
	}
	return r, nil
}

// ProfileUpdateAnswer is the PUA of TS 29.329 6.1.4.
type ProfileUpdateAnswer struct {
	Result diameter.Result
}

func (a ProfileUpdateAnswer) Message(request *diam.Message, local diameter.Identity) *diam.Message {
	return NewAnswer(request, local, a.Result)
}

func ParseProfileUpdateAnswer(m *diam.Message) (ProfileUpdateAnswer, error) {
	var raw diameter.ResultAVPs
	if err := m.Unmarshal(&raw); err != nil {
		return ProfileUpdateAnswer{}, fmt.Errorf("PUA: %w", err)
	}
	result, err := raw.Result()
	if err != nil {
		return ProfileUpdateAnswer{}, fmt.Errorf("PUA: %w", err)
	}
	return ProfileUpdateAnswer{Result: result}, nil
}
