package lab

import (
	"log/slog"
	"net/http"
	"strings"
)

// subscriberAPI forwards the subscriber endpoints to the HSS
// provisioning API, /api/subscribers[/{impi}] to /subscriptions[/{impi}].
func subscriberAPI(provisioning string, log *slog.Logger) (http.Handler, error) {
	return apiProxy("HSS provisioning API", provisioning, func(path string) string {
		return "/subscriptions" + strings.TrimPrefix(path, "/api/subscribers")
	}, log)
}
