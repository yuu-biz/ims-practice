package nhss_test

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/nhss"
)

const (
	impi  = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	impu  = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	scscf = "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"
)

// hss stands for the HSS behind the three handlers, with one
// subscriber. The operations that the examples do not use are those of
// the embedded interfaces, which are nil.
type hss struct {
	nhss.ImsUECM
	nhss.ImsSDM
}

// GenerateSipAuthData answers with one IMS-AKA vector: the RAND, RES,
// CK and IK of TS 35.208 test set 1, and the AUTN of its SQN, AMF and
// MAC-A, where a real HSS runs Milenage with the subscriber's key.
func (hss) GenerateSipAuthData(_ context.Context, impi string, _ nhss.SipAuthenticationInfoRequest) (nhss.SipAuthenticationInfoResult, error) {
	return nhss.SipAuthenticationInfoResult{Impi: impi, ThreeGAkaAvs: []nhss.ThreeGAkaAv{{
		Rand: "23553cbe9637a89d218ae64dae47bf35",
		Xres: "a54211d5e3ba50bf",
		Autn: "55f328b43577b9b94a9ffac354dfafb3",
		Ck:   "b40ba9a3c58b2a05bbf0d987b21bf8cb",
		Ik:   "f769bcd751044604127672711c6d3441",
	}}}, nil
}

// PutScscfRegistration records the S-CSCF of the user, created the
// first time, with the identities of the implicit registration set.
func (hss) PutScscfRegistration(_ context.Context, _ nhss.ImsUeId, reg nhss.ScscfRegistration) (*nhss.ScscfRegistration, bool, error) {
	reg.IrsImpus = []string{impu, "tel:+12025550101"}
	return &reg, true, nil
}

func (hss) GetProfileData(context.Context, nhss.ImsUeId) (nhss.ImsProfileData, error) {
	return nhss.ImsProfileData{ImsServiceProfiles: []nhss.ImsServiceProfile{{
		PublicIdentifierList: []nhss.PublicIdentifier{
			{PublicIdentity: nhss.PublicIdentity{ImsPublicId: impu, IdentityType: nhss.DistinctImpu, IrsIsDefault: true}},
			{PublicIdentity: nhss.PublicIdentity{ImsPublicId: "tel:+12025550101", IdentityType: nhss.DistinctImpu}},
		},
	}}}, nil
}

// serve serves the three APIs of h, as ExampleNewServer shows, on a
// free port of the loopback, and returns their apiRoot.
func serve(h hss) (apiRoot string, stop func()) {
	mux := http.NewServeMux()
	mux.Handle(nhss.UECMRoot+"/", nhss.NewImsUECMHandler(h))
	mux.Handle(nhss.UEAURoot+"/", nhss.NewImsUEAUHandler(h))
	mux.Handle(nhss.SDMRoot+"/", nhss.NewImsSDMHandler(h))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	srv := nhss.NewServer(l.Addr().String(), mux)
	go srv.Serve(l)
	return "http://" + l.Addr().String(), func() { srv.Close() }
}

// The S-CSCF's part of an initial registration over N70: the
// authentication data (MAR), the registration (SAR) and the profile
// (the User-Data of the SAA).
func ExampleClient_Registration() {
	apiRoot, stop := serve(hss{})
	defer stop()
	ctx := context.Background()
	c := nhss.NewClient(apiRoot, nhss.NFTypeSCSCF)
	auth, err := c.GenerateSipAuthData(ctx, impi, nhss.SipAuthenticationInfoRequest{
		CscfServerName:          scscf,
		SipAuthenticationScheme: nhss.DigestAKAv1MD5,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("RAND", auth.ThreeGAkaAvs[0].Rand)

	// After the UE has answered the challenge.
	reg, err := c.Registration(ctx, nhss.ImsUeIdFromImpu(impu), nhss.ScscfRegistration{
		Impi:                impi,
		ImsRegistrationType: nhss.InitialRegistration,
		CscfServerName:      scscf,
		DeregCallbackUri:    "http://scscf.ims.mnc001.mcc001.3gppnetwork.org/nhss-callbacks/irs-1",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("implicit registration set", reg.IrsImpus)
	profile, err := c.GetProfileData(ctx, nhss.ImsUeIdFromImpu(impu))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(profile.ImsServiceProfiles), "service profile")
	// Output:
	// RAND 23553cbe9637a89d218ae64dae47bf35
	// implicit registration set [sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org tel:+12025550101]
	// 1 service profile
}

// An HSS serves the three APIs on one SBI port, next to its Diameter
// node: ":80" for an "http" apiRoot (TS 29.500 5.3). h implements the
// three interfaces; here it is the hss of this example.
func ExampleNewServer() {
	var h hss
	mux := http.NewServeMux()
	mux.Handle(nhss.UECMRoot+"/", nhss.NewImsUECMHandler(h))
	mux.Handle(nhss.UEAURoot+"/", nhss.NewImsUEAUHandler(h))
	mux.Handle(nhss.SDMRoot+"/", nhss.NewImsSDMHandler(h))
	// The example listens on a free port of the loopback instead.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	srv := nhss.NewServer(l.Addr().String(), mux)
	go srv.Serve(l)
	defer srv.Close()

	profile, err := nhss.NewClient("http://"+l.Addr().String(), nhss.NFTypeSCSCF).GetProfileData(context.Background(), nhss.ImsUeIdFromImpu(impu))
	if err != nil {
		log.Fatal(err)
	}
	for _, id := range profile.ImsServiceProfiles[0].PublicIdentifierList {
		fmt.Println(id.PublicIdentity.ImsPublicId, id.PublicIdentity.IrsIsDefault)
	}
	// Output:
	// sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org true
	// tel:+12025550101 false
}
