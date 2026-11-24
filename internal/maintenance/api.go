package maintenance

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"time"
)

// The paths of the API.
const (
	PathPrefix        = "/maintenance/v1/"
	StatePath         = PathPrefix + "state"
	ConfigurationPath = PathPrefix + "config"
	LogLevelPath      = PathPrefix + "log-level"
	AlarmsPath        = PathPrefix + "alarms"
	AlarmEventsPath   = PathPrefix + "alarm-events"

	administrativeStatePattern = PathPrefix + "functions/{role}/administrative-state"
	ackStatePattern            = PathPrefix + "alarms/{id}/ack-state"
)

// AdministrativeStatePath is the path of the administrative state of a
// role.
func AdministrativeStatePath(role string) string {
	return PathPrefix + "functions/" + url.PathEscape(role) + "/administrative-state"
}

// AckStatePath is the path of the acknowledgement state of an alarm.
func AckStatePath(alarmID string) string {
	return PathPrefix + "alarms/" + url.PathEscape(alarmID) + "/ack-state"
}

// Build is the version of the software that runs the node, which the
// node supplies (ReadBuild gives it from the binary).
type Build struct {
	Version   string `json:"version,omitempty"`
	Revision  string `json:"revision,omitempty"`
	GoVersion string `json:"go_version,omitempty"`
}

// NodeState is the answer to GET /maintenance/v1/state: the node, the
// ManagedElement of TS 28.622 4.3.3, with each of its roles.
type NodeState struct {
	// Node is the node's name from its configuration.
	Node string `json:"node"`
	// Instance tells apart the processes that run the same
	// configuration, such as the host or pod name.
	Instance  string    `json:"instance,omitempty"`
	Build     Build     `json:"build"`
	StartTime time.Time `json:"start_time"`
	// ConfigurationGeneration counts the configuration changes, or is
	// the generation that the last change carried.
	ConfigurationGeneration uint64          `json:"configuration_generation"`
	LogLevel                slog.Level      `json:"log_level"`
	Functions               []FunctionState `json:"functions"`
}

// FunctionState is one role of the node, its ManagedFunction (TS 28.622
// 4.3.4), with the state attributes of TS 28.622 5.3.
type FunctionState struct {
	Role                string              `json:"role"`
	AdministrativeState AdministrativeState `json:"administrative_state"`
	OperationalState    OperationalState    `json:"operational_state"`
	// Users is what Function.Users counts, absent for a role that
	// counts none.
	Users *int `json:"users,omitempty"`
	// ShutdownDeadline is when a SHUTTINGDOWN role becomes LOCKED even
	// with users left; absent without one.
	ShutdownDeadline *time.Time `json:"shutdown_deadline,omitempty"`
	// Summary is what Function.Summary gives, absent without one.
	Summary json.RawMessage `json:"summary,omitempty"`
}

// AdministrativeStateRequest is the body of PUT
// /maintenance/v1/functions/{role}/administrative-state. The value asks
// for the event of X.731 7.1.3 that leads to it: UNLOCKED for unlock,
// SHUTTINGDOWN for shut down, LOCKED for lock.
type AdministrativeStateRequest struct {
	AdministrativeState AdministrativeState `json:"administrative_state"`
	// TimeoutSeconds bounds a shut down (ManagedFunction.ShutDown); 0
	// for none. Only SHUTTINGDOWN takes it.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Operator names who asks, for the log.
	Operator string `json:"operator,omitempty"`
}

// LogLevel is the answer to GET /maintenance/v1/log-level. The level
// has the names of log/slog: DEBUG, INFO, WARN, ERROR, or one of them
// with an offset such as INFO+2.
type LogLevel struct {
	Level slog.Level `json:"level"`
}

// LogLevelRequest is the body of PUT /maintenance/v1/log-level.
type LogLevelRequest struct {
	Level    slog.Level `json:"level"`
	Operator string     `json:"operator,omitempty"`
}

// LogLevelChange is the answer to PUT /maintenance/v1/log-level.
type LogLevelChange struct {
	Level    slog.Level `json:"level"`
	Previous slog.Level `json:"previous"`
}

// ErrorBody is the body of every answer that is not a success.
type ErrorBody struct {
	Error string `json:"error"`
	// Settings names, for a refused configuration change, each setting
	// that was refused and why.
	Settings map[string]string `json:"settings,omitempty"`
}
