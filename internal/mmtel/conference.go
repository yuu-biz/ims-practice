package mmtel

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the conference focus of the AS (TS 24.147 5.3.2):
// the ad hoc conference that GSMA IR.92 2.3.3 asks the network for. A
// UE that has two calls creates a conference by an INVITE to the
// conference factory URI, and then asks the focus with a REFER for each
// of its peers to bring the peer in, with a Replaces of its call with
// that peer (TS 24.147 5.3.1.3.3, option 2b). A user who has the
// conference URI, such as a participant who left, joins with an INVITE
// to it (5.3.2.4.1). The focus has a dialog with each participant and,
// for each, a dialog with the MRFC that joins the participant to the
// mix of the conference on the MRFP (RFC 4240 5, TS 24.147 4). The AS
// is also the notification service of the conference event package
// (conferenceevent.go).
//
// The record of a conference is in the Store: its URI, its
// creator, the conference of the MRFC, the participants with their
// dialogs and the subscriptions. The dialogs themselves live in the
// instance that created the conference, as those of a call do (AS.uri),
// and the conference ends with it: no other instance takes it over.
//
// The factory URI and the conference URIs are public service
// identities that the AS hosts (TS 23.228 4.3.6), reached in two of the
// ways of TS 23.228 5.4.12. The factory URI is one of the originating
// side (5.4.12.1): the INVITE to it reaches the AS by the originating
// filter criteria of its creator, and the AS takes it as the focus
// instead of sending it on. A user whose originating services are on
// another AS creates no conference here. A conference URI is one of a
// subdomain (5.4.12.3): sip:conf-<id>@<ConferenceDomain>, in the home
// domain with no port, as a PSI that leaves the network has (TS 23.003
// 13.5). DNS resolves the subdomain to this instance, which the S-CSCF
// asks for every request to the conference URI, and an I-CSCF asks for
// one from another network (TS 24.229 5.3.2.1 step 3). So each request
// for a conference reaches the instance that has its dialogs, and the
// URI names no node.

// isFactoryURI reports whether uri is the conference factory URI of the
// home network (TS 24.147 5.3.2.3.1 step 1), the default one of TS
// 23.003 13.10 (ims.ConferenceFactoryURI).
func (as *AS) isFactoryURI(uri string) bool {
	u, err := sip.ParseURI(uri)
	factory, _ := sip.ParseURI(ims.ConferenceFactoryURI(as.cfg.HomeDomain))
	return err == nil && u.User == factory.User && strings.EqualFold(u.Host, factory.Host)
}

// maximumUsers is how many users a conference of this AS takes, which
// the conference event package tells as maximum-user-count (GSMA IR.92
// 2.3.3). It is a policy of this AS; neither TS 24.147 nor IR.92 names
// a number.
const maximumUsers = 6

// refusal is why a conference takes no more participants: the final
// response to a user who would join, and the text of its Warning.
type refusal struct {
	code int
	text string
}

var (
	// conferenceEnded is the refusal once the conference has ended: it
	// has given its URI up (TS 24.147 5.3.2.7), so a user who would join
	// gets the 404 of a conference URI that is not allocated (5.3.2.4.1
	// step 1).
	conferenceEnded = &refusal{code: 404, text: "The conference has ended"}
	// conferenceFull is the refusal while the conference has
	// maximumUsers participants.
	conferenceFull = &refusal{code: 403, text: "The conference is full"}
	// conferenceNotStored is the refusal when the ConferenceStore failed
	// to keep the participant: the focus answers a request whose state
	// it could not write with 500 (RFC 3261 21.5.1).
	conferenceNotStored = &refusal{code: 500, text: "Conference record not stored"}
)

// referExpires is how long the subscription of a REFER lasts: longer
// than the INVITE it asks for may take (RFC 3515 3.4), which a proxy
// gives up on after 3 minutes (RFC 3261 16.6 step 11).
const referExpires = 4 * time.Minute

// ConferenceRecord is what the AS keeps of a conference while it lasts.
type ConferenceRecord struct {
	// ID names the conference: the user part of its URI, and the
	// conference of the MRFC (conf=<ID>).
	ID string
	// URI is the conference URI (TS 24.147 5.3.2.3.1 step 3).
	URI string
	// Creator is the served user who created the conference with the
	// factory URI; the conference ends when the creator leaves
	// (5.3.2.7).
	Creator string
	// MRFC is the Request-URI of the conference at the MRFC (RFC 4240 5).
	MRFC          string
	Participants  []ConferenceParticipant
	Subscriptions []ConferenceSubscription
}

// ConferenceParticipant is one participant: what the conference event
// package tells of it, its dialog with the focus, and the focus's
// dialog with the MRFC for its media.
type ConferenceParticipant struct {
	confinfo.Participant
	Dialog sip.Dialog
	Media  sip.Dialog
}

// ConferenceSubscription is a subscription to the conference event
// package outside the dialog of a conference session (conferenceevent.go).
type ConferenceSubscription struct {
	sip.NotifierSubscription
	// Subscriber is the P-Asserted-Identity of the SUBSCRIBE.
	Subscriber string
	// Version is that of the last document sent (RFC 4575 4.3).
	Version uint32
}

func (r ConferenceRecord) clone() ConferenceRecord {
	r.Participants = slices.Clone(r.Participants)
	r.Subscriptions = slices.Clone(r.Subscriptions)
	return r
}

// info is what the conference event package tells of the conference.
func (r ConferenceRecord) info() confinfo.Conference {
	c := confinfo.Conference{URI: r.URI, MaximumUserCount: maximumUsers}
	for _, p := range r.Participants {
		c.Participants = append(c.Participants, p.Participant)
	}
	return c
}

// conference is a conference that this instance hosts: its record, and
// the dialogs of its participants.
type conference struct {
	as *AS
	// contact is the Contact of every dialog of the focus with a
	// participant: the conference URI with isfocus.
	contact sip.NameAddr
	// route is the Route of the INVITEs that the focus sends: the
	// S-CSCF that the creator's INVITE came through.
	route []string
	log   *slog.Logger

	// anonymous counts the participants whose identity the focus has
	// withheld, which names each of them by an anonymous URI of its own
	// (participant). A number is never given twice, so a user that the
	// focus refused leaves a gap. The count is kept with the dialogs of
	// the conference, which all live on this instance.
	anonymous atomic.Uint32

	mu     sync.Mutex
	record ConferenceRecord
	// parties are the participants of the record, in its order: parties[i]
	// is the participant of record.Participants[i]. A participant is
	// found in the record by its place here, not by its dialog with the
	// focus, which a user who dials in does not have until the 200.
	parties []*party
	ended   bool
	// expiry stops the expiry timer of each subscription, by its dialog
	// (conferenceevent.go).
	expiry map[sip.DialogID]func() bool
	// outboxes hold the NOTIFYs on their way to each subscription
	// outside a dialog, by its dialog (conferenceevent.go).
	outboxes map[sip.DialogID]*outbox
}

// party is a participant of a conference in this instance. Its fields
// are guarded by the mu of the conference.
type party struct {
	leg     partyLeg
	media   *sip.OutgoingCall
	creator bool
	// dialog is the ID of the participant's dialog with the focus, which
	// a subscription within that dialog goes by (conferenceevent.go).
	dialog sip.DialogID
	// inDialog is a subscription within that dialog, and outbox holds
	// the NOTIFYs on their way in it (conferenceevent.go), and the BYE
	// that ends it at the end of the conference (end).
	inDialog *inDialogSubscription
	outbox   outbox
}

// leg is what the dialogs of both sides have in common: both an
// IncomingCall and an OutgoingCall are one. The focus has one dialog
// per participant, not two to relay between, so it sends the requests
// within them itself (changeMedia, notifyInDialog).
type leg interface {
	Update(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	ReInvite(body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Send(method sip.Method, body sip.Body, headers ...sip.HeaderField) (*sip.Message, error)
	Ack(body sip.Body, headers ...sip.HeaderField) error
}

// partyLeg is the dialog of a participant with the focus: the creator's
// IncomingCall or an invited user's OutgoingCall.
type partyLeg interface {
	leg
	Dialog() (sip.Dialog, bool)
	Bye(headers ...sip.HeaderField) error
	Done() <-chan struct{}
}

// newConferenceID is an ID that no other conference has: random, lower
// case, since the conference IDs of the MRFC do not depend on case.
func newConferenceID() string {
	return "conf-" + strings.ToLower(rand.Text()[:12])
}

// conferenceURI is the URI of the conference id: a public service
// identity in the subdomain of the conferences of this instance.
func (as *AS) conferenceURI(id string) string {
	return "sip:" + id + "@" + as.cfg.ConferenceDomain
}

// conferenceID is the ID of the conference that uri names, and false
// when uri is not in the subdomain of the conference URIs of this
// instance.
func (as *AS) conferenceID(uri string) (string, bool) {
	u, err := sip.ParseURI(uri)
	if err != nil || !strings.EqualFold(strings.TrimSuffix(u.Host, "."), as.cfg.ConferenceDomain) {
		return "", false
	}
	return u.User, true
}

// conferenceAt is the conference that this instance hosts at the
// conference URI uri, nil when there is none.
func (as *AS) conferenceAt(uri string) *conference {
	id, ok := as.conferenceID(uri)
	if !ok {
		return nil
	}
	f, ok := as.conferences.Load(id)
	if !ok {
		return nil
	}
	return f.(*conference)
}

// createConference is TS 24.147 5.3.2.3.1: the served user's INVITE to
// the conference factory URI, which reaches the focus with the
// originating services of the creator already run (sessioncase.go).
// fields are its header fields as those services left them, where the
// focus reads what the creator asks of privacy. The focus allocates a
// conference URI, joins the creator to the mix at the MRFC with the
// creator's offer, and answers with the MRFC's answer and the
// conference URI with isfocus as Contact. The creator is the first
// participant until it leaves, which ends the conference (5.3.2.7).
//
// SIMPLIFIED: TS 24.147 5.3.2.3.1 step 2 and TS 24.229 5.7.1.4 and
// 5.7.1.5. The AS takes the identity that the S-CSCF asserts and lets
// every user it serves create a conference; it has no conference
// policy to authorize by (TS 24.147 4). No node of this project writes
// charging records, so the P-Charging-Function-Addresses of 5.3.2.2.2
// are not written.
func (c *call) createConference(fields *sip.Message) {
	as := c.as
	req := c.in.Request
	if as.cfg.MRFC == "" || as.cfg.ConferenceDomain == "" {
		c.log.Warn("conference refused: the AS has no MRFC or no subdomain for conference URIs")
		c.legs.NoLeg()
		c.in.Reject(503, as.warning("No media resources for conferences"))
		return
	}
	id := newConferenceID()
	f := &conference{as: as, route: sCSCFRoute(c.route), log: c.log.With("conference", id),
		contact: sip.NameAddr{URI: as.conferenceURI(id), Params: sip.Params{{Name: isfocus}}}}
	f.record = ConferenceRecord{ID: id, URI: as.conferenceURI(id), Creator: c.served.URI, MRFC: as.mrfcConference(id)}
	f.log.Info("conference created", "uri", f.record.URI, "creator", c.served.URI)

	// 5.3.2.3.1: the focus asks the mixer for the resources, and answers
	// once they are through-connected.
	media, final, err := f.inviteMRFC(sip.BodyOf(req))
	if err != nil || final.StatusCode >= 300 {
		f.log.Warn("the MRFC did not join the creator", "status", statusCode(final), "error", errText(err))
		code := 503
		if final != nil {
			code = final.StatusCode
		}
		c.legs.NoLeg()
		c.in.Reject(code, as.warning("No conference at the MRFC"))
		return
	}
	as.conferences.Store(id, f)
	f.admit(c.in, &party{media: media, creator: true}, final, f.participant(fields, c.served.URI, confinfo.DialedIn))
}

// joinConference is TS 24.147 5.3.2.4.1: an INVITE to a conference URI
// from a user who joins the conference, or comes back to it after
// leaving (5.3.1.4.1). DNS leads the subdomain of the URI to this
// instance, so the INVITE comes by no filter criterion and has no
// P-Served-User: from the S-CSCF after the originating services of a
// user of the home network, or from the I-CSCF for a user of another
// network. The focus joins the user to the mix at the MRFC with the
// user's offer, as it joins the creator (createConference).
//
// SIMPLIFIED: TS 24.147 5.3.2.4.1 step 2 and TS 24.229 5.7.1.4 and
// 5.7.1.5. The focus takes the identity in the P-Asserted-Identity of
// the INVITE, as it takes the one that the S-CSCF asserts for the
// creator, and checks nothing more of the user. It does not check
// where the INVITE came from: TS 24.229 5.7.1.4 b i counts on the
// P-Asserted-Identity of a user inside the trust domain (4.4), and a
// UAS uses none from a node that it does not trust (RFC 3325 8). It
// has no conference policy (TS 24.147 4): any user may join while the
// conference has room, whether or not the user has been in it before.
// And it answers a request that has no P-Asserted-Identity and whose
// Privacy holds neither "id" nor "user" with 403, not with the
// challenge of TS 24.229 5.7.1.4 b ii; the AS holds no credentials of
// users. In the networks of this project, every request reaches the
// focus with the identity that a P-CSCF or a trusted peer asserted.
func (as *AS) joinConference(in *sip.IncomingCall, log *slog.Logger) {
	req := in.Request
	f := as.conferenceAt(req.RequestURI)
	if f == nil {
		// Step 1: no conference has this URI, or none has it any more.
		log.Info("INVITE to a conference URI that is not allocated", "request_uri", req.RequestURI)
		in.Reject(404)
		return
	}
	// TS 24.229 5.7.1.4: a request whose Privacy holds "id" or "user" is
	// anonymous and needs no identity of its own (item a); any other
	// request without a P-Asserted-Identity leaves the focus with no
	// identity for the user (item b ii). "header" is one of those others
	// here, although the focus does withhold the identity of such a party
	// in the conference state (identityRestricted). The focus logs and
	// tells the subscribers the name that the conference state uses, not
	// the identity behind it.
	who := f.participant(req, "", confinfo.DialedIn)
	privacy := sip.ParsePrivacy(req.Get("Privacy"))
	if who.User == "" && !privacy.Has(sip.PrivacyID) && !privacy.Has(sip.PrivacyUser) {
		f.log.Info("INVITE to the conference without an asserted identity")
		in.Reject(403, as.warning("No asserted identity"))
		return
	}
	user := who.Entity()
	// join checks again: other users may join while the MRFC sets up the
	// media of this one.
	f.mu.Lock()
	no := f.room()
	f.mu.Unlock()
	if no != nil {
		in.Reject(no.code, as.warning(no.text))
		return
	}
	media, final, err := f.inviteMRFC(sip.BodyOf(req))
	if err != nil || final.StatusCode >= 300 {
		f.log.Warn("the MRFC did not join the user", "user", user, "status", statusCode(final), "error", errText(err))
		code := 503
		if final != nil {
			code = final.StatusCode
		}
		in.Reject(code, as.warning("No conference at the MRFC"))
		return
	}
	select {
	case <-in.Done():
		// The user cancelled while the MRFC set up the media, and has had
		// its 487 (RFC 3261 9.2): the user never joins.
		f.log.Info("the user cancelled before the focus answered", "user", user)
		media.Bye()
		return
	default:
	}
	f.admit(in, &party{media: media}, final, who)
}

// admit answers the INVITE of in once the mixer has through-connected
// the user's media: with the answer of final, the 200 of the MRFC, and
// the conference URI with isfocus as Contact (TS 24.147 5.3.2.3.1 and
// 5.3.2.4.1), and the charging vector of 5.3.2.2.2. The
// user, which the conference state knows as info, is then participant
// p, dialed in, until its dialog ends.
func (f *conference) admit(in *sip.IncomingCall, p *party, final *sip.Message, info confinfo.Participant) {
	req := in.Request
	p.leg = in
	in.Handle(f.handle(p))
	in.SetContact(f.contact, false)
	headers := []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<" + f.record.URI + ">"}}
	if pcv := responseChargingVector(req, f.as.cfg.HomeDomain); pcv != "" {
		headers = append(headers, sip.HeaderField{Name: "P-Charging-Vector", Value: pcv})
	}
	// The 200 gives the user the conference URI, to which it subscribes
	// (IR.92 2.3.3) as soon as it has it, maybe before the focus has the
	// ACK. The user is in the conference by then.
	if no := f.join(p, info); no != nil {
		f.log.Info("user not taken into the conference", "user", info.Entity(), "reason", no.text)
		p.media.Bye()
		in.Reject(no.code, f.as.warning(no.text))
		if p.creator {
			// No one else has the conference URI yet, so the conference
			// ends with the refusal of its creator.
			f.mu.Lock()
			f.ended = true
			f.mu.Unlock()
			f.deallocate()
		}
		return
	}
	ack, err := in.Accept(sip.BodyOf(final), headers...)
	if err != nil {
		f.log.Info("the participant did not acknowledge the answer", "creator", p.creator, "error", err.Error())
		f.left(p)
		return
	}
	f.dialogConfirmed(p)
	if len(req.Body) == 0 {
		// The INVITE had no offer, so the MRFC's 200 had one, and the
		// user's ACK has the answer (RFC 3261 13.2.2.4).
		p.media.Ack(sip.BodyOf(ack))
	}
	<-in.Done()
	f.left(p)
}

// sCSCFRoute is the Route of the INVITEs that a focus sends to invite
// users on behalf of the conference URI, a PSI that the AS hosts (TS
// 24.229 5.7.3): the S-CSCF of the creator's INVITE, without the
// original dialog identifier, which belongs to that INVITE, and with
// "orig". The S-CSCF then serves the PSI in the originating case, and
// routes the INVITE as it routes a call of a user: to the S-CSCF of an
// invited user of the home network, or through the IBCF to the network
// of an invited user of another network (TS 23.228 5.4.12.5 c).
//
// SIMPLIFIED: TS 24.229 5.7.3 NOTE 4. An AS learns the S-CSCF that hosts
// the PSI over Sh. The configuration of this network names one S-CSCF,
// which serves every user and PSI of the network; when it runs as
// several instances, they share one name and keep their state in one
// store.
func sCSCFRoute(route []string) []string {
	if len(route) == 0 {
		return nil
	}
	na, err := sip.ParseNameAddr(route[0])
	if err != nil {
		return nil
	}
	u, err := sip.ParseURI(na.URI)
	if err != nil {
		return nil
	}
	u.Params = slices.DeleteFunc(u.Params, func(p sip.Param) bool { return p.Name == "odi" })
	u.Params.Set("orig", "")
	return []string{sip.NameAddr{URI: u.String()}.String()}
}

// mrfcConference is the Request-URI of the conference id at the MRFC:
// sip:conf=<id>@<MRFC> (RFC 4240 5).
func (as *AS) mrfcConference(id string) string {
	u, err := sip.ParseURI(as.cfg.MRFC)
	if err != nil {
		return ""
	}
	u.User = "conf=" + id
	return u.String()
}

// inviteMRFC adds a party to the mix of the conference at the MRFC,
// straight over Mr' (TS 24.229 10.2.3.2), with offer, which may be
// empty: the MRFC then offers in its 200 (RFC 4240 5). It returns once
// the INVITE has its final response.
func (f *conference) inviteMRFC(offer sip.Body) (*sip.OutgoingCall, *sip.Message, error) {
	ua := f.as.ua.Load()
	if ua == nil {
		return nil, nil, errNotListening
	}
	out, err := ua.Invite(sip.Invitation{RequestURI: f.record.MRFC, From: f.record.URI, To: f.record.MRFC, Body: offer})
	if err != nil {
		return nil, nil, err
	}
	var final *sip.Message
	for resp := range out.Responses() {
		if resp.StatusCode >= 200 {
			final = resp
		}
	}
	if final == nil {
		return out, nil, out.Err()
	}
	return out, final, nil
}

// join adds the participant p to the record, and tells the subscribers
// (RFC 4575 3.6). The dialog of p has been set up, except for that of a
// user who dialed in, which dialogConfirmed records later. It returns
// nil, or why it did not take p: the focus sets up the media of a
// participant before it joins, and meanwhile the conference may end, or
// other users may fill it.
func (f *conference) join(p *party, info confinfo.Participant) *refusal {
	d, _ := p.leg.Dialog()
	m, _ := p.media.Dialog()
	if info.DisplayText == "" {
		// GSMA IR.92 2.3.3: without a display text, the user's entity
		// stands in for it, so that every participant can be told apart.
		info.DisplayText = info.Entity()
	}
	// RFC 4575 5.7: the focus makes the key of each endpoint, unique
	// among those of the user, and may take the Contact or a GRUU for
	// it. The Contact that the focus has is the address of a UE, or, for
	// a user of another network, the URI of the IBCF that took the place
	// of the user's own (TS 24.229 5.10.5). The IBCF keeps that URI out
	// of the header fields that cross, but passes this document on as it
	// came, so a subscriber of another network would read it there. A
	// UUID as a URN (RFC 9562 4) names no node and no address.
	info.Endpoint = "urn:uuid:" + uuid.New().String()
	f.mu.Lock()
	if no := f.room(); no != nil {
		f.mu.Unlock()
		return no
	}
	p.dialog = d.ID
	f.parties = append(f.parties, p)
	f.record.Participants = append(f.record.Participants, ConferenceParticipant{Participant: info, Dialog: d, Media: m})
	if err := f.putRecord(); err != nil {
		f.parties = f.parties[:len(f.parties)-1]
		f.record.Participants = f.record.Participants[:len(f.record.Participants)-1]
		f.mu.Unlock()
		f.log.Warn("participant not stored", "user", info.Entity(), "error", err.Error())
		return conferenceNotStored
	}
	f.mu.Unlock()
	f.log.Info("participant joined", "user", info.Entity(), "joining_method", string(info.JoiningMethod))
	f.notify(nil)
	return nil
}

// dialogConfirmed puts the dialog of p into its record once the 200
// has created it. A user who dials in joins before the focus answers
// its INVITE, when there is no dialog yet to record.
func (f *conference) dialogConfirmed(p *party) {
	d, _ := p.leg.Dialog()
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.entry(p); e != nil {
		e.Dialog = d
	}
	p.dialog = d.ID
	f.writeRecord()
}

// left is TS 24.147 5.3.2.6.1 once the dialog of p has ended: the focus
// releases the participant's resources at the MRFC and tells the
// subscribers that it is disconnected. When the creator has left, or
// the last participant, the conference ends (5.3.2.7 item 3).
func (f *conference) left(p *party) {
	if err := p.media.Bye(); err != nil && !errors.Is(err, sip.ErrCallEnded) {
		f.log.Warn("BYE to the MRFC failed", "error", err.Error())
	}
	f.mu.Lock()
	f.setStatus(p, confinfo.Disconnected)
	if f.ended {
		f.mu.Unlock()
		return
	}
	connected := 0
	for _, o := range f.record.Participants {
		if o.Status != confinfo.Disconnected {
			connected++
		}
	}
	ends := p.creator || connected == 0
	var others []*party
	if ends {
		f.ended = true
		others = slices.Clone(f.parties)
	}
	f.writeRecord()
	f.mu.Unlock()
	f.log.Info("participant left", "creator", p.creator, "connected", connected)
	if !ends {
		f.notify(nil)
		f.mu.Lock()
		f.remove(p)
		f.mu.Unlock()
		return
	}
	f.end(others)
}

// end is TS 24.147 5.3.2.7: the focus removes every participant that is
// still there with a BYE (5.3.2.6.2.3), ends every subscription to the
// conference event package (5.3.3.4, RFC 4575 3.3), and gives the
// conference URI up.
//
// The BYE of each participant goes into the outbox of its dialog,
// behind the NOTIFYs that notify left there: the last of them ends the
// subscription within the dialog, which the BYE would end without it.
// end waits for every BYE, each behind the NOTIFYs of its own dialog
// only, so that a participant that does not answer holds up no other.
func (f *conference) end(parties []*party) {
	f.mu.Lock()
	for _, p := range parties {
		f.setStatus(p, confinfo.Disconnected)
	}
	f.mu.Unlock()
	f.notify(nil)
	var byes sync.WaitGroup
	f.mu.Lock()
	for _, p := range parties {
		leg := p.leg
		byes.Add(1)
		f.post(&p.outbox, func() bool {
			defer byes.Done()
			// The BYE ends the dialog, after which left releases the
			// participant's resources at the MRFC.
			if err := leg.Bye(); err != nil && !errors.Is(err, sip.ErrCallEnded) {
				f.log.Warn("BYE to a participant failed", "error", err.Error())
			}
			return true
		})
	}
	f.mu.Unlock()
	byes.Wait()
	f.deallocate()
	f.log.Info("conference ended")
}

// deallocate gives the conference URI up (TS 24.147 5.3.2.7).
func (f *conference) deallocate() {
	f.as.conferences.Delete(f.record.ID)
	if err := f.as.conferenceStore.DeleteConference(context.Background(), f.record.ID); err != nil {
		// No request waits for this. The record stays in the store, with
		// no time to live; its conference ID is random and never used
		// again, so it names no conference that is up.
		f.log.Warn("ended conference left in the store", "error", err.Error())
	}
}

// writeRecord writes the record of the conference after a change that
// has already been answered, or that no request asked for: a dialog
// that was confirmed or ended, a change of media, a NOTIFY. When the
// store fails, the focus goes on with the conference in this instance,
// and logs the failure. Each write is of the whole record, so the next
// one that succeeds brings the store up to date. f.mu is held.
func (f *conference) writeRecord() {
	if err := f.putRecord(); err != nil {
		f.log.Warn("conference record not written", "error", err.Error())
	}
}

// putRecord writes the record of the conference, and returns the
// failure of the store. It writes nothing once the conference has
// ended: the focus has given the conference URI up and taken the
// record out of the store (deallocate), and a write after that would
// leave the other instances a conference that no instance hosts, for
// as long as the store keeps it. The focus sets ended under f.mu, and
// deallocates after it, so a write that gets this far is one from
// before the end. A caller that answers a request by the failure of
// the store (join, subscribe) refuses an ended conference before it
// writes, and so never takes this for a write that went through. f.mu
// is held.
func (f *conference) putRecord() error {
	if f.ended {
		return nil
	}
	return f.as.conferenceStore.PutConference(context.Background(), f.record)
}

// room is nil while the conference takes one more participant, and
// otherwise why it takes none. f.mu is held.
func (f *conference) room() *refusal {
	switch {
	case f.ended:
		return conferenceEnded
	case len(f.record.Participants) >= maximumUsers:
		return conferenceFull
	}
	return nil
}

// entry is the record of p, nil when p is not in the conference. f.mu
// is held.
func (f *conference) entry(p *party) *ConferenceParticipant {
	i := slices.Index(f.parties, p)
	if i < 0 {
		return nil
	}
	return &f.record.Participants[i]
}

// setStatus is the status of p in the record. f.mu is held.
func (f *conference) setStatus(p *party, status confinfo.EndpointStatus) bool {
	e := f.entry(p)
	if e == nil {
		return false
	}
	changed := e.Status != status
	e.Status = status
	return changed
}

// remove takes p out of the conference. f.mu is held.
func (f *conference) remove(p *party) {
	if i := slices.Index(f.parties, p); i >= 0 {
		f.parties = slices.Delete(f.parties, i, i+1)
		f.record.Participants = slices.Delete(f.record.Participants, i, i+1)
	}
	f.writeRecord()
}

// handle answers the requests of a participant within its dialog with
// the focus.
func (f *conference) handle(p *party) func(*sip.InDialogRequest) {
	return func(r *sip.InDialogRequest) {
		switch r.Request.Method {
		case sip.REFER:
			f.refer(r)
		case sip.SUBSCRIBE:
			f.subscribeInDialog(r, p)
		case sip.INVITE, sip.UPDATE:
			f.changeMedia(r, p)
		default:
			r.Reject(405)
		}
	}
}

// changeMedia passes an offer of the participant on to its dialog with
// the MRFC, which changes how the MRFP sends to it, and the answer back
// (TS 24.229 5.8.4.1.1.1). A participant that holds the conference, by
// an offer of sendonly or inactive (TS 24.610 4.5.2.1), is on-hold in
// the conference event package (GSMA IR.92 2.3.3), and connected again
// once it takes the conference back.
func (f *conference) changeMedia(r *sip.InDialogRequest, p *party) {
	body := sip.BodyOf(r.Request)
	var resp *sip.Message
	var err error
	switch {
	case r.Request.Method == sip.UPDATE && !r.Offer:
		// A session refresh of the participant's own dialog (RFC 4028 9).
		r.Accept(sip.Body{})
		return
	case r.Request.Method == sip.UPDATE:
		resp, err = p.media.Update(body)
	default:
		resp, err = p.media.ReInvite(body)
	}
	switch {
	case errors.Is(err, sip.ErrInviteInProgress), errors.Is(err, sip.ErrOfferPending):
		r.Reject(491)
		return
	case err != nil:
		f.log.Info("change of media not passed on to the MRFC", "error", err.Error())
		r.Reject(500)
		return
	case resp.StatusCode >= 300:
		r.Reject(resp.StatusCode)
		return
	}
	ack, err := r.Accept(sip.BodyOf(resp))
	switch {
	case errors.Is(err, sip.ErrCallEnded):
		// The call with the participant ended: its BYE answered the
		// request with 487 (RFC 3261 15.1.2), or the 2xx went out and the
		// call ended before the ACK came. Either way there is no session
		// left to record.
		return
	case err != nil:
		f.log.Info("answer of the MRFC not passed on to the participant", "error", err.Error())
		return
	}
	if r.Request.Method == sip.INVITE && !r.Offer {
		p.media.Ack(sip.BodyOf(ack))
	}
	if !r.Offer {
		return
	}
	status := confinfo.Connected
	if held(body) {
		status = confinfo.OnHold
	}
	f.mu.Lock()
	changed := f.setStatus(p, status)
	f.writeRecord()
	f.mu.Unlock()
	if changed {
		f.notify(nil)
	}
}

// held reports whether an offer puts the media on hold: its audio does
// not send (RFC 3264 8.4).
func held(offer sip.Body) bool {
	s, err := sdp.Parse(offer.Content)
	if err != nil {
		return false
	}
	m := s.AudioMedia()
	if m == nil {
		return false
	}
	d := s.Direction(*m)
	return d == "sendonly" || d == "inactive"
}

// refer is TS 24.147 5.3.2.5.2: a participant asks the focus to invite
// the user of the Refer-To, which, in the three-way session of GSMA
// IR.92 2.3.3, carries a Replaces of the participant's call with that
// user. The focus accepts the REFER, invites the user (inviteUser), and
// reports the final response in the last NOTIFY of the REFER.
//
// SIMPLIFIED: TS 24.147 5.3.2.6.2.2. A REFER whose Refer-To has the
// method BYE, which asks the focus to remove a participant, is refused
// with 403: IR.92 2.3.3 does not ask for it.
func (f *conference) refer(r *sip.InDialogRequest) {
	rt, err := sip.ParseReferTo(r.Request.Get("Refer-To"))
	if err != nil {
		r.Reject(400)
		return
	}
	if method := referredMethod(rt.URI); method != "" && method != string(sip.INVITE) {
		r.Reject(403, f.as.warning("Only invitations are taken"))
		return
	}
	if _, isTel := ims.TelNumber(rt.URI); !isTel {
		if _, err := sip.ParseURI(rt.URI); err != nil {
			// RFC 3515 2.4.2: a UA that cannot reach a URI of another
			// scheme does not accept a REFER to it. Nor can the focus
			// reach a tel URI that holds no number.
			r.Reject(403, f.as.warning("Refer-To is neither a SIP URI nor a tel URI with a number"))
			return
		}
	}
	// join checks again: other users may join while the invited user is
	// alerted.
	f.mu.Lock()
	no := f.room()
	f.mu.Unlock()
	if no != nil {
		r.Reject(403, f.as.warning(no.text))
		return
	}
	sub, err := r.AcceptRefer(referExpires)
	if err != nil {
		f.log.Info("REFER not accepted", "error", err.Error())
		return
	}
	status := f.inviteUser(rt, referredBy(r.Request))
	if err := sub.Notify(status); err != nil {
		f.log.Info("final NOTIFY of the REFER failed", "error", err.Error())
	}
}

// referredBy is the Referred-By that the INVITE to the referred user
// carries (TS 24.605 4.6.5): the one of the REFER, which names the
// participant that asked for the invitation, and none when that
// participant asked for privacy with "user". The focus adds none of its
// own, so a REFER that asks for privacy with "header" and carries no
// Referred-By leads to an INVITE without one, as the clause asks.
func referredBy(req *sip.Message) string {
	if sip.ParsePrivacy(req.Get("Privacy")).Has(sip.PrivacyUser) {
		return ""
	}
	return req.Get("Referred-By")
}

// referredMethod is the method URI parameter of a Refer-To URI, "" when
// it has none, which means INVITE (TS 24.147 5.3.2.5.2 NOTE).
func referredMethod(uri string) string {
	u, err := sip.ParseURI(uri)
	if err != nil {
		return ""
	}
	m, _ := u.Params.Get("method")
	return strings.ToUpper(m)
}

// inviteUser is TS 24.147 5.3.2.5.4: the focus INVITEs the user of rt
// to the conference, with the conference URI in P-Asserted-Identity and,
// with isfocus, in Contact, and with the Referred-By and the Replaces of
// the REFER (steps 4 and 4a). The focus sends it as an originating UA on
// behalf of the conference URI (TS 24.147 5.3.2.2.1): the PSI in
// P-Asserted-Identity is also the identity that the S-CSCF serves (TS
// 24.229 5.7.3), and the Route is that of sCSCFRoute. The focus first
// gets an offer from the MRFC, which offers PCMU only, so that the
// user's answer settles the leg on the codec of the mixer. It returns
// the status for the last NOTIFY of the REFER: the final status of the
// INVITE, or the code of the refusal when the conference did not take
// the user who answered.
func (f *conference) inviteUser(rt sip.ReferTo, referredBy string) int {
	ua := f.as.ua.Load()
	if ua == nil {
		return 500
	}
	media, final, err := f.inviteMRFC(sip.Body{})
	if err != nil || final.StatusCode >= 300 {
		f.log.Warn("the MRFC gave no termination for the invited user", "status", statusCode(final), "error", errText(err))
		return 503
	}
	headers := []sip.HeaderField{
		{Name: "P-Asserted-Identity", Value: "<" + f.record.URI + ">"},
		// TS 24.229 5.7.3: an AS that originates a request starts its
		// charging vector.
		{Name: "P-Charging-Vector", Value: sip.ChargingVector{ICID: sip.NewCallID(), OrigIOI: f.as.cfg.HomeDomain}.String()},
	}
	if referredBy != "" {
		headers = append(headers, sip.HeaderField{Name: "Referred-By", Value: referredBy})
	}
	if rt.Replaces != nil {
		replaces := *rt.Replaces
		other, ok, err := otherSide(context.Background(), f.as.store, replaces)
		if err != nil {
			// The focus cannot tell which call the Replaces names, and
			// reports 500 (RFC 3261 21.5.1) in the last NOTIFY.
			f.log.Warn("call store failed", "error", err.Error())
			media.Bye()
			return 500
		}
		if ok {
			// The participant named its own call with the user, which this
			// AS relays: the user knows that call by the other leg (TS
			// 24.229 5.7.5.1).
			replaces = other
		}
		headers = append(headers, replaces.Header())
	}
	p := &party{media: media}
	out, err := ua.Invite(sip.Invitation{
		RequestURI: requestURIOf(rt.URI), From: f.record.URI, To: rt.URI, Route: f.route,
		Contact: f.contact, Headers: headers, Body: sip.BodyOf(final), OnRequest: f.handle(p),
	})
	if err != nil {
		f.log.Warn("INVITE to the invited user not sent", "error", err.Error())
		media.Bye()
		return 500
	}
	f.mu.Lock()
	p.leg = out
	f.mu.Unlock()
	// The user's answer comes in the first reliable provisional response
	// when the user takes preconditions, and its 2xx then carries no SDP
	// (RFC 3262 5); otherwise it comes in the 2xx.
	var answered *sip.Message
	var answer sip.Body
	for resp := range out.Responses() {
		if b := sip.BodyOf(resp); len(b.Content) > 0 && resp.StatusCode < 300 {
			answer = b
		}
		if resp.StatusCode >= 200 {
			answered = resp
		}
	}
	switch {
	case answered == nil:
		f.log.Info("the invited user did not answer", "error", errText(out.Err()))
		media.Bye()
		return 408
	case answered.StatusCode >= 300:
		f.log.Info("the invited user declined", "status", answered.StatusCode)
		media.Bye()
		return answered.StatusCode
	}
	// The user's answer goes to the MRFC in the ACK of its 200, and the
	// MRFP then mixes the user in (RFC 3725 4.1 has the same order).
	if err := media.Ack(answer); err != nil {
		f.log.Warn("answer of the invited user not passed on to the MRFC", "error", err.Error())
	}
	if no := f.join(p, f.participant(answered, rt.URI, confinfo.DialedOut)); no != nil {
		f.log.Info("invited user not taken into the conference", "reason", no.text)
		out.Bye()
		media.Bye()
		return no.code
	}
	go func() {
		<-out.Done()
		f.left(p)
	}()
	return answered.StatusCode
}

// requestURIOf is the Request-URI of the INVITE to the user of a
// Refer-To URI. A SIP URI of a global number with user=phone, as a UE
// writes a tel URI with a Replaces (TS 24.147 5.3.1.5.3), is its tel
// URI again (RFC 3261 19.1.6), which the S-CSCF translates with ENUM
// (TS 24.229 5.4.3.2 step 10). A GRUU is not such a URI: it names a UA
// instance, not a number.
func requestURIOf(uri string) string {
	n, ok := ims.ParseTelephoneNumber(uri)
	if !ok || n.SIP == nil || !strings.HasPrefix(n.Number, "+") {
		return uri
	}
	// A GRUU names one UA instance of the user (RFC 5627 3.1), and the
	// focus invites that instance: the tel URI of the number would
	// address the user instead, whose other instances would be alerted.
	if _, gruu := n.SIP.Params.Get("gr"); gruu {
		return uri
	}
	return "tel:" + n.Number
}

// otherSide is the Replaces that names, for the next hop, the other leg
// of the call of this AS that r names (TS 24.229 5.7.5.1), and false
// when r names no leg of a call here. The error is the failure of the
// store.
func otherSide(ctx context.Context, store Store, r sip.Replaces) (sip.Replaces, bool, error) {
	record, ok, err := store.CallOf(ctx, r.DialogID())
	if err != nil || !ok {
		return sip.Replaces{}, false, err
	}
	other := record.Outgoing
	if record.Outgoing.ID == r.DialogID() {
		other = record.Incoming
	}
	// The UA at the other end of that leg compares to-tag with its own
	// tag, which is the remote tag here (RFC 3891 3).
	return sip.Replaces{CallID: other.ID.CallID, ToTag: other.ID.RemoteTag, FromTag: other.ID.LocalTag, EarlyOnly: r.EarlyOnly}, true, nil
}

// participant is the entry of a new participant in the conference
// state: the identity that the network asserted for it in m, its INVITE
// or the 2xx that answered the focus's INVITE, how it came in, and that
// it is connected. fallback is its identity when m asserts none.
//
// A participant that asked for privacy is named by an anonymous URI
// instead, which tells the other participants that someone is there
// without telling them who: the focus must take such a request and
// reveal the identity in no URI of the document (RFC 4575 8.2). The
// identity of the originating party of a session with OIR (TS 24.605
// 4.6.5) and of its terminating party with TIR (4.6.3) both stay out of
// the notifications, and the focus learns of either from the Privacy
// header field: of the INVITE for a user that creates a conference or
// dials in, of the 2xx for one that the focus invited. The services of
// the party put it there before the focus sees the message, OIR at the
// AS of the user that makes the session (TS 24.607 4.5.2.4) and TIR at
// the AS of the user that answers it (TS 24.608 4.5.2.9), so a user
// whose identity the network restricts is anonymous in the conference
// state whether or not its UE asked for anything.
func (f *conference) participant(m *sip.Message, fallback string, how confinfo.JoiningMethod) confinfo.Participant {
	p := confinfo.Participant{User: assertedUser(m, fallback), Status: confinfo.Connected, JoiningMethod: how}
	if identityRestricted(m) {
		p.AnonymousURI, p.DisplayText = confinfo.AnonymousName(f.anonymous.Add(1))
	}
	return p
}

// identityRestricted reports whether the identity of the party that
// sent m is one that the network keeps to itself, which the focus reads
// from the Privacy header field (RFC 3323 4.2, RFC 3325 7): "id",
// "user" or "header". The AS of a user with OIR inserts "id" or
// "header" by the subscription option of that user, and both say that
// the identity is restricted (TS 24.607 4.5.2.4 item 1 a and b): the AS
// of the callee anonymizes the header fields of a request that carries
// "header" and gives it "id" in their place (4.5.2.9). So the identity
// of a party with OIR stays out of the conference notifications
// whichever of the two its AS chose (TS 24.605 4.6.5), as does that of
// a party with TIR (4.6.3). TS 24.605 4.6.5 reads "header" as a request
// for privacy at the focus elsewhere too: it is one of the two values
// that keep a Referred-By out of the INVITE to a referred user
// (referredBy).
//
// Whether the AS has a verified identity of the user is another
// question, which TS 24.229 5.7.1.4 answers from "id" and "user" alone
// (joinConference).
func identityRestricted(m *sip.Message) bool {
	p := sip.ParsePrivacy(m.Get("Privacy"))
	return p.Has(sip.PrivacyID) || p.Has(sip.PrivacyUser) || p.Has(sip.PrivacyHeader)
}

// assertedUser is the identity of a participant: the first
// P-Asserted-Identity of its INVITE or 2xx, and fallback without one.
func assertedUser(m *sip.Message, fallback string) string {
	if na, err := sip.ParseNameAddr(m.Get("P-Asserted-Identity")); err == nil {
		return na.URI
	}
	return fallback
}

func statusCode(m *sip.Message) int {
	if m == nil {
		return 0
	}
	return m.StatusCode
}
