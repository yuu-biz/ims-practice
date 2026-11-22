package rx

import (
	"fmt"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

// The enumerations of the service information on the wire. Package
// policy counts from "not supplied"; Rx counts as TS 29.214 numbers its
// values.
var (
	// Flow-Status, TS 29.214 5.3.11.
	flowStatusValues = map[policy.FlowStatus]uint32{
		policy.EnabledUplink:   0,
		policy.EnabledDownlink: 1,
		policy.Enabled:         2,
		policy.Disabled:        3,
		policy.Removed:         4,
	}
	// Flow-Usage, TS 29.214 5.3.12. NO_INFORMATION is the default, so it
	// is never written.
	flowUsageValues = map[policy.FlowUsage]uint32{
		policy.NoInformation: 0,
		policy.RTCP:          1,
		policy.AFSignalling:  2,
	}
	// Media-Type, TS 29.214 5.3.19.
	mediaTypeValues = map[policy.MediaType]uint32{
		policy.Audio:       0,
		policy.Video:       1,
		policy.Data:        2,
		policy.Application: 3,
		policy.Control:     4,
		policy.Text:        5,
		policy.Message:     6,
		policy.OtherMedia:  0xffffffff,
	}
)

// lookup finds the key of a wire value in one of the tables above.
func lookup[K comparable](table map[K]uint32, value uint32) (K, bool) {
	for k, v := range table {
		if v == value {
			return k, true
		}
	}
	var zero K
	return zero, false
}

// mediaComponentDescription writes the Media-Component-Description AVP
// in the order of TS 29.214 5.3.16.
func mediaComponentDescription(c policy.MediaComponent) *diam.AVP {
	children := []*diam.AVP{diameter.VendorAVP(avpMediaComponentNumber, mv, datatype.Unsigned32(c.Number))}
	for _, s := range c.SubComponents {
		children = append(children, mediaSubComponent(s))
	}
	if v, ok := mediaTypeValues[c.Type]; ok {
		children = append(children, diameter.VendorAVP(avpMediaType, mv, datatype.Enumerated(v)))
	}
	children = appendUnsigned(children, avpMaxRequestedBandwidthUL, c.MaxRequestedBandwidthUL)
	children = appendUnsigned(children, avpMaxRequestedBandwidthDL, c.MaxRequestedBandwidthDL)
	if v, ok := flowStatusValues[c.FlowStatus]; ok {
		children = append(children, diameter.VendorAVP(avpFlowStatus, mv, datatype.Enumerated(v)))
	}
	children = appendUnsigned(children, avpRSBandwidth, c.RSBandwidth)
	children = appendUnsigned(children, avpRRBandwidth, c.RRBandwidth)
	for _, codec := range c.Codecs {
		children = append(children, diameter.VendorAVP(avpCodecData, mv, datatype.OctetString(codec.String())))
	}
	return diameter.VendorAVP(avpMediaComponentDescription, mv, &diam.GroupedAVP{AVP: children})
}

// mediaSubComponent writes the Media-Sub-Component AVP in the order of
// TS 29.214 5.3.18.
func mediaSubComponent(s policy.MediaSubComponent) *diam.AVP {
	children := []*diam.AVP{diameter.VendorAVP(avpFlowNumber, mv, datatype.Unsigned32(s.FlowNumber))}
	for _, f := range s.Flows {
		children = append(children, diameter.VendorAVP(avpFlowDescription, mv, datatype.IPFilterRule(f.String())))
	}
	if s.Usage != policy.NoInformation {
		children = append(children, diameter.VendorAVP(avpFlowUsage, mv, datatype.Enumerated(flowUsageValues[s.Usage])))
	}
	return diameter.VendorAVP(avpMediaSubComponent, mv, &diam.GroupedAVP{AVP: children})
}

func appendUnsigned(avps []*diam.AVP, code uint32, value *uint32) []*diam.AVP {
	if value == nil {
		return avps
	}
	return append(avps, diameter.VendorAVP(code, mv, datatype.Unsigned32(*value)))
}

// parseMediaComponentDescription reads a Media-Component-Description.
// AVPs this project does not use are skipped, as TS 29.214 5.3.16
// allows with its *[ AVP ].
func parseMediaComponentDescription(a *diam.AVP) (policy.MediaComponent, error) {
	children, err := grouped(a)
	if err != nil {
		return policy.MediaComponent{}, err
	}
	var c policy.MediaComponent
	hasNumber := false
	for _, child := range children {
		if child.VendorID != diameter.VendorID3GPP {
			continue
		}
		switch child.Code {
		case avpMediaComponentNumber:
			c.Number, hasNumber = unsigned(child), true
		case avpMediaSubComponent:
			s, err := parseMediaSubComponent(child)
			if err != nil {
				return policy.MediaComponent{}, err
			}
			c.SubComponents = append(c.SubComponents, s)
		case avpMediaType:
			if c.Type, err = enumerated(child, mediaTypeValues, "Media-Type"); err != nil {
				return policy.MediaComponent{}, err
			}
		case avpMaxRequestedBandwidthUL:
			c.MaxRequestedBandwidthUL = pointer(unsigned(child))
		case avpMaxRequestedBandwidthDL:
			c.MaxRequestedBandwidthDL = pointer(unsigned(child))
		case avpFlowStatus:
			if c.FlowStatus, err = enumerated(child, flowStatusValues, "Flow-Status"); err != nil {
				return policy.MediaComponent{}, err
			}
		case avpRSBandwidth:
			c.RSBandwidth = pointer(unsigned(child))
		case avpRRBandwidth:
			c.RRBandwidth = pointer(unsigned(child))
		case avpCodecData:
			codec, err := policy.ParseCodecData(octets(child))
			if err != nil {
				return policy.MediaComponent{}, fmt.Errorf("%w: %v", ErrInvalidServiceInformation, err)
			}
			c.Codecs = append(c.Codecs, codec)
		}
	}
	if !hasNumber {
		return policy.MediaComponent{}, fmt.Errorf("%w: Media-Component-Description without Media-Component-Number", ErrInvalidServiceInformation)
	}
	return c, nil
}

func parseMediaSubComponent(a *diam.AVP) (policy.MediaSubComponent, error) {
	children, err := grouped(a)
	if err != nil {
		return policy.MediaSubComponent{}, err
	}
	var s policy.MediaSubComponent
	hasNumber := false
	for _, child := range children {
		if child.VendorID != diameter.VendorID3GPP {
			continue
		}
		switch child.Code {
		case avpFlowNumber:
			s.FlowNumber, hasNumber = unsigned(child), true
		case avpFlowDescription:
			f, err := policy.ParseFlowDescription(octets(child))
			if err != nil {
				return policy.MediaSubComponent{}, fmt.Errorf("%w: %v", ErrFilterRestrictions, err)
			}
			s.Flows = append(s.Flows, f)
		case avpFlowUsage:
			if s.Usage, err = enumerated(child, flowUsageValues, "Flow-Usage"); err != nil {
				return policy.MediaSubComponent{}, err
			}
		}
	}
	if !hasNumber {
		return policy.MediaSubComponent{}, fmt.Errorf("%w: Media-Sub-Component without Flow-Number", ErrInvalidServiceInformation)
	}
	return s, nil
}

// Flows is the Flows AVP (TS 29.214 5.3.10): the flows of one media
// component, all of them when FlowNumbers is empty.
type Flows struct {
	MediaComponentNumber uint32
	FlowNumbers          []uint32
}

func (f Flows) avp() *diam.AVP {
	children := []*diam.AVP{diameter.VendorAVP(avpMediaComponentNumber, mv, datatype.Unsigned32(f.MediaComponentNumber))}
	for _, n := range f.FlowNumbers {
		children = append(children, diameter.VendorAVP(avpFlowNumber, mv, datatype.Unsigned32(n)))
	}
	return diameter.VendorAVP(avpFlows, mv, &diam.GroupedAVP{AVP: children})
}

func parseFlows(a *diam.AVP) (Flows, error) {
	children, err := grouped(a)
	if err != nil {
		return Flows{}, err
	}
	var f Flows
	for _, child := range children {
		switch {
		case child.VendorID != diameter.VendorID3GPP:
		case child.Code == avpMediaComponentNumber:
			f.MediaComponentNumber = unsigned(child)
		case child.Code == avpFlowNumber:
			f.FlowNumbers = append(f.FlowNumbers, unsigned(child))
		}
	}
	return f, nil
}

func grouped(a *diam.AVP) ([]*diam.AVP, error) {
	g, ok := a.Data.(*diam.GroupedAVP)
	if !ok {
		return nil, fmt.Errorf("%w: AVP %d is not grouped", ErrInvalidServiceInformation, a.Code)
	}
	return g.AVP, nil
}

// unsigned reads an Unsigned32 or Enumerated value; anything else reads
// as 0, which the dictionary of the message already ruled out.
func unsigned(a *diam.AVP) uint32 {
	switch d := a.Data.(type) {
	case datatype.Unsigned32:
		return uint32(d)
	case datatype.Enumerated:
		return uint32(d)
	}
	return 0
}

func enumerated[K comparable](a *diam.AVP, table map[K]uint32, name string) (K, error) {
	k, ok := lookup(table, unsigned(a))
	if !ok {
		return k, fmt.Errorf("%w: %s %d is not a value of TS 29.214", ErrInvalidServiceInformation, name, unsigned(a))
	}
	return k, nil
}

func octets(a *diam.AVP) string {
	switch d := a.Data.(type) {
	case datatype.OctetString:
		return string(d)
	case datatype.IPFilterRule:
		return string(d)
	}
	return ""
}

func pointer(v uint32) *uint32 { return &v }
