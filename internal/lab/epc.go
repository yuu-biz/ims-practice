package lab

import (
	"log/slog"
	"net/http"
	"strings"
)

// The endpoints of the EPC stub that the Lab passes on, under
// /api/epc: a learner reads a UE's bearers, and makes the network
// deactivate one or detach the UE, procedures that the network
// initiates, from the host, where the addresses of the Compose network
// may not be reachable. The UE API of the stub is not among them.
var epcRoutes = []string{
	"GET /api/epc/ues/{imsi}",
	"DELETE /api/epc/network-initiated/ues/{imsi}/bearers/{ebi}",
	"POST /api/epc/network-initiated/ues/{imsi}/detach",
}

// epcAPI forwards the routes of epcRoutes to the EPC stub at base,
// /api/epc/... to /... (see epc.EPC.Handler).
func epcAPI(base string, log *slog.Logger) (http.Handler, error) {
	return apiProxy("EPC stub", base, func(path string) string {
		return strings.TrimPrefix(path, "/api/epc")
	}, log)
}
