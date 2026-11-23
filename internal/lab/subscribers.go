package lab

import (
	"log/slog"
	"net/http"
)

// subscriberAPI forwards the subscriber endpoint to the HSS
// provisioning API, /api/subscribers to /subscriptions. The Lab lists
// the subscriptions and changes none of them: an operator provisions
// them, which the OAM does.
func subscriberAPI(provisioning string, log *slog.Logger) (http.Handler, error) {
	return apiProxy("HSS provisioning API", provisioning, func(string) string {
		return "/subscriptions"
	}, log)
}
