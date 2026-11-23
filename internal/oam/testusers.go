package oam

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// Test users are marked in the HSS, as an attribute of the
// subscription, and the HSS picks the service profile of each
// subscription, the iFCs that its S-CSCF gets:
// the one named on the subscription, else the one for test users if the
// subscription is a test user's, else the default. A feature, such as a
// new application server, is enabled for test users only by putting it
// in the test users' profile and nowhere else. Admitting test users
// only makes the HSS refuse the registration of every other user. All
// of it is the HSS's data and settings: the OAM changes them through the
// HSS provisioning API and the maintenance API of the HSS nodes, and
// keeps nothing.
//
// The HSS keeps the test_user and service_profile attributes and the
// service profiles of its configuration, picks the profile of a
// subscription as above, and takes the three settings below on the
// maintenance API of its node. A node whose configuration carries
// neither the profiles nor those settings is not an HSS of this
// network, and the OAM answers 502 and names what is missing.
const (
	SettingAdmission              = "hss.admission"
	SettingDefaultServiceProfile  = "hss.default_service_profile"
	SettingTestUserServiceProfile = "hss.test_user_service_profile"
)

// Admission is the value of hss.admission: which users the HSS lets
// register.
type Admission string

const (
	AdmitAll       Admission = "all"
	AdmitTestUsers Admission = "test_users"
)

// hssServiceView is the part of the configuration view of an HSS node
// that the OAM reads: each service profile by name, with the features
// that it has.
//
//	{"hss": {"service_profiles": {"default": {"features": ["sms"]}, "trial": {"features": ["sms", "mmtel"]}}}}
type hssServiceView struct {
	HSS struct {
		ServiceProfiles map[string]struct {
			Features []string `json:"features"`
		} `json:"service_profiles"`
	} `json:"hss"`
}

// TestUsers is the answer to GET /oam/v1/test-users: how the HSS treats
// test users now, as one HSS node shows it.
type TestUsers struct {
	// HSSNode is the node of the inventory that answered.
	HSSNode                string    `json:"hss_node"`
	Admission              Admission `json:"admission"`
	DefaultServiceProfile  string    `json:"default_service_profile"`
	TestUserServiceProfile string    `json:"test_user_service_profile"`
	// ServiceProfiles are the features of each service profile.
	ServiceProfiles map[string][]string `json:"service_profiles"`
}

// TestUsers reads the service profiles and the settings from the first
// HSS node of the inventory that answers. The HSS nodes of a role keep
// one configuration (ChangeConfiguration).
func (s *Server) TestUsers(ctx context.Context) (TestUsers, error) {
	nodes, err := s.role("hss")
	if err != nil {
		return TestUsers{}, fmt.Errorf("%w: no HSS node in the inventory", ErrUnavailable)
	}
	var failures []string
	for _, e := range nodes {
		callCtx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
		conf, err := maintenance.NewClient(e.URL, s.httpClient).Configuration(callCtx)
		cancel()
		if err != nil {
			failures = append(failures, e.Name+": "+s.nodeError(err).Error)
			continue
		}
		return testUsersOf(e.Name, conf)
	}
	return TestUsers{}, fmt.Errorf("%w: no HSS node answered: %s", ErrUpstream, strings.Join(failures, "; "))
}

func testUsersOf(node string, conf maintenance.Configuration) (TestUsers, error) {
	var view hssServiceView
	if err := json.Unmarshal(conf.Configuration, &view); err != nil || view.HSS.ServiceProfiles == nil {
		return TestUsers{}, fmt.Errorf("%w: HSS node %s has no service profiles in its configuration", ErrUpstream, node)
	}
	t := TestUsers{HSSNode: node, ServiceProfiles: map[string][]string{}}
	for name, p := range view.HSS.ServiceProfiles {
		t.ServiceProfiles[name] = p.Features
	}
	for name, dst := range map[string]*string{
		SettingAdmission:              (*string)(&t.Admission),
		SettingDefaultServiceProfile:  &t.DefaultServiceProfile,
		SettingTestUserServiceProfile: &t.TestUserServiceProfile,
	} {
		v := changeableValue(conf, name)
		if v == nil || json.Unmarshal(v, dst) != nil {
			return TestUsers{}, fmt.Errorf("%w: HSS node %s has no changeable setting %s", ErrUpstream, node, name)
		}
	}
	return t, nil
}

// FeatureState is the answer to "is this feature active for this
// subscriber", from the data that the HSS has now.
type FeatureState struct {
	PrivateIdentity string `json:"private_identity"`
	Feature         string `json:"feature"`
	Active          bool   `json:"active"`
	// Reason says why the feature is not active.
	Reason   string `json:"reason,omitempty"`
	TestUser bool   `json:"test_user"`
	// ServiceProfile is the profile that the HSS gives the
	// subscription, and ProfileSource why: subscription (named on it),
	// test_users or default.
	ServiceProfile string    `json:"service_profile"`
	ProfileSource  string    `json:"profile_source"`
	Admission      Admission `json:"admission"`
	Admitted       bool      `json:"admitted"`
	HSSNode        string    `json:"hss_node"`
}

// Feature says whether feature is active for the subscription of impi:
// the HSS admits the user, and the service profile that the HSS gives
// it has the feature. It reads one subscription and the HSS
// configuration, never the other subscribers.
func (s *Server) Feature(ctx context.Context, impi, feature string) (FeatureState, error) {
	if s.hss == nil {
		return FeatureState{}, fmt.Errorf("%w: no HSS provisioning API", ErrUnavailable)
	}
	t, err := s.TestUsers(ctx)
	if err != nil {
		return FeatureState{}, err
	}
	known := false
	for _, fs := range t.ServiceProfiles {
		known = known || slices.Contains(fs, feature)
	}
	if !known {
		return FeatureState{}, fmt.Errorf("%w: no service profile of the HSS has feature %q", ErrNotFound, feature)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
	defer cancel()
	sub, err := s.hss.get(callCtx, impi)
	if err != nil {
		return FeatureState{}, hssLookupError(err)
	}
	f := FeatureState{PrivateIdentity: sub.PrivateIdentity, Feature: feature, TestUser: sub.TestUser,
		Admission: t.Admission, HSSNode: t.HSSNode}
	switch {
	case sub.ServiceProfile != "":
		f.ServiceProfile, f.ProfileSource = sub.ServiceProfile, "subscription"
	case sub.TestUser:
		f.ServiceProfile, f.ProfileSource = t.TestUserServiceProfile, "test_users"
	default:
		f.ServiceProfile, f.ProfileSource = t.DefaultServiceProfile, "default"
	}
	f.Admitted = t.Admission != AdmitTestUsers || sub.TestUser
	features, ok := t.ServiceProfiles[f.ServiceProfile]
	switch {
	case !f.Admitted:
		f.Reason = "the HSS admits test users only"
	case !ok:
		f.Reason = fmt.Sprintf("the HSS has no service profile %q", f.ServiceProfile)
	case !slices.Contains(features, feature):
		f.Reason = fmt.Sprintf("service profile %q does not have it", f.ServiceProfile)
	default:
		f.Active = true
	}
	return f, nil
}

// TestUserRequest is the body of PUT
// /oam/v1/subscriptions/{impi}/test-user.
type TestUserRequest struct {
	TestUser *bool  `json:"test_user"`
	Operator string `json:"operator,omitempty"`
}

// SetTestUser marks the subscription of impi as a test user's, or no
// longer, and leaves its credentials as they are.
func (s *Server) SetTestUser(ctx context.Context, impi string, testUser bool, operator string) (Subscription, error) {
	if s.hss == nil {
		return Subscription{}, fmt.Errorf("%w: no HSS provisioning API", ErrUnavailable)
	}
	s.log.Info("test user mark requested", "private_identity", impi, "test_user", testUser, "operator", operator)
	ctx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
	defer cancel()
	sub, err := s.hss.patch(ctx, impi, hssPatch{TestUser: &testUser})
	if err != nil {
		return Subscription{}, hssLookupError(err)
	}
	return sub, nil
}

// ServiceProfileRequest is the body of PUT
// /oam/v1/test-users/service-profile.
type ServiceProfileRequest struct {
	ServiceProfile string `json:"service_profile"`
	Operator       string `json:"operator,omitempty"`
	Comment        string `json:"comment,omitempty"`
}

// AdmissionRequest is the body of PUT /oam/v1/test-users/admission.
type AdmissionRequest struct {
	Admission Admission `json:"admission"`
	Operator  string    `json:"operator,omitempty"`
	Comment   string    `json:"comment,omitempty"`
}

// SetTestUserServiceProfile makes profile the one of the test users, on
// every HSS node: the features of that profile become active for the
// test users, and for no one else. It is a configuration change, in the
// history, that a rollback undoes.
func (s *Server) SetTestUserServiceProfile(ctx context.Context, req ServiceProfileRequest) (FanOut[ChangeRecord], error) {
	t, err := s.TestUsers(ctx)
	if err != nil {
		return FanOut[ChangeRecord]{}, err
	}
	if _, ok := t.ServiceProfiles[req.ServiceProfile]; !ok {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: service_profile: %q is none of %s", ErrInvalidRequest,
			req.ServiceProfile, strings.Join(slices.Sorted(maps.Keys(t.ServiceProfiles)), ", "))
	}
	return s.changeHSS(ctx, SettingTestUserServiceProfile, req.ServiceProfile, req.Operator, req.Comment)
}

// SetAdmission sets which users the HSS lets register, on every HSS
// node.
func (s *Server) SetAdmission(ctx context.Context, req AdmissionRequest) (FanOut[ChangeRecord], error) {
	if req.Admission != AdmitAll && req.Admission != AdmitTestUsers {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: admission: %q is neither %s nor %s", ErrInvalidRequest, req.Admission, AdmitAll, AdmitTestUsers)
	}
	return s.changeHSS(ctx, SettingAdmission, string(req.Admission), req.Operator, req.Comment)
}

func (s *Server) changeHSS(ctx context.Context, setting, value, operator, comment string) (FanOut[ChangeRecord], error) {
	nodes, err := s.role("hss")
	if err != nil {
		return FanOut[ChangeRecord]{}, fmt.Errorf("%w: no HSS node in the inventory", ErrUnavailable)
	}
	v, _ := json.Marshal(value)
	return s.ChangeConfiguration(ctx, nodes, maintenance.ConfigurationChangeRequest{
		Settings: map[string]json.RawMessage{setting: v}, Operator: operator, Comment: comment,
	})
}

func (s *Server) testUserRoutes(rt *routes) {
	rt.handle(http.MethodGet, PathPrefix+"test-users", func(w http.ResponseWriter, r *http.Request) {
		t, err := s.TestUsers(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	})
	rt.handle(http.MethodPut, PathPrefix+"test-users/service-profile", func(w http.ResponseWriter, r *http.Request) {
		var req ServiceProfileRequest
		if err := readJSON(w, r, &req, false); err != nil {
			writeError(w, err)
			return
		}
		f, err := s.SetTestUserServiceProfile(r.Context(), req)
		writeFanOut(w, f, err)
	})
	rt.handle(http.MethodPut, PathPrefix+"test-users/admission", func(w http.ResponseWriter, r *http.Request) {
		var req AdmissionRequest
		if err := readJSON(w, r, &req, false); err != nil {
			writeError(w, err)
			return
		}
		f, err := s.SetAdmission(r.Context(), req)
		writeFanOut(w, f, err)
	})
	rt.handle(http.MethodPut, PathPrefix+"subscriptions/{impi}/test-user", func(w http.ResponseWriter, r *http.Request) {
		var req TestUserRequest
		err := readJSON(w, r, &req, false)
		if err == nil && req.TestUser == nil {
			err = fmt.Errorf("%w: test_user: required", ErrInvalidRequest)
		}
		if err != nil {
			writeError(w, err)
			return
		}
		sub, err := s.SetTestUser(r.Context(), r.PathValue("impi"), *req.TestUser, req.Operator)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sub)
	})
	rt.handle(http.MethodGet, PathPrefix+"subscriptions/{impi}/features/{feature}", func(w http.ResponseWriter, r *http.Request) {
		f, err := s.Feature(r.Context(), r.PathValue("impi"), r.PathValue("feature"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
}

func writeFanOut[T any](w http.ResponseWriter, f FanOut[T], err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}
