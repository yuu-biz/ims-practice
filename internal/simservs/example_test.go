package simservs_test

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/yuu-biz/ims-practice/internal/simservs"
)

// serviceData stands for the ServiceData of the Repository Data that
// the AS reads from the HSS with UDR (Service-Indication MMTEL-Services).
const serviceData = `<MMTelServices xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <complete-communication-diversion>
    <communication-diversion>
      <cp:ruleset>
        <cp:rule id="cfu"><cp:conditions><rule-deactivated/></cp:conditions><cp:actions><forward-to><target/></forward-to></cp:actions></cp:rule>
        <cp:rule id="cfb"><cp:conditions><busy/></cp:conditions><cp:actions><forward-to><target>tel:+12025550150</target></forward-to></cp:actions></cp:rule>
      </cp:ruleset>
    </communication-diversion>
    <operator-communication-diversion authorized="true">
      <communication-retention-on-invocation>clear-communication-on-invocation-of-diversion</communication-retention-on-invocation>
      <retention-when-diverting-rejected-at-diverted-to-user>no-action-at-diverting-user</retention-when-diverting-rejected-at-diverted-to-user>
      <total-number-of-diversions-for-each-communication>5</total-number-of-diversions-for-each-communication>
      <cdiv-indication-timer>0</cdiv-indication-timer>
      <communication-forwarding-on-no-reply-timer>20</communication-forwarding-on-no-reply-timer>
    </operator-communication-diversion>
  </complete-communication-diversion>
  <complete-communication-barring>
    <incoming-communication-barring>
      <cp:ruleset>
        <cp:rule id="acr"><cp:conditions><anonymous/></cp:conditions><cp:actions><allow>false</allow></cp:actions></cp:rule>
      </cp:ruleset>
    </incoming-communication-barring>
    <outgoing-communication-barring/>
    <operator-incoming-communication-barring authorized="true"/>
    <operator-outgoing-communication-barring authorized="true"/>
  </complete-communication-barring>
</MMTelServices>`

// The MMTel AS on a terminating INVITE: barring first, then diversion
// (TS 24.611 4.6.7).
func ExampleEvaluateDiversion() {
	m, err := simservs.ParseMMTelServices([]byte(serviceData))
	if err != nil {
		panic(err)
	}
	call := simservs.CallInfo{CallingIdentity: "tel:+12025550170", Media: []string{"audio"}}

	cb := m.CompleteCommunicationBarring
	if cb != nil && cb.OperatorIncomingCommunicationBarring.Authorized {
		if r := simservs.EvaluateIncomingBarring(&cb.IncomingCommunicationBarring, call); !r.Allow {
			fmt.Println("reject", r.MatchedRules)
		}
	}
	cdiv := m.CompleteCommunicationDiversion
	if cdiv == nil || !cdiv.OperatorCommunicationDiversion.Authorized {
		return
	}
	if r := simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.AtCommunicationSetup, call); r.Diverts() {
		fmt.Println("divert now to", r.ForwardTo.Target)
	}
	// Later the UE answers 486 (Busy Here).
	if r := simservs.EvaluateDiversion(&cdiv.CommunicationDiversion, simservs.OnBusy, call); r.Diverts() {
		fmt.Println("busy: divert to", r.ForwardTo.Target, "notify caller:", r.ForwardTo.NotifyCallerOrDefault())
	}
	fmt.Println("no reply timer:", cdiv.NoReplyTimer(), "s")
	// Output:
	// busy: divert to tel:+12025550150 notify caller: true
	// no reply timer: 20 s
}

// The XCAP server on
// PUT /simservs.ngn.etsi.org/users/sip:+12025550170@ims.mnc001.mcc001.3gppnetwork.org/simservs.xml/~~/simservs/communication-diversion/cp:ruleset/cp:rule%5B@id=%22cfu%22%5D?xmlns(cp=urn:ietf:params:xml:ns:common-policy)
func ExampleDocument_PutElement() {
	nodeSelector, _ := url.PathUnescape(`simservs/communication-diversion/cp:ruleset/cp:rule%5B@id=%22cfu%22%5D`)
	query, _ := url.PathUnescape(`xmlns(cp=urn:ietf:params:xml:ns:common-policy)`)
	body := []byte(`<cp:rule id="cfu"><cp:actions><forward-to><target>tel:+12025550153</target></forward-to></cp:actions></cp:rule>`)

	stored, err := simservs.ParseDocument([]byte(serviceData)) // from UDR
	if err != nil {
		panic(err)
	}
	before, err := simservs.UserDocument(stored)
	if err != nil {
		panic(err)
	}
	sel, err := simservs.ParseNodeSelector(nodeSelector, query, simservs.NamespaceSimservs)
	if err != nil {
		panic(err) // ErrUndefinedPrefix: 400, ErrUnsupportedSelector: 404
	}
	after := before.Clone()
	created, err := after.PutElement(sel, body)
	if err == nil {
		err = simservs.ValidateSimservsDocument(after)
	}
	if err == nil {
		err = simservs.CheckOwnerChange(before, after)
	}
	next := stored.Clone()
	if err == nil {
		err = simservs.SetUserDocument(next, after)
	}
	var conflict *simservs.ConflictError
	if errors.As(err, &conflict) {
		fmt.Printf("409 %s\n%s\n", simservs.ErrorMIMEType, conflict.Report())
		return
	}
	// next.RootElement() goes back to the HSS with PUR, Sequence-Number + 1.
	fmt.Println("created:", created)
	got, contentType, _ := after.Get(sel)
	fmt.Println(contentType)
	fmt.Println(string(got))
	// Output:
	// created: false
	// application/xcap-el+xml
	// <cp:rule id="cfu"><cp:actions><forward-to><target>tel:+12025550153</target></forward-to></cp:actions></cp:rule>
}
