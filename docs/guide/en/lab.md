# Watch the IMS in the Lab

[日本語](../ja/lab.md)

The Lab is a web page where you try things on the IMS and watch what happens. It shows the registrations and the calls of the IMS while they happen, and draws each one as a ladder diagram. It also shows the subscriptions that the HSS holds, and drives the UE simulators `phone1`, `phone2` and `phone3`, so that you can make a call and watch its signaling without typing commands.

## Before you start

- Docker with Docker Compose is running.
- A web browser.

## Open the Lab

Run this command at the root of the repository:

```sh
docker compose up -d --build
```

Open <http://localhost:8080> in the browser. The dot next to the title says `live` while the page receives updates from the Lab's server.

The Lab is published on the host's loopback address only. It asks for no password, and anyone who reaches it can read what the HSS holds, drive the UEs, detach a UE from the access network and, while the fault agent runs, break a node. To open it from another machine on your network, create `compose.override.yaml` at the root of the repository. Compose reads this file after `compose.yaml` by itself:

```yaml
services:
  lab:
    ports: !override
      - "8080:8080"
```

Run `docker compose up -d` again, and open `http://<address of the host>:8080` on the other machine. Delete the file when you no longer need it.

That the Lab asks for no password is a trade-off of a network for learning, which only you reach on `127.0.0.1`. A page of another site that you open in the same browser can still send requests to `127.0.0.1`, though. The browser lets such a page send a POST whose Content-Type is `text/plain`, a form or multipart, or that has none, without asking the server first, and only keeps the answer from the page (the CORS-safelisted method and request-header of the Fetch Standard). So the Lab, the UE simulators and the fault agent take a POST or a PUT only with `Content-Type: application/json`, one without a body too, and answer any other with 415 (Unsupported Media Type). Before a request of that Content-Type to another site, the browser asks the server with a preflight `OPTIONS` request. These APIs do not answer it with the `Access-Control-Allow-*` header fields, so the browser never sends the request itself. A `curl` of your own adds `-H 'Content-Type: application/json'`. That keeps out the requests that a page of another site sends without a preflight, and no more. A page that the browser takes for the same origin as the API needs no preflight, and can send JSON: one of a domain whose name its owner later has resolve to `127.0.0.1`, which is called DNS rebinding. Nor does it keep out a person who reaches the port. The APIs ask for no password and look at no `Host` header, which the network of an operator would.

## What each panel shows

| Panel | What it shows |
| --- | --- |
| UE simulators | One card per UE simulator: its public identity (IMPU), whether it is registered, its current call and the session of that call, its answer policy, the short messages it sent and received, and its supplementary services. The buttons and forms drive the UE. |
| Calls | One row per call, with its caller, callee, state and final status code, and the number of its dialogs. Select a Call-ID to draw the ladder of the whole call. |
| Ladder diagram | The SIP and Cx messages of the selected call or registration, one column per node. Select an arrow to see the whole message and the clauses of the specifications that describe it. For a MESSAGE that carries a short message, the details also show its RP and TP fields, its text and its body in hex. **Copy as text** writes the whole ladder out as monospace text. |
| Registrations | One row per public identity that the Lab saw register, with its contact, expiry and S-CSCF. Select an identity to draw the ladder of its last REGISTER. |
| Subscribers (HSS) | One row per subscription that the HSS holds, with its public identities and the barred ones among them, whether it is marked as a test user, the service profile it gets and its alerting tone. The panel changes none of them: an operator provisions a subscription from the OAM. |
| Access network (EPC) | One row per UE that is attached to the EPC stub, with the address the stub gave it, its EPS bearers and the Rx sessions of its calls. The buttons start the procedures that the network initiates: deactivating a bearer and detaching the UE. |
| Faults | The faults that the fault agent holds, and a form that injects one. The panel is off until that agent runs. |

The Lab knows only what the nodes tell it. Each node, and each UE simulator, sends a copy of every SIP and Diameter message it sends or receives to the Lab over HEP (see "Copies for the Lab (HEP)" in [the capture guide](capture.md)). The Lab builds the registrations and the calls from those copies alone and never asks a node for its state. Three panels are the exception: the UE cards show what each UE reports through its control API, and the access network and fault panels what the EPC stub and the fault agent report through theirs. None of the three is built from the copies, so for a moment a UE card and the Registrations panel can disagree.

## How the ladder is built

A message that goes from one node to another is reported twice, once by its sender and once by its receiver. The ladder pairs a sent copy and a received copy with the same source, destination and content into one arrow, so each arrow is one message on the wire; a retransmission that is identical still gets its own arrow. HEP does not say whether a node sent or received a copy, so the Lab tells it by the address the copy came from, which is the node's own end of the message. A node on two networks, such as the IBCF of [the interconnect guide](interconnect.md), sends its copies from its address on the network of the Lab, so the configuration of the Lab names its other address under `node_addresses`.

A Cx message carries nothing that names the SIP request it was sent for: the Session-Id of Diameter and the Call-ID of SIP are unrelated. The Lab therefore links them by the rule of the procedures. A Cx request belongs to the SIP request that the same node received last, at most 2 seconds before, and that names the same user: for a UAR, MAR or SAR, a REGISTER whose Authorization username is the User-Name or whose To is the Public-Identity; for an LIR, a request whose Request-URI is the Public-Identity. The answer follows its request through the Session-Id. The rule is in `internal/lab/causes.go`.

An Rx session of the P-CSCF links the same way (TS 29.214 A.1, A.4). Its first AAR names the UE only by its address: the Framed-IP-Address, or the Framed-IPv6-Prefix for a UE on IPv6 (TS 29.214 4.4.1). It belongs to the SIP message with SDP that the P-CSCF received last from that address, the INVITE of a calling UE or the 183 of a called one, or to the REGISTER from it, whichever came last. The rest of the session, its STR, RAR or ASR and the answers, follows through the Session-Id. The ladder draws the PCRF in the column next to the P-CSCF; [the VoLTE guide](volte.md) shows the messages.

Select a Cx or Rx arrow: its details say under **Link to this ladder** that the Lab inferred the link by this rule, and that the message itself does not carry it.

A call through the MMTel AS is more than one dialog. The AS is a routeing B2BUA (TS 24.229 5.7.5): it answers the INVITE that the S-CSCF sends it and sends a new INVITE of its own, with a Call-ID of its own. A call from UE 1 to UE 2 is therefore three dialogs: UE 1 with its AS, that AS with the AS of UE 2, and that AS with UE 2. A diverted call has more. Inside the network each dialog carries the same icid-value in P-Charging-Vector, which the caller's P-CSCF puts in and each AS keeps (TS 24.229 5.2.6.3.3 step 7, 5.7.5.1). The P-CSCF removes the header from what it sends to a UE. The Lab lists the dialogs with one icid-value as one call: the row shows the caller's dialog, its Call-ID, state and status, and the Dialogs column counts them. The ladder draws all of them, with a column for the MMTel AS. Select an arrow to see which Call-ID it belongs to. Diverted calls and the other services are in [the MMTel guide](mmtel.md).

A node can send a message to itself, as the S-CSCF does when it serves both the caller and the callee. The ladder draws that as an arrow that turns back to the same column. The capture on the Docker network does not have that message, because it never leaves the container. It is the one kind of message that the Lab shows and the pcap does not.

## Copy the ladder as text

**Copy as text** under the ladder writes the ladder out as monospace text: a row of node names over the lifelines, then one row per message, with its arrow drawn from the sender's lifeline to the receiver's. It is the form of the ladder diagrams in these guides, so the text goes straight into a `text` block of a guide or of your own notes.

Each label sits at the left end of its arrow, and dashes fill the rest, so that the arrowhead reaches the lifeline of the node that received the message. An arrow that runs from right to left starts with `<`. A node that sends to itself turns back to its own lifeline as `<-+`, with the label beside it. Two columns of the same kind of node, such as the UE of the caller and the UE of the callee, are numbered from the left.

A column is as wide as the longest label that crosses it, and the label of a SIP message is its whole request or status line. A call's ladder is therefore wide; once the text is in your notes, shorten the labels and number the steps by hand, as the ladder diagrams in these guides do.

Copying goes through the browser's clipboard, which a browser gives only to a page it considers secure. <http://localhost:8080> is secure; the same Lab opened over plain HTTP from another machine is not. There the button writes the text out below it instead, for you to select and copy.

## The subscribers the HSS holds

The Subscribers panel lists the subscriptions of the HSS as it holds them now, and changes none of them. Provisioning a subscription is an operator's work, which the operator does from the OAM ([Run the network from the OAM](registration.md#run-the-network-from-the-oam)). The panel is where you see what such a change did.

Each row shows what the HSS holds for one subscription:

- **Public identities**, with `(barred)` after each identity that is barred. The S-CSCF lets a barred identity register and keeps it out of every other IMS communication (TS 29.228 B.2.1). Every subscription of this network that authenticates with IMS AKA carries one, the temporary public identity that a card with a USIM and no ISIM derives from the IMSI and registers. [The identities of a phone without an ISIM](volte.md#the-identities-of-a-phone-without-an-isim) follows that identity.
- **Test user**, the mark of a subscription that an operator keeps for trying a feature out. It picks the service profile of a test user, and while the network admits the test users alone it also decides who may register at all, as [Admit only the test users](registration.md#admit-only-the-test-users) shows.
- **Service profile**, the profile that decides which filter criteria the S-CSCF gets for the user, and so which application server each of the user's requests reaches. The cell holds the name that the subscription itself carries. A subscription that carries none gets the profile that the settings of the HSS name for its kind of subscription, and the cell says which instead, as `from the settings: default`. An operator changes that setting while the HSS runs, with the same maintenance request that changes the admission policy ([Admit only the test users](registration.md#admit-only-the-test-users)).
- **Alerting tone (CAT)**, the tone that the callers of the user hear while it rings (TS 24.182). A tone with `(off)` after it is provisioned and stopped, so the callers hear their own ringing tone; `none` is a subscription that has no tone at all. [CAT: the tone that the called user chooses](mrf.md#cat-the-tone-that-the-called-user-chooses) provisions one and follows the call that plays it.

## Watch what a change to a subscription does

The HSS does two things of its own accord that are worth watching here: it pushes a user profile that changed to the S-CSCF, and it cancels a registration. Each of the two follows a change that an operator makes to a subscription, so the OAM makes the change and the Lab shows what came of it.

The OAM's port is on no address of the host, and BusyBox `wget` sends no method but `GET` and `POST`, so each request below is written out to `nc` from inside the OAM's container.

Add a subscription for the experiment, so that the subscribers the other guides use stay as they are:

```sh
docker compose exec oam sh -c '
body="{\"public_identities\":[\"sip:001010000000005@ims.mnc001.mcc001.3gppnetwork.org\"],\"password\":\"test-password-5\"}"
printf "PUT /oam/v1/subscriptions/001010000000005@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8080'
```

A row for it appears in the Subscribers panel. Register it with a UE simulator, which stays registered until it is stopped:

```sh
docker compose run --no-deps -d --name lab-subscriber-ue ue register \
  --impi 001010000000005@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:001010000000005@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-5
```

The Registrations panel gets a row for `sip:001010000000005@ims.mnc001.mcc001.3gppnetwork.org`. Select the identity to draw the ladder of that REGISTER.

### The network pushes a changed profile

Send the subscription again with the service profile `no-services`, which holds no filter criteria at all:

```sh
docker compose exec oam sh -c '
body="{\"public_identities\":[\"sip:001010000000005@ims.mnc001.mcc001.3gppnetwork.org\"],\"password\":\"test-password-5\",\"service_profile\":\"no-services\"}"
printf "PUT /oam/v1/subscriptions/001010000000005@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8080'
```

The Service profile cell of the row becomes `no-services`. The user is registered, so the HSS does not leave its S-CSCF with the criteria it sent at the registration: it sends the whole user profile there at once, in a Push-Profile-Request (PPR). "Change the service profile of a registered user" in [the registration guide](registration.md#change-the-service-profile-of-a-registered-user) follows that sequence and says what the requests of the user then reach. The capture holds the PPR and its answer, and no ladder draws them, because the HSS sends them on its own and not for a SIP request.

### The network cancels a registration

Remove the subscription:

```sh
docker compose exec oam sh -c '
printf "DELETE /oam/v1/subscriptions/001010000000005@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\n\r\n" |
  nc -w 5 127.0.0.1 8080'
```

The row leaves the Subscribers panel, and the registration leaves the Registrations panel. The HSS sends a Registration-Termination-Request (RTR) to the S-CSCF (TS 29.228 6.1.3), the S-CSCF answers with an RTA, and it tells the UE with a reg-event NOTIFY. `docker logs lab-subscriber-ue` ends with the UE deregistered by the network, and the UE does not register again. The whole sequence is in "Remove a subscription: deregistration by the HSS" in [the registration guide](registration.md#remove-a-subscription-deregistration-by-the-hss). The capture shows the RTR and the RTA, and no ladder draws them, for the same reason as the PPR above: the HSS sends the RTR on its own, not for a SIP request, so the Lab ties it to no call and no registration.

Remove the container of the UE when you are done:

```sh
docker rm lab-subscriber-ue
```

## Drive the UE simulators

The three UE simulators `phone1` (subscriber 1, `tel:+12025550101`), `phone2` (subscriber 2, `tel:+12025550102`) and `phone3` (subscriber 3, `tel:+12025550103`) do not start with the rest of the system. Start them with the `phones` profile:

```sh
docker compose --profile phones up -d --build
```

Until then their cards say that they are not reachable. Once started, each one registers by itself. If a card still says `not registered`, press **Register**.

The cards of `volte10` and `volte12` are the VoLTE UEs of the `volte` profile, which attach to the EPC stub and register with IMS AKA. Start them with `docker compose --profile volte up -d volte10 volte12`, and call `tel:+12025550112` from `volte10`; [The VoLTE call flow](volte.md) follows such a call. The two profiles use different subscribers and addresses, so they can run at the same time.

To make a call:

1. On the card of `phone2`, set the answer policy to `manual`. The UE then rings until you answer. With `auto` it answers after a second; with `reject` it turns every call down with 486 (Busy Here).
2. On the card of `phone1`, press **Call**. The form starts with the tel URI of the other UE, which it takes from the HSS subscription.
3. The card of `phone2` shows an incoming call in the state `ringing`. Press **Answer**. Both cards show the call as `active`. The **Session** row of each card shows the codec of the call, the direction of the audio in the SDP of each side, the precondition table (RFC 3312) and whether the access network has reserved the media resources. `phone1` and `phone2` reach the P-CSCF straight over the Compose network, an access network that needs no reservation, so their cards say `no reservation of media resources needed on this access network`.
4. Press **Hold** on the card of `phone1`. The UE sends a re-INVITE whose offer makes the audio `sendonly` (TS 24.610), and the Session row of each card shows the new directions. **Resume** takes the call back with `sendrecv`.
5. Type digits such as `12#` and press **Send DTMF**. The UE sends them as telephone events in its RTP (RFC 4733), and the Session row of the other card shows them as received.
6. Press **Hang up** on either card. Once a call is answered this sends a BYE. Before that, the caller sends a CANCEL and the callee declines with 603 (Decline).

Select **ladder** next to a call to draw it while it goes on. Each card knows only its own dialog, but the ladder shows the whole call, from either card. A button is greyed out when the UE would refuse what it does, such as a second call while one is on the line.

To send a short message (SMS over IP, see [the SMS guide](sms.md)):

1. On the card of `phone1`, fill in the SMS form. The number starts as that of the other UE, `+12025550102`. Type a text of up to 160 characters of the GSM 7 bit alphabet, or up to 70 other characters such as Japanese. Tick **status report** to ask the SC to report the delivery.
2. Press **Send SMS**. The message appears under **SMS sent** as `waiting for the submit report`, and soon `accepted by the SC`.
3. The card of `phone2` shows the text under **SMS received**, with the sender's number.
4. With a status report asked for, the entry on `phone1` ends with `status report: delivered (TP-ST 0x00)`.

A number that no subscriber has, such as `+12025550198`, ends as `refused by the SC: RP-ERROR, RP-Cause 1, TP-FCS 0xc3`. Subscriber 4, `+12025550104`, does not register here: its short message is accepted, and the status report says `not delivered, the SC has given up (TP-ST 0x62)`.

Each entry has a **ladder** link that draws the MESSAGE that carried the short message. The submit report, the delivery report and the status report are MESSAGEs of their own, each with its own Call-ID.

The cards follow the short messages through the same event stream that keeps the call up to date: each UE sends its whole status, short messages included, whenever something changes.

To forward calls (the supplementary services, which the UE sets over Ut with XCAP):

1. On the card of `phone2`, open **Supplementary services (Ut/XCAP)**. The UE reads its document, `simservs.xml`, from the XCAP server of the MMTel AS, and the card lists every service: call forwarding (CFU, CFB, CFNR, CFNRc, CFNL), barring (BAIC, BIC-Roam, ACR, BAOC, BOIC, BOIC-exHC) and the default of OIR. Each starts as `off`, as the HSS provisions them (GSMA IR.92 2.3.2).
2. Choose a service, such as **CFU (unconditional)**. The target starts as the tel URI of the other UE; type another, such as `tel:+12025550103`, if you like. Press **On**. The list then shows `CFU (unconditional): on to tel:+12025550103`, and the ETag under it has gone up by one.
3. Press **Off** to switch it off again. The target stays registered, as IR.92 2.3.2 has the UE keep it.

Each press makes the UE send XCAP requests on its own: a GET of the whole document, a PUT of the one rule with `If-Match`, and another GET for the list. The XCAP server reads and writes the document in the HSS over Sh (UDR and PUR, Service-Indication `MMTEL-Services`). The ladders show none of this, because neither HTTP nor these Sh requests belong to a SIP request; the capture has all of it, as HTTP on port 80 and Diameter on port 3868. The first GET gets 401 with two HTTP Digest challenges, SHA-256 and then MD5, and the UE answers the first.

To hear a customized alerting tone (CAT, TS 24.182), give subscriber 2 the tone `cat1` through the OAM, as [CAT: the tone that the called user chooses](mrf.md#cat-the-tone-that-the-called-user-chooses) shows, and watch the Alerting tone (CAT) cell of its row change. The MMTel AS reads the tone over Sh for each call to that user. Call `phone2` from `phone1` again: the MRFP plays the tone to `phone1` as early media until `phone2` answers, and the Session row of `phone1` says `rang with early media from the network`. With the tone off, the same row says `rang with the UE's own ringing tone`.

To take a second call and make a conference of the two (TS 24.615, TS 24.147):

1. Call `phone2` from `phone1`, and let it answer.
2. Call `phone1` from `phone3`. The card of `phone1` shows the new call under **Other call** as `waiting`: `phone1` rang with `Alert-Info: <urn:alert:service:call-waiting>`.
3. Press **Answer** on `phone1`. It puts the call with `phone2` on hold and answers `phone3`. **Swap** goes from one call to the other.
4. Press **Merge** on `phone1`. It creates a conference at the MMTel AS and asks it with a REFER for each peer to bring the peer in. The **Conference** row of each of the three cards then lists the three users, as the conference event package tells them. The MRFP mixes the audio.

[The MRF guide](mrf.md) follows the tone, the waiting call and the conference in the capture.

The same settings are on the command line: `ue xcap get`, `ue xcap set cfu on tel:+12025550103` and `ue xcap oir restricted`. `ue xcap noreply 5` sets how long a call rings before CFNR diverts it, from 5 to 180 seconds; the card leaves it at 20 seconds.

The UE simulators use subscribers 1, 2 and 3, the same as the `ue`, `ue2` and `ue3` services of the `ue` profile. Run one of the two profiles at a time, or the two UEs of a subscriber will replace each other's registration.

## Take a bearer away, and detach a UE

The **Access network (EPC)** panel lists every UE that has attached to the EPC stub, which are `volte10` and `volte12` of the `volte` profile. Each row has the IMSI, the address the stub gave the UE, the access point name, the UE's EPS bearers and the Session-Ids of the Rx sessions that its P-CSCF holds for it.

A UE that has attached has its default bearer, EBI 5 with QCI 5, which carries the SIP signaling (IR.92 4.4.1). While a call is set up, the dedicated bearer of the voice appears beside it, QCI 1 (4.4.2).

- **Release** next to the voice bearer makes the network deactivate it, as a lost radio link would (TS 24.301 6.4.4.2). The PCRF aborts the Rx session of the call, the P-CSCF ends the call with a BYE in the user's name, and both UE cards go back to no call. "The network takes the bearer" in [the VoLTE guide](volte.md) follows the whole sequence in the capture.
- The default bearer has no **Release**. It goes with the PDN connection, so the stub refuses to deactivate it and asks for a detach instead.
- **Detach** makes the network detach the UE (5.5.2.3). The PDN connection ends, the PCRF aborts every Rx session of the UE, and the UE answers with DETACH ACCEPT. The stub asks for no re-attach, so the UE stays off the access network until you start it again, for example with `docker compose --profile volte up -d volte10`.

The stub owns this state and the Lab keeps no copy of it, so the panel asks the stub again every few seconds.

## Break a node on purpose

The **Faults** panel stops, freezes and crashes a node, loses its packets, delays them, and cuts it off from other nodes, so that you can watch a failure beside the signaling. It needs the fault agent, which starts only when you ask for it, because it drives the Docker Engine. [Fault injection](../../../deploy/chaos/README.md) explains why, how to start the agent and what each fault does. Until it runs, the panel says so and the form is off.

Choose a target, such as `pcscf`, and a kind. The form then offers the fields of that kind: a duration for the faults that last, the delay and the jitter of `delay`, the percentage of `loss`, the peers of `partition`, and the Compose network whose interface the fault sits on. Press **Inject**. The fault appears in the table with its id and its state, and **Clear** takes it away again. A fault also ends by itself when its duration is up, five minutes by default, so a forgotten one does not stay. A `crash` and a `recreate` happen once: the node comes back on its own, so such a fault ends as `done` and has nothing to clear.

The agent checks every request and the panel shows what it answers. Asking for a second fault that the target already has gives `conflict`, and a fault that Docker could not carry out appears in the state `failed` with the reason beside it.

## These APIs are this project's own

Three interfaces here come from no 3GPP or IETF specification, and none of them is part of the IMS:

- The HEP copies. HEP is the protocol of the Homer project for collecting signaling. The capture holds the copies as UDP packets to port 9060 besides the real messages.
- The HTTP API of the Lab, listed in the package comment of `internal/lab/lab.go` and described in OpenAPI in [`internal/lab/openapi.yaml`](../../../internal/lab/openapi.yaml).
- The control API of the UE simulator (`ue serve`), listed in the comment of `Phone.Handler` in `internal/ue/phone_http.go` and described in [`internal/ue/openapi.yaml`](../../../internal/ue/openapi.yaml), and for the supplementary services in the comment of `SupplementaryServices.Handler` in `internal/ue/xcap.go`. "Drive a UE over HTTP" in [the call guide](call.md) uses it directly.

What you see in the ladder, though, is the signaling of the IMS as the specifications define it. [The HTTP APIs](apis.md) lists every HTTP API of the project, with the specification each follows or the OpenAPI file of this project that describes it.

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
