package maintenance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// pcscf stands for a role: it counts its sessions, refuses new ones
// unless it is UNLOCKED, and reports each session that ends.
type pcscf struct {
	mu       sync.Mutex
	sessions int
	// retryAfter is a setting that changes while the node runs.
	retryAfter int
	fn         *maintenance.ManagedFunction
}

func (p *pcscf) users() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions
}

// invite stands for a new INVITE: a role that is not UNLOCKED takes no
// new session, and would answer 503 with Retry-After.
func (p *pcscf) invite() bool {
	if p.fn.AdministrativeState() != maintenance.Unlocked {
		return false
	}
	p.mu.Lock()
	p.sessions++
	p.mu.Unlock()
	return true
}

func (p *pcscf) bye() {
	p.mu.Lock()
	p.sessions--
	p.mu.Unlock()
	// After the lock is released: UserQuit calls users.
	p.fn.UserQuit()
}

func (p *pcscf) getRetryAfter() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.retryAfter
}

func (p *pcscf) setRetryAfter(v int) {
	p.mu.Lock()
	p.retryAfter = v
	p.mu.Unlock()
}

// view is the configuration file of the node as the API shows it. A
// secret is a maintenance.Secret, so it cannot be shown.
type view struct {
	Node  string `json:"node"`
	PCSCF struct {
		Listen     string `json:"listen"`
		RetryAfter int    `json:"retry_after"`
	} `json:"pcscf"`
	XCAP struct {
		NonceKey maintenance.Secret `json:"nonce_key"`
	} `json:"xcap"`
}

// Example shows how a node mounts the maintenance API, next to its
// /metrics on the same listener, and how an operator drains its P-CSCF
// through the client.
func Example() {
	// The node's log handler takes its level from a LevelVar, which the
	// API sets. A node writes to os.Stdout; the example discards.
	level := new(slog.LevelVar)
	log := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level}))

	p := &pcscf{retryAfter: 30}
	node, err := maintenance.New(maintenance.Config{
		Node:     "pcscf-1",
		Instance: "pcscf-1-7d9f",
		Build:    maintenance.ReadBuild(),
		LogLevel: level,
		Log:      log,
		Functions: []maintenance.Function{{
			Role:  "pcscf",
			Users: p.users,
		}},
		Configuration: func() any {
			var v view
			v.Node = "pcscf-1"
			v.PCSCF.Listen = "0.0.0.0:5060"
			v.PCSCF.RetryAfter = p.getRetryAfter()
			v.XCAP.NonceKey = maintenance.Redacted("test-nonce-key")
			return v
		},
		Settings: []maintenance.Setting{
			maintenance.NewSetting("pcscf.retry_after", "seconds in the Retry-After of a 503 while shutting down",
				p.getRetryAfter,
				func(v int) error {
					if v < 0 || v > 3600 {
						return errors.New("must be from 0 to 3600")
					}
					return nil
				},
				p.setRetryAfter),
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	p.fn = node.ManagedFunction("pcscf")

	mux := http.NewServeMux()
	mux.Handle(maintenance.PathPrefix, node.Handler())
	mux.Handle("GET /metrics", metrics.NewHandler(metrics.NewRegistry()))
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx := context.Background()
	oam := maintenance.NewClient(server.URL, server.Client())

	p.invite()
	p.invite()
	f, err := oam.SetAdministrativeState(ctx, "pcscf", maintenance.AdministrativeStateRequest{
		AdministrativeState: maintenance.ShuttingDown,
		TimeoutSeconds:      600,
		Operator:            "test-operator",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("after shut down:", f.AdministrativeState, *f.Users, "users")
	fmt.Println("new INVITE taken:", p.invite())
	p.bye()
	p.bye()
	state, _ := oam.State(ctx)
	fmt.Println("after the last BYE:", state.Functions[0].AdministrativeState, *state.Functions[0].Users, "users")

	change, err := oam.ChangeConfiguration(ctx, maintenance.ConfigurationChangeRequest{
		Settings: map[string]json.RawMessage{"pcscf.retry_after": json.RawMessage(`120`)},
		Operator: "test-operator",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("generation %d: %s %s -> %s\n", change.Generation, change.Changes[0].Name, change.Changes[0].Previous, change.Changes[0].Value)
	conf, _ := oam.Configuration(ctx)
	fmt.Println(string(conf.Configuration))

	if _, err := oam.SetLogLevel(ctx, maintenance.LogLevelRequest{Level: slog.LevelDebug}); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("log level:", level.Level())

	if _, err := oam.SetAdministrativeState(ctx, "pcscf", maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.Unlocked}); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("new INVITE taken:", p.invite())
	// Output:
	// after shut down: SHUTTINGDOWN 2 users
	// new INVITE taken: false
	// after the last BYE: LOCKED 0 users
	// generation 1: pcscf.retry_after 30 -> 120
	// {"node":"pcscf-1","pcscf":{"listen":"0.0.0.0:5060","retry_after":120},"xcap":{"nonce_key":"REDACTED"}}
	// log level: DEBUG
	// new INVITE taken: true
}

// scscfHSS stands for the part of an S-CSCF that watches its Diameter
// connection to the HSS: it raises an alarm when the connection goes
// down and clears it when the connection is back.
type scscfHSS struct {
	node *maintenance.Node
	log  *slog.Logger
}

// hssDown is the alarm's identity: the same values each time, so that
// raising it again updates the one record (TS 28.111 6.4).
var hssDown = maintenance.AlarmIdentity{
	Role:            "scscf",
	Peer:            "hss.ims.mnc001.mcc001.3gppnetwork.org",
	AlarmType:       maintenance.CommunicationsAlarm,
	ProbableCause:   maintenance.CommunicationsSubsystemFailure,
	SpecificProblem: "Diameter peer down",
}

func (s *scscfHSS) connectionDown(reason string) {
	if err := s.node.RaiseAlarm(maintenance.Alarm{AlarmIdentity: hssDown, PerceivedSeverity: maintenance.Major, AdditionalText: reason}); err != nil {
		s.log.Error("alarm refused", "error", err)
	}
}

func (s *scscfHSS) connectionUp() {
	if err := s.node.ClearAlarm(hssDown); err != nil {
		s.log.Error("alarm refused", "error", err)
	}
}

// ExampleNode_RaiseAlarm shows how a role raises and clears an alarm,
// and how the operator reads and acknowledges it through the client.
func ExampleNode_RaiseAlarm() {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	node, err := maintenance.New(maintenance.Config{
		Node:          "scscf-1",
		LogLevel:      new(slog.LevelVar),
		Log:           log,
		Functions:     []maintenance.Function{{Role: "scscf"}},
		Configuration: func() any { return map[string]string{"node": "scscf-1"} },
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	mux := http.NewServeMux()
	mux.Handle(maintenance.PathPrefix, node.Handler())
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx := context.Background()
	oam := maintenance.NewClient(server.URL, server.Client())

	s := &scscfHSS{node: node, log: log}
	s.connectionDown("no answer to 3 DWR")
	s.connectionDown("no answer to 3 DWR")
	l, _ := oam.Alarms(ctx, maintenance.AlarmFilter{})
	r := l.AlarmRecords[0]
	fmt.Println(l.NumOfAlarmRecords, r.ObjectInstance)
	fmt.Println(r.AlarmType, r.ProbableCause, r.SpecificProblem, r.PerceivedSeverity, r.AckState)

	s.connectionUp()
	notCleared := false
	l, _ = oam.Alarms(ctx, maintenance.AlarmFilter{Cleared: &notCleared})
	fmt.Println("not cleared:", len(l.AlarmRecords), "in the list:", l.NumOfAlarmRecords)

	if _, err := oam.SetAckState(ctx, r.AlarmID, maintenance.AckStateRequest{AckState: maintenance.Acknowledged, Operator: "test-operator"}); err != nil {
		fmt.Println(err)
		return
	}
	l, _ = oam.Alarms(ctx, maintenance.AlarmFilter{})
	fmt.Println("after the acknowledgement:", l.NumOfAlarmRecords)
	// Output:
	// 1 ManagedElement=scscf-1,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org
	// COMMUNICATIONS_ALARM COMMUNICATIONS_SUBSYSTEM_FAILURE Diameter peer down MAJOR UNACKNOWLEDGED
	// not cleared: 0 in the list: 1
	// after the acknowledgement: 0
}
