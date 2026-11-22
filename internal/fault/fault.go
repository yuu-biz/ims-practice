package fault

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Kind is what a fault does to its target.
type Kind string

const (
	Stop     Kind = "stop"
	Kill     Kind = "kill"
	Pause    Kind = "pause"
	Crash    Kind = "crash"
	Recreate Kind = "recreate"

	Delay     Kind = "delay"
	Loss      Kind = "loss"
	Partition Kind = "partition"
)

// Kinds are the kinds a request may ask for. deploy/chaos/fault.sh has
// a command for each of them (TestFaultScriptAndPackageAgree).
var Kinds = []Kind{Stop, Kill, Pause, Crash, Recreate, Delay, Loss, Partition}

// lasting tells whether a fault of this kind stays on the target until
// something clears it. A crash and a recreate happen once: the node
// dies and the container runtime brings it back, so there is nothing to
// undo and no duration to run out.
func (k Kind) lasting() bool { return k != Crash && k != Recreate }

// State is where a fault is in its life. Applying and Clearing last as
// long as the executor works; Done, Cleared, Expired and Failed are
// ends.
type State string

const (
	Applying State = "applying"
	Active   State = "active"
	Clearing State = "clearing"
	// Done is the end of a fault that happens once, a crash or a
	// recreate: it is over as soon as the executor has done it, and
	// there is nothing to clear.
	Done    State = "done"
	Cleared State = "cleared"
	Expired State = "expired"
	Failed  State = "failed"
)

func (s State) ended() bool { return s == Done || s == Cleared || s == Expired || s == Failed }

// The bounds of a request. A lab fault lasts minutes; an hour is long
// enough for any exercise and short enough that a forgotten one ends.
const (
	DefaultDuration = 5 * time.Minute
	MinDuration     = time.Second
	MaxDuration     = time.Hour
	// MaxDelay keeps the delay well below the SIP timers B and F
	// (64*T1, 32 s, RFC 3261 17.1.1.2 and 17.1.2.2) so that a delayed
	// call can still be set up.
	MaxDelay = 10 * time.Second
)

// Duration is a time.Duration that JSON carries as a Go duration
// string, such as "200ms" or "5m0s".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("a duration is a string such as \"200ms\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Request is what to inject: the body of POST /fault/v1/faults. Only
// the fields of its kind may be set.
type Request struct {
	// Target is the Compose service of the node, such as "pcscf" or
	// "ibcf-a", or one container of it, such as "pcscf:2", when the
	// service runs several (docker compose up --scale). The number is
	// the container's com.docker.compose.container-number label.
	Target string `json:"target"`
	Kind   Kind   `json:"kind"`
	// Network: kinds delay, loss and partition. The Compose network,
	// such as "nni", on whose interface of the target the fault sits;
	// empty means every interface of the target.
	Network string `json:"network,omitempty"`
	// Delay and Jitter: kind delay. Each packet that the target sends
	// waits Delay, give or take up to Jitter.
	Delay  Duration `json:"delay,omitzero"`
	Jitter Duration `json:"jitter,omitzero"`
	// LossPercent: kind loss. Each IP packet that the target sends or
	// receives is dropped with this chance, from 1 to 100.
	LossPercent int `json:"loss_percent,omitzero"`
	// Peers: kind partition. What the target can no longer reach nor
	// hear: a service, with every container of it ("pcscf"), one
	// container ("pcscf:2"), every service that a path.Match pattern
	// matches ("scscf-*"), or an IPv4 address. None cuts the target
	// off from everything.
	Peers []string `json:"peers,omitempty"`
	// Duration is how long the fault lasts; zero means
	// DefaultDuration. The kinds crash and recreate happen once, so
	// they take none.
	Duration Duration `json:"duration,omitzero"`
}

// Fault is a fault that Manager has taken, with its state.
type Fault struct {
	ID string `json:"id"`
	Request
	State State `json:"state"`
	// Error says why the fault failed.
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when the fault ends by itself. A fault that happens
	// once has none.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
}

// ErrInvalidRequest is the answer to a request that the API cannot
// take as it is.
var ErrInvalidRequest = errors.New("invalid request")

// serviceName is what Compose takes as the name of a service, and of a
// network.
var serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// targetName is a service, maybe with the number of one container.
var targetName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*(:[1-9][0-9]*)?$`)

// peerName is a target, or a path.Match pattern of services.
var peerName = regexp.MustCompile(`^[a-z0-9*?][a-z0-9_.*?-]*(:[1-9][0-9]*)?$`)

// splitTarget parses "pcscf:2" into "pcscf" and "2", and "pcscf" into
// "pcscf" and "".
func splitTarget(t string) (service, number string) {
	service, number, _ = strings.Cut(t, ":")
	return service, number
}

// validate checks r and fills in the default duration.
func (r *Request) validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, a...))
	}
	if !targetName.MatchString(r.Target) {
		return bad("target %q is neither a Compose service nor service:number", r.Target)
	}
	if !slices.Contains(Kinds, r.Kind) {
		return bad("unknown kind %q", r.Kind)
	}
	if r.Kind != Delay && (r.Delay != 0 || r.Jitter != 0) {
		return bad("delay and jitter are for the kind delay")
	}
	if r.Kind != Loss && r.LossPercent != 0 {
		return bad("loss_percent is for the kind loss")
	}
	if r.Kind != Partition && len(r.Peers) != 0 {
		return bad("peers are for the kind partition")
	}
	if r.Network != "" {
		if r.Kind != Delay && r.Kind != Loss && r.Kind != Partition {
			return bad("network is for the kinds delay, loss and partition")
		}
		if !serviceName.MatchString(r.Network) {
			return bad("network %q is not the name of a Compose network", r.Network)
		}
	}
	switch r.Kind {
	case Delay:
		if r.Delay <= 0 || time.Duration(r.Delay) > MaxDelay {
			return bad("delay must be above 0 and at most %v", MaxDelay)
		}
		if r.Jitter < 0 || r.Jitter > r.Delay {
			return bad("jitter must be from 0 to the delay")
		}
	case Loss:
		if r.LossPercent < 1 || r.LossPercent > 100 {
			return bad("loss_percent must be from 1 to 100")
		}
	case Partition:
		for i, p := range r.Peers {
			// The Compose network is IPv4 only (compose.yaml), so the
			// rules match IPv4 headers.
			if a, err := netip.ParseAddr(p); err == nil {
				if !a.Is4() {
					return bad("peer %q is not an IPv4 address", p)
				}
			} else if !peerName.MatchString(p) {
				return bad("peer %q is neither a Compose service, a pattern of services nor an address", p)
			}
			if p == r.Target {
				return bad("the target cannot be its own peer")
			}
			if slices.Contains(r.Peers[:i], p) {
				return bad("peer %q is listed twice", p)
			}
		}
	}
	if !r.Kind.lasting() {
		if r.Duration != 0 {
			return bad("duration is for the faults that last; a %s is over as soon as it has happened", r.Kind)
		}
		return nil
	}
	if r.Duration == 0 {
		r.Duration = Duration(DefaultDuration)
	}
	if d := time.Duration(r.Duration); d < MinDuration || d > MaxDuration {
		return bad("duration must be from %v to %v", MinDuration, MaxDuration)
	}
	return nil
}

// slot names what a fault occupies on its target; two faults in the
// same slot would undo each other. The node faults that last share one
// slot, since a stopped node can be neither killed nor paused. Delay
// owns the root qdisc of the interface. Loss and partition each have
// their own nftables table, but two of one kind would make clearing one
// look like it did nothing.
//
// A fault that happens once occupies nothing, and takes no slot: it is
// over before the next request can meet it.
func (r Request) slot() string {
	switch {
	case !r.Kind.lasting():
		return ""
	case r.Kind == Stop || r.Kind == Kill || r.Kind == Pause:
		return r.Target + "/node"
	default:
		return r.Target + "/" + string(r.Kind)
	}
}
