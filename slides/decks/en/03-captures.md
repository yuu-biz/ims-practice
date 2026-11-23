---
title: Reading a capture, and the Lab
footer: ims-practice study session 3. Captures and the Lab
lang: en
---

# Reading a capture, and the Lab

ims-practice study session 3 (where the capture comes from, how to filter it, and what the Lab draws beside it)

---

[layout: summary]

## What you will know after this session

To begin: both tools show the same signaling. Why open a capture at all, when the Lab draws the ladder for you?

- Where the capture file comes from, and what is in it.
- How a display filter is put together, and where the filter of each session is written down.
- How to get a ladder out of Wireshark, and how to hear the audio.
- What the Lab builds, and what it only infers.
- The one message the Lab shows that the capture does not have.

> Guides: [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md) (`docs/guide/en/capture.md`) and [Watch the IMS in the Lab](../../../docs/guide/en/lab.md) (`docs/guide/en/lab.md`)
> No part of the IMS is explained here. Every session from here on reads a capture, and most of them open the Lab as well.

---

## One network, one file

```text
            captures/ims-<start time in UTC>.pcap
                        ^
                        |
                 capture: tcpdump on the bridge ims0
                        ^
                        |  one Docker network, 172.28.0.0/24
  dns  hss  icscf  scscf  pcscf  agw  lab  ipsmgw  mmtel  epc  mrfc  mrfp  UEs
```

- Every node talks over one Docker network, so one file holds the whole conversation between them.
- The capture container starts first and the nodes after it, so their first messages are in the file.
- Each start of that container writes a new file, named after its start time.
- Session 10 adds a second network and the link between it and this one. Each of the three bridges then has a capture container and a file of its own.

```sh
docker compose up -d --build
```

> Guide: ["Start the system"](../../../docs/guide/en/capture.md#start-the-system) and ["Open the capture file"](../../../docs/guide/en/capture.md#open-the-capture-file)

---

## Open it in Wireshark

1. Open the newest file in `captures/` with **File > Open**.
2. Type the filter of the session in the display filter bar and press Enter.
3. For the ladder, select **Statistics > Flow Graph** and tick **Limit to display filter**.

- Wireshark opens the file while the capture still runs. **View > Reload** brings in the packets that came since.
- To see packets as they happen, stream the capture into Wireshark: `scripts/capture-live.sh` in Git Bash, `scripts/capture-live.ps1` in PowerShell 7.4 or later.
- The files stay in `captures/` until you delete them, so the capture of a session can be read again later.

> Guide: ["Open the capture file"](../../../docs/guide/en/capture.md#open-the-capture-file) and ["Watch the traffic live"](../../../docs/guide/en/capture.md#watch-the-traffic-live)

---

## The display filter

This is the filter of the session on registration.

```text
sip || diameter.cmd.code >= 300 || dns.qry.type == 35 || dns.qry.type == 33
```

| A term | What it keeps |
| --- | --- |
| `sip`, `diameter`, `rtp` | Every packet of that protocol. |
| `diameter.cmd.code == 302` | A field compared with a value. |
| `diameter.cmd.code in {302, 306, 307}` | Several values of one field. |
| `ip.addr == 172.28.0.70` | Both directions of one node. `ip.src` and `ip.dst` take one of them. |
| `gsm_sms.sms_text contains "Hello"` | A field that holds those bytes. |

- `&&` and `||` join the terms, and `not` or `!` turns one around. The filter above keeps the SIP, the Cx and the DNS lookups of the registration, and hides the Diameter watchdogs.
- Each guide gives the filter of its scenario under "Open the capture". Several guides also end with a table of narrower filters, each with what it finds.

> Guide: [the registration guide, "Open the capture"](../../../docs/guide/en/registration.md#open-the-capture) and [the capture guide, "Display filters for SMS over IP"](../../../docs/guide/en/capture.md#display-filters-for-sms-over-ip)

---

## What is in the file before any UE registers

The filter is `diameter`.

| Look at | What to see |
| --- | --- |
| CER and CEA | Five connections to port 3868, one from each node that asks a question over Diameter. |
| The applications in them | Cx from the I-CSCF and the S-CSCF, Sh from the two application servers, Rx from the P-CSCF. |
| DWR and DWA | A side that has heard nothing for about 30 seconds asks whether its peer is alive. |
| DPR and DPA | The node that stops says goodbye before it closes the connection. |

```sh
docker compose stop scscf
```

- The S-CSCF sends a DPR with Disconnect-Cause `REBOOTING`, and the HSS answers with a DPA. `docker compose start scscf` brings a new CER and CEA.

> Guide: ["What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start), with the table of all five connections. RFC 6733. RFC 3539 3.4.1.

---

## The ladder, and the audio

- **Statistics > Flow Graph** with **Limit to display filter** draws a ladder of the messages that the filter left.
- **Telephony > VoIP Calls** lists each SIP dialog as a call. **Flow Sequence** draws the ladder of that dialog, **Play Streams** plays both directions of its audio.
- **Telephony > RTP > RTP Streams** lists the streams themselves, such as those of the MRFP.
- RTP runs on ports that only the SDP announces. Wireshark finds them when the SIP of the call is in the same capture, which it is here. For a stream that shows up as plain UDP, switch on **Analyze > Enabled Protocols > RTP > rtp_udp**, the RTP heuristic.

> Guide: [the call guide, "Open the capture"](../../../docs/guide/en/call.md#open-the-capture) and [the MRF guide, "Open the capture"](../../../docs/guide/en/mrf.md#open-the-capture)

---

## The copies in the file

- A node can send a copy of every SIP and Diameter message it sends or receives to the Lab, which listens on UDP port 9060. The copies use HEP version 3.
- A node sends them when its configuration has a `hep` section. The UE simulator sends them with `-hep lab:9060`. Without either, nothing is sent.
- The copies are in the capture too, as UDP packets to port 9060. Wireshark has no HEP dissector of its own, so hide them with `not udp.port == 9060`.
- Those copies are all the Lab knows. It asks no node for its state, and no node waits for an answer from it.

> Guide: ["Copies for the Lab (HEP)"](../../../docs/guide/en/capture.md#copies-for-the-lab-hep)

---

## Open the Lab

```sh
docker compose up -d --build
```

- Open <http://localhost:8080>. The dot next to the title says `live` while the page receives updates from the Lab's server.
- The Lab is published on the host's loopback address only. It asks for no password, and anyone who reaches it can delete subscribers, drive the UEs and detach a UE from the access network.
- The Lab is not part of the IMS, and it is not on the path of any call. A node starts and runs even when the Lab is not there.

> Guide: ["Open the Lab"](../../../docs/guide/en/lab.md#open-the-lab)

---

## What each panel shows

| Panel | What it shows |
| --- | --- |
| UE simulators | One card per UE simulator: its identity, whether it is registered, its call, its short messages and its supplementary services. The buttons and forms drive the UE. |
| Calls | One row per call, with its caller, callee, state, final status code and number of dialogs. |
| Ladder diagram | The SIP and Cx messages of the selected call or registration, one column per node. |
| Registrations | One row per public identity that registered, with its contact, its expiry and its S-CSCF. |
| Subscribers (HSS) | The subscriptions the HSS holds. Test subscribers can be added and deleted here. |
| Access network (EPC) | One row per UE attached to the EPC stub, with its address, its bearers and the Rx sessions of its calls. |
| Faults | The faults the fault agent holds, and a form that injects one. The panel is off until that agent runs. |

- Select an arrow of a ladder to see the whole message and the clauses of the specifications that describe it.
- **Copy as text** under a ladder writes it out as monospace text, in the form of the ladder diagrams of the guides.

> Guide: ["What each panel shows"](../../../docs/guide/en/lab.md#what-each-panel-shows) and ["Copy the ladder as text"](../../../docs/guide/en/lab.md#copy-the-ladder-as-text)

---

## How the ladder is built: two copies, one arrow

```text
  the sender's copy    ---+
                          +--> one arrow, when source, destination and content agree
  the receiver's copy  ---+
```

- Every message between two nodes is reported twice, once by each of them. The Lab pairs the two, so one arrow is one message on the wire.
- A retransmission with the same content still gets an arrow of its own.
- Three panels are the exception. The UE cards, the access network and the faults come from the APIs of the UEs, the EPC stub and the fault agent, so a card and the Registrations panel can disagree for a moment.

> Guide: ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built) and ["What each panel shows"](../../../docs/guide/en/lab.md#what-each-panel-shows)

---

## What the Lab infers: Cx and Rx

- A Cx message carries nothing that names the SIP request it was sent for. The Session-Id of Diameter and the Call-ID of SIP are unrelated.
- The Lab links them by the rule of the procedures. A Cx request belongs to the SIP request that the same node received last, at most 2 seconds before, and that names the same user.
- An Rx session links the same way (TS 29.214 A.1, A.4). Its first AAR names the UE only by its address, so it goes with the last message with SDP, or the REGISTER, that the P-CSCF had from that address.
- Select such an arrow. Under **Link to this ladder** its details say that the Lab inferred the link, and that the message itself does not carry it.

> Guide: ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built). The rule is in `internal/lab/causes.go`.

---

## One call, several dialogs

```text
  UE 1 --- dialog 1 --- MMTel AS --- dialog 2 --- MMTel AS --- dialog 3 --- UE 2
                   the same icid-value in P-Charging-Vector on all three
```

- The MMTel AS is a routeing B2BUA (TS 24.229 5.7.5). It answers the INVITE the S-CSCF sends it and sends a new one with a Call-ID of its own, so a call between two users is three dialogs.
- Inside the network every dialog carries the same icid-value, which the caller's P-CSCF puts in and each AS keeps (TS 24.229 5.2.6.3.3 step 7, 5.7.5.1). The P-CSCF takes the header off what it sends to a UE.
- The Lab shows the dialogs of one icid-value as one call, counts them in the Dialogs column, and draws all of them in one ladder.

> Guide: ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built)

---

## The one message the capture does not have

```text
  S-CSCF            drawn by the Lab            in the pcap
    |
    +---+  INVITE   an arrow back to the        nothing: the message
    |<--+           same column                 never leaves the container
    |
```

- A node can send a message to itself, as the S-CSCF does when it serves the caller and the callee at once.
- It is the one kind of message that the Lab shows and the pcap does not. Everything else of a ladder is in the capture too.
- The other way round, the capture has what no ladder draws, such as the RTR the HSS sends when a registered subscriber is deleted. The HSS sends it on its own, not for a SIP request, so the Lab ties it to nothing.

> Guide: ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built) and ["Manage the subscribers"](../../../docs/guide/en/lab.md#manage-the-subscribers)

---

## Drive the UE simulators

```sh
docker compose --profile phones up -d --build
```

1. On the card of `phone2`, set the answer policy to `manual`. The UE then rings until you answer.
2. On the card of `phone1`, press **Call**. The card of `phone2` shows an incoming call. Press **Answer**.
3. Select **ladder** next to the call. It is drawn while the call goes on.

- `phone1`, `phone2` and `phone3` use subscribers 1, 2 and 3, the same as `ue`, `ue2` and `ue3`. Run one of the two profiles at a time.
- The HTTP API of the Lab and the control API of the UE simulator follow no specification and are this project's own. What the ladder draws is the signaling of the IMS.

> Guide: ["Drive the UE simulators"](../../../docs/guide/en/lab.md#drive-the-ue-simulators) and ["These APIs are this project's own"](../../../docs/guide/en/lab.md#these-apis-are-this-projects-own)

---

[layout: exercise]

## Looking back

1. Where does the capture file come from, and why does it hold the first message of every node?
2. Which filter keeps one node, and which one hides the copies that go to the Lab?
3. A Cx request and a REGISTER are in one ladder. What put them together?
4. A call through the MMTel AS has three Call-IDs. What makes the Lab draw one call?
5. Which message does the Lab show that the capture has not, and why?

---

[layout: answers]

## Looking back: answers

1. From the capture container, which runs tcpdump on the Docker bridge. It starts before the nodes.
2. `ip.addr == 172.28.0.70` keeps both directions of one node, and `not udp.port == 9060` hides the copies.
3. The rule of the procedures: the same node, the same user, at most 2 seconds apart. The Lab inferred it, and the details of the arrow say so.
4. One icid-value in P-Charging-Vector, which the caller's P-CSCF put in and each AS kept.
5. A message from a node to itself, such as the S-CSCF to itself when it serves both sides of a call. It never leaves the container, so the bridge never sees it.

---

## Next session

- Next is registration, the first flow read in a capture. It is also the first ladder to open in the Lab.
- Before then, start the system once, open the newest file in `captures/` with the filter `diameter`, and find the five CERs.

```sh
docker compose up -d --build
```

> The guide of the next session is [Follow a UE registering with the IMS](../../../docs/guide/en/registration.md). To stop the system, see ["Stop the system"](../../../docs/guide/en/capture.md#stop-the-system).
