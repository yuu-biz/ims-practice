package nhss_test

import (
	"context"
	"log"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/nhss"
)

// The S-CSCF's part of an initial registration over N70: the
// authentication data (MAR), the registration (SAR) and the profile
// (the User-Data of the SAA).
func ExampleClient_Registration() {
	ctx := context.Background()
	hss := nhss.NewClient("http://hss.ims.example", nhss.NFTypeSCSCF)
	const (
		impi  = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
		impu  = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
		scscf = "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"
	)
	auth, err := hss.GenerateSipAuthData(ctx, impi, nhss.SipAuthenticationInfoRequest{
		CscfServerName:          scscf,
		SipAuthenticationScheme: nhss.DigestAKAv1MD5,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("RAND %s", auth.ThreeGAkaAvs[0].Rand)

	// After the UE has answered the challenge.
	if _, err := hss.Registration(ctx, nhss.ImsUeIdFromImpu(impu), nhss.ScscfRegistration{
		Impi:                impi,
		ImsRegistrationType: nhss.InitialRegistration,
		CscfServerName:      scscf,
		DeregCallbackUri:    "http://scscf.ims.mnc001.mcc001.3gppnetwork.org/nhss-callbacks/irs-1",
	}); err != nil {
		log.Fatal(err)
	}
	profile, err := hss.GetProfileData(ctx, nhss.ImsUeIdFromImpu(impu))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d service profiles", len(profile.ImsServiceProfiles))
}

// An HSS serves the three APIs on one SBI port, next to its Diameter
// node. uecm, ueau and sdm are its implementations of the interfaces.
func ExampleNewServer() {
	var (
		uecm nhss.ImsUECM
		ueau nhss.ImsUEAU
		sdm  nhss.ImsSDM
	)
	mux := http.NewServeMux()
	mux.Handle(nhss.UECMRoot+"/", nhss.NewImsUECMHandler(uecm))
	mux.Handle(nhss.UEAURoot+"/", nhss.NewImsUEAUHandler(ueau))
	mux.Handle(nhss.SDMRoot+"/", nhss.NewImsSDMHandler(sdm))
	go nhss.NewServer(":80", mux).ListenAndServe()
}
