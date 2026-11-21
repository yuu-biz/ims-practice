package ims

import (
	"strings"
	"testing"
)

func TestServiceInfoBody(t *testing.T) {
	body := ServiceInfoBody("<msisdn>12025550101</msisdn>")
	if !strings.Contains(string(body), `<ims-3gpp version="1"><service-info>&lt;msisdn&gt;12025550101&lt;/msisdn&gt;</service-info></ims-3gpp>`) {
		t.Errorf("body = %s", body)
	}
	got, err := ParseServiceInfo(body)
	if err != nil || got != "<msisdn>12025550101</msisdn>" {
		t.Errorf("ParseServiceInfo = %q, %v", got, err)
	}
	if _, err := ParseServiceInfo([]byte(`<ims-3gpp version="1"><alternative-service/></ims-3gpp>`)); err == nil {
		t.Error("a body without service-info was read")
	}
}
