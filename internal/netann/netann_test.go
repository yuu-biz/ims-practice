package netann

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

func TestParse(t *testing.T) {
	tests := []struct {
		uri  string
		want Request
	}{
		// The examples of RFC 4240 3 and 3.3.
		{"sip:annc@ms2.example.net;play=http://audio.example.net/allcircuitsbusy.g711",
			Request{Announcement: &Announcement{Prompt: "http://audio.example.net/allcircuitsbusy.g711"}}},
		{"sip:annc@ms2.example.net;play=file://fileserver.example.net//geminii/yourHoroscope.wav",
			Request{Announcement: &Announcement{Prompt: "file://fileserver.example.net//geminii/yourHoroscope.wav"}}},
		{"sip:annc@ms.example.net;play=file://fs.example.net//clips/my-intro.dvi;content-type=video/mpeg%3bencode%d3314M-25/625-50",
			Request{Announcement: &Announcement{Prompt: "file://fs.example.net//clips/my-intro.dvi"}}},
		// The example of RFC 4240 5 and its diagram in 5.1.
		{"sip:conf=uniqueIdentifier@mediaserver.example.net", Request{Conference: &Conference{ID: "uniqueidentifier"}}},
		{"sip:conf=123@ms.example.net;isfocus", Request{Conference: &Conference{ID: "123"}}},
		// What the MMTel AS of this project sends.
		{"sip:annc@mrfc.ims.example.net;play=/provisioned/cat1;repeat=forever;delay=500;duration=60000",
			Request{Announcement: &Announcement{Prompt: "/provisioned/cat1", Repeat: RepeatForever, Delay: 500 * time.Millisecond, Duration: time.Minute}}},
		// Service names, parameter names and the conference ID do not
		// depend on case, and escapes stand for their characters.
		{"sip:ANNC@mrfc.ims.example.net;PLAY=/provisioned/notice;Repeat=2;x-unknown=1",
			Request{Announcement: &Announcement{Prompt: "/provisioned/notice", Repeat: 2}}},
		{"sip:Conf=Room%41@mrfc.ims.example.net", Request{Conference: &Conference{ID: "rooma"}}},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got, err := Parse(uri(t, tt.uri))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse = %+v %+v, want %+v %+v", got.Announcement, got.Conference, tt.want.Announcement, tt.want.Conference)
			}
		})
	}
}

func TestParseRefuses(t *testing.T) {
	tests := []struct {
		uri    string
		status int
		reason string
	}{
		{"sip:annc@mrfc.example.net", 400, "Mandatory play parameter missing"}, // RFC 4240 3
		{"sip:annc@mrfc.example.net;play=", 400, "Mandatory play parameter missing"},
		{"sip:annc@mrfc.example.net;play=/provisioned/a;repeat=twice", 400, "Invalid repeat parameter"},
		{"sip:annc@mrfc.example.net;play=/provisioned/a;delay=-5", 400, "Invalid delay parameter"},
		{"sip:annc@mrfc.example.net;play=/provisioned/a;duration=1.5", 400, "Invalid duration parameter"},
		{"sip:conf@mrfc.example.net", 404, "Not Found"}, // RFC 4240 5
		{"sip:conf=@mrfc.example.net", 404, "Not Found"},
		{"sip:dialog@mrfc.example.net;voicexml=http://as.example.net/menu.vxml", 488, "Not Acceptable Here"}, // RFC 4240 2
		{"sip:annc=1@mrfc.example.net;play=/provisioned/a", 488, "Not Acceptable Here"},
		{"sip:alice@mrfc.example.net", 488, "Not Acceptable Here"},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			_, err := Parse(uri(t, tt.uri))
			var e *Error
			if !errors.As(err, &e) || e.Status != tt.status || e.Reason != tt.reason {
				t.Errorf("Parse error = %v, want %d %s", err, tt.status, tt.reason)
			}
		})
	}
}

func TestProvisioned(t *testing.T) {
	tests := []struct {
		prompt string
		id     string
		ok     bool
	}{
		{"/provisioned/notice", "notice", true},
		{"/provisioned/cat2", "cat2", true},
		{"/provisioned/", "", false},
		{"/provisioned/cat-2", "", false}, // announcement-id is ALPHA and DIGIT only (RFC 4240 3.3)
		{"http://audio.example.net/allcircuitsbusy.g711", "", false},
	}
	for _, tt := range tests {
		id, ok := Announcement{Prompt: tt.prompt}.Provisioned()
		if id != tt.id || ok != tt.ok {
			t.Errorf("Provisioned(%q) = %q, %v; want %q, %v", tt.prompt, id, ok, tt.id, tt.ok)
		}
	}
}

func TestService(t *testing.T) {
	for u, want := range map[string]string{
		"sip:annc@mrfc.example.net;play=/provisioned/a": "annc",
		"sip:CONF=7@mrfc.example.net":                   "conf",
		"sip:alice@example.net":                         "alice",
	} {
		if got := Service(uri(t, u)); got != want {
			t.Errorf("Service(%s) = %q, want %q", u, got, want)
		}
	}
}

func uri(t *testing.T, s string) sip.URI {
	t.Helper()
	u, err := sip.ParseURI(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
