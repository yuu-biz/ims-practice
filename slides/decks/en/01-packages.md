---
title: The package layout of this repository
footer: ims-practice study session 1. Package layout
lang: en
---

# The package layout of this repository

ims-practice study session 1 (where each node and each specification lives in the code)

---

[layout: summary]

## What you will know after this session

- There are only two executables.
- Which package holds which node.
- Which package holds which specification.
- Where to start reading the code.

> The signaling comes in the next sessions. This one makes sure you can find the file to open then.
> The overview is in [README.md](../../../README.md), the abbreviations in the [glossary](../../../docs/glossary.md).

---

## The top of the repository

| Place | What is there |
| --- | --- |
| `cmd/` | The entry points of the executables: `ims` and `ue`. |
| `internal/` | All of the implementation. The subject of this session. |
| `deploy/compose/` | One configuration file per node, and the DNS zones. |
| `compose.yaml` | Puts the nodes on one Docker network. |
| `docs/` | The guides (`docs/guide/`) and the glossary. |
| `scripts/` | The e2e tests, one per scenario, and live capture. |
| `web/lab/` | The web UI of the Lab. |
| `images/` | Helper images, such as the capture container. |

---

## Two executables

- `cmd/ims` is one IMS node. Which node it is comes from `roles` in its configuration file, so one image serves as every node.
- `cmd/ue` is the UE simulator, with the subcommands `register`, `call`, `answer`, `serve`, `sms`, `xcap` and `ctl`.

```yaml
# deploy/compose/scscf.yaml
node: scscf
roles: [scscf]
home_domain: ims.mnc001.mcc001.3gppnetwork.org
```

- The roles of the core: `hss`, `icscf`, `scscf`, `pcscf`, `agw`, `lab` and `ipsmgw`. For VoLTE and the services: `epc`, `mmtel`, `mrfc`, `mrfp`. At the border to another network: `ibcf`.
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
| `lab` | 172.28.0.60 | `lab` | `lab.yaml` |
| `ipsmgw`, `mmtel` | 172.28.0.70, 75 | `ipsmgw`, `mmtel` | `ipsmgw.yaml`, `mmtel.yaml` |
| `epc`, `mrfc`, `mrfp` | 172.28.0.80, 85, 86 | `epc`, `mrfc`, `mrfp` | `epc.yaml`, `mrfc.yaml`, `mrfp.yaml` |
| `ue`, `ue2`, `ue3` | 172.28.0.100, 101, 103 | `cmd/ue` | command line |

- Not in the table: the SC stub inside `ipsmgw`, the UEs of the Lab and of VoLTE (`phone1` to `phone3`, `volte-a`, `volte-b`), `softphone` and `capture`.
- The IBCF and a second network are in another Compose project, `deploy/interconnect/compose.yaml`.

> Guide: [Capture signaling and open it in Wireshark, "What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start)

---

## internal/ in layers

| Layer | Packages |
| --- | --- |
| Roles: how a node behaves | `pcscf` `icscf` `scscf` `hss` `ipsmgw` `sc` `agw` `lab` `ue` `epc` `mmtel` `mrfc` `ibcf` |
| IMS rules and interfaces | `ims` `ifc` `reginfo` `cx` `sh` `rx` `policy` `simservs` `interconnect` `netann` `confinfo` |
| Protocols | `sip` `sipdns` `diameter` `sdp` `rtp` `rtcp` `sms` `hep` `xcap` |
| Codecs and algorithms | `g711` `amr` `evs` `wav` `mixer` `milenage` `aka` `precondition` |
| Start-up and observation | `config` `hepagent` `metrics` |
| Not imported by `cmd/ims` or `cmd/ue` | `kvstore` `maintenance` `oam` `fault` `fivegc` `n5` `nhss` `sbi` `h248` `nas` `sgd` `smsc` `rtptest` |

- The role packages combine the layers below them. The role `mrfp` is the package `agw`, with `mixer` for the announcements and conferences.
- The layers follow the documents: RFC 3261 is the package `sip`, TS 24.229 the role packages.
- The protocol layer knows no role. `sip` is SIP that knows nothing about the IMS.

---

## The role packages: CSCFs and HSS

| Package | Node | Main specifications |
| --- | --- | --- |
| `pcscf` | P-CSCF, the first SIP hop of the UE. | TS 24.229 |
| `icscf` | I-CSCF, the entry point of the home network. | TS 24.229, TS 29.228 |
| `scscf` | S-CSCF, the registrar. It also evaluates the iFCs. | TS 24.229, TS 29.228 |
| `hss` | The HSS stub. It answers Cx and Sh. | TS 29.228, TS 29.328 |

- Inside a role, there is one file per procedure.
  - `scscf.go` is registration, `session.go` is calls, `services.go` is the iFCs, `regevent.go` is the reg event.

> Guide: [Follow a UE registering with the IMS, "The nodes"](../../../docs/guide/en/registration.md#the-nodes)

---

## The role packages: AS, media, UE, observation

| Package | Node | Main specifications |
| --- | --- | --- |
| `ipsmgw` | IP-SM-GW, the application server of SMS over IP. | TS 24.341, TS 23.204 |
| `sc` | The SC stub, in the process of the IP-SM-GW. | TS 23.204 |
| `mmtel` | The MMTel AS: supplementary services. | TS 24.173, TS 24.604 to 24.611, TS 24.623 |
| `agw` | The media relay, in the role of the IMS-AGW. | RFC 3550. The control API is this project's own |
| `ue` | The UE simulator. | TS 24.229, TS 24.341 |
| `lab` | Builds registrations and calls from HEP copies. | this project's own |

- A stub keeps the interface of the specification real and makes the inside simple. The HSS and the SC are stubs.
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

---

## The other packages

| Package | What it holds |
| --- | --- |
| `ims` | The IMS rules that more than one role applies. |
| `config` | Turns the YAML configuration into validated values, and refuses a wrong one at start-up. |
| `hep`, `hepagent` | Send copies of the signaling to the Lab (HEP3). |
| `g711`, `wav` | G.711 μ-law and WAV: the audio of the UE simulator. |
| `epc`, `policy`, `rx`, `milenage`, `aka`, `amr`, `rtcp`, `precondition` | Parts of the VoLTE call. Not covered in this session. |
| `h248`, `sgd`, `smsc`, `nas`, `fivegc` and more | Parts that no role starts. Only their own tests use them. |

- Neither `cmd/ims` nor `cmd/ue` imports the parts that no role starts: `go list -deps ./cmd/...` does not list them.

> Guide: [Capture signaling and open it in Wireshark, "Copies for the Lab (HEP)"](../../../docs/guide/en/capture.md#copies-for-the-lab-hep)

---

## State lives in a Store

- A role reads and writes the state of its users through an interface called `Store`, so that the state can live outside the node.
- The implementation in the Compose setup is `MemoryStore`, which keeps the state in the process.

| File | What it keeps |
| --- | --- |
| `internal/scscf/store.go` | Registrations, pending challenges, where an iFC evaluation goes on |
| `internal/pcscf/pcscf.go` | The IP association of each UE |
| `internal/hss/store.go` | Subscriptions |
| `internal/ipsmgw/store.go` | Short messages on their way, subscriptions |

- With a shared store behind `Store`, a role can run on several nodes.
- Why not a plain map in the S-CSCF? A second S-CSCF could not see the registrations. This is where an IMS differs from a PBX.

> Guide: [Follow a short message through the IMS, "12. The delivery report reaches the IP-SM-GW through UE 2's criteria"](../../../docs/guide/en/sms.md#12-the-delivery-report-reaches-the-ip-sm-gw-through-ue-2s-criteria)

---

## The boundaries that can be replaced

Only the places where a stub stands for a real node, or where one part has two ways of doing its work, are interfaces.

| Boundary | Its shape in the code | What could replace it |
| --- | --- | --- |
| HSS | Cx and Sh, the Diameter messages themselves | A full HSS |
| Control of the media relay | `Relay` in `pcscf` | Control by H.248 (`h248` has the messages; no role uses it) |
| SC | `ServiceCentre` in `ipsmgw` | A client of SGd (`sgd` has the messages; no role uses it) |
| Combination of roles | `roles` in the configuration | Several roles in one node |

- Nowhere else is there an abstraction for the sake of replacing something. An abstraction with a single use only adds a layer to read through.
- The HSS stub speaks the real Cx and Sh, so that a capture shows what a real HSS would send.

---

## Three handles for reading the code

1. **The package comment.** It says first which node the package is and which specifications it follows.
2. **Clause numbers.** The comment at each step of a procedure gives the clause of the specification, so that you can open it next to the code.
3. **`SIMPLIFIED:`** marks a place that simplifies the specification, and says what was left out and why.

```sh
grep -rn "SIMPLIFIED:" internal cmd
```

- The top of `internal/scscf/scscf.go` has all three.

> Guide: [Follow a UE registering with the IMS, "What this project simplifies"](../../../docs/guide/en/registration.md#what-this-project-simplifies)

---

## Follow one REGISTER through the code

| Step | Node | File to open | What to find |
| --- | --- | --- | --- |
| 1 | UE | `internal/ue/ue.go` | Where the REGISTER is built |
| 2 | P-CSCF | `internal/pcscf/pcscf.go`, `internal/sipdns/locate.go` | Where Path is added, where DNS gives the I-CSCF |
| 3 | I-CSCF | `internal/icscf/icscf.go` | Where the UAR is sent |
| 4 | S-CSCF | `internal/scscf/scscf.go` | Where MAR and SAR are sent, where 401 and 200 are returned |
| 5 | HSS | `internal/hss/hss.go` | Where UAR, MAR and SAR are answered |

- The shapes of the messages are in `internal/cx/messages.go`.
- In pairs: find what the last column names, then read the clause number in the comment next to it.

> The next session reads the same flow in a capture. Its guide is [Follow a UE registering with the IMS](../../../docs/guide/en/registration.md).

---

## Tests that run the whole system

- The tests of `internal/ue` run the whole IMS inside one process and check registration and calls.
- The tests of `internal/ipsmgw` do the same for SMS.
- The `scripts/e2e-*.sh` scripts, one per scenario, run the Compose setup and check that the capture holds the expected messages.

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -v ims-practice-gomod:/go/pkg/mod \
  -w /src golang:1.27.4-alpine go test ./...
```

---

[layout: exercise]

## Looking back

1. What do you change to run the S-CSCF and the I-CSCF in one node?
2. Which package lists the AVPs of the UAR? Which package sends the UAR?
3. How many `SIMPLIFIED:` comments are there today?
4. Which direction is right: the P-CSCF code uses `sip`, or `sip` uses `pcscf`?

---

[layout: answers]

## Looking back: answers

1. List both in `roles` in the configuration file. The code does not change.
2. `cx` lists them; `icscf` sends the UAR.
3. Count them with the `grep` of "Three handles for reading the code". The number changes as the code changes.
4. `pcscf` uses `sip`, never the other way round.

---

## Next session, and where to read more

- Next is registration: start the system, run one REGISTER, and read the capture.
- Before then, try [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md) once.

| Document | Place |
| --- | --- |
| What the project is, and how to run it | [README.md](../../../README.md) |
| Glossary | [docs/glossary.md](../../../docs/glossary.md) |
| Guides | [docs/guide/en/](../../../docs/guide/en/) |
