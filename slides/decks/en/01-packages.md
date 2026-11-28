---
title: The package layout of this repository
footer: ims-practice 1. Package layout
lang: en
format: visual
---

```ims-model
nodes:
  - {id: cmd-ims, label: cmd/ims, role: one node of the IMS, at: [2.5, 0]}
  - {id: config, label: config, role: reads the settings, at: [1, 0]}
  - {id: pcscf, label: pcscf, role: the role P-CSCF, at: [1, 1]}
  - {id: icscf, label: icscf, role: the role I-CSCF, at: [2, 1]}
  - {id: scscf, label: scscf, role: the role S-CSCF, at: [3, 1]}
  - {id: hss, label: hss, role: the role HSS, at: [4, 1]}
  - {id: cx, label: cx, role: the messages of Cx, at: [3.5, 2]}
  - {id: sip, label: sip, role: SIP（RFC 3261）, at: [1.5, 3]}
  - {id: diameter, label: diameter, role: Diameter（RFC 6733）, at: [3.5, 3]}
  - {id: store, label: scscf.Store, role: holds the state, at: [3, 2]}
  - {id: memory, label: MemoryStore, role: in the process, at: [2.5, 3]}
  - {id: shared, label: scscfstore, role: on a shared store, at: [3.5, 3]}
  - {id: valkey, label: Valkey, role: a shared store, at: [4.5, 3]}
  - {id: relay, label: pcscf.Relay, role: controls the media node, at: [1, 2]}
  - {id: agw-client, label: agw.Client, role: the HTTP API, at: [0.5, 3]}
  - {id: mediactl, label: h248/mediactl, role: H.248（Iq）, at: [1.5, 3]}
  - {id: ipsmgw, label: ipsmgw, role: the role IP-SM-GW, at: [3, 1]}
  - {id: centre, label: ServiceCentre, role: the SC it calls, at: [3, 2]}
  - {id: sc-stub, label: sc, role: a stub in the process, at: [2.5, 3]}
  - {id: iwmsc, label: SMSIWMSC, role: to the sc node over SGd, at: [3.5, 3]}
  - {id: ue, label: ue, role: the UE simulator, at: [0, 1]}
events:
  # One executable becomes any node
  - {id: reads-roles, rel: reads roles, from: cmd-ims, to: config}
  - {id: starts-scscf, rel: starts, from: cmd-ims, to: scscf}
  - {id: starts-icscf, rel: starts, from: cmd-ims, to: icscf}
  # The layers and the direction of import
  - {id: pcscf-sip, rel: import, from: pcscf, to: sip}
  - {id: icscf-sip, rel: import, from: icscf, to: sip}
  - {id: scscf-sip, rel: import, from: scscf, to: sip}
  - {id: icscf-cx, rel: import, from: icscf, to: cx}
  - {id: scscf-cx, rel: import, from: scscf, to: cx}
  - {id: hss-cx, rel: import, from: hss, to: cx}
  - {id: cx-diameter, rel: import, from: cx, to: diameter}
  # Where the state lives
  - {id: uses-store, rel: reads and writes, from: scscf, to: store}
  - {id: memory-is-store, rel: implements, from: memory, to: store}
  - {id: shared-is-store, rel: implements, from: shared, to: store}
  - {id: shared-valkey, rel: stores in, from: shared, to: valkey}
  # The boundaries that can be replaced
  - {id: uses-relay, rel: uses, from: pcscf, to: relay}
  - {id: client-is-relay, rel: implements, from: agw-client, to: relay}
  - {id: mediactl-is-relay, rel: implements, from: mediactl, to: relay}
  - {id: uses-centre, rel: uses, from: ipsmgw, to: centre}
  - {id: stub-is-centre, rel: implements, from: sc-stub, to: centre}
  - {id: iwmsc-is-centre, rel: implements, from: iwmsc, to: centre}
  # One REGISTER
  - {id: reg-ue-p, msg: REGISTER, from: ue, to: pcscf, proto: SIP}
  - {id: reg-p-i, msg: REGISTER, from: pcscf, to: icscf, proto: SIP}
  - {id: uar, msg: UAR, from: icscf, to: hss, proto: Cx}
  - {id: uaa, msg: UAA, from: hss, to: icscf, proto: Cx}
  - {id: reg-i-s, msg: REGISTER, from: icscf, to: scscf, proto: SIP}
  - {id: mar, msg: MAR, from: scscf, to: hss, proto: Cx}
  - {id: maa, msg: MAA, from: hss, to: scscf, proto: Cx}
```

---

# The package layout of this repository

(where each node and each specification lives in the code)

---

[layout: summary]

## What this deck covers

- There are only three executables, and one of them is every IMS node.
- Which package holds which node.
- Which package holds which specification.
- Where to start reading the code.

> The signaling is read in the decks on registration and calls and in the chapters of the book. This deck shows where the files to open then are.
> Book: [What IMS does](../../../docs/book/en/overview.md)
> The overview is in [README.md](../../../README.md), the abbreviations in the [glossary](../../../docs/glossary.md).

---

## The top of the repository

| Place | What is there |
| --- | --- |
| `cmd/` | The entry points of the executables: `ims`, `ue` and `oss`. |
| `internal/` | All of the implementation. The subject of this deck. |
| `deploy/compose/` | One configuration file per node, and the DNS zones. |
| `compose.yaml` | Puts the nodes on one Docker network. |
| `docs/` | The guides (`docs/guide/`) and the glossary. |
| `scripts/` | The e2e tests, one per scenario, and live capture. |
| `web/` | The web UIs of the Lab (`lab/`) and of the OAM server (`oam/`). |
| `images/` | Helper images, such as the capture container. |

---

## Three executables

```ims-scene
id: one-binary
nodes: [cmd-ims, config, pcscf, icscf, scscf, hss]
lead: Which node it becomes is decided by roles in the settings file, not by the code.
steps:
  - id: read
    title: cmd/ims reads roles
    show: [reads-roles]
    focus: [cmd-ims, config]
    text: Package `config` reads the settings file and refuses wrong settings at the start.
  - id: one
    title: With roles [scscf] it becomes an S-CSCF
    show: [starts-scscf]
    active: starts-scscf
    checkpoint: true
    text: The same image becomes another node with only another settings file.
  - id: two
    title: Two roles make one node do both
    show: [starts-icscf]
    focus: [cmd-ims, icscf, scscf]
    text: 'With `roles: [icscf, scscf]` one process runs an I-CSCF and an S-CSCF. The code does not change.'
```


- `cmd/ims` is one IMS node. Which node it is comes from `roles` in its configuration file, so one image serves as every node.
- `cmd/ue` is the UE simulator, with the subcommands `register`, `call`, `answer`, `serve`, `sms`, `xcap` and `ctl`.
- `cmd/oss` stands in for the OSS, which reads and acknowledges the alarms of the OAM server. It is not an IMS node.

```yaml
# deploy/compose/scscf.yaml
node: scscf
roles: [scscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
```

- The roles of the core: `hss`, `icscf`, `scscf`, `pcscf`, `agw`, `lab`, `ipsmgw` and `sc`. For VoLTE and the services: `epc`, `mmtel`, `mrfc`, `mrfp`. At the border to another network: `ibcf`. To operate the nodes and to break them on purpose: `oam` and `fault`.
- Two entries in `roles` make one node play both roles.

> The list of roles is `knownRoles` in `internal/config/config.go`.

---

## The nodes of the Compose setup and their roles

| Node | Address | Role | Configuration |
| --- | --- | --- | --- |
| `dns` | 172.28.0.2 | CoreDNS | `deploy/compose/dns/` |
| `hss` | 172.28.0.10 | `hss` | `hss.yaml` |
| `icscf` | 172.28.0.20 | `icscf` | `icscf.yaml` |
| `scscf` | 172.28.0.30 | `scscf` | `scscf.yaml` |
| `pcscf` | 172.28.0.40 | `pcscf` | `pcscf.yaml` |
| `agw` | 172.28.0.50 | `agw` | `agw.yaml` |
| `lab`, `oam` | 172.28.0.60, 65 | `lab`, `oam` | `lab.yaml`, `oam.yaml` |
| `ipsmgw`, `sc`, `mmtel` | 172.28.0.70, 71, 75 | `ipsmgw`, `sc`, `mmtel` | `ipsmgw.yaml`, `sc.yaml`, `mmtel.yaml` |
| `epc`, `mrfc`, `mrfp` | 172.28.0.80, 85, 86 | `epc`, `mrfc`, `mrfp` | `epc.yaml`, `mrfc.yaml`, `mrfp.yaml` |
| `ue`, `ue2`, `ue3` | 172.28.0.100, 101, 103 | `cmd/ue` | command line |

- Not in the table: the UEs of the Lab and of VoLTE (`phone1` to `phone3`, `volte10`, `volte12`), `softphone`, `oss` and `capture`. The fault agent has a Compose file of its own in `deploy/chaos/`.
- The IBCF and a second network are in another Compose project, `deploy/interconnect/compose.yaml`.

> Guide: [Capture signaling and open it in Wireshark, "What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start)

---

## internal/ in layers

```ims-scene
id: layers
nodes: [pcscf, icscf, scscf, hss, cx, sip, diameter]
lead: An arrow points the way of an import. A role uses the layers below it, and a layer below knows nothing of those above.
steps:
  - id: sip
    title: The SIP roles use sip
    show: [pcscf-sip, icscf-sip, scscf-sip]
    active: ''
    focus: [sip]
    text: RFC 3261 is in package `sip`, TS 24.229 in the role packages. The layers follow the documents of the specifications.
  - id: cx
    title: The roles of Cx use diameter through cx
    only: [icscf-cx, scscf-cx, hss-cx, cx-diameter]
    active: ''
    focus: [cx, diameter]
    checkpoint: true
    text: '`cx` lays out the messages of Cx with their AVPs in the order of the specification; `diameter` is the base protocol.'
  - id: down
    title: A layer below knows nothing of those above
    only: []
    active: ''
    focus: [sip, diameter]
    text: |
      `sip` imports no package of `internal/`. It is SIP that knows nothing of the IMS.
      `pcscf` uses `sip`, never the other way round.
```


| Layer | Packages |
| --- | --- |
| Roles: how a node behaves | `pcscf` `icscf` `scscf` `hss` `ipsmgw` `sc` `smsc` `agw` `lab` `ue` `epc` `mmtel` `mrfc` `ibcf` `oam` |
| IMS rules and interfaces | `ims` `ifc` `reginfo` `cx` `sh` `rx` `sgd` `policy` `simservs` `interconnect` `netann` `confinfo` `b2bua` |
| Protocols | `sip` `sipdns` `diameter` `sdp` `rtp` `rtcp` `sms` `hep` `xcap` `h248` `ipsec` `accessapi` |
| Codecs and algorithms | `g711` `amr` `evs` `wav` `mixer` `milenage` `aka` `precondition` |
| Start-up, state and operation | `config` `hepagent` `metrics` `kvstore` `maintenance` `fault` `mns` `jex` |
| Not imported by any command of `cmd/` | `fivegc` `n5` `nhss` `sbi` `nas` `rtptest` `openapitest` `yamledit` |

- The role packages combine the layers below them. The role `mrfp` is the package `agw`, with `mixer` for the announcements and conferences, and the role `sc` is the package `smsc`.
- The layers follow the documents: RFC 3261 is the package `sip`, TS 24.229 the role packages.
- The protocol layer knows no role. `sip` is SIP that knows nothing about the IMS.

---

## The role packages: CSCFs and HSS

| Package | Node | Main specifications |
| --- | --- | --- |
| `pcscf` | P-CSCF, the first SIP hop of the UE. | TS 24.229 |
| `icscf` | I-CSCF, the entry point of the home network. | TS 24.229, TS 29.228 |
| `scscf` | S-CSCF, the registrar. It also evaluates the iFCs. | TS 24.229, TS 29.228 |
| `hss` | The HSS stub. It answers Cx, Sh and S6c. | TS 29.228, TS 29.328, TS 29.338 |

- Inside a role, there is one file per procedure.
  - `scscf.go` is registration, `session.go` is calls, `services.go` is the iFCs, `regevent.go` is the reg event.

> Guide: [Follow a UE registering with the IMS, "The nodes"](../../../docs/guide/en/registration.md#the-nodes)

---

## The role packages: AS, media, UE, observation

| Package | Node | Main specifications |
| --- | --- | --- |
| `ipsmgw` | IP-SM-GW, the application server of SMS over IP. | TS 24.341, TS 23.204 |
| `smsc` | The Service Centre of SMS, the role `sc`. It holds each short message and retries. | TS 23.040, TS 29.338 |
| `sc` | The SC stub, for an IP-SM-GW whose configuration names no Service Centre. | TS 23.204 |
| `mmtel` | The MMTel AS: supplementary services. | TS 24.173, TS 24.604 to 24.611, TS 24.623 |
| `agw` | The media relay, in the role of the IMS-AGW. | RFC 3550. The control API is this project's own |
| `ue` | The UE simulator. | TS 24.229, TS 24.341 |
| `lab` | Builds registrations and calls from HEP copies. | this project's own |
| `oam` | The OAM server: operates the nodes through their maintenance API. | TS 28.111 towards the OSS |

- A stub keeps the interface of the specification real and makes the inside simple. The HSS is a stub, and so is `sc`.
- The Lab is not part of the IMS. It only shows what the nodes send it.

> Guide: [Watch the IMS in the Lab, "These APIs are this project's own"](../../../docs/guide/en/lab.md#these-apis-are-this-projects-own)

---

## The protocol packages

| Package | What it holds | Specification |
| --- | --- | --- |
| `sip` | Messages, transport, transactions, dialogs, the UA | RFC 3261 |
| `sipdns` | Finds SIP servers with NAPTR, SRV and A | RFC 3263, RFC 6116 |
| `diameter` | The base protocol, the peer state, the dictionary | RFC 6733 |
| `cx`, `sh` | The Cx and Sh messages | TS 29.228, 29.229, 29.328, 29.329 |
| `sdp` | SDP, offer and answer | RFC 3264 |
| `rtp` | RTP packets | RFC 3550 |
| `sms` | The RP layer, the TP layer, the text encodings | TS 24.011, 23.040, 23.038 |
| `reginfo`, `ifc` | The XML of the reg event, the initial filter criteria | RFC 3680, TS 29.228 |

- The identifiers are the terms of the specifications, such as `cx.ServerAssignmentRequest`. In `cx` and `sh` each AVP is a field of a Go struct, in the order of the specification.
- The shape of a message and the decision to send it are in different packages: `cx` lists the AVPs of the UAR in the order of the specification, and `icscf` decides to send the UAR.

---

## The other packages

| Package | What it holds |
| --- | --- |
| `ims` | The IMS rules that more than one role applies. |
| `config` | Turns the YAML configuration into validated values, and refuses a wrong one at start-up. |
| `hep`, `hepagent` | Send copies of the signaling to the Lab (HEP3). |
| `g711`, `wav` | G.711 μ-law and WAV: the audio of the UE simulator. |
| `epc`, `policy`, `rx`, `milenage`, `aka`, `amr`, `rtcp`, `precondition` | Parts of the VoLTE call. Not covered in this deck. |
| `nas`, `fivegc`, `n5`, `nhss`, `sbi` and more | Parts that no role starts. Only their own tests use them. |

- No command of `cmd/` imports the parts that no role starts: `go list -deps ./cmd/...` does not list them.

> Guide: [Capture signaling and open it in Wireshark, "Copies for the Lab (HEP)"](../../../docs/guide/en/capture.md#copies-for-the-lab-hep)

---

## State lives in a Store

```ims-scene
id: state
nodes: [scscf, store, memory, shared, valkey]
lead: The state of the users can live outside the node. What a role reads and writes is only the interface Store.
steps:
  - id: uses
    title: The S-CSCF reads and writes the registrations through Store
    show: [uses-store]
    active: ''
    focus: [scscf, store]
    text: It puts the registrations, the state of an authentication in progress and where an iFC evaluation is in Store.
  - id: memory
    title: MemoryStore in the Compose setup
    show: [memory-is-store]
    active: ''
    focus: [memory, store]
    checkpoint: true
    text: The state is in the process.
  - id: shared
    title: A shared store in the redundant setup
    show: [shared-is-store, shared-valkey]
    active: ''
    focus: [shared, valkey]
    text: |
      In the setup of `deploy/redundancy/`, `scscfstore` makes the same Store on top of Valkey.
      The code of the S-CSCF does not change.
```


- A role reads and writes the state of its users through an interface called `Store`, so that the state can live outside the node.
- The implementation in the Compose setup is `MemoryStore`, which keeps the state in the process.

| File | What it keeps |
| --- | --- |
| `internal/scscf/store.go` | Registrations, pending challenges, where an iFC evaluation goes on |
| `internal/pcscf/pcscf.go` | The IP association of each UE |
| `internal/hss/store.go` | Subscriptions |
| `internal/ipsmgw/store.go` | Short messages on their way, subscriptions |
| `internal/smsc/store.go` | The short messages the Service Centre holds, by recipient |

- With a shared store behind `Store`, a role can run on several nodes.
- Why not a plain map in the S-CSCF? A second S-CSCF could not see the registrations. This is where an IMS differs from a PBX.

> Guide: [Follow a short message through the IMS, "12. The delivery report reaches the IP-SM-GW through UE 2's criteria"](../../../docs/guide/en/sms.md#12-the-delivery-report-reaches-the-ip-sm-gw-through-ue-2s-criteria)

---

## The boundaries that can be replaced

```ims-scene
id: boundaries
nodes: [pcscf, relay, agw-client, mediactl, ipsmgw, centre, sc-stub, iwmsc]
lead: Only where one part has two ways of working can it be replaced. The settings choose either way.
steps:
  - id: relay
    title: The P-CSCF drives the media node through Relay
    show: [uses-relay, client-is-relay, mediactl-is-relay]
    active: ''
    focus: [pcscf, relay]
    text: There are two implementations. `agw.Client` talks to the HTTP API of the media node, `h248/mediactl` talks H.248 over Iq.
  - id: centre
    title: The IP-SM-GW calls the SC through ServiceCentre
    show: [uses-centre, stub-is-centre, iwmsc-is-centre]
    active: ''
    focus: [ipsmgw, centre]
    text: There are two implementations. The stub `sc` in the process, and `SMSIWMSC`, which reaches the service centre of the `sc` node over SGd.
```


Only the places where a stub stands for a real node, or where one part has two ways of doing its work, are interfaces.

| Boundary | Its shape in the code | What could replace it |
| --- | --- | --- |
| HSS | Cx, Sh and S6c, the Diameter messages themselves | A full HSS |
| Control of the media relay | `Relay` in `pcscf` | Control by H.248 on Iq (`h248/mediactl`), which the configuration chooses |
| SC | `ServiceCentre` in `ipsmgw` | The Service Centre of the `sc` node over SGd (`sgd`) in place of the stub, which the configuration chooses |
| Combination of roles | `roles` in the configuration | Several roles in one node |

- Nowhere else is there an abstraction for the sake of replacing something. An abstraction with a single use only adds a layer to read through.
- The HSS stub speaks the real Cx, Sh and S6c, so that a capture shows what a real HSS would send.

---

## Three handles for reading the code

1. **The package comment.** It says first which node the package is and which specifications it follows.
2. **Clause numbers.** The comment at each step of a procedure gives the clause of the specification, so that you can open it next to the code.
3. **`SIMPLIFIED:`** marks a place that simplifies the specification, and says what was left out and why.

```sh
grep -rn "SIMPLIFIED:" internal cmd
```

- `internal/scscf/scscf.go` has all three. The package comment is at the top; the clause numbers and `SIMPLIFIED:` are in the comments of the functions.
- The number of `SIMPLIFIED:` comments changes whenever the code changes, so this deck does not give it. The `grep` above lists them as they are now.

> Guide: [Follow a UE registering with the IMS, "What this project simplifies"](../../../docs/guide/en/registration.md#what-this-project-simplifies)

---

## Follow one REGISTER through the code

```ims-scene
id: trace
view: ladder
nodes: [ue, pcscf, icscf, scscf, hss]
lead: Each arrow of a capture is sent by some code.
steps:
  - id: ue
    title: The UE builds the REGISTER
    show: [reg-ue-p]
    text: "`internal/ue/ue.go`"
  - id: pcscf
    title: The P-CSCF adds Path and finds the I-CSCF in DNS
    show: [reg-p-i]
    text: "`internal/pcscf/pcscf.go`, `internal/sipdns/locate.go`"
  - id: icscf
    title: The I-CSCF sends the UAR
    show: [uar, uaa, reg-i-s]
    active: uar
    checkpoint: true
    text: "`internal/icscf/icscf.go`. The shape of the message is in `internal/cx/messages.go`."
  - id: scscf
    title: The S-CSCF sends MAR and SAR, and returns 401 and 200
    show: [mar, maa]
    active: mar
    text: "`internal/scscf/scscf.go`. The SAR and the 200 OK come after the second REGISTER."
  - id: hss
    title: The HSS answers UAR, MAR and SAR
    focus: [hss]
    active: ''
    text: "`internal/hss/hss.go`"
```


| Step | Node | File | What is there |
| --- | --- | --- | --- |
| 1 | UE | `internal/ue/ue.go` | Where the REGISTER is built (`newREGISTER`) |
| 2 | P-CSCF | `internal/pcscf/pcscf.go`, `internal/sipdns/locate.go` | Where Path is added, where DNS gives the I-CSCF (`register`, `pathURI`, `Locate`) |
| 3 | I-CSCF | `internal/icscf/icscf.go` | Where the UAR is sent (`register`) |
| 4 | S-CSCF | `internal/scscf/scscf.go` | Where MAR and SAR are sent, where 401 and 200 are returned (`challenge` sends the MAR and the 401, `accept` the SAR and the 200) |
| 5 | HSS | `internal/hss/hss.go` | Where UAR, MAR and SAR are answered (`userAuthorization`, `multimediaAuth`, `serverAssignment`) |

- The shapes of the messages are in `internal/cx/messages.go`.
- The comment next to each of these functions gives the clause of its step.

> The deck on registration reads the same flow in a capture. Its guide is [Follow a UE registering with the IMS](../../../docs/guide/en/registration.md).

---

## Tests that run the whole system

- The tests of `internal/ue` run the whole IMS inside one process and check registration and calls.
- The tests of `internal/ipsmgw` do the same for SMS.
- The `scripts/e2e-*.sh` scripts, one per scenario, run the Compose setup and check that the capture holds the expected messages.
- To run them yourself, run this command at the root of the repository. Reading needs none of it.

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -v ims-practice-gomod:/go/pkg/mod \
  -w /src golang:1.27.4-alpine go test ./...
```

---

## Where to read more

| Document | Place |
| --- | --- |
| What the project is, and how to run it | [README.md](../../../README.md) |
| Glossary | [docs/glossary.md](../../../docs/glossary.md) |
| Guides | [docs/guide/en/](../../../docs/guide/en/) |
