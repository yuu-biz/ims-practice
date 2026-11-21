package confinfo

import (
	"errors"
	"fmt"
)

// check enforces what RFC 4575 says a document MUST hold, and the
// values its schema (section 6) allows.
func (d Document) check() error {
	if d.Entity == "" {
		return errors.New("confinfo: conference-info without entity")
	}
	if err := d.State.check(); err != nil {
		return fmt.Errorf("confinfo: conference-info: %w", err)
	}
	state := d.State.effective()
	// RFC 4575 5.2: a full document has at least these two.
	if state == Full && (d.Description == nil || d.Users == nil) {
		return errors.New("confinfo: a full conference-info needs conference-description and users")
	}
	if d.Description != nil {
		if err := d.Description.check(); err != nil {
			return fmt.Errorf("confinfo: conference-description: %w", err)
		}
	}
	if d.Users != nil {
		if err := d.Users.check(state); err != nil {
			return fmt.Errorf("confinfo: %w", err)
		}
	}
	return nil
}

func (s State) check() error {
	switch s {
	case "", Full, Partial, Deleted:
		return nil
	}
	return fmt.Errorf("state %q", s)
}

// checkChild applies RFC 4575 4.4: under a full parent every child is
// full too.
func checkChild(parent, child State) error {
	if err := child.check(); err != nil {
		return err
	}
	if parent == Full && child.effective() != Full {
		return fmt.Errorf("state %q under a full parent", child)
	}
	return nil
}

func (d *Description) check() error {
	for _, list := range []*URIs{d.ConfURIs, d.ServiceURIs} {
		if list == nil {
			continue
		}
		if err := list.State.check(); err != nil {
			return err
		}
		for _, e := range list.Entries {
			if e.URI == "" {
				return errors.New("entry without uri")
			}
		}
	}
	if d.AvailableMedia != nil {
		labels := make(map[string]bool)
		for _, m := range d.AvailableMedia.Entries {
			// RFC 4575 5.3.4: the label is unique in the document.
			if m.Label == "" || labels[m.Label] {
				return fmt.Errorf("available-media entry with empty or repeated label %q", m.Label)
			}
			labels[m.Label] = true
			if m.Type == "" {
				return fmt.Errorf("available-media entry %q without type", m.Label)
			}
			if err := m.Status.check(); err != nil {
				return fmt.Errorf("available-media entry %q: %w", m.Label, err)
			}
		}
	}
	return nil
}

func (u *Users) check(parent State) error {
	if err := checkChild(parent, u.State); err != nil {
		return fmt.Errorf("users: %w", err)
	}
	state := u.State.effective()
	// RFC 4575 4.5 and 5.6: entity is the key of a user and is unique
	// among the participants.
	seen := make(map[string]bool)
	for _, user := range u.Users {
		if user.Entity == "" || seen[user.Entity] {
			return fmt.Errorf("user with empty or repeated entity %q", user.Entity)
		}
		seen[user.Entity] = true
		if err := user.check(state); err != nil {
			return fmt.Errorf("user %s: %w", user.Entity, err)
		}
	}
	return nil
}

func (u User) check(parent State) error {
	if err := checkChild(parent, u.State); err != nil {
		return err
	}
	state := u.State.effective()
	// RFC 4575 4.5 and 5.7: entity is the key of an endpoint and is
	// unique among those of the user.
	seen := make(map[string]bool)
	for _, e := range u.Endpoints {
		if e.Entity == "" || seen[e.Entity] {
			return fmt.Errorf("endpoint with empty or repeated entity %q", e.Entity)
		}
		seen[e.Entity] = true
		if err := e.check(state); err != nil {
			return fmt.Errorf("endpoint %s: %w", e.Entity, err)
		}
	}
	return nil
}

func (e Endpoint) check(parent State) error {
	if err := checkChild(parent, e.State); err != nil {
		return err
	}
	switch e.Status {
	case "", Pending, DialingOut, DialingIn, Alerting, OnHold, Connected, MutedViaFocus, Disconnecting, Disconnected:
	default:
		return fmt.Errorf("status %q", e.Status)
	}
	switch e.JoiningMethod {
	case "", DialedIn, DialedOut, FocusOwner:
	default:
		return fmt.Errorf("joining-method %q", e.JoiningMethod)
	}
	switch e.DisconnectionMethod {
	case "", Departed, Booted, Failed, Busy:
	default:
		return fmt.Errorf("disconnection-method %q", e.DisconnectionMethod)
	}
	// RFC 4575 4.5 and 5.8: id is the key of a media stream and is
	// unique within the endpoint.
	seen := make(map[string]bool)
	for _, m := range e.Media {
		if m.ID == "" || seen[m.ID] {
			return fmt.Errorf("media with empty or repeated id %q", m.ID)
		}
		seen[m.ID] = true
		if err := m.Status.check(); err != nil {
			return fmt.Errorf("media %s: %w", m.ID, err)
		}
	}
	if e.CallInfo != nil && e.CallInfo.SIP != nil {
		s := e.CallInfo.SIP
		if s.CallID == "" || s.FromTag == "" || s.ToTag == "" {
			return errors.New("call-info sip without call-id, from-tag or to-tag")
		}
	}
	return nil
}

func (s MediaStatus) check() error {
	switch s {
	case "", SendRecv, SendOnly, RecvOnly, Inactive:
		return nil
	}
	return fmt.Errorf("status %q", s)
}
