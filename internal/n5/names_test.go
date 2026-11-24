package n5

import (
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// attributeNames are the attributes of each type, as the schema of the
// same name in TS29514_Npcf_PolicyAuthorization.yaml names them. The
// names were taken from that file in the archive of TS 29.514 V20.1.0
// on the 3GPP file server, so a field whose tag is spelt otherwise
// fails TestAttributeNames. The attributes of ProblemDetails, from TS
// 29.571, are checked in package sbi.
var attributeNames = []struct {
	v      any
	file   string
	schema string
	names  []string
}{
	{AppSessionContext{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AppSessionContext", []string{"ascReqData", "ascRespData", "evsNotif"}},
	{AppSessionContextReqData{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AppSessionContextReqData", []string{"afAppId", "afChargId", "evSubsc", "medComponents", "servInfStatus", "notifUri", "suppFeat", "ueIpv4", "ueIpv6"}},
	{AppSessionContextRespData{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AppSessionContextRespData", []string{"suppFeat"}},
	{AppSessionContextUpdateDataPatch{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AppSessionContextUpdateDataPatch", []string{"ascReqData"}},
	{AppSessionContextUpdateData{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AppSessionContextUpdateData", []string{"afAppId", "evSubsc", "medComponents", "servInfStatus"}},
	{MediaComponent{}, "TS29514_Npcf_PolicyAuthorization.yaml", "MediaComponent", []string{"afAppId", "codecs", "fStatus", "marBwDl", "marBwUl", "medCompN", "medSubComps", "medType", "rrBw", "rsBw"}},
	{MediaSubComponent{}, "TS29514_Npcf_PolicyAuthorization.yaml", "MediaSubComponent", []string{"fNum", "fDescs", "flowUsage"}},
	{EventsSubscReqData{}, "TS29514_Npcf_PolicyAuthorization.yaml", "EventsSubscReqData", []string{"events", "notifUri"}},
	{AfEventSubscription{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AfEventSubscription", []string{"event"}},
	{EventsNotification{}, "TS29514_Npcf_PolicyAuthorization.yaml", "EventsNotification", []string{"evSubsUri", "evNotifs", "failedResourcAllocReports", "succResourcAllocReports"}},
	{AfEventNotification{}, "TS29514_Npcf_PolicyAuthorization.yaml", "AfEventNotification", []string{"event", "flows"}},
	{Flows{}, "TS29514_Npcf_PolicyAuthorization.yaml", "Flows", []string{"fNums", "medCompN"}},
	{ResourcesAllocationInfo{}, "TS29514_Npcf_PolicyAuthorization.yaml", "ResourcesAllocationInfo", []string{"mcResourcStatus", "flows"}},
	{TerminationInfo{}, "TS29514_Npcf_PolicyAuthorization.yaml", "TerminationInfo", []string{"termCause", "resUri"}},
}

func TestAttributeNames(t *testing.T) {
	for _, a := range attributeNames {
		if got := openapitest.JSONNames(a.v); !slices.Equal(got, a.names) {
			t.Errorf("%T has the attributes %q, the schema %s of %s has %q", a.v, got, a.schema, a.file, a.names)
		}
	}
}
