# ims-practice

[日本語](README.ja.md)

ims-practice is a learning implementation of the 3GPP IP Multimedia Subsystem (IMS). You run it on your own machine and follow each call and SMS from the first SIP message to the last, in Wireshark and in a dashboard.

## What you can learn from it

The project is teaching material for engineers who are new to telecom core networks. It aims to show three things side by side:

- Which node sends which message, and in what order, when a phone registers, makes a call, or sends an SMS.
- Where the 3GPP specifications define each of those messages.
- How the code implements what the specification says.

## Status

A UE simulator registers with SIP Digest and calls another UE by its telephone number, through the P-CSCF, the S-CSCFs, the I-CSCF, ENUM and the media relay. The capture shows every SIP, Diameter, DNS and RTP packet. A dashboard at http://localhost:8080 draws the ladder diagram of each call and registration from HEP copies, manages the subscribers in the HSS, and drives two UE simulators. UEs send each other short messages over IP, and the MMTel AS gives the supplementary services, such as identity presentation, call forwarding and barring, which a UE sets up over Ut. VoLTE UEs attach to a stub of the EPC, register with IMS AKA, and call each other in AMR-WB with preconditions, while the P-CSCF has the PCRF authorise the media of each call over Rx.

## Architecture

```text
 UE simulator or softphone
      │ SIP            │ RTP
      ▼                ▼
   P-CSCF ──────────► media relay (IMS-AGW role)
      │    control
      ▼
   I-CSCF ──► S-CSCF ──► application servers (IP-SM-GW, MMTel AS)
      │          │                  │
      │          │                  ▼
      │          │               MRF (MRFC, MRFP)
      └── Cx ────┴──► HSS stub
```

- One container image runs every CSCF role. Configuration selects the role, so a node can be a P-CSCF, an I-CSCF, an S-CSCF, or several at once.
- The HSS and the access network, an EPC with a PCRF, are stubs. They present real interfaces to the IMS, such as Diameter Cx, Sh and Rx, so a full implementation can take the place of either without a change to the IMS.
- The S-CSCF invokes application servers through iFCs: the IP-SM-GW for SMS, and the MMTel AS for supplementary services such as call forwarding and barring.
- At the border with another IMS network, the IBCF and the TrGW hide the topology and relay the media.
- Signaling is not encrypted by default, so Wireshark decodes every message without extra setup.

## Requirements

- Docker with Docker Compose. Docker Desktop on Windows is the reference environment.
- Wireshark, to read the captures.

## Documentation

- [Capture signaling and open it in Wireshark](docs/guide/en/capture.md).
- [Follow a UE registering with the IMS](docs/guide/en/registration.md).
- [Follow a voice call through the IMS](docs/guide/en/call.md).
- [Follow a short message through the IMS](docs/guide/en/sms.md).
- [Supplementary services with the MMTel AS](docs/guide/en/mmtel.md).
- [Announcements, alerting tones and conferences with the MRF](docs/guide/en/mrf.md): an announcement before a barred call, the customized alerting tone, call waiting and a conference of three.
- [The VoLTE call flow](docs/guide/en/volte.md): the EPC stub, IMS AKA, policy control over Rx, and a call in AMR-WB with preconditions.
- [Connect two IMS networks](docs/guide/en/interconnect.md): the IBCF and the TrGW at the border, topology hiding, the interconnect profiles and what happens when they do not match, and a short message across.
- [Watch the IMS in the dashboard](docs/guide/en/dashboard.md), and drive two UE simulators from it.
- [Glossary](docs/glossary.md). Abbreviations used in IMS, in English and Japanese.
- [Contribution rules](CONTRIBUTING.md), in Japanese, with the list of the development records.

## License

The code, configuration and scripts are licensed under the [Apache License 2.0](LICENSE). The guides, the glossary and the slide decks are licensed under [CC BY 4.0](LICENSE-docs). [NOTICE](NOTICE) lists the third-party components and data files.

These licenses cover the copyright in this repository. They do not grant any licence to patents of others, such as patents essential to the 3GPP specifications or to the AMR, AMR-WB and EVS codecs. If you build a product on this code, check which patent licences it needs.

You do not have to tell anyone that you use this project. If you use it in a product or a commercial service, we would be glad to hear about it in an Issue.
