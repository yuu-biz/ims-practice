package oam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// AlarmWatchState is how far the OAM has sent the notifications of the
// alarm journal of one node (maintenance.AlarmEvents). It lives outside
// the OAM process, so that the instance that watches the node next
// goes on from where the last one stopped.
type AlarmWatchState struct {
	// Generation counts the watches of the node. Each instance that
	// takes the watch raises it before it writes anything else, so that
	// an instance that has lost the watch, and still writes with the
	// generation it had, is refused.
	Generation uint64 `json:"generation"`
	// Seed is random, chosen when the state is first written. The
	// notificationIds that the OAM makes for the notifications about
	// the alarm list of the node start from it (alarmNotificationID).
	Seed string `json:"seed"`
	// JournalID is the journal of the node that LastNumber counts in;
	// empty before the first answer of the node.
	JournalID string `json:"journal_id,omitempty"`
	// LastNumber is the number of the last event whose notifications
	// are sent.
	LastNumber uint64 `json:"last_number"`
	// Unreachable says that the node stopped answering, and that
	// notifyPotentialFaultyAlarmList said so.
	Unreachable bool `json:"unreachable,omitempty"`
	// Transitions counts the times the node stopped answering and came
	// back, which numbers the notifications that say so.
	Transitions uint64 `json:"transitions,omitempty"`
	// Lost says that the store could not read the state that it had:
	// the node is watched from the end of its journal, as one never
	// watched, but what happened since the last state is not notified.
	Lost bool `json:"lost,omitempty"`
}

// ErrWatchLost is the answer to an instance that writes the state of a
// watch that another instance has taken since.
var ErrWatchLost = errors.New("another instance has taken the watch of the node")

// AlarmWatch is the watch of one node that an OAM instance holds: the
// right to send the notifications of the node's alarm journal, and to
// write how far it has sent them. Renew may run while Save does, as
// the watcher renews on a goroutine of its own.
type AlarmWatch interface {
	// State is the state as the watch began, with the generation it
	// raised.
	State() AlarmWatchState
	// Save writes st, which keeps the generation of State. It is
	// ErrWatchLost when another instance has taken the watch since.
	Save(ctx context.Context, st AlarmWatchState) error
	// Renew keeps the watch for one more lease. It is ErrWatchLost when
	// another instance has taken the watch.
	Renew(ctx context.Context) error
	// Release gives the watch up, for another instance to take.
	Release(ctx context.Context) error
}

// AlarmWatchStore keeps the watches of the nodes and their states.
// Each node is watched by one instance at a time.
type AlarmWatchStore interface {
	// Watch takes the watch of node for the OAM instance named
	// instance, and raises the generation of its state. ok is false,
	// with no error, when another instance holds it.
	Watch(ctx context.Context, node, instance string) (w AlarmWatch, ok bool, err error)
	// AlarmWatchState reads the state of node: the zero state when no
	// instance has watched it yet.
	AlarmWatchState(ctx context.Context, node string) (AlarmWatchState, error)
	// Present says that the OAM instance named instance runs, for one
	// lease, and counts the instances that do, itself included.
	Present(ctx context.Context, instance string) (int, error)
}

// FileAlarmWatchStore keeps the state of each node in a file of its own
// in a directory, <node>.json, which each change writes anew and renames
// into place (replaceFile). It serves one OAM instance, which holds the
// watch of every node: the watches are in its memory, and a second
// instance on the same directory would take them as well. Several
// instances share a kvstore.Store instead (internal/kvstore/oamstore).
//
// A state file that cannot be read, such as one that a crash of the
// file system left empty, counts as a state that was lost (Lost): the
// node is watched again from the end of its journal, the log says so,
// and the OSSs are sent a notifyAlarmListRebuilt for the node, since
// the notifications of what happened in between are not sent. Refusing
// to watch the node would send no notification at all.
type FileAlarmWatchStore struct {
	dir string
	log *slog.Logger
	mu  sync.Mutex
	// held is the generation of each watch that is held.
	held map[string]uint64
}

// NewFileAlarmWatchStore uses dir, which it makes if need be, and logs
// to log; nil for none.
func NewFileAlarmWatchStore(dir string, log *slog.Logger) (*FileAlarmWatchStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &FileAlarmWatchStore{dir: dir, log: log, held: map[string]uint64{}}, nil
}

func (f *FileAlarmWatchStore) path(node string) string { return filepath.Join(f.dir, node+".json") }

func (f *FileAlarmWatchStore) read(node string) (AlarmWatchState, error) {
	b, err := os.ReadFile(f.path(node))
	if errors.Is(err, os.ErrNotExist) {
		return AlarmWatchState{}, nil
	}
	if err != nil {
		return AlarmWatchState{}, err
	}
	var st AlarmWatchState
	if err := json.Unmarshal(b, &st); err != nil {
		f.log.Warn("the watch state of a node cannot be read; the node is watched from the end of its journal",
			"node", node, "file", f.path(node), "error", err.Error())
		return AlarmWatchState{Lost: true}, nil
	}
	return st, nil
}

func (f *FileAlarmWatchStore) write(node string, st AlarmWatchState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return replaceFile(f.path(node), b)
}

func (f *FileAlarmWatchStore) Watch(_ context.Context, node, _ string) (AlarmWatch, bool, error) {
	if !maintenance.IsName(node) {
		return nil, false, fmt.Errorf("oam: %q is no node name", node)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, held := f.held[node]; held {
		return nil, false, nil
	}
	st, err := f.read(node)
	if err != nil {
		return nil, false, err
	}
	if st.Seed == "" {
		st.Seed = randomHex(8)
	}
	st.Generation++
	if err := f.write(node, st); err != nil {
		return nil, false, err
	}
	f.held[node] = st.Generation
	return &fileAlarmWatch{store: f, node: node, state: st}, true, nil
}

func (f *FileAlarmWatchStore) AlarmWatchState(_ context.Context, node string) (AlarmWatchState, error) {
	if !maintenance.IsName(node) {
		return AlarmWatchState{}, fmt.Errorf("oam: %q is no node name", node)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read(node)
}

// Present counts this instance only: the store serves one.
func (f *FileAlarmWatchStore) Present(context.Context, string) (int, error) { return 1, nil }

type fileAlarmWatch struct {
	store *FileAlarmWatchStore
	node  string
	state AlarmWatchState
}

func (w *fileAlarmWatch) State() AlarmWatchState { return w.state }

// holds says, with the lock held, whether the watch is still this one.
func (w *fileAlarmWatch) holds() bool {
	g, ok := w.store.held[w.node]
	return ok && g == w.state.Generation
}

func (w *fileAlarmWatch) Save(_ context.Context, st AlarmWatchState) error {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if !w.holds() || st.Generation != w.state.Generation {
		return ErrWatchLost
	}
	return w.store.write(w.node, st)
}

func (w *fileAlarmWatch) Renew(context.Context) error {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if !w.holds() {
		return ErrWatchLost
	}
	return nil
}

func (w *fileAlarmWatch) Release(context.Context) error {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if w.holds() {
		delete(w.store.held, w.node)
	}
	return nil
}
