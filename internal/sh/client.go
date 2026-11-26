package sh

import (
	"context"
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// HSS is how an application server reaches the HSS over Sh.
// *diameter.PeerGroup, the instances of the HSS, is one.
type HSS interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// Client sends the Sh requests of an application server, such as the
// IP-SM-GW, and reads the answers.
type Client struct {
	node  *diameter.Node
	hss   HSS
	realm string
}

// NewClient returns a client that sends from node to hss. realm is the
// Destination-Realm, the home network of the users. Destination-Host
// is left out, as TS 29.229 5.5 allows when one HSS serves the realm
// (TS 29.328 6.5): here a server farm, every instance of which holds
// every subscriber (TS 29.228 6.4).
func NewClient(node *diameter.Node, hss HSS, realm string) *Client {
	return &Client{node: node, hss: hss, realm: realm}
}

// ResultError is an answer that did not succeed. Callers compare Result
// with the codes they handle, such as
// diameter.Experimental(cx.DiameterErrorUserUnknown).
type ResultError struct {
	Command string // "UDA" or "PUA"
	Result  diameter.Result
}

func (e *ResultError) Error() string { return fmt.Sprintf("sh: %s with %v", e.Command, e.Result) }

// UserData reads one data reference of a user with a UDR (Sh-Pull, TS
// 29.328 6.1.1). The ShData is empty when the HSS has none of the data.
func (c *Client) UserData(ctx context.Context, id UserIdentity, ref DataReference) (ShData, error) {
	return c.pull(ctx, UserDataRequest{UserIdentity: id, DataReferences: []DataReference{ref}})
}

// pull sends r, with the header filled in, and reads the data of the
// answer.
func (c *Client) pull(ctx context.Context, r UserDataRequest) (ShData, error) {
	if err := checkUserIdentity(r.UserIdentity); err != nil {
		return ShData{}, err
	}
	r.Header = diameter.NewHeader(c.node, "", c.realm)
	answer, err := c.hss.Request(ctx, r.Message())
	if err != nil {
		return ShData{}, err
	}
	uda, err := ParseUserDataAnswer(answer)
	if err != nil {
		return ShData{}, err
	}
	if !uda.Result.Success() {
		return ShData{}, &ResultError{Command: "UDA", Result: uda.Result}
	}
	if len(uda.UserData) == 0 {
		return ShData{}, nil
	}
	return ParseShData(uda.UserData)
}

// ProfileUpdate writes one data reference of a user with a PUR
// (Sh-Update, TS 29.328 6.1.2).
func (c *Client) ProfileUpdate(ctx context.Context, id UserIdentity, ref DataReference, data ShData) error {
	if err := checkUserIdentity(id); err != nil {
		return err
	}
	answer, err := c.hss.Request(ctx, ProfileUpdateRequest{
		Header:         diameter.NewHeader(c.node, "", c.realm),
		UserIdentity:   id,
		DataReferences: []DataReference{ref},
		UserData:       data.Marshal(),
	}.Message())
	if err != nil {
		return err
	}
	pua, err := ParseProfileUpdateAnswer(answer)
	if err != nil {
		return err
	}
	if !pua.Result.Success() {
		return &ResultError{Command: "PUA", Result: pua.Result}
	}
	return nil
}

// PublicIdentities returns the public identities of the user that has
// msisdn (TS 29.328 7.6.2). The SC of the IP-SM-GW uses it to turn the
// TP-DA of a short message into the user to deliver it to.
func (c *Client) PublicIdentities(ctx context.Context, msisdn string) ([]string, error) {
	data, err := c.UserData(ctx, UserIdentity{MSISDN: msisdn}, IMSPublicIdentity)
	if err != nil || data.PublicIdentifiers == nil {
		return nil, err
	}
	return data.PublicIdentifiers.IMSPublicIdentities, nil
}

// IMSI returns the IMSI of the user of publicID (TS 29.328 7.6.24), or
// "" when the HSS has none. The IP-SM-GW needs it to recognise the
// user of a short message that reaches it over SGd, which names the
// recipient by IMSI.
func (c *Client) IMSI(ctx context.Context, publicID string) (string, error) {
	data, err := c.UserData(ctx, UserIdentity{PublicIdentity: publicID}, IMSI)
	if err != nil || data.IMSI == nil {
		return "", err
	}
	return *data.IMSI, nil
}

// UpdateSMSRegistrationInfo registers the IP-SM-GW whose E.164 number
// is ipsmgwNumber (digits only) for the user's short messages, or
// removes the registration when it is empty (TS 29.328 6.1.2.1 step
// 4b).
//
// A registration announces that S6c reaches this IP-SM-GW, so that the
// HSS keeps its Diameter identity and can name it to an SMS-GMSC as the
// node that serves the user. A removal leaves the indication out: it
// registers no IP-SM-GW for the HSS to reach.
func (c *Client) UpdateSMSRegistrationInfo(ctx context.Context, id UserIdentity, ipsmgwNumber string) error {
	info := &SMSRegistrationInfoData{IPSMGWNumber: ipsmgwNumber}
	if ipsmgwNumber != "" {
		supported := S6cSupported
		info.S6cSupport = &supported
	}
	return c.ProfileUpdate(ctx, id, SMSRegistrationInfo, ShData{ShIMSData: &ShIMSData{SMSRegistrationInfo: info}})
}

// RepositoryData reads the repository data that the HSS keeps for the
// user of publicID under serviceIndication, such as the MMTEL-Services
// of TS 29.364 7.2.0 (Sh-Pull, TS 29.328 6.1.1, 7.6.1). Its
// SequenceNumber is what an update of it starts from. found is false
// when the HSS has none.
func (c *Client) RepositoryData(ctx context.Context, publicID, serviceIndication string) (data TransparentData, found bool, err error) {
	got, err := c.pull(ctx, UserDataRequest{
		UserIdentity:       UserIdentity{PublicIdentity: publicID},
		ServiceIndications: []string{serviceIndication},
		DataReferences:     []DataReference{RepositoryData},
	})
	if err != nil {
		return TransparentData{}, false, err
	}
	for _, d := range got.RepositoryData {
		if d.ServiceIndication == serviceIndication && d.ServiceData != nil {
			return d, true, nil
		}
	}
	return TransparentData{}, false, nil
}

// CreateRepositoryData stores serviceData, one XML element, as new
// repository data of the user of publicID, with Sequence Number 0 (Sh-
// Update, TS 29.328 6.1.2.1). If the HSS has data under
// serviceIndication already, it fails with a *ResultError of
// DiameterErrorTransparentDataOutOfSync.
func (c *Client) CreateRepositoryData(ctx context.Context, publicID, serviceIndication string, serviceData []byte) error {
	return c.updateRepositoryData(ctx, publicID, TransparentData{
		ServiceIndication: serviceIndication, SequenceNumber: 0, ServiceData: &ServiceData{XML: serviceData},
	})
}

// UpdateRepositoryData replaces the repository data that the AS read
// with Sequence Number read by serviceData (Sh-Update, TS 29.328
// 6.1.2.1). If the data has changed since, it fails with a *ResultError
// of DiameterErrorTransparentDataOutOfSync, and the AS reads it again
// (NOTE 5 there).
func (c *Client) UpdateRepositoryData(ctx context.Context, publicID, serviceIndication string, read uint16, serviceData []byte) error {
	return c.updateRepositoryData(ctx, publicID, TransparentData{
		ServiceIndication: serviceIndication, SequenceNumber: NextSequenceNumber(read), ServiceData: &ServiceData{XML: serviceData},
	})
}

// DeleteRepositoryData removes the repository data that the AS read
// with Sequence Number read: a PUR without ServiceData (TS 29.328
// 6.1.2.1). It fails as UpdateRepositoryData does.
func (c *Client) DeleteRepositoryData(ctx context.Context, publicID, serviceIndication string, read uint16) error {
	return c.updateRepositoryData(ctx, publicID, TransparentData{ServiceIndication: serviceIndication, SequenceNumber: NextSequenceNumber(read)})
}

func (c *Client) updateRepositoryData(ctx context.Context, publicID string, d TransparentData) error {
	return c.ProfileUpdate(ctx, UserIdentity{PublicIdentity: publicID}, RepositoryData, ShData{RepositoryData: []TransparentData{d}})
}

// checkUserIdentity refuses a User-Identity that TS 29.329 6.3.1 would
// not carry, before anything is sent.
func checkUserIdentity(id UserIdentity) error {
	switch {
	case id.PublicIdentity == "" && id.MSISDN == "":
		return fmt.Errorf("sh: User-Identity needs a public identity or an MSISDN")
	case id.PublicIdentity != "" && id.MSISDN != "":
		return fmt.Errorf("sh: User-Identity holds a public identity or an MSISDN, not both")
	case id.MSISDN != "" && !ValidMSISDN(id.MSISDN):
		return fmt.Errorf("sh: MSISDN %q is not 1 to 15 digits", id.MSISDN)
	}
	return nil
}
