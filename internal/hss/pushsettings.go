package hss

import "context"

// This file is the push of TS 29.228 6.6.1 for a change of the settings
// that name the service profile of a kind of subscription (Settings).
// The clause has the HSS push the complete user profile to the S-CSCF
// as soon as the profile of a registered or unregistered identity
// changes. A change of either name changes the profile of every
// subscription that name decides for, where a provisioning request
// changes the one subscription it names (profileChanged), so the HSS
// reads the subscribers of that one name from the index of the served
// subscribers (Store.ListServed) and sends a PPR to each.

// profilePushPage is how many private identities the HSS reads from
// that index at a time. The pushes go out one subscriber at a time
// whatever it is; it only bounds how many identities the HSS holds
// while it works through them.
const profilePushPage = 100

// pushChangedSettings pushes the user profile to the S-CSCF of every
// served subscriber whose service profile name comes from a setting
// that the change from before to after gives another value. before are
// the settings that the change replaced in the Store, so those of the
// role (commitSettings). A change of the admission policy alone, and
// one that names the profile that was in force already, push nothing:
// no subscription gets another profile from either.
//
// The pushes run behind the change rather than in it: the maintenance
// API answers the operator once the settings are in force, and must not
// wait for the S-CSCFs of a network of registered users
// (maintenance.ChangeConfiguration). They end with the node (Close).
func (h *HSS) pushChangedSettings(before, after *settings) {
	var sources []ProfileSource
	if before.testUserServiceProfile != after.testUserServiceProfile {
		sources = append(sources, ProfileOfTestUsers)
	}
	if before.defaultServiceProfile != after.defaultServiceProfile {
		sources = append(sources, ProfileOfOtherUsers)
	}
	if len(sources) == 0 {
		return
	}
	peers := h.peers.Load()
	if peers == nil {
		// A node that takes no Diameter connections reaches no S-CSCF,
		// as it sends no alert either (alertServiceCentre).
		h.log.Warn("no profile pushed for the changed settings: this node takes no Diameter connections")
		return
	}
	// lifeMu holds the check of the node's life and the count of the
	// pushes together, so that Close, which ends the one and then waits
	// for the other, never waits while a push it did not count starts.
	h.lifeMu.Lock()
	defer h.lifeMu.Unlock()
	if h.life.Err() != nil {
		// The node is stopping (Close), so a PPR would reach no S-CSCF.
		return
	}
	h.pushes.Add(1)
	go func() {
		defer h.pushes.Done()
		for _, source := range sources {
			h.pushToSource(h.life, *peers, source)
		}
	}()
}

// pushToSource sends one PPR per served subscriber whose service
// profile name comes from source, reading the index of those
// subscribers a page at a time.
//
// Each PPR carries the profile of the subscriber and the settings of
// the moment it is sent (pushProfile), which is what keeps two changes
// close together in order. The push of the earlier change may still be
// running when the later one starts, and the two take the same lock per
// subscriber, so they never send to one S-CSCF at once; whichever sends
// second reads the settings as they then are. An older push that
// overtakes a newer one therefore sends the profile in force, not the
// one of its own change, and the S-CSCF is left with the profile the
// HSS has.
//
// A push that fails is logged and nothing more: the settings are in
// force either way, and that S-CSCF reads the profile at the next
// registration of the user. The whole round of pushes may be made
// again, here or on another instance, since a PPR only replaces what
// the S-CSCF holds with the profile the HSS has (TS 29.228 6.2.2.1).
func (h *HSS) pushToSource(ctx context.Context, peers Peers, source ProfileSource) {
	// Each push logs the profile it carried (pushProfile); this names
	// the setting whose change set these pushes off.
	log := h.log.With("profile_source", string(source))
	pushed, after := 0, ""
	for {
		ids, next, err := h.store.ListServed(ctx, source, after, profilePushPage)
		if err != nil {
			log.Warn("the profiles of the changed setting were not all pushed", "pushed", pushed, "error", err.Error())
			return
		}
		for _, privateID := range ids {
			if ctx.Err() != nil {
				log.Warn("the push of the changed setting stopped with the node", "pushed", pushed)
				return
			}
			// A subscriber that the index names and the store no longer
			// has, or no longer serves, gets no PPR: pushProfile reads it
			// before it builds one.
			if err := h.pushProfile(ctx, peers, privateID); err != nil {
				log.Warn("PPR failed", "private_identity", privateID, "error", err.Error())
				continue
			}
			pushed++
		}
		if next == "" {
			log.Info("pushed the profile of the changed setting", "subscribers", pushed)
			return
		}
		after = next
	}
}
