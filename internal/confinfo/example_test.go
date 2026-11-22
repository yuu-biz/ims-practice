package confinfo_test

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
)

// A participant reads the document of a NOTIFY.
func ExampleParse() {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<conference-info xmlns="urn:ietf:params:xml:ns:conference-info"
    entity="sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org" version="0">
  <conference-description><maximum-user-count>3</maximum-user-count></conference-description>
  <users>
    <user entity="sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org">
      <endpoint entity="sip:+12025550101@192.0.2.11:5060"><status>connected</status></endpoint>
    </user>
  </users>
</conference-info>`)
	doc, err := confinfo.Parse(body)
	if err != nil {
		panic(err)
	}
	for _, u := range doc.Users.Users {
		for _, e := range u.Endpoints {
			fmt.Println(u.Entity, e.Status)
		}
	}
	// Output:
	// sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org connected
}

// The body of the NOTIFY the conference AS sends after Bob has been
// put on hold in a three-party call. The version comes from the
// subscription the NOTIFY belongs to.
func ExampleConference_FullState() {
	conf := confinfo.Conference{
		URI:              "sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org",
		MaximumUserCount: 3,
		Participants: []confinfo.Participant{
			{User: "sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org", DisplayText: "Alice",
				Endpoint: "urn:uuid:2f6c3e8a-41b7-4d0e-9a55-0c1d7e3b9f21", Status: confinfo.Connected, JoiningMethod: confinfo.DialedIn},
			{User: "sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org", DisplayText: "Bob",
				Endpoint: "urn:uuid:8b04d9c2-6e13-4f7a-b8c6-5a2e91d0f4b7", Status: confinfo.OnHold, JoiningMethod: confinfo.DialedOut},
		},
	}
	previous := uint32(3) // the version of the last NOTIFY in this subscription
	version, err := confinfo.NextVersion(previous)
	if err != nil {
		panic(err)
	}
	body, err := conf.FullState(version).Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Println("Content-Type:", confinfo.ContentType)
	fmt.Print(string(body))
	// Output:
	// Content-Type: application/conference-info+xml
	// <?xml version="1.0" encoding="UTF-8"?>
	// <conference-info xmlns="urn:ietf:params:xml:ns:conference-info" entity="sip:conf-7f3a@mmtel.ims.mnc001.mcc001.3gppnetwork.org" state="full" version="4">
	//   <conference-description>
	//     <maximum-user-count>3</maximum-user-count>
	//   </conference-description>
	//   <users>
	//     <user entity="sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org">
	//       <display-text>Alice</display-text>
	//       <endpoint entity="urn:uuid:2f6c3e8a-41b7-4d0e-9a55-0c1d7e3b9f21">
	//         <status>connected</status>
	//         <joining-method>dialed-in</joining-method>
	//       </endpoint>
	//     </user>
	//     <user entity="sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org">
	//       <display-text>Bob</display-text>
	//       <endpoint entity="urn:uuid:8b04d9c2-6e13-4f7a-b8c6-5a2e91d0f4b7">
	//         <status>on-hold</status>
	//         <joining-method>dialed-out</joining-method>
	//       </endpoint>
	//     </user>
	//   </users>
	// </conference-info>
}
