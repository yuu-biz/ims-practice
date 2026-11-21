package sh

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// fakeHSS answers each request with answer and keeps what it received,
// after a trip through the wire format.
type fakeHSS struct {
	t        *testing.T
	answer   func(request *diam.Message) *diam.Message
	received []*diam.Message
}

func (f *fakeHSS) Request(_ context.Context, request *diam.Message) (*diam.Message, error) {
	m := wire(f.t, request)
	f.received = append(f.received, m)
	return wire(f.t, f.answer(m)), nil
}

func newClient(t *testing.T, answer func(*diam.Message) *diam.Message) (*Client, *fakeHSS) {
	f := &fakeHSS{t: t, answer: answer}
	node := diameter.NewNode(diameter.Identity{OriginHost: ipsmgw, OriginRealm: realm}, []uint32{diameter.ApplicationIDSh}, diameter.Timers{}, slog.New(slog.DiscardHandler))
	return NewClient(node, f, realm), f
}

func TestClientPublicIdentitiesSendsAUDRByMSISDN(t *testing.T) {
	c, f := newClient(t, func(req *diam.Message) *diam.Message {
		data := ShData{PublicIdentifiers: &PublicIdentifiers{IMSPublicIdentities: []string{impu, tel}}}
		return UserDataAnswer{Result: cx.Base(cx.DiameterSuccess), UserData: data.Marshal()}.Message(req, hss)
	})
	ids, err := c.PublicIdentities(context.Background(), msisdn)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{impu, tel}) {
		t.Errorf("identities = %v", ids)
	}
	udr, err := ParseUserDataRequest(f.received[0])
	if err != nil {
		t.Fatal(err)
	}
	if udr.UserIdentity != (UserIdentity{MSISDN: msisdn}) || !reflect.DeepEqual(udr.DataReferences, []DataReference{IMSPublicIdentity}) ||
		udr.OriginHost != ipsmgw || udr.DestinationRealm != realm || udr.SessionID == "" {
		t.Errorf("UDR = %+v", udr)
	}
}

func TestClientReturnsTheResultOfAFailedAnswer(t *testing.T) {
	unknown := cx.Experimental(cx.DiameterErrorUserUnknown)
	c, _ := newClient(t, func(req *diam.Message) *diam.Message {
		return UserDataAnswer{Result: unknown}.Message(req, hss)
	})
	_, err := c.PublicIdentities(context.Background(), "12025550188")
	var re *ResultError
	if !errors.As(err, &re) || re.Result != unknown {
		t.Errorf("err = %v, want a ResultError with %v", err, unknown)
	}
}

func TestClientUpdateSMSRegistrationInfoSendsAPUR(t *testing.T) {
	c, f := newClient(t, func(req *diam.Message) *diam.Message {
		return ProfileUpdateAnswer{Result: cx.Base(cx.DiameterSuccess)}.Message(req, hss)
	})
	if err := c.UpdateSMSRegistrationInfo(context.Background(), UserIdentity{PublicIdentity: impu}, "12025550199"); err != nil {
		t.Fatal(err)
	}
	pur, err := ParseProfileUpdateRequest(f.received[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := ParseShData(pur.UserData)
	if err != nil {
		t.Fatal(err)
	}
	if pur.UserIdentity.PublicIdentity != impu || !reflect.DeepEqual(pur.DataReferences, []DataReference{SMSRegistrationInfo}) ||
		data.ShIMSData == nil || data.ShIMSData.SMSRegistrationInfo == nil || data.ShIMSData.SMSRegistrationInfo.IPSMGWNumber != "12025550199" {
		t.Errorf("PUR = %+v, data %+v", pur, data)
	}
}

func TestClientRefusesABadUserIdentityBeforeSending(t *testing.T) {
	c, f := newClient(t, nil)
	for _, id := range []UserIdentity{{}, {PublicIdentity: impu, MSISDN: msisdn}, {MSISDN: "+12025550101"}} {
		if _, err := c.UserData(context.Background(), id, IMSPublicIdentity); err == nil {
			t.Errorf("UserData(%+v) succeeded", id)
		}
	}
	if len(f.received) != 0 {
		t.Errorf("%d requests sent", len(f.received))
	}
}

// TestClientRepositoryData: each call sends the PUR or UDR that TS
// 29.328 6.1.1.1 and 6.1.2.1 describe, with the Sequence Number of the
// change.
func TestClientRepositoryData(t *testing.T) {
	stored := TransparentData{ServiceIndication: "MMTEL-Services", SequenceNumber: 65535, ServiceData: &ServiceData{XML: []byte("<a/>")}}
	c, f := newClient(t, func(req *diam.Message) *diam.Message {
		if req.Header.CommandCode == CommandUserData {
			return UserDataAnswer{Result: cx.Base(cx.DiameterSuccess), UserData: ShData{RepositoryData: []TransparentData{stored}}.Marshal()}.Message(req, hss)
		}
		return ProfileUpdateAnswer{Result: cx.Base(cx.DiameterSuccess)}.Message(req, hss)
	})
	ctx := context.Background()
	got, found, err := c.RepositoryData(ctx, impu, "MMTEL-Services")
	if err != nil || !found || !reflect.DeepEqual(got, stored) {
		t.Fatalf("RepositoryData = %+v, %v, %v", got, found, err)
	}
	if udr, _ := ParseUserDataRequest(f.received[0]); !reflect.DeepEqual(udr.ServiceIndications, []string{"MMTEL-Services"}) ||
		!reflect.DeepEqual(udr.DataReferences, []DataReference{RepositoryData}) || udr.UserIdentity.PublicIdentity != impu {
		t.Errorf("UDR = %+v", udr)
	}
	if err := c.CreateRepositoryData(ctx, impu, "MMTEL-Services", []byte("<a/>")); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateRepositoryData(ctx, impu, "MMTEL-Services", 65535, []byte("<b/>")); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRepositoryData(ctx, impu, "MMTEL-Services", 1); err != nil {
		t.Fatal(err)
	}
	for i, want := range []TransparentData{
		{ServiceIndication: "MMTEL-Services", SequenceNumber: 0, ServiceData: &ServiceData{XML: []byte("<a/>")}},
		{ServiceIndication: "MMTEL-Services", SequenceNumber: 1, ServiceData: &ServiceData{XML: []byte("<b/>")}},
		{ServiceIndication: "MMTEL-Services", SequenceNumber: 2},
	} {
		pur, err := ParseProfileUpdateRequest(f.received[i+1])
		if err != nil {
			t.Fatal(err)
		}
		data, err := ParseShData(pur.UserData)
		if err != nil || !reflect.DeepEqual(pur.DataReferences, []DataReference{RepositoryData}) || !reflect.DeepEqual(data.RepositoryData, []TransparentData{want}) {
			t.Errorf("PUR %d has %v and %+v, %v; want %+v", i, pur.DataReferences, data.RepositoryData, err, want)
		}
	}
}
