package lab

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/fault"
)

// The endpoints of the fault agent that the Lab passes on, under
// /api/fault: a learner reads the faults the agent holds, injects one
// and clears it. The agent is on a network of its own, which the Lab
// joins only with deploy/chaos/agent.yaml, so the browser reaches it
// through the Lab or not at all.
var faultRoutes = []string{
	"GET /api/fault/faults",
	"POST /api/fault/faults",
	"DELETE /api/fault/faults/{id}",
}

// faultAPI forwards the routes of faultRoutes to the fault agent at
// base, /api/fault/... to the paths of the agent's API (see the
// documentation of the fault package).
func faultAPI(base string, log *slog.Logger) (http.Handler, error) {
	return apiProxy("fault agent", base, func(path string) string {
		return fault.PathPrefix + strings.TrimPrefix(path, "/api/fault/")
	}, log)
}
