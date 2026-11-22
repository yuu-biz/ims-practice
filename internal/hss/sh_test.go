package hss

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

const (
	// msisdnA is the number of tel, the tel URI of store_test.go.
	msisdnA    = "12025550101"
	ipsmgwHost = "ipsmgw." + realm
	// ipsmgwNumber is the IP-SM-GW's own E.164 number, a fictitious one.
	ipsmgwNumber = "12025550199"
)

// shNetwork is the HSS listening for Diameter with Cx and Sh, and an
// IP-SM-GW connected to it with Sh only, as the two roles would run.
type shNetwork struct {
	hss  *HSS
	node *diameter.Node // the IP-SM-GW's
	peer *diameter.Peer // its connection to the HSS
}

func newShNetwork(t *testing.T) shNetwork {
	t.Helper()
	return newShNetworkWith(t,
		Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "test-password-1"},
		Subscription{PrivateIdentity: otherImp, PublicIdentities: []string{"sip:001010000000002@" + realm}, Password: "test-password-2"},
	)
}

func newShNetworkWith(t *testing.T, subscriptions ...Subscription) shNetwork {
	t.Helper()
	store, err := NewMemoryStore(subscriptions...)
	if err != nil {
		t.Fatal(err)
	}
	timers := diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, timers, quiet())
	h := New(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, hssNode, quiet())
	hssNode.Handle(h.Handle)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	node := diameter.NewNode(diameter.Identity{OriginHost: ipsmgwHost, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDSh}, timers, quiet())
	peer := node.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: "hss." + realm})
	t.Cleanup(peer.Stop)
	return shNetwork{hss: h, node: node, peer: peer}
}

// request sends m to the HSS, waiting for the CER/CEA if the
// connection is not open yet.
func (n shNetwork) request(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		answer, err := n.peer.Request(context.Background(), m)
		if errors.Is(err, diameter.ErrPeerUnavailable) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		return answer
	}
}

func (n shNetwork) udr(t *testing.T, id sh.UserIdentity, ref sh.DataReference, sets ...sh.IdentitySet) (diameter.Result, *sh.ShData) {
	t.Helper()
	a, err := sh.ParseUserDataAnswer(n.request(t, sh.UserDataRequest{
		Header: diameter.NewHeader(n.node, "", realm), UserIdentity: id,
		DataReferences: []sh.DataReference{ref}, IdentitySets: sets,
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.UserData) == 0 {
		return a.Result, nil
	}
	data, err := sh.ParseShData(a.UserData)
	if err != nil {
		t.Fatal(err)
	}
	return a.Result, &data
}

func (n shNetwork) pur(t *testing.T, id sh.UserIdentity, refs []sh.DataReference, data []byte) diameter.Result {
	t.Helper()
	a, err := sh.ParseProfileUpdateAnswer(n.request(t, sh.ProfileUpdateRequest{
		Header: diameter.NewHeader(n.node, "", realm), UserIdentity: id, DataReferences: refs, UserData: data,
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a.Result
}

func smsRegistration(number string) []byte {
	return sh.ShData{ShIMSData: &sh.ShIMSData{SMSRegistrationInfo: &sh.SMSRegistrationInfoData{IPSMGWNumber: number}}}.Marshal()
}

var success = diameter.Base(diameter.DiameterSuccess)

// The SC stub turns the TP-DA of a short message into the recipient's
// public identities with this UDR.
func TestUserDataFindsThePublicIdentitiesOfAnMSISDN(t *testing.T) {
	n := newShNetwork(t)
	result, data := n.udr(t, sh.UserIdentity{MSISDN: msisdnA}, sh.IMSPublicIdentity)
	if result != success || data == nil || data.PublicIdentifiers == nil {
		t.Fatalf("UDA = %v, %+v", result, data)
	}
	if got := data.PublicIdentifiers.IMSPublicIdentities; len(got) != 2 || got[0] != impu || got[1] != tel {
		t.Errorf("IMSPublicIdentity = %v, want [%s %s]", got, impu, tel)
	}

	// TS 29.328 7.6.2: REGISTERED_IDENTITIES holds nothing while the
	// user is not registered, and without Notif-Eff no data means no
	// User-Data (6.1.1.1 step 5).
	if result, data := n.udr(t, sh.UserIdentity{MSISDN: msisdnA}, sh.IMSPublicIdentity, sh.RegisteredIdentities); result != success || data != nil {
		t.Errorf("REGISTERED_IDENTITIES of an unregistered user = %v, %+v; want success without User-Data", result, data)
	}
}

func TestUserDataReadsTheMSISDN(t *testing.T) {
	n := newShNetwork(t)
	result, data := n.udr(t, sh.UserIdentity{PublicIdentity: impu}, sh.MSISDN)
	if result != success || data == nil || data.PublicIdentifiers == nil || len(data.PublicIdentifiers.MSISDNs) != 1 ||
		data.PublicIdentifiers.MSISDNs[0] != msisdnA {
		t.Fatalf("UDA = %v, %+v; want MSISDN %s", result, data, msisdnA)
	}
	// The other subscriber has no tel URI, and so no MSISDN.
	if result, data := n.udr(t, sh.UserIdentity{PublicIdentity: "sip:001010000000002@" + realm}, sh.MSISDN); result != success || data != nil {
		t.Errorf("UDA for a user without MSISDN = %v, %+v", result, data)
	}
}

// TestTheMSISDNOfATelURIWithSeparators: the MSISDN that Sh gives is
// the one that the profile of the SAA carries, the canonical number of
// the tel URI (TS 29.229 6.3.2), whatever visual separators and
// parameters the configuration wrote (RFC 3966 3).
func TestTheMSISDNOfATelURIWithSeparators(t *testing.T) {
	n := newShNetworkWith(t, Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+1-202-555-0101;foo=bar"}, Password: "p"})
	result, data := n.udr(t, sh.UserIdentity{PublicIdentity: impu}, sh.MSISDN)
	if result != success || data == nil || data.PublicIdentifiers == nil || len(data.PublicIdentifiers.MSISDNs) != 1 ||
		data.PublicIdentifiers.MSISDNs[0] != msisdnA {
		t.Fatalf("UDA = %v, %+v; want MSISDN %s", result, data, msisdnA)
	}
	if result, data := n.udr(t, sh.UserIdentity{MSISDN: msisdnA}, sh.IMSPublicIdentity); result != success || data == nil {
		t.Errorf("UDR by MSISDN %s = %v, %+v; want the user", msisdnA, result, data)
	}
}

func TestSubscriptionMSISDN(t *testing.T) {
	for _, tt := range []struct {
		ids  []string
		want string
	}{
		{[]string{impu, "tel:+12025550101"}, "12025550101"},
		{[]string{impu, "tel:+1-202-555-0101"}, "12025550101"},
		{[]string{impu, "tel:+1.202.555.0101;foo=bar"}, "12025550101"},
		// A local number is no MSISDN; the next tel URI is.
		{[]string{"tel:5550101;phone-context=example.com", "tel:+12025550102"}, "12025550102"},
		{[]string{"tel:+1202555010212345"}, ""},
		{[]string{impu}, ""},
	} {
		if got := (Subscription{PublicIdentities: tt.ids}).MSISDN(); got != tt.want {
			t.Errorf("MSISDN of %v = %q, want %q", tt.ids, got, tt.want)
		}
	}
}

func TestUserDataReadsTheSCSCFName(t *testing.T) {
	n := newShNetwork(t)
	if result, data := n.udr(t, sh.UserIdentity{PublicIdentity: impu}, sh.SCSCFName); result != success || data != nil {
		t.Errorf("before registration: %v, %+v; want success without User-Data", result, data)
	}
	mar(t, n.hss, scscf)
	sar(t, n.hss, scscf, cx.AssignRegistration)
	result, data := n.udr(t, sh.UserIdentity{PublicIdentity: tel}, sh.SCSCFName)
	if result != success || data == nil || data.ShIMSData == nil || data.ShIMSData.SCSCFName != scscf {
		t.Errorf("after registration: %v, %+v; want %s", result, data, scscf)
	}
}

// The errors come from the steps of TS 29.328 6.1.1.1 and 6.1.2.1 and
// their codes from TS 29.329 6.2 and TS 29.229 6.2.
func TestShErrors(t *testing.T) {
	n := newShNetwork(t)
	byMSISDN := sh.UserIdentity{MSISDN: msisdnA}
	tests := []struct {
		name string
		got  func() diameter.Result
		want diameter.Result
	}{
		{"unknown MSISDN", func() diameter.Result {
			r, _ := n.udr(t, sh.UserIdentity{MSISDN: "12025550188"}, sh.IMSPublicIdentity)
			return r
		}, diameter.Experimental(cx.DiameterErrorUserUnknown)},
		{"unknown public identity", func() diameter.Result {
			r, _ := n.udr(t, sh.UserIdentity{PublicIdentity: "sip:nobody@" + realm}, sh.MSISDN)
			return r
		}, diameter.Experimental(cx.DiameterErrorUserUnknown)},
		// Table 7.6.1: an MSISDN is not a key of S-CSCFName (step 3).
		{"S-CSCFName by MSISDN", func() diameter.Result {
			r, _ := n.udr(t, byMSISDN, sh.SCSCFName)
			return r
		}, diameter.Experimental(sh.DiameterErrorOperationNotAllowed)},
		// InitialFilterCriteria (13) is not in the stub's permissions
		// list (step 1).
		{"data the stub does not serve", func() diameter.Result {
			r, _ := n.udr(t, byMSISDN, sh.DataReference(13))
			return r
		}, diameter.Experimental(sh.DiameterErrorUserDataCannotBeRead)},
		// Table 7.6.1 allows only Sh-Pull of IMSPublicIdentity.
		{"update of read-only data", func() diameter.Result {
			return n.pur(t, byMSISDN, []sh.DataReference{sh.IMSPublicIdentity}, smsRegistration(ipsmgwNumber))
		}, diameter.Experimental(sh.DiameterErrorUserDataCannotBeModified)},
		{"update without SMSRegistrationInfo", func() diameter.Result {
			return n.pur(t, byMSISDN, []sh.DataReference{sh.SMSRegistrationInfo}, sh.ShData{}.Marshal())
		}, diameter.Base(diameter.DiameterUnableToComply)},
		{"two data references without Update-Eff-Enhance", func() diameter.Result {
			return n.pur(t, byMSISDN, []sh.DataReference{sh.SMSRegistrationInfo, sh.SMSRegistrationInfo}, smsRegistration(ipsmgwNumber))
		}, diameter.Base(diameter.DiameterUnableToComply)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(); got != tt.want {
				t.Errorf("result = %v, want %v", got, tt.want)
			}
		})
	}
}

// The IP-SM-GW registers itself for a user with a PUR (TS 29.328
// 6.1.2.1 step 4b), and anyone allowed can read it back.
func TestProfileUpdateRegistersTheIPSMGW(t *testing.T) {
	n := newShNetwork(t)
	sms := []sh.DataReference{sh.SMSRegistrationInfo}
	if r := n.pur(t, sh.UserIdentity{PublicIdentity: impu}, sms, smsRegistration(ipsmgwNumber)); r != success {
		t.Fatalf("PUR = %v", r)
	}
	readBack := func() string {
		t.Helper()
		result, data := n.udr(t, sh.UserIdentity{MSISDN: msisdnA}, sh.SMSRegistrationInfo)
		if result != success {
			t.Fatalf("UDA = %v", result)
		}
		if data == nil {
			return ""
		}
		return data.ShIMSData.SMSRegistrationInfo.IPSMGWNumber
	}
	if got := readBack(); got != ipsmgwNumber {
		t.Errorf("IP-SM-GW-Number = %q, want %q", got, ipsmgwNumber)
	}

	// A registration in between writes the subscriber again and keeps
	// the IP-SM-GW.
	mar(t, n.hss, scscf)
	sar(t, n.hss, scscf, cx.AssignRegistration)
	if got := readBack(); got != ipsmgwNumber {
		t.Errorf("after a SAR, IP-SM-GW-Number = %q, want %q", got, ipsmgwNumber)
	}

	// An empty IP-SM-GW-Number removes the registration.
	if r := n.pur(t, sh.UserIdentity{MSISDN: msisdnA}, sms, smsRegistration("")); r != success {
		t.Fatalf("PUR with an empty number = %v", r)
	}
	if got := readBack(); got != "" {
		t.Errorf("after removal, IP-SM-GW-Number = %q", got)
	}
}

// The client an AS uses, over a real connection: the IP-SM-GW
// registers, then its SC finds the recipient by MSISDN.
func TestShClientAgainstTheHSS(t *testing.T) {
	n := newShNetwork(t)
	// The first request waits for the CER/CEA; the client does not.
	n.udr(t, sh.UserIdentity{MSISDN: msisdnA}, sh.MSISDN)
	c := sh.NewClient(n.node, n.peer, realm)
	ctx := context.Background()
	if err := c.UpdateSMSRegistrationInfo(ctx, sh.UserIdentity{PublicIdentity: impu}, ipsmgwNumber); err != nil {
		t.Fatal(err)
	}
	ids, err := c.PublicIdentities(ctx, msisdnA)
	if err != nil || len(ids) != 2 || ids[0] != impu {
		t.Errorf("PublicIdentities = %v, %v", ids, err)
	}
	data, err := c.UserData(ctx, sh.UserIdentity{MSISDN: msisdnA}, sh.SMSRegistrationInfo)
	if err != nil || data.ShIMSData == nil || data.ShIMSData.SMSRegistrationInfo.IPSMGWNumber != ipsmgwNumber {
		t.Errorf("SMSRegistrationInfo = %+v, %v", data, err)
	}
	var re *sh.ResultError
	if _, err := c.PublicIdentities(ctx, "12025550188"); !errors.As(err, &re) || re.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("unknown MSISDN: %v", err)
	}
}

const mmtelServices = "MMTEL-Services"

// readRepository reads the repository data of id under serviceIndication
// with a UDR.
func (n shNetwork) readRepository(t *testing.T, id sh.UserIdentity, serviceIndication string) (diameter.Result, []sh.TransparentData) {
	t.Helper()
	a, err := sh.ParseUserDataAnswer(n.request(t, sh.UserDataRequest{
		Header: diameter.NewHeader(n.node, "", realm), UserIdentity: id,
		ServiceIndications: []string{serviceIndication}, DataReferences: []sh.DataReference{sh.RepositoryData},
	}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.UserData) == 0 {
		return a.Result, nil
	}
	data, err := sh.ParseShData(a.UserData)
	if err != nil {
		t.Fatal(err)
	}
	return a.Result, data.RepositoryData
}

// writeRepository sends one RepositoryData in a PUR.
func (n shNetwork) writeRepository(t *testing.T, id sh.UserIdentity, d sh.TransparentData) diameter.Result {
	t.Helper()
	return n.pur(t, id, []sh.DataReference{sh.RepositoryData}, sh.ShData{RepositoryData: []sh.TransparentData{d}}.Marshal())
}

func repository(seq uint16, data string) sh.TransparentData {
	d := sh.TransparentData{ServiceIndication: mmtelServices, SequenceNumber: seq}
	if data != "" {
		d.ServiceData = &sh.ServiceData{XML: []byte(data)}
	}
	return d
}

// TestRepositoryData walks the Sh-Update rules of TS 29.328 6.1.2.1
// step 6 and the Sh-Pull of 6.1.1.1 for one Service-Indication: new
// data comes with Sequence Number 0, each change with the next one, a
// change from another version is out of sync, and an update without
// ServiceData removes the data.
func TestRepositoryData(t *testing.T) {
	n := newShNetwork(t)
	user := sh.UserIdentity{PublicIdentity: impu}
	v0 := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/mmtel"><complete-communication-diversion/></MMTelServices>`
	v1 := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/mmtel"/>`
	outOfSync := diameter.Experimental(sh.DiameterErrorTransparentDataOutOfSync)
	notAllowed := diameter.Experimental(sh.DiameterErrorOperationNotAllowed)

	if result, data := n.readRepository(t, user, mmtelServices); result != success || data != nil {
		t.Fatalf("read before any data: %v, %+v", result, data)
	}
	for _, step := range []struct {
		name string
		pur  sh.TransparentData
		want diameter.Result
		// stored is what a read gives afterwards; nil for nothing.
		stored *sh.TransparentData
	}{
		{"change of nothing", repository(1, v0), outOfSync, nil},
		{"remove nothing", repository(0, ""), notAllowed, nil},
		{"create", repository(0, v0), success, ptr(repository(0, v0))},
		{"create again", repository(0, v1), outOfSync, ptr(repository(0, v0))},
		{"skip a version", repository(2, v1), outOfSync, ptr(repository(0, v0))},
		{"change", repository(1, v1), success, ptr(repository(1, v1))},
		{"same change again", repository(1, v1), outOfSync, ptr(repository(1, v1))},
		{"too much", repository(2, "<a>"+strings.Repeat("x", maxServiceData)+"</a>"), diameter.Experimental(sh.DiameterErrorTooMuchData), ptr(repository(1, v1))},
		{"remove", repository(2, ""), success, nil},
	} {
		if got := n.writeRepository(t, user, step.pur); got != step.want {
			t.Errorf("%s: PUA %v, want %v", step.name, got, step.want)
		}
		result, data := n.readRepository(t, user, mmtelServices)
		if want := []sh.TransparentData(nil); step.stored != nil {
			want = []sh.TransparentData{*step.stored}
			if result != success || !reflect.DeepEqual(data, want) {
				t.Errorf("%s: read %v, %+v; want %+v", step.name, result, data, want)
			}
		} else if result != success || data != nil {
			t.Errorf("%s: read %v, %+v; want nothing", step.name, result, data)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// TestRepositoryDataOfAliases: the data is the subscription's, so the
// tel URI reads what the SIP URI wrote (TS 29.328 6.1.2.1 step 6), while
// another subscription and another Service-Indication have their own.
// An MSISDN is no key of repository data (Table 7.6.1).
func TestRepositoryDataOfAliases(t *testing.T) {
	n := newShNetwork(t)
	if got := n.writeRepository(t, sh.UserIdentity{PublicIdentity: impu}, repository(0, "<a/>")); got != success {
		t.Fatalf("create: %v", got)
	}
	if _, data := n.readRepository(t, sh.UserIdentity{PublicIdentity: tel}, mmtelServices); len(data) != 1 || string(data[0].ServiceData.XML) != "<a/>" {
		t.Errorf("read by the tel URI: %+v", data)
	}
	if _, data := n.readRepository(t, sh.UserIdentity{PublicIdentity: "sip:001010000000002@" + realm}, mmtelServices); data != nil {
		t.Errorf("another subscription read %+v", data)
	}
	if _, data := n.readRepository(t, sh.UserIdentity{PublicIdentity: impu}, "Ut-Credentials"); data != nil {
		t.Errorf("another Service-Indication read %+v", data)
	}
	if result, _ := n.readRepository(t, sh.UserIdentity{MSISDN: msisdnA}, mmtelServices); result != diameter.Experimental(sh.DiameterErrorOperationNotAllowed) {
		t.Errorf("read by MSISDN: %v, want DIAMETER_ERROR_OPERATION_NOT_ALLOWED", result)
	}
}

// TestRepositoryDataWrapsAround: after 65535 the next change has 1
// (TS 29.328 6.1.2.1), never 0.
func TestRepositoryDataWrapsAround(t *testing.T) {
	n := newShNetwork(t)
	// Sh would take 65535 updates to get there, so the data goes into
	// the store as one that had them.
	if ok, err := n.hss.store.CompareAndSwapRepositoryData(context.Background(), impi, nil, repository(65535, "<a/>")); err != nil || !ok {
		t.Fatalf("cannot store the data: %v, %v", ok, err)
	}
	user := sh.UserIdentity{PublicIdentity: impu}
	if got := n.writeRepository(t, user, repository(0, "<b/>")); got != diameter.Experimental(sh.DiameterErrorTransparentDataOutOfSync) {
		t.Errorf("0 after 65535: %v", got)
	}
	if got := n.writeRepository(t, user, repository(1, "<b/>")); got != success {
		t.Errorf("1 after 65535: %v", got)
	}
}

// TestShClientRepositoryDataAgainstTheHSS is the round trip that the
// MMTel AS makes: create, read, update from the version read, and a
// second update from the same version, which is out of sync.
func TestShClientRepositoryDataAgainstTheHSS(t *testing.T) {
	n := newShNetwork(t)
	n.readRepository(t, sh.UserIdentity{PublicIdentity: impu}, mmtelServices) // waits for the CER/CEA
	c := sh.NewClient(n.node, n.peer, realm)
	ctx := context.Background()
	if err := c.CreateRepositoryData(ctx, impu, mmtelServices, []byte("<a/>")); err != nil {
		t.Fatal(err)
	}
	read, found, err := c.RepositoryData(ctx, tel, mmtelServices)
	if err != nil || !found || read.SequenceNumber != 0 || string(read.ServiceData.XML) != "<a/>" {
		t.Fatalf("RepositoryData = %+v, %v, %v", read, found, err)
	}
	if err := c.UpdateRepositoryData(ctx, impu, mmtelServices, read.SequenceNumber, []byte("<b/>")); err != nil {
		t.Fatal(err)
	}
	var re *sh.ResultError
	if err := c.UpdateRepositoryData(ctx, impu, mmtelServices, read.SequenceNumber, []byte("<c/>")); !errors.As(err, &re) ||
		re.Result != diameter.Experimental(sh.DiameterErrorTransparentDataOutOfSync) {
		t.Errorf("second update from the same version: %v", err)
	}
	read, _, _ = c.RepositoryData(ctx, impu, mmtelServices)
	if read.SequenceNumber != 1 || string(read.ServiceData.XML) != "<b/>" {
		t.Errorf("after the update: %+v", read)
	}
	if err := c.DeleteRepositoryData(ctx, impu, mmtelServices, read.SequenceNumber); err != nil {
		t.Fatal(err)
	}
	if _, found, err := c.RepositoryData(ctx, impu, mmtelServices); found || err != nil {
		t.Errorf("after the removal: found %v, %v", found, err)
	}
}

// TestMMTelServicesThroughTheHSS: the MMTelServices document of TS
// 29.364 goes into ServiceData as XML, not encoded (TS 29.328 Annex D,
// tServiceData is xs:any), and comes back byte for byte, with both of
// its namespaces, so that the MMTel AS parses what it wrote.
func TestMMTelServicesThroughTheHSS(t *testing.T) {
	element := `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <complete-communication-diversion>
    <communication-diversion active="true">
      <cp:ruleset>
        <cp:rule id="cfnl">
          <cp:conditions><not-registered/></cp:conditions>
          <cp:actions><forward-to><target>tel:+12025550102</target></forward-to></cp:actions>
        </cp:rule>
      </cp:ruleset>
    </communication-diversion>
    <operator-communication-diversion authorized="true"/>
  </complete-communication-diversion>
</MMTelServices>`
	n := newShNetwork(t)
	n.readRepository(t, sh.UserIdentity{PublicIdentity: impu}, mmtelServices) // waits for the CER/CEA
	c := sh.NewClient(n.node, n.peer, realm)
	ctx := context.Background()
	if err := c.CreateRepositoryData(ctx, impu, mmtelServices, []byte(element)); err != nil {
		t.Fatal(err)
	}
	read, found, err := c.RepositoryData(ctx, tel, mmtelServices)
	if err != nil || !found || string(read.ServiceData.XML) != element {
		t.Errorf("RepositoryData = %+v (found %v, %v), want the element as it was", read, found, err)
	}
}

// TestUserDataLeavesOutBarredIdentities is TS 29.328 7.6.2: every
// Identity-Set lists only the identities that are not barred, so an AS
// never learns the temporary public identity of a UE without an ISIM.
func TestUserDataLeavesOutBarredIdentities(t *testing.T) {
	temporary := "sip:001010000000010@" + realm
	n := newShNetworkWith(t, Subscription{PrivateIdentity: "001010000000010@" + realm,
		PublicIdentities: []string{temporary, "sip:+12025550110@" + realm, "tel:+12025550110"},
		BarredIdentities: []string{temporary}, Password: "test-password-10"})
	result, data := n.udr(t, sh.UserIdentity{MSISDN: "12025550110"}, sh.IMSPublicIdentity)
	if result != success || data == nil || data.PublicIdentifiers == nil {
		t.Fatalf("UDA %v %+v", result, data)
	}
	if got := data.PublicIdentifiers.IMSPublicIdentities; len(got) != 2 || slices.Contains(got, temporary) {
		t.Errorf("IMSPublicIdentity = %v, want the SIP URI and the tel URI of the number only", got)
	}
}
