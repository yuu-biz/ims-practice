package hss

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
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
// whose name a setting gives has one push in the Store (ProfilePush). A
// change of the name starts a round of it, which brings every served
// subscriber of the source to the profile of that name, and which is
// due at a time in a time index. Whichever instance claims the round
// when it is due sends the PPRs of one page and records each one, so a
// user gets one PPR for a change however many instances took the change
// on. An instance that stops leaves its claim to run out, and the
// instance that claims the round next goes on from the last PPR
// recorded. No instance holds a round for longer than a page, so the
// pages of one round go to whichever instance claims each.
//
// An instance reaches an S-CSCF only over the connection that the
// S-CSCF opened to it, and an S-CSCF that names one instance of the HSS
// connects to that one alone. A PPR that the instance of a round cannot
// send for want of that connection is left for the others
// (ProfilePushRetry): any instance claims it, and the one that holds the
// connection sends it, while the round goes on with the rest.
//
// The rounds of one source go one after another. A change made while a
// round is under way starts no round of its own; the round under way
// starts the next one as it ends, for the settings then in force
// (endRound). Every change therefore gets its PPRs, as late as the
// round before it takes, and no two rounds of a source send to one
// subscriber at once. Each round sends the profile of its own name, not
// that of a later change: the round after it sends that one.

// profilePushPage is how many private identities the HSS reads from
// that index at a time, the page that one claim of a round covers. The
// pushes go out one subscriber at a time whatever it is; it bounds how
// many identities an instance holds while it works through them, and
// how much of a round one instance takes on before another may.
const profilePushPage = 100

// profilePushLease is how long a page of a round is the claiming
// instance's alone. It is longer than one PPR takes at worst: the wait
// for the lock of the subscriber (pushLockWait), the reads of the Store,
// and the PPA, which the Diameter answer timer bounds. So every claim
// moves the round on by one subscriber at least, even towards an S-CSCF
// that does not answer; what a claim leaves of its page, the next one
// goes on with.
const profilePushLease = 30 * time.Second

// profilePushRetryTime is how long the PPR of a subscriber that no
// instance could reach is tried again. An S-CSCF whose instance of the
// HSS is made again, or taken back into service, connects again after
// Tc, which RFC 6733 2.1 recommends at 30 s, and this covers a few of
// those. After it, the S-CSCF reads the profile at the next
// registration of the user.
const profilePushRetryTime = 2 * time.Minute

// profilePushRetries bounds how many of those PPRs an instance claims at
// a sweep.
const profilePushRetries = 100

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

// withName is set with name as the service profile name of the
// subscriptions of source.
func (set settings) withName(source ProfileSource, name string) *settings {
	if source == ProfileOfTestUsers {
		set.testUserServiceProfile = name
	} else {
		set.defaultServiceProfile = name
	}
	return &set
}

// roundStarts bounds the tries of startRound that the writes of other
// instances overtake.
const roundStarts = 10

// startRounds starts a round of the push of each source for set, the
// settings of the Store, where one is due (startRound). A failure is
// logged and nothing more: every instance asks again at its next sweep.
func (h *HSS) startRounds(ctx context.Context, set *settings) {
	for _, source := range profilePushSources {
		if err := h.startRound(ctx, source, set); err != nil && ctx.Err() == nil {
			h.log.Warn("the round of the push of the changed setting was not started; the next sweep starts it",
				"profile_source", string(source), "error", err.Error())
		}
	}
}

// startRound starts a round of the push of source for set, unless none
// is due: the last round went out for the name that set gives the
// source, or for a later change; or a round is under way, which starts
// the next one as it ends (endRound). A source that no push has been
// recorded for yet is in step with settings of generation 0, those of a
// configuration file, which every S-CSCF has from the SAAs; after a
// change it has a round to make.
//
// The instance whose write changed the settings asks this at once, and
// every instance asks it at each sweep for the settings it reads, so a
// change whose instance stopped before it started the round is pushed
// all the same.
//
// The round is recorded before it is made due, so that a claim that
// comes between the two finds the round and sends it, and one that
// finds none may take the round out of the time index (endRound): the
// due that follows puts it back. An instance that stops between the two
// leaves a round that is recorded and never due. No claim updates it,
// and an instance that claims a round records each PPR within a lease,
// so a round under way that nothing has recorded for two leases has no
// claim; startRound makes it due again then.
func (h *HSS) startRound(ctx context.Context, source ProfileSource, set *settings) error {
	name := set.nameFor(source)
	for range roundStarts {
		p, recorded, err := h.store.ProfilePush(ctx, source)
		if err != nil {
			return err
		}
		var old *ProfilePush
		switch {
		case !recorded && set.generation == 0:
			_, err := h.store.CompareAndSwapProfilePush(ctx, nil,
				ProfilePush{Source: source, Name: name, Done: true, Updated: stamp()})
			return err
		case recorded && !p.Done:
			if time.Since(p.Updated) > 2*h.pushLease() {
				return h.store.ScheduleProfilePush(ctx, source, dueNow())
			}
			return nil
		case recorded && (p.Name == name || p.Generation >= set.generation):
			return nil
		case recorded:
			old = &p
		}
		next := ProfilePush{Source: source, Name: name, Generation: set.generation, Round: p.Round + 1, Updated: stamp()}
		swapped, err := h.store.CompareAndSwapProfilePush(ctx, old, next)
		if err != nil {
			return err
		}
		if !swapped {
			// Another instance recorded the push in between: decide again
			// on what it recorded.
			continue
		}
		h.log.Info("pushing the profile of the changed setting", "profile_source", string(source),
			"service_profile", name, "generation", set.generation, "round", next.Round)
		if err := h.store.ScheduleProfilePush(ctx, source, dueNow()); err != nil {
			return err
		}
		h.sweepNow()
		return nil
	}
	return errPushOvertaken
}

// pushLease is the PushLease of the configuration, or profilePushLease.
func (h *HSS) pushLease() time.Duration { return cmp.Or(h.config.PushLease, profilePushLease) }

// stamp is the time a push is recorded at, in the form that a Store
// gives back: whole milliseconds in UTC, so that a round compares equal
// to itself once it has been written and read again.
func stamp() time.Time { return time.UnixMilli(time.Now().UnixMilli()).UTC() }

// dueNow is the time to make a round due at once. A time index holds
// whole milliseconds and rounds a time up, so that nothing is due
// early; the millisecond that has begun is due to a claim at once.
func dueNow() time.Time { return time.UnixMilli(time.Now().UnixMilli()) }

// errUnservableRound: the round names a service profile that the
// configuration of this instance does not define, so this instance
// cannot build its PPRs.
var errUnservableRound = errors.New("the round names a service profile that this instance does not define")

// errPushOvertaken: another round of the push, or another instance that
// claimed the same round, has recorded a point other than the one this
// instance went on from.
var errPushOvertaken = errors.New("the push was overtaken")

// claimPushes claims the rounds that are due and sends one page of each
// (pushPage), each on a goroutine of its own, which Close waits for. A
// node that takes no Diameter connections reaches no S-CSCF, so it
// claims none and leaves them to the instances that do; so does an
// instance that the operator has shut down or locked, which takes on no
// new work and whose connections are closing or closed (maintenance.go).
func (h *HSS) claimPushes() {
	peers := h.peers.Load()
	if peers == nil {
		return
	}
	if m := h.managed.Load(); m != nil && m.AdministrativeState() != maintenance.Unlocked {
		return
	}
	claims, err := h.store.ClaimProfilePushes(h.life, time.Now(), h.pushLease(), len(profilePushSources))
	if err != nil {
		if h.life.Err() == nil {
			h.log.Warn("the pushes of the changed settings were not claimed; the next round claims them", "error", err.Error())
		}
		return
	}
	for _, c := range claims {
		h.pushes.Go(func() { h.pushPage(*peers, c) })
	}
	retries, err := h.store.ClaimProfilePushRetries(h.life, time.Now(), h.pushLease(), profilePushRetries)
	if err != nil {
		if h.life.Err() == nil {
			h.log.Warn("the PPRs left for another instance were not claimed; the next round claims them", "error", err.Error())
		}
		return
	}
	for _, c := range retries {
		h.pushes.Go(func() { h.pushRetry(*peers, c) })
	}
}

// pushRetry sends a PPR that a round left for another instance
// (ProfilePushRetry). One that this instance cannot send either is made
// due again after an interval, for whichever instance claims it then,
// until profilePushRetryTime has passed. A later round of the source
// pushes every subscriber of it, this one included, so the PPR of an
// earlier round is not sent.
func (h *HSS) pushRetry(peers Peers, c ProfilePushRetryClaim) {
	ctx, cancel := context.WithDeadline(h.life, c.Until)
	defer cancel()
	log := h.log.With("profile_source", string(c.Source), "private_identity", c.PrivateID)
	p, recorded, err := h.store.ProfilePush(ctx, c.Source)
	if err != nil {
		log.Warn("the PPR left for another instance was not read; the next claim sends it", "error", err.Error())
		return
	}
	if recorded && p.Round == c.Round {
		// A name that this instance does not define is another
		// instance's to send, as a connection that it does not hold is.
		err := h.config.CheckServiceProfileName(string(c.Source), p.Name)
		another := err != nil
		if err == nil {
			err = h.pushRoundProfile(ctx, peers, c.PrivateID, p)
			another = errors.Is(err, diameter.ErrPeerUnavailable)
		}
		switch {
		case ctx.Err() != nil:
			return
		case another && time.Since(c.Since) < profilePushRetryTime:
			if err := h.store.ScheduleProfilePushRetry(ctx, c.ProfilePushRetry, time.Now().Add(h.sweepInterval())); err != nil {
				log.Warn("the PPR left for another instance was not made due again; its claim runs out first", "error", err.Error())
			}
			return
		case err != nil:
			log.Warn("PPR failed", "error", err.Error())
		}
	}
	if err := h.store.DoneProfilePushRetry(ctx, c); err != nil {
		log.Warn("a PPR left for another instance stays due; the next claim finds it sent", "error", err.Error())
	}
}

// pushPage goes on with the round of c.Source for one page, within the
// claim. While subscribers are left it makes the round due again at
// once; at the last one it ends the round (endRound). A page that the
// claim, or the node, ends before its last PPR leaves the claim to run
// out, and the instance that claims the round next goes on from the
// last PPR recorded; so does a page that another instance overtook,
// which that one goes on with.
func (h *HSS) pushPage(peers Peers, c ProfilePushClaim) {
	ctx, cancel := context.WithDeadline(h.life, c.Until)
	defer cancel()
	log := h.log.With("profile_source", string(c.Source))
	p, more, err := h.pushOn(ctx, peers, c.Source)
	switch {
	case errors.Is(err, errPushOvertaken):
		return
	case errors.Is(err, errUnservableRound):
		// Another instance, whose configuration defines the name, sends
		// the round; this one leaves it for an interval.
		log.Error("the round of the push names a service profile that this instance does not define; it leaves the round to another", "error", err.Error())
		if err := h.store.ScheduleProfilePush(ctx, c.Source, time.Now().Add(h.sweepInterval())); err != nil {
			log.Warn("the round of the push was not left to another instance; its claim runs out first", "error", err.Error())
		}
		return
	case ctx.Err() != nil:
		if h.life.Err() == nil {
			log.Warn("the push of the changed setting stopped with its claim; the next claim goes on with it")
		}
		return
	case err != nil:
		log.Warn("the push of the changed setting failed on the store; the next claim goes on with it", "error", err.Error())
		return
	case more:
		if err := h.store.ScheduleProfilePush(ctx, c.Source, dueNow()); err != nil {
			log.Warn("the next page of the push was not made due; the claim runs out first", "error", err.Error())
			return
		}
		h.sweepNow()
		return
	}
	if err := h.endRound(ctx, c, p); err != nil {
		log.Warn("the end of the round of the push was not recorded; the claim runs out first", "error", err.Error())
	}
}

// endRound ends the claim of a round that has sent the PPR of its last
// subscriber, p, or that found no round to send. The settings in force
// may have given the source another name since the round began, by a
// change that started no round of its own as the round was under way
// (startRound): the next round starts here then.
//
// Otherwise the round leaves the time index, unless something made it
// due again since the claim, which ends the claim: a round that another
// instance started after this one read the push is recorded before it
// is made due, so it is either seen here or puts itself back.
func (h *HSS) endRound(ctx context.Context, c ProfilePushClaim, p *ProfilePush) error {
	if p != nil {
		h.log.Info("pushed the profile of the changed setting", "profile_source", string(c.Source),
			"service_profile", p.Name, "generation", p.Generation, "round", p.Round)
	}
	stored, ok, err := h.store.Settings(ctx)
	if err != nil {
		return err
	}
	if ok {
		if err := h.startRound(ctx, c.Source, settingsOf(stored)); err != nil {
			return err
		}
	}
	return h.store.DoneProfilePush(ctx, c)
}

// pushOn sends the PPRs of one page of the round of source to the
// S-CSCF of each subscriber, and records each in the Store. It returns
// the round once it has sent the PPR of its last subscriber, and
// whether subscribers are left after the page; a nil round and none
// left for a claim that found no round under way.
//
// Each PPR carries the profile of the subscriber as it is when it is
// sent, under the name of the round (pushRoundProfile). The pushes of
// one subscriber are serialised, and the rounds of one source never
// run at once, so the profile of a later round never lands at an S-CSCF
// before that of an earlier one.
//
// A PPR that fails is logged and nothing more: the settings are in
// force either way, and that S-CSCF reads the profile at the next
// registration of the user. A PPR that this instance could not send for
// want of a connection to the S-CSCF is left for another instance first
// (ProfilePushRetry), and the round goes on. A PPR that is sent twice, when an instance
// stops after its PPA and before it records it, only replaces what the
// S-CSCF holds with the profile it has (TS 29.228 6.2.2.1).
func (h *HSS) pushOn(ctx context.Context, peers Peers, source ProfileSource) (round *ProfilePush, more bool, err error) {
	p, recorded, err := h.store.ProfilePush(ctx, source)
	if err != nil {
		return nil, false, err
	}
	if !recorded || p.Done {
		return nil, false, nil
	}
	if err := h.config.CheckServiceProfileName(string(source), p.Name); err != nil {
		return nil, false, fmt.Errorf("%w: %w", errUnservableRound, err)
	}
	ids, nextPage, err := h.store.ListServed(ctx, source, p.After, profilePushPage)
	if err != nil {
		return nil, false, err
	}
	for _, privateID := range ids {
		// A subscriber that the index names and the store no longer has,
		// or no longer serves, gets no PPR: pushRoundProfile reads it
		// before it builds one.
		err := h.pushRoundProfile(ctx, peers, privateID, p)
		switch {
		case ctx.Err() != nil:
			return nil, false, ctx.Err()
		case errors.Is(err, diameter.ErrPeerUnavailable):
			// This instance holds no connection to the S-CSCF of the
			// subscriber: another instance may.
			r := ProfilePushRetry{Source: source, Round: p.Round, PrivateID: privateID, Since: stamp()}
			if err := h.store.ScheduleProfilePushRetry(ctx, r, dueNow()); err != nil {
				return nil, false, err
			}
			h.log.Info("the PPR goes to an instance that the S-CSCF is connected to", "private_identity", privateID)
		case err != nil:
			h.log.Warn("PPR failed", "private_identity", privateID, "error", err.Error())
		}
		next := p
		next.After, next.Updated = privateID, stamp()
		if err := h.recordPush(ctx, &p, next); err != nil {
			return nil, false, err
		}
		p = next
	}
	if nextPage != "" {
		return nil, true, nil
	}
	done := p
	done.Done, done.Updated = true, stamp()
	if err := h.recordPush(ctx, &p, done); err != nil {
		return nil, false, err
	}
	return &done, false, nil
}

// pushRoundProfile pushes the profile of privateID that the round p
// gives it: the settings in force with the name of the round for its
// source. A subscriber whose profile no longer comes from the source of
// the round, because a provisioning request changed it since the index
// was read, gets no PPR from the round; that request pushed its profile.
func (h *HSS) pushRoundProfile(ctx context.Context, peers Peers, privateID string, p ProfilePush) error {
	return h.pushProfileUnder(ctx, peers, privateID, func(u Subscriber) (*settings, error) {
		if profileSourceOf(u.Subscription) != p.Source {
			return nil, nil
		}
		return h.settings.Load().withName(p.Source, p.Name), nil
	})
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
