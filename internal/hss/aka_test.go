package hss

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
)

const (
	akaImpi = "001010000000010@" + realm
	akaImpu = "sip:001010000000010@" + realm
)

// akaSecret is K and OPc of MILENAGE test set 1, TS 35.208 4.3.1.
var akaSecret = aka.Secret{
	K:   [16]byte{0x46, 0x5b, 0x5c, 0xe8, 0xb1, 0x99, 0xb4, 0x9f, 0xaa, 0x5f, 0x0a, 0x2e, 0xe2, 0x38, 0xa6, 0xbc},
	OPc: [16]byte{0xcd, 0x63, 0xcb, 0x71, 0x95, 0x4a, 0x9f, 0x4e, 0x48, 0xa5, 0x99, 0x4e, 0x37, 0xa0, 0x2b, 0xaf},
}

func akaSubscription(password string) Subscription {
	return Subscription{PrivateIdentity: akaImpi, PublicIdentities: []string{akaImpu}, Password: password,
		AKA: &AKACredentials{Secret: akaSecret}}
}

func newAKAHSS(t *testing.T, subs ...Subscription) *HSS {
	t.Helper()
	store, err := NewMemoryStore(subs...)
	if err != nil {
		t.Fatal(err)
	}
	return newHSSOn(store)
}

func newHSSOn(store Store) *HSS {
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	return New(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, node, quiet())
}

func akaMAR(t *testing.T, h *HSS, scheme, server string, resync *cx.Resynchronization) cx.MultimediaAuthAnswer {
	t.Helper()
	a, err := cx.ParseMultimediaAuthAnswer(exchange(t, h, cx.MultimediaAuthRequest{Header: hdr,
		UserName: akaImpi, PublicIdentity: akaImpu, SIPAuthenticationScheme: scheme, SIPNumberAuthItems: 1,
		ServerName: server, SIPAuthorization: resync}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// vectorSQN is the SQN that av carries, as a USIM that has accepted
// nothing yet reads it from the AUTN (TS 33.102 6.3.3).
func vectorSQN(t *testing.T, av *aka.AuthenticationVector) aka.SQN {
	t.Helper()
	if av == nil {
		t.Fatal("no vector")
	}
	usim := aka.NewUSIM(akaSecret, 0)
	if _, err := usim.Authenticate(av.RAND, av.AUTN); err != nil {
		t.Fatalf("USIM: %v", err)
	}
	return usim.SQNMS()
}

// TestAKAVectorsAuthenticateTheUSIM is the HSS side of IMS-AKA
// registrations: each MAA carries a vector that a USIM with the same
// secret accepts, with RES equal to XRES, and a later one is still
// fresh for the USIM.
func TestAKAVectorsAuthenticateTheUSIM(t *testing.T) {
	h := newAKAHSS(t, akaSubscription(""))
	usim := aka.NewUSIM(akaSecret, 0)
	for i := range 2 {
		a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil)
		av := a.AuthenticationVector
		if a.Result != cx.Base(cx.DiameterSuccess) || a.SIPAuthenticationScheme != cx.SchemeDigestAKAv1MD5 || av == nil || a.SIPDigestAuthenticate != nil {
			t.Fatalf("MAA %d = %+v", i, a)
		}
		r, err := usim.Authenticate(av.RAND, av.AUTN)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if string(r.RES) != string(av.XRES) || r.CK != av.CK || r.IK != av.IK {
			t.Fatalf("vector %d: USIM gives %+v for %+v", i, r, av)
		}
	}
	// TS 29.228 6.3.1 step 5: the MAR stored the S-CSCF and set the
	// authentication pending flag, as for SIP Digest. The I-CSCF is sent
	// back to that S-CSCF, and a deregistration finds it (6.1.1.1).
	if a := uar(t, h, akaImpi, akaImpu, cx.Registration); a.Result != cx.Experimental(cx.DiameterSubsequentRegistration) || a.ServerName != scscf {
		t.Errorf("UAA after the MAR = %+v, want SUBSEQUENT_REGISTRATION with %s", a, scscf)
	}
	if a := uar(t, h, akaImpi, akaImpu, cx.DeRegistration); a.Result != cx.Base(cx.DiameterSuccess) || a.ServerName != scscf {
		t.Errorf("UAA for a deregistration while the authentication is pending = %+v, want success with %s", a, scscf)
	}
}

// TestAKAResynchronisation is TS 33.203 6.1.3 seen from the HSS: the
// USIM has seen higher sequence numbers than the HSS, refuses the
// vector with an AUTS, and the MAR with RAND || AUTS returns a vector
// that it accepts.
func TestAKAResynchronisation(t *testing.T) {
	h := newAKAHSS(t, akaSubscription(""))
	// The first vector has SQN 1 with IND 1; the USIM accepted a higher
	// one with the same IND.
	usim := aka.NewUSIM(akaSecret, aka.NewSQN(4000, 1))

	av := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil).AuthenticationVector
	_, err := usim.Authenticate(av.RAND, av.AUTN)
	var failure aka.SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("USIM: %v, want a synchronisation failure", err)
	}

	resync := &cx.Resynchronization{RAND: av.RAND, AUTS: failure.AUTS}
	// Only the S-CSCF that the HSS stored may re-synchronise (TS 29.228
	// 6.3.1 step 4).
	if a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf2, resync); a.Result != cx.Base(cx.DiameterUnableToComply) {
		t.Errorf("re-synchronisation from another S-CSCF = %+v", a)
	}
	a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, resync)
	if a.Result != cx.Base(cx.DiameterSuccess) || a.AuthenticationVector == nil {
		t.Fatalf("MAA after the AUTS = %+v", a)
	}
	if _, err := usim.Authenticate(a.AuthenticationVector.RAND, a.AuthenticationVector.AUTN); err != nil {
		t.Fatalf("USIM refuses the re-synchronised vector: %v", err)
	}
}

func TestAKAForgedAUTSKeepsSQN(t *testing.T) {
	h := newAKAHSS(t, akaSubscription(""))
	av := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, nil).AuthenticationVector
	before := vectorSQN(t, av)

	// An AUTS from a USIM with another key, claiming a far higher SQN.
	other := akaSecret
	other.K[0] ^= 0xff
	stranger := aka.NewUSIM(other, aka.NewSQN(1<<20, 0))
	replay := aka.NewAuthenticationVector(other, av.RAND, 1, aka.AMF{})
	_, err := stranger.Authenticate(replay.RAND, replay.AUTN)
	var failure aka.SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("stranger USIM: %v", err)
	}

	// TS 33.102 6.3.5 step 6: a vector still comes back, from the SQN
	// that the HSS had.
	a := akaMAR(t, h, cx.SchemeDigestAKAv1MD5, scscf, &cx.Resynchronization{RAND: av.RAND, AUTS: failure.AUTS})
	if a.Result != cx.Base(cx.DiameterSuccess) || a.AuthenticationVector == nil {
		t.Fatalf("MAA = %+v", a)
	}
	if after := vectorSQN(t, a.AuthenticationVector); after != before.Next() {
		t.Errorf("SQN went from %d to %d, want one step", before, after)
	}
}

// TestAuthenticationSchemeChoice is TS 29.228 6.3.1 step 4 for each kind
// of subscription.
func TestAuthenticationSchemeChoice(t *testing.T) {
	akaResync := &cx.Resynchronization{}
	for _, tc := range []struct {
		name     string
		password string
		akaCreds bool
		scheme   string
		resync   *cx.Resynchronization
		want     cx.Result
		answered string
	}{
		{"AKA only, Unknown", "", true, cx.SchemeUnknown, nil, cx.Experimental(cx.DiameterErrorAuthSchemeNotSupported), ""},
		{"AKA only, SIP Digest", "", true, cx.SchemeSIPDigest, nil, cx.Experimental(cx.DiameterErrorAuthSchemeNotSupported), ""},
		{"AKA only, AKA", "", true, cx.SchemeDigestAKAv1MD5, nil, cx.Base(cx.DiameterSuccess), cx.SchemeDigestAKAv1MD5},
		{"Digest only, AKA", "p", false, cx.SchemeDigestAKAv1MD5, nil, cx.Experimental(cx.DiameterErrorAuthSchemeNotSupported), ""},
		{"both, Unknown", "p", true, cx.SchemeUnknown, nil, cx.Base(cx.DiameterSuccess), cx.SchemeSIPDigest},
		{"both, AKA", "p", true, cx.SchemeDigestAKAv1MD5, nil, cx.Base(cx.DiameterSuccess), cx.SchemeDigestAKAv1MD5},
		{"both, AUTS with SIP Digest", "p", true, cx.SchemeSIPDigest, akaResync, cx.Base(cx.DiameterUnableToComply), ""},
		{"another scheme", "p", true, "NASS-Bundled", nil, cx.Experimental(cx.DiameterErrorAuthSchemeNotSupported), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := akaSubscription(tc.password)
			if !tc.akaCreds {
				sub.AKA = nil
			}
			h := newAKAHSS(t, sub)
			a := akaMAR(t, h, tc.scheme, scscf, tc.resync)
			if a.Result != tc.want || a.SIPAuthenticationScheme != tc.answered {
				t.Fatalf("MAA = %+v, want %v with %q", a, tc.want, tc.answered)
			}
			if tc.answered == cx.SchemeSIPDigest && (a.SIPDigestAuthenticate == nil || a.AuthenticationVector != nil) {
				t.Errorf("SIP Digest answer = %+v", a)
			}
		})
	}
}

// TestSeveralHSSInstancesNeverReuseAnSQN runs two HSS instances on one
// store, each with its own subscriber locks, as two nodes in front of a
// shared database would be. Every vector must take its own
// SQN.
func TestSeveralHSSInstancesNeverReuseAnSQN(t *testing.T) {
	store, err := NewMemoryStore(akaSubscription(""))
	if err != nil {
		t.Fatal(err)
	}
	instances := []*HSS{newHSSOn(store), newHSSOn(store)}
	const perInstance = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	var vectors []*aka.AuthenticationVector
	for _, h := range instances {
		wg.Go(func() {
			for range perInstance {
				a := h.multimediaAuth(cx.MultimediaAuthRequest{UserName: akaImpi, PublicIdentity: akaImpu,
					SIPAuthenticationScheme: cx.SchemeDigestAKAv1MD5, SIPNumberAuthItems: 1, ServerName: scscf})
				if a.AuthenticationVector == nil {
					t.Errorf("MAA = %+v", a)
					continue
				}
				mu.Lock()
				vectors = append(vectors, a.AuthenticationVector)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	used := map[aka.SQN]bool{}
	for _, av := range vectors {
		sqn := vectorSQN(t, av)
		if used[sqn] {
			t.Errorf("SQN %d used twice", sqn)
		}
		used[sqn] = true
	}
	if len(used) != len(instances)*perInstance {
		t.Errorf("%d SQNs in %d vectors", len(used), len(instances)*perInstance)
	}
}

// TestAKAOverDiameter runs the registration and the
// re-synchronisation through a real Cx connection on the loopback
// interface, as the S-CSCF would.
func TestAKAOverDiameter(t *testing.T) {
	store, err := NewMemoryStore(akaSubscription(""))
	if err != nil {
		t.Fatal(err)
	}
	hssID := diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}
	timers := diameter.Timers{Watchdog: time.Minute, Reconnect: time.Second, Answer: 2 * time.Second}
	hssNode := diameter.NewNode(hssID, []uint32{diameter.ApplicationIDCx}, timers, quiet())
	h := New(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, hssNode, quiet())
	hssNode.Handle(h.Handle)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	t.Logf("HSS listens on %s", server.Addr())
	scscfNode := diameter.NewNode(diameter.Identity{OriginHost: "scscf." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, timers, quiet())
	peer := scscfNode.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: hssID.OriginHost})
	t.Cleanup(peer.Stop)

	request := func(resync *cx.Resynchronization) cx.MultimediaAuthAnswer {
		t.Helper()
		m := cx.MultimediaAuthRequest{Header: cx.NewHeader(scscfNode, "", realm), UserName: akaImpi, PublicIdentity: akaImpu,
			SIPAuthenticationScheme: cx.SchemeDigestAKAv1MD5, SIPNumberAuthItems: 1, ServerName: scscf, SIPAuthorization: resync}.Message()
		deadline := time.Now().Add(5 * time.Second)
		for {
			answer, err := peer.Request(context.Background(), m)
			if err == nil {
				a, err := cx.ParseMultimediaAuthAnswer(answer)
				if err != nil {
					t.Fatal(err)
				}
				return a
			}
			// The connection opens in the background.
			if time.Now().After(deadline) {
				t.Fatalf("MAR: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	usim := aka.NewUSIM(akaSecret, aka.NewSQN(77, 1))
	av := request(nil).AuthenticationVector
	if av == nil {
		t.Fatal("no vector in the MAA")
	}
	_, err = usim.Authenticate(av.RAND, av.AUTN)
	var failure aka.SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("USIM: %v, want a synchronisation failure", err)
	}
	av = request(&cx.Resynchronization{RAND: av.RAND, AUTS: failure.AUTS}).AuthenticationVector
	if av == nil {
		t.Fatal("no vector after the AUTS")
	}
	r, err := usim.Authenticate(av.RAND, av.AUTN)
	if err != nil || string(r.RES) != string(av.XRES) {
		t.Fatalf("USIM after re-synchronisation: %+v, %v", r, err)
	}
}
