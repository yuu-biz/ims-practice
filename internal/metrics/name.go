package metrics

import (
	"fmt"
	"strings"
)

// The names follow the Prometheus data model and naming practices
// (prometheus/docs: concepts/data_model.md, practices/naming.md), kept
// to the subset that every tool accepts without quoting:
//
//   - Only lower case letters, digits and "_", not starting with a
//     digit. The data model also allows upper case and ":"; upper case
//     would break the snake_case convention, and ":" is reserved for
//     recording rules.
//   - A counter ends in "_total"; nothing else does.
//   - No name ends in "_bucket", "_count" or "_sum", because a
//     histogram writes its samples under those suffixes.
//   - Label names starting with "__" are reserved for Prometheus, "le"
//     is the bucket label of a histogram and "quantile" the label of a
//     summary.

func isSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func validateMetricName(name string, k kind) error {
	if !isSnakeCase(name) {
		return fmt.Errorf("metrics: name %q is not lower case letters, digits and underscores", name)
	}
	for _, suffix := range []string{"_bucket", "_count", "_sum"} {
		if strings.HasSuffix(name, suffix) {
			return fmt.Errorf("metrics: name %q ends in %q, which a histogram uses for its samples", name, suffix)
		}
	}
	total := strings.HasSuffix(name, "_total")
	if k == counterKind && !total {
		return fmt.Errorf("metrics: counter %q does not end in _total", name)
	}
	if k != counterKind && total {
		return fmt.Errorf("metrics: %s %q ends in _total, which is for counters", k, name)
	}
	return nil
}

func validateLabelName(name string) error {
	switch {
	case !isSnakeCase(name):
		return fmt.Errorf("metrics: label name %q is not lower case letters, digits and underscores", name)
	case strings.HasPrefix(name, "__"):
		return fmt.Errorf("metrics: label name %q starts with __, which Prometheus reserves", name)
	case name == "le", name == "quantile":
		return fmt.Errorf("metrics: label name %q is used by Prometheus for histogram and summary samples", name)
	}
	return nil
}
