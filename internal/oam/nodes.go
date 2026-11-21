package oam

import (
	"context"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// targets is the endpoints that a request names: the node of the
// {node} path segment, or every node of the {role} one.
func (s *Server) targets(r *http.Request) ([]Endpoint, error) {
	if name := r.PathValue("node"); name != "" {
		e, err := s.node(name)
		if err != nil {
			return nil, err
		}
		return []Endpoint{e}, nil
	}
	return s.role(r.PathValue("role"))
}

// State reads the state of each target (GET /maintenance/v1/state).
func (s *Server) State(ctx context.Context, targets []Endpoint) FanOut[maintenance.NodeState] {
	return fanOut(ctx, s, targets, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.NodeState, error) {
		return c.State(ctx)
	})
}

// SetAdministrativeState asks each target for the event of X.731 7.1.3
// that req names, on role: SHUTTINGDOWN starts the drain, UNLOCKED
// cancels it, LOCKED locks. A node that is in the state already changes
// nothing and succeeds, so the same request may go to every node of a
// role.
func (s *Server) SetAdministrativeState(ctx context.Context, targets []Endpoint, role string, req maintenance.AdministrativeStateRequest) FanOut[maintenance.FunctionState] {
	s.log.Info("administrative state requested", "role", role, "nodes", names(targets),
		"administrative_state", string(req.AdministrativeState), "operator", req.Operator)
	return fanOut(ctx, s, targets, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.FunctionState, error) {
		return c.SetAdministrativeState(ctx, role, req)
	})
}

// LogLevel reads the log level of each target.
func (s *Server) LogLevel(ctx context.Context, targets []Endpoint) FanOut[maintenance.LogLevel] {
	return fanOut(ctx, s, targets, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.LogLevel, error) {
		return c.LogLevel(ctx)
	})
}

// SetLogLevel sets the log level of each target. The level belongs to
// the node, so it changes for every role of the node.
func (s *Server) SetLogLevel(ctx context.Context, targets []Endpoint, req maintenance.LogLevelRequest) FanOut[maintenance.LogLevelChange] {
	s.log.Info("log level requested", "nodes", names(targets), "level", req.Level.String(), "operator", req.Operator)
	return fanOut(ctx, s, targets, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.LogLevelChange, error) {
		return c.SetLogLevel(ctx, req)
	})
}

// Configuration reads the configuration of each target, with its
// secrets redacted by the node.
func (s *Server) Configuration(ctx context.Context, targets []Endpoint) FanOut[maintenance.Configuration] {
	return fanOut(ctx, s, targets, func(ctx context.Context, c *maintenance.Client, _ Endpoint) (maintenance.Configuration, error) {
		conf, err := c.Configuration(ctx)
		if err == nil {
			// The node checks its view before it answers; a node of
			// another build might not, and the OAM passes on nothing
			// that holds a secret.
			err = maintenance.CheckRedacted(conf)
		}
		return conf, err
	})
}

func names(es []Endpoint) []string {
	ns := make([]string, len(es))
	for i, e := range es {
		ns[i] = e.Name
	}
	return ns
}

func (s *Server) nodeRoutes(rt *routes) {
	rt.handle(http.MethodGet, PathPrefix+"inventory", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Inventory{Nodes: s.nodes})
	})
	rt.handle(http.MethodGet, PathPrefix+"state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.State(r.Context(), s.nodes))
	})
	for _, scope := range []string{PathPrefix + "nodes/{node}/", PathPrefix + "roles/{role}/"} {
		rt.handle(http.MethodGet, scope+"state", s.withTargets(func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
			writeJSON(w, http.StatusOK, s.State(r.Context(), ts))
		}))
		rt.handle(http.MethodGet, scope+"log-level", s.withTargets(func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
			writeJSON(w, http.StatusOK, s.LogLevel(r.Context(), ts))
		}))
		rt.handle(http.MethodPut, scope+"log-level", s.withTargets(func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
			var req maintenance.LogLevelRequest
			if err := readJSON(w, r, &req, false); err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, s.SetLogLevel(r.Context(), ts, req))
		}))
		rt.handle(http.MethodGet, scope+"config", s.withTargets(func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
			writeJSON(w, http.StatusOK, s.Configuration(r.Context(), ts))
		}))
	}
	administrativeState := func(w http.ResponseWriter, r *http.Request, ts []Endpoint) {
		var req maintenance.AdministrativeStateRequest
		if err := readJSON(w, r, &req, false); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.SetAdministrativeState(r.Context(), ts, r.PathValue("role"), req))
	}
	rt.handle(http.MethodPut, PathPrefix+"nodes/{node}/functions/{role}/administrative-state", s.withNode(administrativeState))
	rt.handle(http.MethodPut, PathPrefix+"roles/{role}/administrative-state", s.withTargets(administrativeState))
}

// withTargets answers 404 for a node or role that the inventory does
// not have, and otherwise calls h with the endpoints of the request.
func (s *Server) withTargets(h func(http.ResponseWriter, *http.Request, []Endpoint)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ts, err := s.targets(r)
		if err != nil {
			writeError(w, err)
			return
		}
		h(w, r, ts)
	}
}

// withNode is withTargets for a path that has both {node} and {role}:
// the node is the target, and the role is the node's to check.
func (s *Server) withNode(h func(http.ResponseWriter, *http.Request, []Endpoint)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, err := s.node(r.PathValue("node"))
		if err != nil {
			writeError(w, err)
			return
		}
		h(w, r, []Endpoint{e})
	}
}

// Handler serves the OAM API at the paths of the package documentation.
// A role mounts it under PathPrefix.
func (s *Server) Handler() http.Handler {
	rt := newRoutes()
	s.nodeRoutes(rt)
	s.subscriptionRoutes(rt)
	s.backupRoutes(rt)
	s.historyRoutes(rt)
	s.testUserRoutes(rt)
	s.alarmRoutes(rt)
	return rt.handler()
}
