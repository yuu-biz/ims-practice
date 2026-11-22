# Guides

[日本語](../ja/README.md)

These guides run the IMS on your machine and follow its messages in a capture, one procedure at a time. Start here: this page gives the order to read them in, the first commands, how to clean up, and what the Docker Compose project starts.

## The order to read them in

1. [Capture signaling and open it in Wireshark](capture.md). Start the system, open the capture, and see the Diameter connections come up.
2. [Follow a UE registering with the IMS](registration.md). REGISTER through the P-CSCF, the I-CSCF and the S-CSCF, with SIP Digest and Cx.
3. [Follow a voice call through the IMS](call.md). An INVITE from one UE to another by telephone number, ENUM, and the media relay.
4. [Follow a short message through the IMS](sms.md). SMS over IP through the IP-SM-GW, with the reports.
5. [Supplementary services with the MMTel AS](mmtel.md). Identity presentation, call forwarding and barring, set up over Ut.
6. [Announcements, alerting tones and conferences with the MRF](mrf.md). An announcement before a barred call, the customized alerting tone, call waiting and a conference of three.
7. [The VoLTE call flow](volte.md). The EPC stub, IMS AKA, policy control over Rx, and a call in AMR-WB with preconditions.
8. [Connect two IMS networks](interconnect.md). The IBCF and the TrGW at the border, topology hiding, and the interconnect profiles.
9. [Watch the IMS in the Lab](lab.md). The ladder diagrams of the HEP copies, the subscribers, and UE simulators driven from the browser.

The [glossary](../../glossary.md) explains the abbreviations.

### The UE of the first guides has no access network

The UE in guides 2 to 6 is a softphone, as on a fixed access or a computer. It has no SIM card and no radio access: it reaches the P-CSCF over the Docker network, registers with SIP Digest, and calls in G.711. So the first guides show the IMS alone, without the EPC, IMS AKA or a voice bearer to wait for.

A mobile phone does more before and around each of those steps. It attaches to the EPC, which gives it an address and the P-CSCF, registers with IMS AKA from its USIM, and waits for a voice bearer before the callee rings. [The VoLTE call flow](volte.md) adds those steps on top of what the first guides have shown.

## The first commands

Run these at the root of the repository. The first build takes several minutes.

```sh
docker compose up -d --build
docker compose run --rm ue register -deregister-after 5s
```

The first command starts the nodes and the capture. Before the second, wait until the I-CSCF and the S-CSCF have opened their Diameter connections to the HSS. A node that starts before the HSS accepts connections tries again after 30 seconds (Tc, RFC 6733 2.1), so allow up to a minute. `docker compose logs scscf | grep I-Open` prints a line once that has happened. The second command registers subscriber 1 and deregisters it 5 seconds later. Then open the newest file in `captures/` in Wireshark, as [Capture signaling and open it in Wireshark](capture.md) shows.

## Clean up

Each guide ends by stopping everything it started:

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

This keeps the images, so the next guide starts without a new build. When you have finished with the guides, delete the images too:

```sh
docker compose --profile ue --profile sim --profile volte down --rmi all -v --remove-orphans
```

The capture files stay in `captures/` until you delete them.

## What the Compose project starts

`docker compose up` starts these services. Each IMS node runs the same image, and its configuration file in `deploy/compose/` selects its role.

| Service | Address | What it is |
| --- | --- | --- |
| `dns` | 172.28.0.2 | DNS of the home network domain: NAPTR, SRV and A records, and ENUM. |
| `hss` | 172.28.0.10 | HSS stub. Cx for the CSCFs, Sh for the application servers. |
| `icscf` | 172.28.0.20 | I-CSCF. |
| `scscf` | 172.28.0.30 | S-CSCF. |
| `pcscf` | 172.28.0.40 | P-CSCF. |
| `agw` | 172.28.0.50 | Media relay in the IMS-AGW role, controlled by the P-CSCF. |
| `lab` | 172.28.0.60 | The Lab at http://localhost:8080, and the HEP collector. |
| `ipsmgw` | 172.28.0.70 | IP-SM-GW, the application server of SMS over IP. |
| `mmtel` | 172.28.0.75 | MMTel AS, the application server of the supplementary services. |
| `epc` | 172.28.0.80 | EPC stub with a PCRF, for the VoLTE UEs. |
| `mrfc` | 172.28.0.85 | MRFC, the control side of the MRF. |
| `mrfp` | 172.28.0.86 | MRFP, which plays and mixes the media. |
| `capture` | host network | tcpdump, which writes the capture files to `captures/`. |

### Profiles

The UEs are in profiles, so `docker compose up` does not start them.

| Profile | Services | How they run |
| --- | --- | --- |
| `ue` | `ue`, `ue2`, `ue3`, `peer` | One command each, with `docker compose run --rm ue register` and the like. `peer` holds pjsua and SIPp, user agents written by others. |
| `sim` | `ue-a`, `ue-b`, `ue-c` | They stay up and do what the Lab asks. `docker compose --profile sim up -d`. |
| `volte` | `volte-a`, `volte-b` | VoLTE UEs that stay up, like those of `sim`. `docker compose --profile volte up -d`. |

`ue` and `sim` use the same subscribers, so run one of the two at a time.

### Subscribers, UEs and services

The test subscribers are in `deploy/compose/hss.yaml`. The guides call a UE by its subscriber or its role in the scenario, such as UE 1 or UE-A; this table maps those names to the services.

| Subscriber | Private identity | Number | Services | Called in the guides |
| --- | --- | --- | --- | --- |
| 1 | `001010000000001@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0101 | `ue`, `ue-a` | UE 1, UE-A, UE A1 in the interconnect guide |
| 2 | `001010000000002@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0102 | `ue2`, `ue-b` | UE 2, UE-B |
| 3 | `001010000000003@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0103 | `ue3`, `ue-c` | UE 3, UE-C |
| 4 | `+12025550104@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0104 | `peer` (pjsua) | pjsua |
| 10 | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0110 | `volte-a` | volte-a |
| 11 | `001010000000011@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0111 | `peer` (SIPp) | SIPp |
| 12 | `001010000000012@ims.mnc001.mcc001.3gppnetwork.org` | +1-202-555-0112 | `volte-b` | volte-b |

The second network of the interconnect guide has its own subscribers in `deploy/interconnect/peer/hss.yaml`: +1-202-555-0151, UE B1 on the service `peer-ue`, and +1-202-555-0152.

### Code that no node runs

Some packages under `internal`, and one screen, are in the repository but in none of the nodes above: no binary in `cmd` builds them in, so nothing they do shows in the capture. Each package says so in its documentation.

| Package or screen | What it is |
| --- | --- |
| `fivegc`, `n5`, `nhss`, `sbi` | The 5GS side: a 5GC stub, the N5 policy API of the PCF, the service based interface of the HSS, and what those APIs share. |
| `nas`, `sgd`, `smsc` | SMS without the IMS: the NAS messages that carry it, the SGd and S6c Diameter messages, and a Service Centre that speaks them. The SC that the IP-SM-GW reaches is the stub of package `sc`. |
| `oam`, `web/oam` | The OAM server, which uses the maintenance API of the nodes, and the OAM screen, which CI builds and no node serves. The S-CSCF serves that API; see [Take the S-CSCF out of service](registration.md#take-the-s-cscf-out-of-service). |
| `kvstore/storetest`, `rtptest` | Helpers that only the tests use. |

## Other ways to run it

| Directory | What it does |
| --- | --- |
| [deploy/chaos](../../../deploy/chaos/README.md) | Faults on purpose: a node that stops or freezes, lost packets, a network split. |
| [deploy/observability](../../../deploy/observability/README.md) | Prometheus and Grafana beside the Compose project. |
| [deploy/k8s](../../../deploy/k8s/README.md) | The same IMS on a local Kubernetes cluster made with kind. |
| [deploy/cloud](../../../deploy/cloud/README.md) | Definitions for cloud resources. Applying them costs money. |

### The state of the users in a store outside the nodes

`deploy/redundancy/redundancy.yaml` adds a Valkey server to the project, and gives a `store` section that names it to every node that holds the state of users: the HSS, the S-CSCF, the P-CSCF, the IP-SM-GW, the MMTel AS, the MRFC and the two media relays, the IMS-AGW and the MRFP. Start the project with that file on top of `compose.yaml`:

```sh
export COMPOSE_FILE=compose.yaml:deploy/redundancy/redundancy.yaml COMPOSE_PATH_SEPARATOR=:
docker compose up -d --build
```

Every command of the guides then runs on that deployment. In a shell without the variable, pass `-f compose.yaml -f deploy/redundancy/redundancy.yaml` to each `docker compose` instead. The nodes keep their addresses, their configuration and their messages, so the capture reads as it does in the guides. What changes is where each node puts what it learns.

```sh
docker compose run --no-deps -d --name registered-ue ue register
docker compose exec valkey valkey-cli --scan --pattern 'ims:*'
```

`ue register` with no `-deregister-after` stays registered until it is stopped, so the keys of its registration are there while you look. Each key is the prefix of this deployment, the role that wrote it, the kind of item, and the name it is found by. Among them are these three, each found by the private identity of subscriber 1:

```text
ims:hss:subscriber:001010000000001@ims.mnc001.mcc001.3gppnetwork.org
ims:scscf:registration:001010000000001@ims.mnc001.mcc001.3gppnetwork.org
ims:pcscf:reg-subscription:001010000000001@ims.mnc001.mcc001.3gppnetwork.org
```

A `:` inside a name, as in a SIP URI or a Call-ID, is written `%3A`, so that the parts of a key stay apart. An item is kept as a hash with the value and a version, and the value is JSON:

```sh
docker compose exec valkey valkey-cli hget \
  ims:scscf:registration:001010000000001@ims.mnc001.mcc001.3gppnetwork.org value
```

That is the registration the S-CSCF would otherwise hold in its own memory: the contact of the UE, when the registration expires, and the user profile that the HSS sent over Cx. `docker stop registered-ue` deregisters the UE, and the next scan finds the registration gone, while the subscriber of the HSS stays. Afterwards, clean up as the guides do, with the variable still set:

```sh
docker rm registered-ue
docker compose --profile ue down -v --remove-orphans
```

The `-v` deletes the volume of the store with the nodes, so the next start begins with nothing stored.

### The media relay controlled over H.248

A media relay is a media gateway that the node handling the call drives from outside. The P-CSCF does that over a small HTTP control API of this project, and `deploy/h248/h248.yaml` switches it to H.248 (ITU-T H.248.1), the standard protocol for the task, which 3GPP profiles for this reference point as Iq (TS 29.334). The messages carry the same procedures, so the same call looks the same everywhere else and only the control between the two nodes changes. Start the project with that file on top of `compose.yaml`:

```sh
export COMPOSE_FILE=compose.yaml:deploy/h248/h248.yaml COMPOSE_PATH_SEPARATOR=:
docker compose up -d --build
```

Every command of the guides then runs on that deployment. In a shell without the variable, pass `-f compose.yaml -f deploy/h248/h248.yaml` to each `docker compose` instead.

Two sections change. The P-CSCF reads `h248` in place of `media_relays`:

```yaml
pcscf:
  h248:
    listen: 0.0.0.0:2944
```

It names no relay. Each relay registers with the P-CSCF itself, and the ID of a context names the relay that holds it, so relays can be added and taken away without this file. A relay registers with one P-CSCF, though, and only that one can change or release the contexts it holds there. So a P-CSCF that controls media this way runs as one node, and its configuration takes neither a `pool` nor the store of the deployment above; it refuses both rather than let a call reach a node that cannot release its context. The media relay reads `h248` in place of `control`, the address of the HTTP API, which it then does not serve:

```yaml
agw:
  h248:
    listen: 0.0.0.0:2944
    host: agw.ims.mnc001.mcc001.3gppnetwork.org
    controller: pcscf.ims.mnc001.mcc001.3gppnetwork.org:2944
```

Port 2944 is the standard port for the text encoding of H.248, and nothing encrypts the messages, so Wireshark reads them in the capture as `megaco`:

```sh
docker compose exec capture sh -c 'tshark -r "$(ls -t /captures/ims-*.pcap | head -n 1)" -Y megaco'
```

Make a call first, and the output holds the whole life of the control between the two nodes:

- `ServiceChange=ROOT` from the relay, which is how it tells the P-CSCF that it has started and has no contexts, and the P-CSCF's reply accepting it. `ROOT` is the termination that stands for the whole gateway.
- `Modify=ROOT` from the P-CSCF, which asks the relay to report when it has heard nothing from the P-CSCF for a while.
- `AuditValue` on `ROOT` from the P-CSCF every ten seconds, asking for nothing: the message it sends when it has nothing else to send, so that neither side takes the other for lost.
- For each call, `Add=ip/1/access/$` and `Add=ip/1/core/$` in one transaction: the P-CSCF asks for a context with a termination towards the UE and one towards the rest of the network, and leaves the identifiers, the address and the ports to the relay, which is what `$` means. The relay answers with what it chose, and the P-CSCF writes that address and port into the SDP it passes on, as it does with the HTTP API.
- `Modify=ip/1/access/…` and `Modify=ip/1/core/…`, which tell each termination where its party takes the media, from the SDP of that party.
- `Subtract=*` when the call ends, which releases both terminations of the context at once.

The same call over the HTTP API is in [a voice call](call.md). Reading the two captures side by side shows one call controlled in two protocols: `Add`, `Modify` and `Subtract` here, and the requests of the HTTP API there.

## How the whole setup is built

These choices hold in every guide.

- **Signaling is not encrypted by default.** Wireshark decodes every SIP, Diameter and DNS message in the capture without keys or extra settings.
- **The nodes use the standard port numbers.** SIP on 5060, Diameter on 3868, DNS on 53, so Wireshark picks the right dissector by the port.
- **Configuration selects the role.** One image runs every IMS role. The file of a node says whether it is a P-CSCF, an I-CSCF, an S-CSCF, an application server or something else, so a node can take several roles at once.
- **A role can grow to several nodes.** A commercial network runs many nodes of each role, and the roles here are built for that too. The Compose project runs one node of each, which is enough to follow the messages. A node that reaches the HSS over Cx or Sh can name several instances of it under `peers`, each with its own `origin_host` and with `kind: hss`, which tells them from the `kind: pcrf` peer that a P-CSCF on the same node reaches over Rx: every instance serves every subscriber, so the node sends to the first one whose connection is open, and sends the request again to the next one when that instance does not answer, has closed its connection, or answers DIAMETER_TOO_BUSY. The request it sends again carries the T bit of the Diameter header, which tells the instance that gets it that it may be a copy of one the first instance is still working on (RFC 6733 5.5.4). Once the S-CSCF knows which instance serves a user, it names that instance in the Destination-Host of the requests of that registration (TS 29.229 5.5), and those go to that instance alone: a request with a fixed destination cannot be taken over. So a registration that has begun stays with the instance that answered, and a user whose instance stops registers again, which starts at whichever instance answers next.
- **Where a node keeps the state of its users is configuration.** The roles that hold such state keep it behind a `Store`: the S-CSCF its registrations, the P-CSCF the IP association of each UE and its own subscription to that UE's registration state, the HSS its subscribers, the IP-SM-GW its short messages on their way, the MMTel AS and the IBCF their calls, the MRFC its conferences, the media relay the contexts whose RTP ports it holds. A node whose configuration file has a `store` section of kind `valkey` keeps it in the Valkey server the section names, where a node recreated under the same name finds what the earlier one left. Without the section, as in the Compose project of these guides, each node keeps the state in the memory of its own process, and it goes with the process. The S-CSCF, the HSS and the IP-SM-GW also take the lock of a user there, so that only one node of the role handles the registration or the subscription data of a given user at a time, while the others go on serving everybody else. An expiry that calls for an action when its time comes, such as the end of a registration or of a subscription to one, is kept with that state as a time: each node looks once a second for the ones whose time has come and takes on those it finds, so a node that starts in place of another handles the expiries that one was watching.
