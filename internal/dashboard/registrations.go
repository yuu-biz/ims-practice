package dashboard

import (
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

type RegistrationState string

const (
	Registered   RegistrationState = "registered"
	Unregistered RegistrationState = "unregistered"
)

// Registration is the state of one public user identity, as the 200 to
// a REGISTER and the NOTIFYs of the reg event package show it.
type Registration struct {
	IMPU    string `json:"impu"`
	IMPI    string `json:"impi"`
	Contact string `json:"contact"`
	// Expires is when the binding runs out, or when it ended for an
	// unregistered identity.
	Expires time.Time         `json:"expires"`
	State   RegistrationState `json:"state"`
	// SCSCF is the S-CSCF from the Service-Route of the 200, such as
	// sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060.
	SCSCF string `json:"scscf"`
	// CallID is the Call-ID of the last REGISTER whose 200 was read, or
	// "" for an identity known only from NOTIFYs. /api/messages with it
	// gives the ladder of that registration.
	CallID string `json:"call_id"`
}

// merge lays the fields that an update knows over the ones it does not.
// A NOTIFY carries no private identity and no REGISTER's Call-ID, and a
// 200 to a deregistration no Service-Route.
func (r Registration) merge(old Registration) Registration {
	if r.IMPI == "" {
		r.IMPI = old.IMPI
	}
	if r.CallID == "" {
		r.CallID = old.CallID
	}
	if r.SCSCF == "" {
		r.SCSCF = old.SCSCF
	}
	if r.Contact == "" {
		r.Contact = old.Contact
	}
	return r
}

// registerIMPI is the private user identity that a REGISTER names in the
// username of its Authorization (TS 24.229 5.1.1.2.1), or "".
func registerIMPI(register *sip.Message) string {
	c, err := sip.ParseCredentials(register.Get("Authorization"))
	if err != nil {
		return ""
	}
	return c.Username
}

// registrationsFromRegisterOK reads a 2xx to a REGISTER. Every identity
// in P-Associated-URI was registered with the one in To (TS 24.229
// 5.4.1.2.2F), so each gets an entry.
func registrationsFromRegisterOK(ok *sip.Message, impi string, at time.Time) []Registration {
	contact := ok.Get("Contact")
	if contact == "" {
		return nil
	}
	expires, _, err := sip.RequestedExpires(ok)
	if err != nil || expires < 0 {
		return nil
	}
	r := Registration{
		IMPI:    impi,
		Contact: addressURI(contact),
		Expires: at.Add(time.Duration(expires) * time.Second),
		State:   Registered,
		SCSCF:   serviceRouteSCSCF(ok.Get("Service-Route")),
		// A response has the Call-ID of its request (RFC 3261 8.2.6.2).
		CallID: ok.Get("Call-ID"),
	}
	if expires == 0 {
		r.State = Unregistered
	}
	impus := ok.GetAll("P-Associated-URI")
	if len(impus) == 0 {
		impus = []string{ok.Get("To")}
	}
	var regs []Registration
	for _, impu := range impus {
		r.IMPU = addressURI(impu)
		regs = append(regs, r)
	}
	return regs
}

// serviceRouteSCSCF strips the user part, such as orig, from the URI of
// the Service-Route.
func serviceRouteSCSCF(serviceRoute string) string {
	na, err := sip.ParseNameAddr(serviceRoute)
	if err != nil {
		return ""
	}
	u, err := sip.ParseURI(na.URI)
	if err != nil {
		return ""
	}
	s := u.Scheme + ":" + u.Host
	if u.Port != 0 {
		s += ":" + strconv.Itoa(u.Port)
	}
	return s
}

// registrationsFromNotify reads the reginfo document of a NOTIFY of the
// reg event package (RFC 3680 5).
func registrationsFromNotify(notify *sip.Message, at time.Time) []Registration {
	if sip.EventType(notify.Get("Event")) != "reg" || len(notify.Body) == 0 {
		return nil
	}
	doc, err := reginfo.Parse(notify.Body)
	if err != nil {
		return nil
	}
	var regs []Registration
	for _, reg := range doc.Registrations {
		r := Registration{IMPU: reg.AOR, State: Unregistered, Expires: at}
		for _, c := range reg.Contacts {
			if reg.State == reginfo.Active && c.State == reginfo.Active {
				r.State, r.Contact = Registered, c.URI
				r.Expires = at.Add(time.Duration(c.Expires) * time.Second)
				break
			}
		}
		regs = append(regs, r)
	}
	return regs
}
