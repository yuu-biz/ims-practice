package hss

import (
	"cmp"
	"context"
	"errors"
	"time"
)

// This file is the push of TS 29.228 6.6.1 for a change of the settings
// that name the service profile of a kind of subscription (Settings).
// The clause has the HSS push the complete user profile to the S-CSCF
// as soon as the profile of a registered or unregistered identity
// changes. A change of either name changes the profile of every
// subscription that name decides for, where a provisioning request
// changes the one subscription it names (profileChanged), so the HSS
// reads the subscribers of that one name from the index of the served
// subscribers (Store.ListServed) and sends a PPR to each.
//
// The instances on one Store share the work. Each of the two sources
// whose name a setting gives has one push in the Store (ProfilePush),
// which is due at a time, and whichever instance claims it when it is
// due sends the PPRs of one page and records each one, so a user gets
// one PPR for a change however many instances took the change on. An
// instance that stops leaves its claim to run out, and the instance
// that claims the push next goes on from the last PPR recorded. No
// instance holds the push for longer than a page, so the pages of one
// push go to whichever instance claims each.
//
// The instance that claims a push also compares it with the settings in
// the Store, and starts a new round from the first subscriber when the
// settings name another profile than the push has gone out for. The
// change itself only makes the push due (startPushes). An instance that
// stops between its change and that leaves a push that is due again
// within an interval, and its change is pushed all the same.

// profilePushPage is how many private identities the HSS reads from
// that index at a time, the page that one claim of a push covers. The
// pushes go out one subscriber at a time whatever it is; it bounds how
// many identities an instance holds while it works through them, and
// how much of a push one instance takes on before another may.
const profilePushPage = 100

// profilePushLease is how long a page of a push is the claiming
// instance's alone. It is longer than one PPR takes at worst: the wait
// for the lock of the subscriber (pushLockWait), the reads of the Store,
// and the PPA, which the Diameter answer timer bounds. So every claim
// moves the push on by one subscriber at least, even towards an S-CSCF
// that does not answer; what a claim leaves of its page, the next one
// goes on with.
const profilePushLease = 30 * time.Second

// profilePushSources are the sources whose service profile name a
// setting gives: those whose subscribers a change of the settings
// pushes.
var profilePushSources = []ProfileSource{ProfileOfTestUsers, ProfileOfOtherUsers}

// nameFor is the service profile name that set gives the subscriptions
// of source.
func (set *settings) nameFor(source ProfileSource) string {
	if source == ProfileOfTestUsers {
		return set.testUserServiceProfile
	}
	return set.defaultServiceProfile
}

// errPushOvertaken: another round of the push, or another instance that
// claimed the same round, has recorded a point other than the one this
// instance went on from.
var errPushOvertaken = errors.New("the push was overtaken")

// startPushes makes the push of each source whose name the change from
// before to after changes due at once, and lets this instance claim it
// at once. A change of the admission policy alone, and one that names
// the profile that was in force already, push nothing: no subscription
// gets another profile from either. A failure is logged and nothing
// more: the push of a source comes due again within an interval, and
// the instance that claims it then finds the change.
func (h *HSS) startPushes(before, after *settings) {
	started := false
	for _, source := range profilePushSources {
		if before.nameFor(source) == after.nameFor(source) {
			continue
		}
		if err := h.store.ScheduleProfilePush(h.life, source, time.Now()); err != nil {
			h.log.Warn("the push of the changed setting was not made due; it comes due within an interval",
				"profile_source", string(source), "error", err.Error())
			continue
		}
		started = true
	}
	if started {
		h.sweepNow()
	}
}

// ensurePushes records the push of each source that the Store has none
// of yet as done for the name that the settings in force give it, and
// makes it due after an interval. The instance that does so is the
// first on the Store, and starts before any change can reach it: every
// S-CSCF has the profiles of these settings from the SAAs. Every push
// that the Store has is due at a time already, since an instance that
// claims a push always makes it due again, and one that stops leaves
// its claim to run out.
func (h *HSS) ensurePushes(ctx context.Context) error {
	set := h.settings.Load()
	for _, source := range profilePushSources {
		if _, ok, err := h.store.ProfilePush(ctx, source); err != nil || ok {
			if err != nil {
				return err
			}
			continue
		}
		if err := h.store.ScheduleProfilePush(ctx, source, time.Now().Add(h.sweepInterval())); err != nil {
			return err
		}
		if _, err := h.store.CompareAndSwapProfilePush(ctx, nil, ProfilePush{Source: source, Name: set.nameFor(source), Done: true}); err != nil {
			return err
		}
	}
	return nil
}

// claimPushes claims the pushes that are due and sends one page of each
// (pushPage), each on a goroutine of its own, which Close waits for. A
// node that takes no Diameter connections reaches no S-CSCF, so it
// claims none and leaves them to the instances that do.
func (h *HSS) claimPushes() {
	peers := h.peers.Load()
	if peers == nil {
		return
	}
	lease := cmp.Or(h.config.PushLease, profilePushLease)
	claims, err := h.store.ClaimProfilePushes(h.life, time.Now(), lease, len(profilePushSources))
	if err != nil {
		if h.life.Err() == nil {
			h.log.Warn("the pushes of the changed settings were not claimed; the next round claims them", "error", err.Error())
		}
		return
	}
	for _, c := range claims {
		h.pushes.Go(func() { h.pushPage(*peers, c) })
	}
}

// pushPage goes on with the push of c.Source for one page, within the
// claim, and makes the push due again: at once while subscribers are
// left, and after an interval once the round is done, so that some
// instance compares it with the settings again then. A page that the
// claim, or the node, ends before its last PPR makes nothing due: the
// claim runs out, and the instance that claims the push next goes on
// from the last PPR recorded. Nor does a page that another round or
// another instance overtook, which that one makes due.
func (h *HSS) pushPage(peers Peers, c ProfilePushClaim) {
	ctx, cancel := context.WithDeadline(h.life, c.Until)
	defer cancel()
	log := h.log.With("profile_source", string(c.Source))
	more, err := h.pushOn(ctx, peers, c.Source)
	switch {
	case errors.Is(err, errPushOvertaken):
		return
	case ctx.Err() != nil:
		if h.life.Err() == nil {
			log.Warn("the push of the changed setting stopped with its claim; the next claim goes on with it")
		}
		return
	case err != nil:
		log.Warn("the push of the changed setting failed on the store; the next claim goes on with it", "error", err.Error())
		return
	}
	at := time.Now()
	if !more {
		at = at.Add(h.sweepInterval())
	}
	if err := h.store.ScheduleProfilePush(ctx, c.Source, at); err != nil {
		log.Warn("the push of the changed setting was not made due again; its claim runs out first", "error", err.Error())
		return
	}
	if more {
		h.sweepNow()
	}
}

// pushOn sends the PPRs of one page of the push of source to the S-CSCF
// of each subscriber, and records each in the Store, and reports
// whether subscribers are left after the page.
//
// It first compares the push with the settings in the Store: a push
// that went out for another name than the settings now give starts a
// new round from the first subscriber, since the subscribers it reached
// have the profile of that name. A source that no push has been
// recorded for yet is in step with settings of generation 0, those of a
// configuration file, which every S-CSCF has from the SAAs; after a
// change it has a round to make, as the instance of the change may have
// stopped before any push of it.
//
// Each PPR carries the profile of the subscriber and the settings in
// force when it is sent (pushProfile), which is what keeps two changes
// close together in order. A round that a new one overtakes may still
// be sending when the new one starts, and the two take the same lock
// per subscriber, so they never send to one S-CSCF at once; whichever
// sends second reads the settings as they then are. A PPR that the
// older round sends late therefore carries the profile in force, not
// the one of its own change, and the S-CSCF is left with the profile
// that the HSS has.
//
// A PPR that fails is logged and nothing more: the settings are in
// force either way, and that S-CSCF reads the profile at the next
// registration of the user. A PPR that is sent twice, when an instance
// stops after its PPA and before it records it, only replaces what the
// S-CSCF holds with the profile it has (TS 29.228 6.2.2.1).
func (h *HSS) pushOn(ctx context.Context, peers Peers, source ProfileSource) (more bool, err error) {
	stored, ok, err := h.store.Settings(ctx)
	if err != nil || !ok {
		// No instance has stored the settings yet: none has been changed.
		return false, err
	}
	name := settingsOf(stored).nameFor(source)
	p, recorded, err := h.store.ProfilePush(ctx, source)
	if err != nil {
		return false, err
	}
	switch {
	case !recorded && stored.Generation == 0:
		_, err := h.store.CompareAndSwapProfilePush(ctx, nil, ProfilePush{Source: source, Name: name, Done: true})
		return false, err
	case !recorded || p.Name != name:
		var old *ProfilePush
		if recorded {
			old = &p
		}
		next := ProfilePush{Source: source, Name: name, Round: p.Round + 1}
		if err := h.recordPush(ctx, old, next); err != nil {
			return false, err
		}
		p = next
		h.log.Info("pushing the profile of the changed setting", "profile_source", string(source),
			"service_profile", name, "round", p.Round)
	case p.Done:
		return false, nil
	}
	ids, nextPage, err := h.store.ListServed(ctx, source, p.After, profilePushPage)
	if err != nil {
		return false, err
	}
	for _, privateID := range ids {
		// A subscriber that the index names and the store no longer has,
		// or no longer serves, gets no PPR: pushProfile reads it before
		// it builds one.
		err := h.pushProfile(ctx, peers, privateID)
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil {
			h.log.Warn("PPR failed", "private_identity", privateID, "error", err.Error())
		}
		next := p
		next.After = privateID
		if err := h.recordPush(ctx, &p, next); err != nil {
			return false, err
		}
		p = next
	}
	if nextPage != "" {
		return true, nil
	}
	done := p
	done.Done = true
	if err := h.recordPush(ctx, &p, done); err != nil {
		return false, err
	}
	h.log.Info("pushed the profile of the changed setting", "profile_source", string(source),
		"service_profile", name, "round", p.Round)
	return false, nil
}

// recordPush records next as the push of its source in place of old,
// and returns errPushOvertaken when the Store has another push than old
// by now.
func (h *HSS) recordPush(ctx context.Context, old *ProfilePush, next ProfilePush) error {
	swapped, err := h.store.CompareAndSwapProfilePush(ctx, old, next)
	if err != nil {
		return err
	}
	if !swapped {
		return errPushOvertaken
	}
	return nil
}
