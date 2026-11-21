package metrics

import (
	"math"
	"strconv"
	"strings"
)

// ContentType is the media type of the text exposition format, version
// 0.0.4 (prometheus/docs: instrumenting/exposition_formats.md). Since
// Prometheus 3.0 a scrape fails without a valid Content-Type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// In a HELP line only the backslash and the line feed are escaped.
var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

// In a label value the double quote is escaped as well.
var labelValueEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)

func escapeHelp(s string) string { return helpEscaper.Replace(s) }

func escapeLabelValue(s string) string { return labelValueEscaper.Replace(s) }

// formatFloat writes a sample value or a bucket bound the way the
// format reads it with Go's strconv.ParseFloat: the shortest decimal
// that reads back as the same number, and "+Inf", "-Inf" and "NaN" for
// the special values.
func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, +1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
