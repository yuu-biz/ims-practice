package sip

import (
	"errors"
	"testing"
)

func TestRequestedExpires(t *testing.T) {
	tests := []struct {
		name     string
		contact  string
		expires  string
		want     int
		wildcard bool
		wantErr  bool
	}{
		{"Contact parameter wins", "<sip:ue@192.0.2.1>;expires=600000", "3600", 600000, false, false},
		{"Expires header", "<sip:ue@192.0.2.1>", "0", 0, false, false},
		{"neither", "<sip:ue@192.0.2.1>", "", NoExpires, false, false},
		{"wildcard with zero", "*", "0", 0, true, false},
		{"wildcard with non-zero", "*", "60", 0, false, true},
		{"wildcard without Expires", "*", "", 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Message{Method: REGISTER, RequestURI: "sip:" + imsDomain}
			m.Add("Contact", tt.contact)
			if tt.expires != "" {
				m.Add("Expires", tt.expires)
			}
			got, wildcard, err := RequestedExpires(m)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !tt.wantErr && (got != tt.want || wildcard != tt.wildcard) {
				t.Errorf("got %d, %v; want %d, %v", got, wildcard, tt.want, tt.wildcard)
			}
		})
	}
}

func TestDecrementMaxForwards(t *testing.T) {
	m := &Message{Method: REGISTER, RequestURI: "sip:" + imsDomain}
	m.Add("Max-Forwards", "1")
	if err := DecrementMaxForwards(m); err != nil || m.Get("Max-Forwards") != "0" {
		t.Fatalf("1 -> %q, %v", m.Get("Max-Forwards"), err)
	}
	if err := DecrementMaxForwards(m); !errors.Is(err, ErrTooManyHops) {
		t.Errorf("0: err = %v, want ErrTooManyHops", err)
	}
}
