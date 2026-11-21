package metrics

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// OtherValue is the value under which a label value outside the
// declared set is counted. Every label has it as its last value.
//
// Counting the event under "other" rather than dropping it keeps the
// sum over all label values equal to the total, which TS 32.409 asks of
// the measurements split by cause (for example 4.1.1.1.3 and 4.1.2.4).
// A series with "other" in the output is also the sign that a role
// declared too small a set.
const OtherValue = "other"

// maxSeries bounds the number of series of one metric, the product of
// the sizes of its label sets. It is not a Prometheus limit; it stops a
// label set that is mistakenly large, such as one value per subscriber,
// at registration.
const maxSeries = 10000

// Label declares one label of a metric: its name and the closed set of
// values it may take. The set comes from the specification or from the
// configuration (methods, response codes, roles, Diameter result
// codes, configured peers), never from what a subscriber sends.
type Label struct {
	Name   string
	Values []string
}

// labelSpace numbers every combination of label values, so that a
// series is found by arithmetic on read-only tables, without a lock.
type labelSpace struct {
	names []string
	// values[n] is the declared set of label n followed by OtherValue.
	values [][]string
	// positions[n] maps a value of label n to its place in values[n].
	positions []map[string]int
	size      int
}

func newLabelSpace(labels []Label) (labelSpace, error) {
	space := labelSpace{size: 1}
	seen := make(map[string]bool, len(labels))
	for _, l := range labels {
		if err := validateLabelName(l.Name); err != nil {
			return labelSpace{}, err
		}
		if seen[l.Name] {
			return labelSpace{}, fmt.Errorf("metrics: label %q declared twice", l.Name)
		}
		seen[l.Name] = true
		if len(l.Values) == 0 {
			return labelSpace{}, fmt.Errorf("metrics: label %q has no values", l.Name)
		}
		values := make([]string, 0, len(l.Values)+1)
		positions := make(map[string]int, len(l.Values))
		for _, v := range l.Values {
			if err := validateLabelValue(l.Name, v); err != nil {
				return labelSpace{}, err
			}
			if _, dup := positions[v]; dup {
				return labelSpace{}, fmt.Errorf("metrics: label %q declares value %q twice", l.Name, v)
			}
			positions[v] = len(values)
			values = append(values, v)
		}
		values = append(values, OtherValue)
		space.names = append(space.names, l.Name)
		space.values = append(space.values, values)
		space.positions = append(space.positions, positions)
		space.size *= len(values)
		if space.size > maxSeries {
			return labelSpace{}, fmt.Errorf("metrics: labels allow more than %d series", maxSeries)
		}
	}
	return space, nil
}

func validateLabelValue(name, value string) error {
	switch {
	case value == "":
		// An empty value means the same as no label (Prometheus data
		// model), so it cannot name a series of its own.
		return fmt.Errorf("metrics: label %q has an empty value", name)
	case value == OtherValue:
		return fmt.Errorf("metrics: label %q declares %q, which is added to every label", name, OtherValue)
	case !utf8.ValidString(value):
		return fmt.Errorf("metrics: label %q has a value that is not UTF-8", name)
	}
	return nil
}

// index returns the number of the series for the given values, one per
// declared label and in the declared order. A wrong number of values is
// a programming error in the role, so it panics like a wrong number of
// arguments would.
func (s *labelSpace) index(metric string, values []string) int {
	if len(values) != len(s.names) {
		panic(fmt.Sprintf("metrics: %s takes %d label values, got %d", metric, len(s.names), len(values)))
	}
	i := 0
	for n, v := range values {
		pos, ok := s.positions[n][v]
		if !ok {
			pos = len(s.values[n]) - 1
		}
		i = i*len(s.values[n]) + pos
	}
	return i
}

// pairs renders the label pairs of series i as they appear between the
// braces of a sample line, for example `role="pcscf",type="initial"`.
func (s *labelSpace) pairs(i int) string {
	parts := make([]string, len(s.names))
	for n := len(s.names) - 1; n >= 0; n-- {
		size := len(s.values[n])
		parts[n] = s.names[n] + `="` + escapeLabelValue(s.values[n][i%size]) + `"`
		i /= size
	}
	return strings.Join(parts, ",")
}

// StatusClass returns the class of a SIP response code ("1xx" to
// "6xx", RFC 3261 7.2), or OtherValue for a number outside 100 to 699.
// Use it with a label declared with StatusClasses.
func StatusClass(code int) string {
	if code < 100 || code > 699 {
		return OtherValue
	}
	return strconv.Itoa(code/100) + "xx"
}

// StatusClasses returns the values StatusClass can return, other than
// OtherValue.
func StatusClasses() []string {
	return []string{"1xx", "2xx", "3xx", "4xx", "5xx", "6xx"}
}
