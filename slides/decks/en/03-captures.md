---
title: Reading a capture, and the Lab
footer: ims-practice 3. Captures and the Lab
lang: en
format: visual
---

```ims-model
nodes:
  - {id: ue, label: UE, role: the phone, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: its first hop, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: entry of the network, at: [1, 0]}
  - {id: scscf, label: S-CSCF, role: "registers, starts services", at: [2, 1]}
  - {id: hss, label: HSS, role: subscriber data, at: [2, 0]}
  - {id: bridge, label: ims0, role: the Docker bridge, at: [1, 2]}
  - {id: capture, label: capture, role: tcpdump, at: [0, 2]}
  - {id: pcap, label: pcap, role: a file in captures/, at: [0, 3]}
  - {id: lab, label: Lab, role: takes the HEP copies, at: [2, 2]}
  - {id: ue-a, label: UE 1, role: the caller, at: [0, 3]}
  - {id: as-o, label: MMTel AS, role: AS of the caller, at: [1, 3]}
  - {id: as-t, label: MMTel AS, role: AS of the callee, at: [2, 3]}
  - {id: ue-b, label: UE 2, role: the callee, at: [3, 3]}
events:
  # One network, one file
  - {id: ue-on, rel: attached, from: ue, to: bridge}
  - {id: pcscf-on, rel: attached, from: pcscf, to: bridge}
  - {id: scscf-on, rel: attached, from: scscf, to: bridge}
  - {id: hss-on, rel: attached, from: hss, to: bridge}
  - {id: listens, rel: listens, from: capture, to: bridge}
  - {id: writes, rel: writes, from: capture, to: pcap}
  # The HEP copies
  - id: invite
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
  - id: copy-sent
    msg: HEP
    from: pcscf
    to: lab
    proto: UDP 9060
    note: (copy of the sender)
  - id: copy-received
    msg: HEP
    from: scscf
    to: lab
    proto: UDP 9060
    note: (copy of the receiver)
  - id: join
    do: makes one arrow of them
    at: lab
    fields:
      - when: the same source, destination and content
      - another arrow: a retransmission of the very same content
  # What the Lab infers
  - id: register
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
  - id: mar
    msg: MAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Session-Id: unrelated to the Call-ID of SIP
  - id: link
    do: links Cx to SIP
    at: lab
    fields:
      - Cx: the last SIP request for the same user that the same node received within 2 seconds
      - Rx: the last message with SDP, or REGISTER, that the P-CSCF received from that address
  # One call, several dialogs
  - {id: dialog-1, rel: dialog 1, from: ue-a, to: as-o, proto: SIP}
  - {id: dialog-2, rel: dialog 2, from: as-o, to: as-t, proto: SIP}
  - {id: dialog-3, rel: dialog 3, from: as-t, to: ue-b, proto: SIP}
  - id: one-call
    do: makes one call by icid-value
    at: lab
    fields:
      - icid-value: the same in all three dialogs
      - Dialogs: 3
  # A transaction per leg, and a dialog from end to end (an example made up to explain)
  - id: tx-inv-ue-p
    msg: INVITE
    from: ue-a
    to: pcscf
    proto: SIP
    note: (first leg)
    fields:
      - Top Via: "UE 1's `branch=z9hG4bK-a`"
      - Call-ID: "`c1`"
      - From and To: "`tag=f1` in From. No tag in To yet"
  - id: tx-inv-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (second leg)
    fields:
      - Top Via: "`branch=z9hG4bK-b`, added by the P-CSCF, over the Via of UE 1"
      - Call-ID: "still `c1`"
  - id: tx-inv-s-as
    msg: INVITE
    from: scscf
    to: as-o
    proto: SIP
    note: (third leg)
    fields:
      - Top Via: "`branch=z9hG4bK-c`, added by the S-CSCF"
      - Call-ID: "still `c1`"
  - id: tx-ok-as-s
    msg: 200 OK
    from: as-o
    to: scscf
    proto: SIP
    fields:
      - Via: "three: `-c`, `-b`, `-a`"
      - To: "`tag=t1`, added by the AS"
      - The transaction it matches: "the top branch `-c`, and INVITE as the method of CSeq"
  - id: tx-ok-s-p
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Via: "`-b` and `-a`: the S-CSCF took its own off"
  - id: tx-ok-p-ue
    msg: 200 OK
    from: pcscf
    to: ue-a
    proto: SIP
    fields:
      - Via: "`-a` alone"
      - Dialog: "Call-ID `c1`, with the tags `f1` and `t1`"
  - id: tx-ack-ue-p
    msg: ACK
    from: ue-a
    to: pcscf
    proto: SIP
    fields:
      - Top Via: "a new `branch=z9hG4bK-d`"
      - Transaction: the ACK to a 2xx is not part of the INVITE transaction
      - Dialog: "the same: Call-ID `c1`, the tags `f1` and `t1`"
  - id: tx-ack-p-s
    msg: ACK
    from: pcscf
    to: scscf
    proto: SIP
  - id: tx-ack-s-as
    msg: ACK
    from: scscf
    to: as-o
    proto: SIP
```

---

# Reading a capture, and the Lab

(where the capture comes from, how to filter it, and what the Lab draws beside it)

---

[layout: summary]

## What this deck covers

To begin: both tools show the same signaling. Why open a capture at all, when the Lab draws the ladder for you?
The answer: the capture is the packets that crossed the bridge. The Lab builds its ladder from the copies the nodes send, and infers which SIP request a Cx or Rx message was for. A capture is what checks an inference, and what shows what the ladder leaves out: RTP, HTTP, and what the HSS sends on its own, such as an RTR.

- Where the capture file comes from, and what is in it.
- How a display filter is put together, and where the filter of each guide is written down.
- How to get a ladder out of Wireshark, and how to hear the audio.
- What the Lab builds, and what it only infers.
- The one message the Lab shows that the capture does not have.

> Book: [Watching the signalling](../../../docs/book/en/observing.md)
> Guides: [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md) (`docs/guide/en/capture.md`) and [Watch the IMS in the Lab](../../../docs/guide/en/lab.md) (`docs/guide/en/lab.md`)
> No part of the IMS is explained here. The other decks and the chapters of the book all read a capture, and most of them use the ladder of the Lab as well.

---

## One network, one file

```ims-scene
id: one-file
nodes: [ue, pcscf, scscf, hss, bridge, capture, pcap]
lead: All nodes are attached to one bridge, so everything between them goes into one file.
steps:
  - id: bridge
    title: All nodes are on one network
    show: [ue-on, pcscf-on, scscf-on, hss-on]
    focus: [bridge]
    text: One Docker network (172.28.0.0/24). The picture draws four of the nodes.
  - id: listen
    title: capture listens on the bridge
    show: [listens]
    focus: [capture, bridge]
    text: The capture container starts first and the nodes after it, so the first message of each node is in the file too.
  - id: file
    title: A new file at each start
    show: [writes]
    focus: [capture, pcap]
    text: The file is named after the time of the start. In the setup of two networks there are three bridges, each with its capture and its file.
```


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
- The setup of two networks adds a second network and the link between it and this one. Each of the three bridges then has a capture container and a file of its own.
- The files stay in `captures/` until they are deleted. A file can be opened while the capture still runs.

> Guide: ["Start the system"](../../../docs/guide/en/capture.md#start-the-system), ["Open the capture file"](../../../docs/guide/en/capture.md#open-the-capture-file), ["Watch the traffic live"](../../../docs/guide/en/capture.md#watch-the-traffic-live) and ["Stop the system"](../../../docs/guide/en/capture.md#stop-the-system)

---

## The display filter

This is the filter of the guide on registration.

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
| CER and CEA | Seven connections to port 3868, from the six nodes that ask a question over Diameter. The IP-SM-GW opens two: to the HSS and to the SC. |
| The applications in them | Cx from the I-CSCF and the S-CSCF, Sh from the two application servers, SGd from the IP-SM-GW to the SC, S6c from the SC to the HSS, Rx from the P-CSCF. |
| DWR and DWA | A side that has heard nothing for about 30 seconds asks whether its peer is alive. |
| DPR and DPA | The node that stops says goodbye before it closes the connection. |

- The CERs of the seven connections are in the file right after the start, before any UE does anything.
- When the S-CSCF stops, it sends a DPR with Disconnect-Cause `REBOOTING`, and the HSS answers with a DPA. A new start brings a new CER and CEA.

> Guide: ["What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start), with the table of all seven connections. RFC 6733. RFC 3539 3.4.1.

---

## The ladder, and the audio

- **Statistics > Flow Graph** with **Limit to display filter** draws a ladder of the messages that the filter left.
- **Telephony > VoIP Calls** lists each SIP dialog as a call. **Flow Sequence** draws the ladder of that dialog, **Play Streams** plays both directions of its audio.
- **Telephony > RTP > RTP Streams** lists the streams themselves, such as those of the MRFP.
- RTP runs on ports that only the SDP announces. Wireshark finds them when the SIP of the call is in the same capture, which it is here. For a stream that shows up as plain UDP, switch on **Analyze > Enabled Protocols > RTP > rtp_udp**, the RTP heuristic.

> Guide: [the call guide, "Open the capture"](../../../docs/guide/en/call.md#open-the-capture) and [the MRF guide, "Open the capture"](../../../docs/guide/en/mrf.md#open-the-capture)

---

## The copies in the file

```ims-scene
id: copies
nodes: [pcscf, scscf, lab]
lead: What the Lab knows is only the copies that the nodes send it.
steps:
  - id: message
    title: A message from node to node
    show: [invite]
    text: The capture holds this message once.
  - id: sent
    title: The sender sends a copy
    show: [copy-sent]
    text: A node with an `hep` section in its settings sends a copy of each SIP and Diameter message it sends or receives to the Lab, over UDP port 9060.
  - id: received
    title: The receiver sends a copy too
    show: [copy-received]
    checkpoint: true
    text: The copies are in the capture as UDP to port 9060 as well. `not udp.port == 9060` hides them.
```


- A node can send a copy of every SIP and Diameter message it sends or receives to the Lab, which listens on UDP port 9060. The copies use HEP version 3.
- A node sends them when its configuration has a `hep` section. The UE simulator sends them with `-hep lab:9060`. Without either, nothing is sent.
- The copies are in the capture too, as UDP packets to port 9060. Wireshark has no HEP dissector of its own, so hide them with `not udp.port == 9060`.
- Those copies are all the Lab knows. It asks no node for its state, and no node waits for an answer from it. The Lab is not part of the IMS and not on the path of any call, so a node starts and runs without it.
- The HTTP API of the Lab and the control API of the UE simulator follow no specification and are this project's own. What the ladder draws is the signaling of the IMS.

> Guide: ["Copies for the Lab (HEP)"](../../../docs/guide/en/capture.md#copies-for-the-lab-hep) and ["These APIs are this project's own"](../../../docs/guide/en/lab.md#these-apis-are-this-projects-own)

---

## What each panel shows

| Panel | What it shows |
| --- | --- |
| UE simulators | One card per UE simulator: its identity, whether it is registered, its call, its short messages and its supplementary services. The buttons and forms drive the UE. |
| Calls | One row per call, with its caller, callee, state, final status code and number of dialogs. |
| Ladder diagram | The SIP and Cx messages of the selected call or registration, one column per node. |
| Registrations | One row per public identity that registered, with its contact, its expiry and its S-CSCF. |
| Subscribers (HSS) | The subscriptions the HSS holds. The panel changes none of them: an operator provisions a subscription from the OAM. |
| Access network (EPC) | One row per UE attached to the EPC stub, with its address, its bearers and the Rx sessions of its calls. |
| Faults | The faults the fault agent holds, and a form that injects one. The panel is off until that agent runs. |

- Select an arrow of a ladder to see the whole message and the clauses of the specifications that describe it.
- **Copy as text** under a ladder writes it out as monospace text, in the form of the ladder diagrams of the guides.

- The Lab is published on the host's loopback address only. It asks for no password, and anyone who reaches it can read what the HSS holds and drive the UEs.

> Guide: ["Open the Lab"](../../../docs/guide/en/lab.md#open-the-lab), ["What each panel shows"](../../../docs/guide/en/lab.md#what-each-panel-shows), ["Copy the ladder as text"](../../../docs/guide/en/lab.md#copy-the-ladder-as-text) and ["Drive the UE simulators"](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## How the ladder is built: two copies, one arrow

```ims-scene
id: one-arrow
nodes: [pcscf, scscf, lab]
lead: One arrow is one message on the network.
steps:
  - id: two
    title: Two copies of one message
    only: [invite, copy-sent, copy-received]
    active: copy-received
    text: They come from both the sender and the receiver.
  - id: join
    title: The Lab makes one arrow of them
    show: [join]
    focus: [lab]
    text: The same source, destination and content make one arrow. A retransmission of the very same content is another arrow.
```


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

```ims-scene
id: infer
nodes: [icscf, scscf, hss, lab]
lead: A Cx message does not say which SIP request it was sent for. The Lab links them by a rule of the procedure.
steps:
  - id: register
    title: The S-CSCF receives a REGISTER
    show: [register]
    text: A SIP request has a Call-ID.
  - id: mar
    title: The S-CSCF sends a MAR
    show: [mar]
    text: The Session-Id of Diameter is unrelated to the Call-ID of SIP.
  - id: link
    title: The Lab links them
    show: [link]
    focus: [lab]
    text: |
      The link is not written in the messages; the Lab infers it.
      Pick the arrow and the details show it under **Link to this ladder**.
```


- A Cx message carries nothing that names the SIP request it was sent for. The Session-Id of Diameter and the Call-ID of SIP are unrelated.
- The Lab links them by the rule of the procedures. A Cx request belongs to the SIP request that the same node received last, at most 2 seconds before, and that names the same user.
- An Rx session links the same way (TS 29.214 A.1, A.4). Its first AAR names the UE only by its address, so it goes with the last message with SDP, or the REGISTER, that the P-CSCF had from that address.
- Select such an arrow. Under **Link to this ladder** its details say that the Lab inferred the link, and that the message itself does not carry it.

> Guide: ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built). The rule is in `internal/lab/causes.go`.

---

## Transactions and dialogs in a capture

```ims-scene
id: transactions
view: ladder
nodes: [ue-a, pcscf, scscf, as-o]
basis: illustration
lead: A transaction lasts one leg; a dialog runs from end to end. The branch of the Via tells the leg, and the Call-ID with the two tags tells the dialog.
steps:
  - id: first
    title: The INVITE of the first leg
    show: [tx-inv-ue-p]
    text: UE 1 sends it with a branch in its own Via. This INVITE and its responses are the transaction of the first leg.
  - id: hops
    title: A new transaction at each hop
    show: [tx-inv-p-s, tx-inv-s-as]
    active: tx-inv-s-as
    detail: [tx-inv-p-s, tx-inv-s-as]
    checkpoint: true
    text: |
      The P-CSCF and the S-CSCF each put a Via of their own, with a new branch, on top. Each leg is a transaction of its own.
      The Call-ID does not change, because a proxy makes no dialog.
  - id: answer
    title: The 200 OK follows the Vias back
    show: [tx-ok-as-s, tx-ok-s-p, tx-ok-p-ue]
    active: tx-ok-p-ue
    detail: [tx-ok-as-s, tx-ok-p-ue]
    checkpoint: true
    text: |
      A node that receives the response finds its transaction by the top branch and the method of CSeq, takes its own Via off and sends it back.
      The tag that the AS put in To settles the dialog between UE 1 and the AS.
  - id: ack
    title: The ACK is another transaction in the same dialog
    show: [tx-ack-ue-p, tx-ack-p-s, tx-ack-s-as]
    active: tx-ack-ue-p
    detail: [tx-ack-ue-p]
    text: The ACK to a 2xx goes with a new branch and is not part of the INVITE transaction. Its Call-ID and tags are the same, so it is a request of the same dialog.
```

- The branches, the Call-ID and the tags of the figure are long values written short, as an example. That a branch starts with `z9hG4bK` is as in real messages (RFC 3261 8.1.1.7). The 100 Trying, the 183, the PRACK and the 180 are left out of the picture.
- The transaction of each leg is in RFC 3261 17, the Via that a proxy adds in 16.6 step 8, the Via it takes off a response in 16.7 step 3, the match of a response to its transaction in 17.1.3, and the dialog ID in 12.
- In a capture, `sip.Call-ID == "<Call-ID>"` keeps one dialog, and `sip.CSeq.method` a method. Beyond the MMTel AS is another dialog, which the AS starts with a new Call-ID.

> Book: [Watching the signalling](../../../docs/book/en/observing.md)

---

## One call, several dialogs

```ims-scene
id: dialogs
nodes: [ue-a, as-o, as-t, ue-b, lab]
lead: The MMTel AS is a routeing B2BUA, so a call between two people is three dialogs. The CSCFs on the way are left out of the picture.
steps:
  - id: first
    title: The dialog of UE 1 and the AS of the caller
    show: [dialog-1]
    text: The AS answers the INVITE that the S-CSCF brought it itself.
  - id: more
    title: The AS sends a new INVITE with a Call-ID of its own
    show: [dialog-2, dialog-3]
    checkpoint: true
    text: The AS of the callee does the same. There are three dialogs.
  - id: icid
    title: The Lab makes one call of them by icid-value
    show: [one-call]
    focus: [lab]
    text: |
      In the network every dialog has the same icid-value, which the P-CSCF of the caller put in.
      The Lab shows the count in the column Dialogs and draws them all in one ladder.
```


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

> Guide: ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built) and ["The network cancels a registration"](../../../docs/guide/en/lab.md#the-network-cancels-a-registration)
