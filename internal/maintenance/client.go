package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client calls the maintenance API of one node, as the OAM server
// and the tests do. Each method is one operation of the API.
type Client struct {
	base string
	http *http.Client
}

// NewClient is a client of the node at base, such as
// http://pcscf-1:8085. A nil httpClient is http.DefaultClient.
func NewClient(base string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{base: strings.TrimSuffix(base, "/"), http: httpClient}
}

// State is GET /maintenance/v1/state.
func (c *Client) State(ctx context.Context) (NodeState, error) {
	var s NodeState
	err := c.do(ctx, http.MethodGet, StatePath, nil, &s)
	return s, err
}

// SetAdministrativeState is PUT
// /maintenance/v1/functions/{role}/administrative-state.
func (c *Client) SetAdministrativeState(ctx context.Context, role string, req AdministrativeStateRequest) (FunctionState, error) {
	var s FunctionState
	err := c.do(ctx, http.MethodPut, AdministrativeStatePath(role), req, &s)
	return s, err
}

// Configuration is GET /maintenance/v1/config.
func (c *Client) Configuration(ctx context.Context) (Configuration, error) {
	var conf Configuration
	err := c.do(ctx, http.MethodGet, ConfigurationPath, nil, &conf)
	return conf, err
}

// ChangeConfiguration is PATCH /maintenance/v1/config.
func (c *Client) ChangeConfiguration(ctx context.Context, req ConfigurationChangeRequest) (ConfigurationChange, error) {
	var change ConfigurationChange
	err := c.do(ctx, http.MethodPatch, ConfigurationPath, req, &change)
	return change, err
}

// LogLevel is GET /maintenance/v1/log-level.
func (c *Client) LogLevel(ctx context.Context) (LogLevel, error) {
	var l LogLevel
	err := c.do(ctx, http.MethodGet, LogLevelPath, nil, &l)
	return l, err
}

// SetLogLevel is PUT /maintenance/v1/log-level.
func (c *Client) SetLogLevel(ctx context.Context, req LogLevelRequest) (LogLevelChange, error) {
	var l LogLevelChange
	err := c.do(ctx, http.MethodPut, LogLevelPath, req, &l)
	return l, err
}

// Alarms is GET /maintenance/v1/alarms, with the filter as its query.
func (c *Client) Alarms(ctx context.Context, f AlarmFilter) (AlarmList, error) {
	path := AlarmsPath
	if q := f.Query().Encode(); q != "" {
		path += "?" + q
	}
	var l AlarmList
	err := c.do(ctx, http.MethodGet, path, nil, &l)
	return l, err
}

// SetAckState is PUT /maintenance/v1/alarms/{id}/ack-state.
func (c *Client) SetAckState(ctx context.Context, alarmID string, req AckStateRequest) (AlarmRecord, error) {
	var a AlarmRecord
	err := c.do(ctx, http.MethodPut, AckStatePath(alarmID), req, &a)
	return a, err
}

// AlarmEvents is GET /maintenance/v1/alarm-events, with q as its
// query. The http.Client of c must wait longer than q.Wait.
func (c *Client) AlarmEvents(ctx context.Context, q AlarmEventsQuery) (AlarmEvents, error) {
	path := AlarmEventsPath
	if v := q.Query().Encode(); v != "" {
		path += "?" + v
	}
	var e AlarmEvents
	err := c.do(ctx, http.MethodGet, path, nil, &e)
	return e, err
}

// SetAckStates is PATCH /maintenance/v1/alarms.
func (c *Client) SetAckStates(ctx context.Context, req AckStatesRequest) (AckStatesChange, error) {
	var change AckStatesChange
	err := c.do(ctx, http.MethodPatch, AlarmsPath, req, &change)
	return change, err
}

func (c *Client) do(ctx context.Context, method, path string, body, answer any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return responseError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(answer)
}

// APIError is an answer of the API that is not a success. StatusCode
// tells the cases apart: 400 for a request the node refuses, 404 for a
// role it does not run or an alarm that is not in its list, 409 for a transition that X.731 does not allow
// or a stale configuration generation, 500 when the node fails.
type APIError struct {
	StatusCode int
	Message    string
	// Settings is ErrorBody.Settings: the refused settings of a
	// configuration change.
	Settings map[string]string
}

func (e *APIError) Error() string { return fmt.Sprintf("maintenance: %d: %s", e.StatusCode, e.Message) }

func responseError(resp *http.Response) error {
	var body ErrorBody
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxRequestBody))
	if json.Unmarshal(b, &body) != nil || body.Error == "" {
		body = ErrorBody{Error: strings.TrimSpace(string(b))}
	}
	return &APIError{StatusCode: resp.StatusCode, Message: body.Error, Settings: body.Settings}
}
