package oam

import (
	"context"
	"errors"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// FanOut is the answer to an operation on several nodes: one entry for
// every node that the operation was for, in the order of the inventory,
// whether it succeeded or not. A node that failed is never left out.
type FanOut[T any] struct {
	Succeeded int             `json:"succeeded"`
	Failed    int             `json:"failed"`
	Nodes     []NodeResult[T] `json:"nodes"`
}

// NodeResult is what one node did. Result is set when the node did
// what was asked; Error is set when something failed. Both are set when
// the node did it and the OAM failed afterwards, such as a
// configuration change that the history could not record.
type NodeResult[T any] struct {
	Node   string     `json:"node"`
	URL    string     `json:"url"`
	Result *T         `json:"result,omitempty"`
	Error  *NodeError `json:"error,omitempty"`
}

// NodeError is why a node failed: the status and ErrorBody of the
// maintenance API when the node answered, or why there was no answer.
type NodeError struct {
	// StatusCode is the status of the node's answer; absent when the
	// node did not answer.
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error"`
	// Settings names each refused setting of a configuration change.
	Settings map[string]string `json:"settings,omitempty"`
}

// fanOut calls op on each target, at most nodeConcurrency at a time and
// each within nodeTimeout, and gathers what each did.
func fanOut[T any](ctx context.Context, s *Server, targets []Endpoint, op func(context.Context, *maintenance.Client, Endpoint) (T, error)) FanOut[T] {
	results := make([]NodeResult[T], len(targets))
	sem := make(chan struct{}, s.nodeConcurrency)
	var wg sync.WaitGroup
	for i, e := range targets {
		results[i] = NodeResult[T]{Node: e.Name, URL: e.URL}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			callCtx, cancel := context.WithTimeout(ctx, s.nodeTimeout)
			defer cancel()
			v, err := op(callCtx, maintenance.NewClient(e.URL, s.httpClient), e)
			if err != nil {
				results[i].Error = s.nodeError(err)
				return
			}
			results[i].Result = &v
		})
	}
	wg.Wait()
	return tally(results)
}

func tally[T any](results []NodeResult[T]) FanOut[T] {
	f := FanOut[T]{Nodes: results}
	for _, r := range results {
		if r.Error != nil {
			f.Failed++
		} else {
			f.Succeeded++
		}
	}
	return f
}

func (s *Server) nodeError(err error) *NodeError {
	var api *maintenance.APIError
	switch {
	case errors.As(err, &api):
		return &NodeError{StatusCode: api.StatusCode, Error: api.Message, Settings: api.Settings}
	case errors.Is(err, context.DeadlineExceeded):
		return &NodeError{Error: "no answer within " + s.nodeTimeout.String()}
	}
	return &NodeError{Error: err.Error()}
}
