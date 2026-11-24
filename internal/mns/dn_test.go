package mns

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestDN(t *testing.T) {
	me := SubNetworkDN("ims").Child(ClassManagedElement, "scscf")
	if got, want := me.String(), "SubNetwork=ims,ManagedElement=scscf"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
	if got, want := me.URILDN(), "/SubNetwork=ims/ManagedElement=scscf"; got != want {
		t.Errorf("URILDN = %q, want %q", got, want)
	}
	parsed, err := ParseDN(me.String())
	if err != nil || !slices.Equal(parsed, me) {
		t.Errorf("ParseDN(%q) = %v, %v", me.String(), parsed, err)
	}
	parsed, err = ParseURILDN(me.URILDN())
	if err != nil || !slices.Equal(parsed, me) {
		t.Errorf("ParseURILDN(%q) = %v, %v", me.URILDN(), parsed, err)
	}
}

// TestChildLeavesTheParent: two children of one DN do not share their
// last RDN.
func TestChildLeavesTheParent(t *testing.T) {
	sn := SubNetworkDN("ims")
	a := sn.Child(ClassManagedElement, "a")
	b := sn.Child(ClassManagedElement, "b")
	if a.String() != "SubNetwork=ims,ManagedElement=a" || b.String() != "SubNetwork=ims,ManagedElement=b" || sn.String() != "SubNetwork=ims" {
		t.Errorf("a %s, b %s, parent %s", a, b, sn)
	}
}

// TestURILDNEscapes: the brackets of an IPv6 peer are no characters of
// a path segment (RFC 3986 3.3), so they are percent-encoded, and read
// back.
func TestURILDNEscapes(t *testing.T) {
	d := SubNetworkDN("ims").Child(ClassManagedElement, "pcscf").Child("Peer", "[2001:db8::7]:6379")
	want := "/SubNetwork=ims/ManagedElement=pcscf/Peer=%5B2001:db8::7%5D:6379"
	if got := d.URILDN(); got != want {
		t.Errorf("URILDN = %q, want %q", got, want)
	}
	parsed, err := ParseURILDN(want)
	if err != nil || !slices.Equal(parsed, d) {
		t.Errorf("ParseURILDN = %v, %v", parsed, err)
	}
}

func TestParseDNRefuses(t *testing.T) {
	for _, s := range []string{
		"",
		"SubNetwork",
		"SubNetwork=",
		"=ims",
		"1Net=ims",
		"Sub-Network=ims",
		"SubNetwork=ims,",
		"SubNetwork=ims,,ManagedElement=a",
		"SubNetwork=a=b",
		"DC=ims.example,SubNetwork=ims/x",
	} {
		if d, err := ParseDN(s); !errors.Is(err, ErrInvalidDN) {
			t.Errorf("ParseDN(%q) = %v, %v; want ErrInvalidDN", s, d, err)
		}
	}
	for _, s := range []string{"", "/", "SubNetwork=ims", "/SubNetwork=ims/", "/SubNetwork=%zz", "/SubNetwork=a%2Fb"} {
		if d, err := ParseURILDN(s); !errors.Is(err, ErrInvalidDN) {
			t.Errorf("ParseURILDN(%q) = %v, %v; want ErrInvalidDN", s, d, err)
		}
	}
}

// TestCheck: a DN built from names that break the rules of RDN is
// found before it is written, since ParseDN would not read it back.
func TestCheck(t *testing.T) {
	if err := SubNetworkDN("ims").Child(ClassManagedElement, "scscf").Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
	for _, d := range []DN{
		nil,
		SubNetworkDN(""),
		SubNetworkDN("ims").Child(ClassManagedElement, ""),
		SubNetworkDN("a,ManagedElement=b"),
		SubNetworkDN("a=b"),
		SubNetworkDN("a/b"),
		SubNetworkDN("ims").Child("Managed-Element", "a"),
		SubNetworkDN("ims").Child("", "a"),
	} {
		if err := d.Check(); !errors.Is(err, ErrInvalidDN) {
			t.Errorf("Check(%q) = %v, want ErrInvalidDN", d.String(), err)
		}
	}
}

func TestMnSRoot(t *testing.T) {
	root, err := ParseMnSRoot("http://oam:8080" + DefaultRoot + "/")
	if err != nil {
		t.Fatal(err)
	}
	alarmList := SubNetworkDN("ims").Child(ClassAlarmList, "1")
	if got, want := root.String(), "http://oam:8080/3GPPManagement"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
	if got, want := root.ResourceURI(alarmList), "http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1"; got != want {
		t.Errorf("ResourceURI = %q, want %q", got, want)
	}
	path := root.ResourcePath(alarmList)
	if path != "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1" {
		t.Errorf("ResourcePath = %q", path)
	}
	if d, err := root.ParseResourcePath(path); err != nil || !slices.Equal(d, alarmList) {
		t.Errorf("ParseResourcePath(%q) = %v, %v", path, d, err)
	}
	peer := SubNetworkDN("ims").Child(ClassManagedElement, "scscf").Child("ManagedFunction", "scscf").Child("Peer", "hss.ims.mnc001.mcc001.3gppnetwork.org")
	if got, want := root.CanonicalURI(peer), "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org"; got != want {
		t.Errorf("CanonicalURI = %q, want %q", got, want)
	}
	for _, p := range []string{
		"/3GPPManagement/ProvMnS/v18/SubNetwork=ims/AlarmList=1",
		"/3GPPManagement/FaultSupervisionMnS/v19/SubNetwork=ims/AlarmList=1",
		"/ProvMnS/v19/SubNetwork=ims/AlarmList=1",
		"/3GPPManagement/ProvMnS/v19",
		"/3GPPManagement/ProvMnS/v19/",
		"/3GPPManagement/ProvMnS/v19SubNetwork=ims",
	} {
		if d, err := root.ParseResourcePath(p); !errors.Is(err, ErrInvalidDN) {
			t.Errorf("ParseResourcePath(%q) = %v, %v; want ErrInvalidDN", p, d, err)
		}
	}
}

// TestMnSRootWithoutRoot: a {root} of no segments is allowed (TS 32.158
// 4.4.2), and the canonical URI is the same as with one.
func TestMnSRootWithoutRoot(t *testing.T) {
	root, err := ParseMnSRoot("https://oam.ims.example")
	if err != nil {
		t.Fatal(err)
	}
	sn := SubNetworkDN("ims")
	if got, want := root.ResourceURI(sn), "https://oam.ims.example/ProvMnS/v19/SubNetwork=ims"; got != want {
		t.Errorf("ResourceURI = %q, want %q", got, want)
	}
	if got, want := root.CanonicalURI(sn), "https://oam.ims.example/SubNetwork=ims"; got != want {
		t.Errorf("CanonicalURI = %q, want %q", got, want)
	}
}

// TestMnSRootKeepsItsEncoding: a {root} with an encoded character stays
// encoded in the URIs built on it, so that %23 does not become a "#"
// that would move the rest of the path into the fragment, and a request
// path is compared with it as sent.
func TestMnSRootKeepsItsEncoding(t *testing.T) {
	sn := SubNetworkDN("ims")
	for _, c := range []struct{ root, uri, path string }{
		{"http://oam/api%23x", "http://oam/api%23x/ProvMnS/v19/SubNetwork=ims", "/api%23x/ProvMnS/v19/SubNetwork=ims"},
		{"http://oam/3GPP%20Management/", "http://oam/3GPP%20Management/ProvMnS/v19/SubNetwork=ims", "/3GPP%20Management/ProvMnS/v19/SubNetwork=ims"},
	} {
		root, err := ParseMnSRoot(c.root)
		if err != nil {
			t.Fatal(err)
		}
		if got := root.ResourceURI(sn); got != c.uri {
			t.Errorf("ResourceURI of %s = %q, want %q", c.root, got, c.uri)
		}
		if d, err := root.ParseResourcePath(c.path); err != nil || !slices.Equal(d, sn) {
			t.Errorf("ParseResourcePath(%q) of %s = %v, %v", c.path, c.root, d, err)
		}
		if got := root.String(); got != strings.TrimSuffix(c.root, "/") {
			t.Errorf("String = %q, want %q", got, c.root)
		}
	}
	root, err := ParseMnSRoot("http://oam/api%23x")
	if err != nil {
		t.Fatal(err)
	}
	if d, err := root.ParseResourcePath("/api#x/ProvMnS/v19/SubNetwork=ims"); !errors.Is(err, ErrInvalidDN) {
		t.Errorf("ParseResourcePath of the decoded path = %v, %v; want ErrInvalidDN", d, err)
	}
}

func TestParseMnSRootRefuses(t *testing.T) {
	for _, s := range []string{
		"", "oam:8080/3GPPManagement", "/3GPPManagement", "ftp://oam/3GPPManagement",
		"http://oam/3GPPManagement?x=1", "http://oam/3GPPManagement?", "http://oam/3GPPManagement#f", "http://u:p@oam/",
		"http://h//", "http://h/a//b", "http://h/a//",
	} {
		if _, err := ParseMnSRoot(s); err == nil {
			t.Errorf("ParseMnSRoot(%q) succeeded", s)
		}
	}
}
