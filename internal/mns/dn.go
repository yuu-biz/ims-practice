package mns

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// RDN is a relative distinguished name: the naming attribute of a
// managed object, whose name is the class name of the object, and its
// value, the id (TS 32.158 4.2.1.0), such as ManagedElement=scscf.
// The class name is letters and digits and starts with a letter; the
// id may hold no comma, equal sign or slash, which separate the parts
// of a DN and of its URI. ParseDN and ParseURILDN refuse an RDN that
// breaks these rules. SubNetworkDN and Child take the names as given
// and check nothing, so a DN built from names that come from outside,
// such as the SubNetwork name of a configuration, may break them. The
// caller checks such a DN with Check once, when it reads the names,
// such as when it starts.
type RDN struct {
	ClassName string
	Id        string
}

// DN is a distinguished name: the RDNs from the root of the object
// tree down to the object (TS 32.158 4.2.1.0), such as
// SubNetwork=ims,ManagedElement=scscf.
//
// The DNs here start at the SubNetwork, the local root, with no DN
// prefix (DC=...): they are local DNs (TS 32.158 4.2.1.1). The host of
// a URI built from them is the producer's own address, as TS 32.158
// 4.2.3 has it for a producer that is given no DN prefix.
type DN []RDN

// SubNetworkDN is the DN of the SubNetwork named name, the root of the
// object tree.
func SubNetworkDN(name string) DN {
	return DN{{ClassName: ClassSubNetwork, Id: name}}
}

// Child is the DN of the object of class className and id that d
// contains. d itself is left as it is.
func (d DN) Child(className, id string) DN {
	child := make(DN, len(d), len(d)+1)
	copy(child, d)
	return append(child, RDN{ClassName: className, Id: id})
}

// Check reports whether the DN keeps the rules of RDN, so that
// ParseDN and ParseURILDN read back what String and URILDN write.
func (d DN) Check() error {
	if len(d) == 0 {
		return fmt.Errorf("%w: empty", ErrInvalidDN)
	}
	for _, r := range d {
		if _, err := parseRDN(r.ClassName + "=" + r.Id); err != nil {
			return fmt.Errorf("%w %q: %v", ErrInvalidDN, d.String(), err)
		}
	}
	return nil
}

// String writes the DN with a comma between the RDNs, the form of the
// objectInstance and systemDN attributes.
func (d DN) String() string {
	parts := make([]string, len(d))
	for i, r := range d {
		parts[i] = r.ClassName + "=" + r.Id
	}
	return strings.Join(parts, ",")
}

// ErrInvalidDN is the answer to a DN or a URI that cannot be read as
// one.
var ErrInvalidDN = errors.New("mns: invalid DN")

// ParseDN reads a DN written with commas between the RDNs, such as
// SubNetwork=ims,ManagedElement=scscf.
func ParseDN(s string) (DN, error) {
	if s == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidDN)
	}
	var d DN
	for part := range strings.SplitSeq(s, ",") {
		r, err := parseRDN(part)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %v", ErrInvalidDN, s, err)
		}
		d = append(d, r)
	}
	return d, nil
}

// parseRDN reads className=id, by the rules of RDN.
func parseRDN(s string) (RDN, error) {
	className, id, ok := strings.Cut(s, "=")
	switch {
	case !ok:
		return RDN{}, fmt.Errorf("%q has no =", s)
	case !validClassName(className):
		return RDN{}, fmt.Errorf("%q is not a class name", className)
	case id == "" || strings.ContainsAny(id, ",=/"):
		return RDN{}, fmt.Errorf("%q is not an id", id)
	}
	return RDN{ClassName: className, Id: id}, nil
}

func validClassName(s string) bool {
	for i, c := range s {
		letter := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return s != ""
}

// URILDN maps the DN to the path of a URI: a slash before each RDN
// (TS 32.158 4.2.3), such as /SubNetwork=ims/ManagedElement=scscf. An
// id with a character that a path segment may not hold, such as the
// brackets of an IPv6 address, is percent-encoded.
func (d DN) URILDN() string {
	var b strings.Builder
	for _, r := range d {
		b.WriteString("/" + r.ClassName + "=" + url.PathEscape(r.Id))
	}
	return b.String()
}

// ParseURILDN reads a DN from the path of a URI that URILDN wrote.
func ParseURILDN(path string) (DN, error) {
	rest, ok := strings.CutPrefix(path, "/")
	if !ok || rest == "" {
		return nil, fmt.Errorf("%w: %q is no URI-LDN", ErrInvalidDN, path)
	}
	var d DN
	for segment := range strings.SplitSeq(rest, "/") {
		s, err := url.PathUnescape(segment)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %v", ErrInvalidDN, path, err)
		}
		r, err := parseRDN(s)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %v", ErrInvalidDN, path, err)
		}
		d = append(d, r)
	}
	return d, nil
}

// The {MnSName} and {MnSVersion} of the provisioning operations (TS
// 32.158 4.4.2). The version is "v" and the first number of the
// version of TS28532_ProvMnS.yaml, 19.2.0, the file that defines the
// operations (TS 32.158 4.6.2).
const (
	ProvMnS        = "ProvMnS"
	ProvMnSVersion = "v19"
)

// DefaultRoot is the {root} of TS 32.158 4.4.2 that the default value
// of MnSRoot in the OpenAPI files ends with. It keeps the paths of the
// 3GPP services apart from the other APIs of the same server.
const DefaultRoot = "/3GPPManagement"

// MnSRoot is {MnSRoot} of TS 32.158 4.4.2: the scheme, the host and
// port of the producer, and the {root} path, such as
// http://oam:8080/3GPPManagement.
type MnSRoot struct {
	// origin is the scheme, "://" and the host and port.
	origin string
	// path is {root} as a URI writes it, percent-encoded: empty, or
	// segments that each start with a slash. A character that would end
	// the path, such as "#", stays encoded (%23), so that the URIs
	// built on it keep the whole of {root} in their path.
	path string
}

// ParseMnSRoot reads an MnSRoot: an absolute http or https URL with no
// user, query or fragment. A slash at the end is dropped; an empty
// segment, as in http://oam//, is refused, since the paths built on it
// would hold "//".
func ParseMnSRoot(s string) (MnSRoot, error) {
	u, err := url.Parse(s)
	switch {
	case err != nil:
		return MnSRoot{}, fmt.Errorf("mns: MnSRoot %q: %v", s, err)
	case u.Scheme != "http" && u.Scheme != "https", u.Host == "":
		return MnSRoot{}, fmt.Errorf("mns: MnSRoot %q is not an http or https URL", s)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil:
		return MnSRoot{}, fmt.Errorf("mns: MnSRoot %q has more than a scheme, a host and a path", s)
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	if strings.Contains(path+"/", "//") {
		return MnSRoot{}, fmt.Errorf("mns: MnSRoot %q has an empty path segment", s)
	}
	return MnSRoot{origin: u.Scheme + "://" + u.Host, path: path}, nil
}

func (r MnSRoot) String() string { return r.origin + r.path }

// provMnSPath is {root}/ProvMnS/v19, percent-encoded.
func (r MnSRoot) provMnSPath() string {
	return r.path + "/" + ProvMnS + "/" + ProvMnSVersion
}

// ResourcePath is the path of the URI of the ProvMnS resource of the
// object d, percent-encoded: {root}/ProvMnS/v19/{URI-LDN}, such as
// /3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1.
func (r MnSRoot) ResourcePath(d DN) string {
	return r.provMnSPath() + d.URILDN()
}

// ResourceURI is the URI that a consumer sends a ProvMnS request to
// for the object d: {MnSRoot}/ProvMnS/v19/{URI-LDN} (TS 32.158 4.4.2).
func (r MnSRoot) ResourceURI(d DN) string {
	return r.origin + r.ResourcePath(d)
}

// ParseResourcePath reads the DN of the object from the path of a
// request, which ResourcePath wrote. The path is the one sent, still
// percent-encoded (url.URL.EscapedPath), and is compared with the
// encoded {root}. A path outside the ProvMnS resources of this root, or
// of another version, is no resource.
func (r MnSRoot) ParseResourcePath(path string) (DN, error) {
	ldn, ok := strings.CutPrefix(path, r.provMnSPath())
	if !ok {
		return nil, fmt.Errorf("%w: %q is not below %s", ErrInvalidDN, path, r.provMnSPath())
	}
	return ParseURILDN(ldn)
}

// CanonicalURI is the canonical URI of the object d (TS 32.158 4.2.4):
// the producer's scheme and host, then the URI-LDN, with neither the
// {root} nor the MnS name and version, such as
// http://oam:8080/SubNetwork=ims/ManagedElement=scscf. It is the href
// of a notification about d.
func (r MnSRoot) CanonicalURI(d DN) string {
	return r.origin + d.URILDN()
}
