package hss

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// MSISDNPlaceholder in the ServiceInfo of a configured criterion stands
// for the MSISDN of the subscriber whose profile the HSS builds. The
// S-CSCF hands ServiceInfo to the application server in the body of the
// third-party REGISTER (TS 24.229 5.4.1.7A). That is how the
// IP-SM-GW learns the number of the user whose registration the S-CSCF
// tells it of (TS 23.204 6.1 step 4).
const MSISDNPlaceholder = "{msisdn}"

// ServiceProfile is one of the service profiles of the network, which
// the HSS holds by name (TS 29.228 B.2.2).
type ServiceProfile struct {
	// Criteria go into the user profile of a subscription that gets this
	// profile. MSISDNPlaceholder in a ServiceInfo becomes the MSISDN of
	// the subscriber.
	Criteria []ifc.InitialFilterCriteria
	// Features are the words an operator uses for what the criteria turn
	// on, such as sms or mmtel: each criterion may name the feature it
	// serves, and this is those names without repetition, in the order
	// the criteria give them. They say what the profile does in the
	// operator's own terms, which the criteria alone do not, and an
	// operator reads them from the maintenance API to see which profile
	// to give a subscription. Nothing of Cx carries them: the user
	// profile has no element for them, so they stay in the HSS as the
	// name of the profile does.
	Features []string
}

// ProfileSource is where the name of the service profile of a
// subscription comes from: the subscription itself, or one of the two
// settings that name the profile of a kind of subscription (Settings).
// The data of the subscription alone decides it, never the settings, so
// a change of a setting moves no subscriber from one source to another,
// and two instances whose settings are a moment apart still agree on
// the source of every subscriber.
//
// A Store holds the subscribers whose profile an S-CSCF has under their
// source (Store.ListServed), so that a change of one of the two
// settings reaches the subscribers it decides for without reading the
// others.
type ProfileSource string

const (
	// ProfileOfSubscription is a subscription that names a service
	// profile of its own, which neither setting decides for.
	ProfileOfSubscription ProfileSource = "subscription"
	// ProfileOfTestUsers is one marked Subscription.TestUser that names
	// none of its own, so TestUserServiceProfile names its profile.
	ProfileOfTestUsers ProfileSource = "test_user"
	// ProfileOfOtherUsers is every other subscription, so
	// DefaultServiceProfile names its profile.
	ProfileOfOtherUsers ProfileSource = "default"
)

// ProfileSources are the three sources, for a Store that keeps an index
// of its own per source. The empty ProfileSource is none of them.
var ProfileSources = []ProfileSource{ProfileOfSubscription, ProfileOfTestUsers, ProfileOfOtherUsers}

// profileSourceOf is where the name of the service profile of s comes
// from.
func profileSourceOf(s Subscription) ProfileSource {
	switch {
	case s.ServiceProfile != "":
		return ProfileOfSubscription
	case s.TestUser:
		return ProfileOfTestUsers
	}
	return ProfileOfOtherUsers
}

// ServedSource is the source under which a Store holds sub in its index
// of the served subscribers, or the empty source for a subscriber it
// holds under none, whose user profile no S-CSCF has. A Store asks it
// of every subscriber it writes, so that the index says what the
// subscribers say (Store.ListServed).
func ServedSource(sub Subscriber) ProfileSource {
	if !served(sub) {
		return ""
	}
	return profileSourceOf(sub.Subscription)
}

// userProfile is the profile of u that the SAA carries in User-Data (TS
// 29.228 6.1.2.1): the identities of the subscription and the criteria
// of its service profile under the settings in force.
func (h *HSS) userProfile(u Subscriber) cx.UserProfile { return h.userProfileIn(h.settings.Load(), u) }

// userProfileIn is the profile of u under the settings set.
func (h *HSS) userProfileIn(set *settings, u Subscriber) cx.UserProfile {
	var criteria []ifc.InitialFilterCriteria
	for _, c := range h.config.ServiceProfiles[set.serviceProfile(u.Subscription)].Criteria {
		c.ApplicationServer.ServiceInfo = strings.ReplaceAll(c.ApplicationServer.ServiceInfo, MSISDNPlaceholder, u.MSISDN())
		criteria = append(criteria, c)
	}
	p := cx.UserProfile{PrivateIdentity: u.PrivateIdentity, PublicIdentities: u.PublicIdentities, InitialFilterCriteria: criteria}
	for _, id := range u.PublicIdentities {
		if u.Barred(id) {
			p.BarredIdentities = append(p.BarredIdentities, id)
		}
	}
	return p
}

// serviceProfile is the name of the service profile of s: the one the
// subscription names, else the profile of its kind of user, a test user
// or any other. The choice is made from the data of this one
// subscription and the settings in force, so the HSS answers for it
// without reading anything else.
func (h *HSS) serviceProfile(s Subscription) string { return h.settings.Load().serviceProfile(s) }

// serviceProfile is the name of the service profile of sub under the
// settings set: the one its source names (profileSourceOf).
func (set *settings) serviceProfile(s Subscription) string {
	switch profileSourceOf(s) {
	case ProfileOfSubscription:
		return s.ServiceProfile
	case ProfileOfTestUsers:
		return set.testUserServiceProfile
	default:
		return set.defaultServiceProfile
	}
}

// CheckServiceProfile checks the name of the service profile of a
// subscription, as the configuration file and the provisioning API give
// it. A subscription that names none takes the profile of its kind of
// user, so an empty name is no mistake; any other name is one that
// ServiceProfiles has, since a name that no profile answers to would
// leave the subscription with no criteria at all.
func (c Config) CheckServiceProfile(name string) error {
	if name == "" {
		return nil
	}
	if _, found := c.ServiceProfiles[name]; !found {
		return fmt.Errorf("service_profile: %q is not a profile of service_profiles, which has %v", name, slices.Sorted(maps.Keys(c.ServiceProfiles)))
	}
	return nil
}

// CheckServiceProfileName checks a setting that names the service
// profile of a kind of subscription, DefaultServiceProfile or
// TestUserServiceProfile, which the configuration file and the
// maintenance API both set; key names the setting in the error. A
// network with service profiles needs a name, so that every
// subscription it answers for has criteria, and a network without them
// takes no name.
func (c Config) CheckServiceProfileName(key, name string) error {
	_, found := c.ServiceProfiles[name]
	switch {
	case len(c.ServiceProfiles) == 0 && name != "":
		return fmt.Errorf("%s: %q, but the node defines no service_profiles", key, name)
	case len(c.ServiceProfiles) == 0:
		return nil
	case name == "":
		return fmt.Errorf("%s: required, one of %v", key, slices.Sorted(maps.Keys(c.ServiceProfiles)))
	case !found:
		return fmt.Errorf("%s: %q is not a profile of service_profiles, which has %v", key, name, slices.Sorted(maps.Keys(c.ServiceProfiles)))
	}
	return nil
}

// MSISDN is the MSISDN of the subscription (TS 29.328 7.6.9): the
// number of the first tel URI among the public identities in its
// canonical form, so without visual separators, parameters or the "+"
// of the global number. It is empty when the subscription has no global
// tel URI of 1 to 15 digits.
//
// SIMPLIFIED: TS 29.328 7.6.9. An HSS holds the MSISDN as subscription
// data of its own. The stub takes it from the first tel URI among the
// public identities, so that the configuration states each number once
// and a user is found by MSISDN through the tel URI.
func (s Subscription) MSISDN() string {
	for _, id := range s.PublicIdentities {
		canonical, err := ims.CanonicalURI(id)
		if err != nil {
			continue
		}
		if number, ok := strings.CutPrefix(canonical, "tel:+"); ok && sh.ValidMSISDN(number) {
			return number
		}
	}
	return ""
}
