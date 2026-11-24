# Capture signaling and open it in Wireshark

[日本語](../ja/capture.md)

Every node in this project talks over one Docker network. A capture container records all traffic on that network, so one file holds the whole conversation between the nodes.

## Before you start

- Docker with Docker Compose is running.
- Wireshark is installed on your machine.

## Start the system

Run this command at the root of the repository:

```sh
docker compose up -d --build
```

The capture container starts first. The nodes start after it, so the capture includes their first messages.

## Open the capture file

Each start of the capture container writes a new file to `captures/`. The name is the start time in UTC, for example `captures/ims-20261119T042120Z.pcap`.

1. Open the newest file in Wireshark with **File > Open**.
2. Type `diameter` in the display filter bar and press Enter.
3. To see the messages as a ladder diagram, select **Statistics > Flow Graph**.

Wireshark can open the file while the capture is still running. Reopen it, or press **View > Reload**, to see newer packets.

## Watch the traffic live

To see packets as they happen, stream the capture into Wireshark.

- In Git Bash, run `scripts/capture-live.sh`. If `wireshark` is not on your `PATH`, set `WIRESHARK` to the full path of the binary first.
- In PowerShell 7.4 or later, run `scripts/capture-live.ps1`. It uses `C:\Program Files\Wireshark\Wireshark.exe` unless you pass `-Wireshark <path>`.

## What you see when the nodes start

The nodes use these addresses. The Service column is the name in `compose.yaml`. A node with a profile does not start with `docker compose up`: start the profile with `--profile <name>`, or run the UE with `docker compose run --rm <service>`.

| Node | Service | Address | Profile |
| --- | --- | --- | --- |
| DNS | `dns` | 172.28.0.2 | |
| HSS | `hss` | 172.28.0.10 | |
| I-CSCF | `icscf` | 172.28.0.20 | |
| S-CSCF | `scscf` | 172.28.0.30 | |
| P-CSCF | `pcscf` | 172.28.0.40 | |
| Media relay (IMS-AGW) | `agw` | 172.28.0.50 | |
| Lab | `lab` | 172.28.0.60 | |
| OAM server | `oam` | 172.28.0.65 | |
| OSS stand-in | `oss` | 172.28.0.66 | `oss` |
| IP-SM-GW | `ipsmgw` | 172.28.0.70 | |
| Service Centre of SMS | `sc` | 172.28.0.71 | |
| MMTel AS | `mmtel` | 172.28.0.75 | |
| EPC stub, with the PCRF stub | `epc` | 172.28.0.80 | |
| MRFC | `mrfc` | 172.28.0.85 | |
| MRFP | `mrfp` | 172.28.0.86 | |
| UE simulator, subscriber 1 | `ue` | 172.28.0.100 | `ue` |
| UE simulator, subscriber 2 | `ue2` | 172.28.0.101 | `ue` |
| pjsua and SIPp | `softphone` | 172.28.0.102 | `ue` |
| UE simulator, subscriber 3 | `ue3` | 172.28.0.103 | `ue` |
| UE simulator that the Lab drives, subscriber 1 | `phone1` | 172.28.0.110 | `phones` |
| UE simulator that the Lab drives, subscriber 2 | `phone2` | 172.28.0.111 | `phones` |
| UE simulator that the Lab drives, subscriber 3 | `phone3` | 172.28.0.112 | `phones` |
| VoLTE UE, subscriber 10 | `volte10` | 172.28.0.120 | `volte` |
| VoLTE UE, subscriber 12 | `volte12` | 172.28.0.121 | `volte` |

The `capture` service has no address: it shares the network of the host and captures on the bridge `ims0`.

Six nodes open a Diameter connection, each to the node in the Server column, on TCP:

| Client | Server | Application | Port |
| --- | --- | --- | --- |
| `icscf` | `hss` | Cx | 3868 |
| `scscf` | `hss` | Cx | 3868 |
| `ipsmgw` | `hss` | Sh | 3868 |
| `ipsmgw` | `sc` | SGd | 3868 |
| `sc` | `hss` | S6c | 3868 |
| `mmtel` | `hss` | Sh | 3868 |
| `pcscf` | `epc` | Rx | 3868 |

The `epc` node holds the PCRF stub, so the P-CSCF's Rx connection goes there and not to the HSS. With the filter `diameter`, the capture shows these messages:

| Message | From | Meaning |
| --- | --- | --- |
| Capabilities-Exchange Request (CER) | I-CSCF, S-CSCF, IP-SM-GW, MMTel AS, P-CSCF | The client says who it is and which application it supports: Cx for the I-CSCF and the S-CSCF, Sh for the two application servers, Rx for the P-CSCF. |
| Capabilities-Exchange Answer (CEA) | HSS, EPC stub | The server answers with Result-Code 2001 (DIAMETER_SUCCESS) and lists the applications it supports. The HSS lists Cx, Sh, which application servers use, and S6c, which a service centre of short messages uses (TS 29.338). The PCRF stub lists Rx. |
| Device-Watchdog Request and Answer (DWR, DWA) | either side | A side that has heard nothing for about 30 seconds checks that its peer is alive. 30 seconds is the default of the watchdog timer (RFC 3539 3.4.1 [1]), and each timer is set up to 2 seconds early or late. |
| Disconnect-Peer Request and Answer (DPR, DPA) | the node that stops | The node says goodbye before it closes the connection. |

To see a DPR and a DPA, stop one node:

```sh
docker compose stop scscf
```

The S-CSCF sends a DPR with Disconnect-Cause `REBOOTING`, and the HSS answers with a DPA. Start the node again with `docker compose start scscf`, and a new CER and CEA follow.

The specification for these messages is RFC 6733 (Diameter base protocol). The watchdog follows RFC 3539.

## Display filters for SMS over IP

A short message travels in the body of a SIP MESSAGE, with the media type `application/vnd.3gpp.sms`. Wireshark decodes that body without any setting: the `GSM A-I/F RP` dissector shows the RP layer (TS 24.011), and the `GSM SMS` dissector the TPDU inside it (TS 23.040), with the text.

| Filter | What it shows |
| --- | --- |
| `sip.Content-Type == "application/vnd.3gpp.sms"` | Every MESSAGE that carries a short message or a report. |
| `gsm_a.rp.msg_type == 0x00` | RP-DATA from a UE: a short message it sends. `0x01` is RP-DATA to a UE, `0x02` and `0x03` are RP-ACK from and to a UE, `0x04` and `0x05` RP-ERROR. |
| `gsm_sms.tp-mti == 1` | SMS-SUBMIT from a UE, and SMS-SUBMIT-REPORT to it. `0` is SMS-DELIVER and SMS-DELIVER-REPORT, `2` SMS-STATUS-REPORT. The direction tells the two of each pair apart. |
| `gsm_sms.sms_text contains "Hello"` | The short messages with that text, in the GSM 7 bit alphabet or in UCS2. |
| `gsm_sms.tp-da == "12025550102"` | The short messages sent to that number. |
| `sip.In-Reply-To` | The submit reports and the delivery reports: each names the MESSAGE it answers. |
| `diameter.cmd.code == 306 \|\| diameter.cmd.code == 307` | The Sh messages (application 16777217): UDR and UDA, PUR and PUA. |

`scripts/e2e-sms.sh` writes the frame list of its short messages to `e2e-out/sms-ladder.txt`.

## Read the SIP inside ESP

The signalling between a VoLTE UE and the P-CSCF can be protected with IPsec ESP, which the deployment in [The VoLTE call flow](volte.md#the-same-agreement-with-esp) turns on. Nothing in it is encrypted, so the SIP is still there to read, but Wireshark needs one setting before it looks inside: until then those packets show as `ESP (SPI=0x...)` and no display filter on `sip` matches them.

In **Edit > Preferences > Protocols > ESP**, tick **Attempt to detect/decode NULL encrypted ESP payloads**. Wireshark then works out where the payload ends by trying the lengths of the integrity check value that the algorithms use, 12, 16, 24 and 32 octets, and dissects what it finds inside. The VoLTE guide gives the other way, an **ESP SAs** row that names the integrity algorithm instead of leaving Wireshark to work the length out, and the options that read the protected ports as SIP over TCP.

## Copies for the Lab (HEP)

A node can also send a copy of every SIP and Diameter message it sends or receives to the Lab, which listens on UDP port 9060. The copies use HEP version 3 (Homer Encapsulation Protocol). A node sends them when its configuration has a `hep` section, for example `hep: {collector: lab:9060}`. The UE simulator sends them when it gets `-hep lab:9060`. Without either, nothing is sent.

The Lab is not on the path of any call, so a node starts even when the collector's name does not resolve, for example because the Lab is not running yet. The node logs a warning and drops the copies meanwhile. When a copy cannot be sent, the node resolves the name again, at most once every 5 seconds, so the copies reach a Lab that starts later.

- The copies are in the capture file too, as UDP packets to port 9060. Wireshark has no built-in HEP dissector. To decode them, install the Lua plugin from [sipcapture/hep-wireshark](https://github.com/sipcapture/hep-wireshark). The SIP and Diameter messages themselves are decoded as before, so you can ignore the copies with the filter `not udp.port == 9060`.
- A SIP copy carries the message exactly as it was on the wire.
- A Diameter copy carries the message as JSON, with protocol type 0x38 `DIAMETER (JSON)`. HEP3 rev. 37 defines no protocol type for a Diameter message in its binary form.

The HEP encoding leaves out two parts of the specification:

| Where | What is left out | Why |
| --- | --- | --- |
| HEP3 rev. 37, Generic chunk types | IPv6 addresses (chunks 0x0005 and 0x0006). | Every node here has an IPv4 address. |
| docs/HEP3-extended-packet-length.md in the HEP repository | The 32-bit length for packets over 65535 octets. | SIP and Diameter messages here stay far below that size. |

## Stop the system

- To stop the nodes and the UEs of every profile and keep the images, run `docker compose --profile ue --profile phones --profile volte down -v --remove-orphans`. The other guides end with the same command, so the next guide starts without a new build.
- When you have finished with the guides, delete the images that the build created as well:

  ```sh
  docker compose --profile ue --profile phones --profile volte down --rmi all -v --remove-orphans
  ```

The capture files stay in `captures/` until you delete them.
