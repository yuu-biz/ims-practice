package sdp

import (
	"slices"

	"github.com/yuu-biz/ims-practice/internal/precondition"
)

// Preconditions reads the a=curr, a=des and a=conf attributes of m (RFC
// 3312 4).
func (m Media) Preconditions() (precondition.Attributes, error) {
	var p precondition.Attributes
	for _, a := range m.Attributes {
		switch a.Name {
		case "curr":
			c, err := precondition.ParseCurrent(a.Value)
			if err != nil {
				return precondition.Attributes{}, err
			}
			p.Current = append(p.Current, c)
		case "des":
			d, err := precondition.ParseDesired(a.Value)
			if err != nil {
				return precondition.Attributes{}, err
			}
			p.Desired = append(p.Desired, d)
		case "conf":
			c, err := precondition.ParseConfirm(a.Value)
			if err != nil {
				return precondition.Attributes{}, err
			}
			p.Confirm = append(p.Confirm, c)
		}
	}
	return p, nil
}

// SetPreconditions replaces the precondition attributes of m with p,
// after the other attributes. A media description that is rejected
// with port 0 carries none: both sides ignore its preconditions (RFC
// 3312 8.1).
func (m *Media) SetPreconditions(p precondition.Attributes) {
	m.Attributes = slices.DeleteFunc(m.Attributes, func(a Attribute) bool {
		return a.Name == "curr" || a.Name == "des" || a.Name == "conf"
	})
	if m.Port == 0 {
		return
	}
	for _, c := range p.Current {
		m.Attributes = append(m.Attributes, Attribute{Name: "curr", Value: c.String()})
	}
	for _, d := range p.Desired {
		m.Attributes = append(m.Attributes, Attribute{Name: "des", Value: d.String()})
	}
	for _, c := range p.Confirm {
		m.Attributes = append(m.Attributes, Attribute{Name: "conf", Value: c.String()})
	}
}
