package sbi

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// attributeNames are the attributes of each type, as the schema of the
// same name in TS29571_CommonData.yaml names them. The names were taken
// from that file in the archive of TS 29.571 V20.1.0 on the 3GPP file
// server, so a field whose tag is spelt otherwise fails
// TestAttributeNames. The packages of the APIs use these types for
// their error answers, so this is where their names are checked.
var attributeNames = []struct {
	v      any
	file   string
	schema string
	names  []string
}{
	{ProblemDetails{}, "TS29571_CommonData.yaml", "ProblemDetails", []string{"type", "title", "status", "detail", "instance", "cause", "invalidParams", "supportedFeatures"}},
	{InvalidParam{}, "TS29571_CommonData.yaml", "InvalidParam", []string{"param", "reason"}},
}

func TestAttributeNames(t *testing.T) {
	for _, a := range attributeNames {
		if got := openapitest.JSONNames(a.v); !slices.Equal(got, a.names) {
			t.Errorf("%T has the attributes %q, the schema %s of %s has %q", a.v, got, a.schema, a.file, a.names)
		}
	}
}

// problemDetails is a ProblemDetails written from the schemas
// ProblemDetails and InvalidParam of TS29571_CommonData.yaml (TS 29.571
// 5.2.4.1), with every attribute that the type declares.
const problemDetails = `{
  "type": "about:blank",
  "title": "Bad Request",
  "status": 400,
  "detail": "the request misses an attribute",
  "instance": "/npcf-policyauthorization/v1/app-sessions",
  "cause": "MANDATORY_IE_MISSING",
  "invalidParams": [{"param": "/ascReqData/notifUri", "reason": "missing"}],
  "supportedFeatures": "10"
}`

// TestProblemDetailsRoundTrip reads the body into the Go type, refusing
// an attribute that the type does not have, and writes it back
// unchanged.
func TestProblemDetailsRoundTrip(t *testing.T) {
	var p ProblemDetails
	dec := json.NewDecoder(strings.NewReader(problemDetails))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(problemDetails), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip changed the body\nwant %s\n got %s", problemDetails, again)
	}
}
