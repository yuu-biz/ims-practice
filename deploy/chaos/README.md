# Fault injection

[日本語](README.ja.md)

This directory lets you break the IMS on purpose and watch what happens: a node that stops, a node that freezes, packets that get lost, and two nodes that can no longer reach each other.

The IMS containers stay exactly as `compose.yaml` defines them. The network faults are put in place by a throw-away container that joins the network namespace of the node, with the `NET_ADMIN` capability, and goes away. `fault.sh` hands the Docker socket to no container. Only the fault agent, which lets the dashboard start faults, has it, and only when you start it with its own override file ([The fault agent](#the-fault-agent)).

Every node is a target: the nodes of `compose.yaml`, those of the second network in [deploy/interconnect](../interconnect/compose.yaml), and those of any other override file. `fault.sh` finds the containers by the labels that Docker Compose puts on them, so there is no list of nodes to keep up to date.

## Before you start

- The system runs as described in [the capture guide](../../docs/guide/en/capture.md).
- Build the tools image once, from the root of the repository:

  ```sh
  docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools
  ```

- Run every command below from the root of the repository. In Git Bash, `deploy/chaos/fault.sh` sets `MSYS_NO_PATHCONV=1` for you.
- `fault.sh` acts on the stack of `COMPOSE_FILE`, as `docker compose` does. For the two IMS networks joined by their IBCFs, set it to the same files that you started them with:

  ```sh
  export COMPOSE_FILE=compose.yaml:deploy/interconnect/compose.yaml:deploy/chaos/chaos.yaml
  ```

## Choosing a target

| Target | Example | What it names |
| --- | --- | --- |
| A service | `mmtel`, `epc`, `volte-a`, `ibcf-a`, `peer-scscf` | The one container of the service. |
| A UE of `docker compose run` | `ue3` | The running container that `docker compose run ue3 ...` started. A UE that has exited is no longer a target. |
| One container of several | `agw:2` | The second container of a service that runs several (`docker compose up --scale agw=2`). With several and no number, `fault.sh` lists them and asks you to name one. |

A partition takes peers. A peer is a service (all of its containers), one container such as `agw:2`, a pattern such as `'scscf-*'` for every service whose name matches, or an IPv4 address.

A network fault sits on every interface of the target, or, with `--network NET`, only on the interface on the Compose network `NET`. The IBCFs and the TrGWs of `deploy/interconnect` have two interfaces: one on their own network and one on `nni`, the link between the two networks.

## The faults

| Command | What it does | How to undo it |
| --- | --- | --- |
| `fault.sh stop scscf` | `docker stop`. The node gets SIGTERM, says goodbye, and exits. | `fault.sh start scscf` |
| `fault.sh kill scscf` | `docker kill`. The node gets SIGKILL and dies at once. | `fault.sh start scscf` |
| `fault.sh crash scscf` | The node's process dies (SIGQUIT) and Docker starts it again by itself, because of `restart: unless-stopped`. | Nothing to undo |
| `fault.sh recreate scscf` | Kills the container, removes it, and creates a new one with the same name and address, as an orchestrator replaces a node that died. What the node kept in its memory is gone. | Nothing to undo |
| `fault.sh pause scscf` | `docker pause`. The node is frozen: its sockets stay open, but it answers nothing. | `fault.sh unpause scscf` |
| `fault.sh loss agw 10` | Drops 10 % of the IP packets that the node sends, and 10 % of those it receives. | `fault.sh clear agw` |
| `fault.sh partition pcscf icscf` | The node can no longer reach the peers, nor hear them. Without peers, it is cut off from everything. | `fault.sh clear pcscf` |
| `fault.sh partition --network nni ibcf-a` | Cuts the IBCF off the link between the two networks; its own network still reaches it. | `fault.sh clear ibcf-a` |
| `fault.sh delay pcscf 200 20` | Delays what the node sends by 200 ms, give or take 20 ms (netem). | `fault.sh clear pcscf` |

`fault.sh show <target>` lists the network faults of a node with their counters: how many packets each rule dropped. `fault.sh heal` undoes everything on every container of the stack. Restarting a node also removes its network faults, because the container gets a new network namespace.

Each fault prints the `docker` command that it runs, so that you can type it yourself. A network fault first runs `ip -o -4 addr show` in the node's namespace, to find the interface that holds the node's address.

### Where a lost packet shows up

The capture container records the bridge that connects the nodes ([the capture guide](../../docs/guide/en/capture.md)). The network faults sit at the node's own network interface, between the node and the bridge:

- A packet that the node sends and loses never reaches the bridge. It is not in the capture.
- A packet that the node receives and loses has crossed the bridge already. It is in the capture, but the node never got it.

The dashboard shows a different view. Each node sends it a copy of every SIP and Diameter message it sends or receives, and the faults let these copies through. So the dashboard shows what the node believes: a message that it sent and the fault dropped is on the dashboard, but not in the capture.

## Exercises

Start the system and open the capture in Wireshark, as in [the capture guide](../../docs/guide/en/capture.md). Registrations and calls are made as in the [registration](../../docs/guide/en/registration.md) and [call](../../docs/guide/en/call.md) guides.

### Stop, kill and pause the S-CSCF

1. `deploy/chaos/fault.sh stop scscf`. With the filter `diameter`, the S-CSCF sends a Disconnect-Peer Request (DPR) and the HSS answers with a DPA. Then the TCP connection closes (FIN). `fault.sh start scscf` brings a new CER and CEA.
2. `deploy/chaos/fault.sh kill scscf`. There is no DPR this time: the kernel closes the TCP connection of the dead process with a FIN. Docker does not restart a node that you killed; start it with `fault.sh start scscf`.
3. `deploy/chaos/fault.sh crash scscf`. The log of the S-CSCF (`docker compose logs scscf`) ends with `SIGQUIT: quit` and a dump of every goroutine, and the node is back a moment later: `docker inspect -f '{{.RestartCount}}' ims-practice-scscf-1` has gone up by one.

### Register while the P-CSCF is frozen

```sh
deploy/chaos/fault.sh pause pcscf
docker compose run --rm ue register -deregister-after 2s
```

In a second terminal, run `deploy/chaos/fault.sh unpause pcscf` after a few seconds. With the filter `sip.Method == "REGISTER"`, the UE sends the REGISTER again after 0.5 s, 1 s and 2 s, each wait twice the one before: timer E of RFC 3261 17.1.2.2. Wireshark marks the copies with `sip.resend == 1`. When the P-CSCF thaws, it answers the REGISTER once; the copies it finds in its socket belong to the same transaction.

### Register while the HSS is frozen

```sh
deploy/chaos/fault.sh pause hss
docker compose run --rm ue register -deregister-after 2s
deploy/chaos/fault.sh unpause hss
```

With the filter `diameter || sip.Status-Code >= 400`, the I-CSCF sends a User-Authorization Request (UAR) that gets no answer. After 5 seconds it gives up and sends 480 to the P-CSCF, and the P-CSCF sends 504 Server Time-out to the UE. Meanwhile a Device-Watchdog Request (DWR) of the I-CSCF, the S-CSCF or the IP-SM-GW that falls due gets no DWA. A node closes its connection once Tw, 30 seconds, has passed twice more after an unanswered DWR (RFC 3539 3.4.1), so freeze the HSS for about two minutes to see that. Once the HSS is back, the node opens a new connection (CER, CEA) within Tc, 30 seconds; the HSS then also sends the UAA that nobody waits for any longer.

### Packet loss on the media relay during a call

```sh
deploy/chaos/fault.sh loss agw 10
```

Make a call, then `deploy/chaos/fault.sh clear agw`. In Wireshark, select **Telephony > RTP > RTP Streams** and analyse the stream from the relay (172.28.0.50) to the callee: the analysis counts lost packets and shows the gaps in the sequence numbers. Some of the lost packets are in the capture (the relay received them and dropped them), some are not (the relay sent them and dropped them). The call itself goes through.

### Packet loss on the S-CSCF

```sh
deploy/chaos/fault.sh loss scscf 20
```

Register and call a few times, then clear the fault. Look for `sip.resend == 1` and `tcp.analysis.retransmission`: the nodes resend what was lost. The S-CSCF also loses some of its DNS queries. The node waits 2 seconds for an answer and does not ask again, so a lost ENUM or NAPTR query can fail the call with 404. With the loss on the P-CSCF instead, a lost query fails a registration with 504. The log says why: `cannot route the request` with `i/o timeout`.

### Cut the P-CSCF off from the I-CSCF

```sh
deploy/chaos/fault.sh partition pcscf icscf
docker compose run --rm ue register -deregister-after 2s
deploy/chaos/fault.sh clear pcscf
```

The capture holds the UE's REGISTER and its copies, more and more spaced out up to 4 s (T2). After 32 s (timer F), the UE gives up, and at the same moment the P-CSCF sends a 504 that comes too late. It holds nothing from the P-CSCF to the I-CSCF. The dashboard, however, shows that the P-CSCF sent the REGISTER to the I-CSCF again and again.

### Cut the S-CSCF off from the HSS

```sh
deploy/chaos/fault.sh partition scscf hss
docker compose run --rm ue register -deregister-after 2s
```

The REGISTER gets 500 after 5 seconds: the S-CSCF sent a Multimedia-Auth Request (MAR) that the dashboard shows and the capture does not. Leave the partition for about two minutes and read `docker compose logs hss` for `peer state`: the watchdog of RFC 3539 (3.4) moves the connection from `OKAY` to `Pending` and `Suspect`, one step for each Tw of 30 seconds, and then closes it. After `fault.sh clear scscf`, the S-CSCF opens a new connection (SYN, CER, CEA) within Tc, 30 seconds, and then the next registration works.

### Freeze the MMTel AS during a call

```sh
docker compose run -d --name ue2-answer ue2 answer -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org -password test-password-2 -ring 1s -tone 6s
docker compose run --rm ue call -tone 6s tel:+12025550102
```

While the call runs, in a second terminal, `deploy/chaos/fault.sh pause mmtel`, and about ten seconds later `deploy/chaos/fault.sh unpause mmtel`. The voice goes on: the RTP goes through the media relay, not through the AS. When UE 1 hangs up, its BYE reaches the AS and gets no answer; with the filter `sip.Method == "BYE"`, the UE, the P-CSCF and the S-CSCF send it again after 0.5 s, 1 s, 2 s and 4 s (timer E). When the AS thaws, it answers every copy that waited in its socket with a 200, and only then sends its own BYE to UE 2 on the other leg of the call (the AS is a B2BUA). Then `docker rm -f ue2-answer`.

### Kill the IBCF during a call to the other network

Start the two IMS networks ([the interconnect guide](../../docs/guide/en/interconnect.md)) and set `COMPOSE_FILE` as in [Before you start](#before-you-start). UE B1 of network B waits for a call, and UE A1 calls it:

```sh
b1=(-domain ims.mnc002.mcc001.3gppnetwork.org -pcscf 172.28.1.40:5060
  -pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060
  -impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org
  -impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org -password test-password-b1)
docker compose run -d --name b1-answer peer-ue answer "${b1[@]}" -ring 1s -tone 12s
docker compose run --rm ue call -tone 10s tel:+12025550151
```

During the call, `deploy/chaos/fault.sh kill ibcf-a`, and three seconds later `deploy/chaos/fault.sh start ibcf-a`. The voice goes on: in the capture of the link (`captures/nni/`), the RTP between the TrGWs has no gap, because only the IBCF died. The BYE at the end crosses the new IBCF A and ends the call in both networks: the IBCF is a proxy, and the Route of the BYE says where it goes. But the new IBCF A has lost what the old one kept of the call, and nobody releases the call's context in TrGW A:

```sh
docker compose exec trgw-a wget -qO- http://127.0.0.1:8080/contexts
```

still lists it after the call. TrGW B, whose IBCF kept running, has none left.

### Cut the link between the two networks

```sh
docker compose run -d --name b1-answer peer-ue answer "${b1[@]}" -ring 1s -tone 30s
docker compose run --rm ue call -tone 6s tel:+12025550151
```

A few seconds into the call, in a second terminal, cut network A's side of the link, for the signalling and for the media:

```sh
deploy/chaos/fault.sh partition --network nni ibcf-a
deploy/chaos/fault.sh partition --network nni trgw-a
```

Network A still reaches both nodes on their other interface. In the capture of the link, the RTP from TrGW B to TrGW A goes on (TrGW A drops it after the bridge), and nothing comes from network A any more. IBCF B's TCP keep-alives to IBCF A, one every 15 s, get no answer. When UE A1 hangs up, the MMTel AS answers its BYE at once and sends its own BYE towards network B; with the filter `sip.Method == "BYE" || sip.CSeq.method == "BYE"` in network A's capture, the S-CSCF sends it to IBCF A again and again, and 32 s later (timer F) IBCF A answers 504. UE B1 never hears the BYE, and its side of the call stays up.

A new call while the link is cut gets 100 Trying from IBCF A, and 504 Server Time-out about 34 s later: IBCF A first tries TCP to IBCF B, as the INVITE is larger than 1300 bytes, gives up after 2 s and sends it over UDP, and timer B ends the transaction. `deploy/chaos/fault.sh show ibcf-a` counts the packets that each rule dropped. Undo it with `deploy/chaos/fault.sh clear ibcf-a` and `deploy/chaos/fault.sh clear trgw-a`; the next call goes through. Then `docker rm -f b1-answer`.

### Several instances of a role and a shared store

A redundant deployment runs the instances of a role as services of their own, such as `scscf-1` and `scscf-2`, which keep their state in a shared store, such as a service `valkey`. They are targets like any other:

- `fault.sh recreate pcscf-1` replaces one P-CSCF; the other instances and the store keep the state.
- `fault.sh kill valkey`, then `fault.sh start valkey`, restarts the store. With an append-only file on a volume, it reads its data again; without persistence, it starts empty, and every instance loses the state it kept there.
- `fault.sh partition valkey 'scscf-*'` cuts the store off from every S-CSCF instance and leaves the other roles alone.

### Take a node off the network

Docker can also unplug a container from the network altogether:

```sh
docker network disconnect ims-practice_ims ims-practice-icscf-1
docker network connect --ip 172.28.0.20 ims-practice_ims ims-practice-icscf-1
```

Give the address again when you reconnect; the addresses are in [the capture guide](../../docs/guide/en/capture.md). While the I-CSCF is off, the capture shows ARP requests for 172.28.0.20 that nobody answers, the P-CSCF resending the REGISTER, and the HSS resending on its TCP connection. The I-CSCF itself sees its connection to the HSS fail. Keep it off for about two minutes, enough for the watchdog to close the connection, and its log shows the watchdog states down to `Closed`; once it is connected again, it opens a new connection within Tc, 30 seconds, and its log shows `I-Open`.

## Delay needs netem

`fault.sh delay` uses `tc qdisc ... netem`, which needs the `sch_netem` module in the kernel that runs Docker. If the command answers `Error: Specified qdisc kind is unknown.`, that kernel has no netem, and this fault cannot run there. Loss and partitions use nftables and do not need netem.

## Privileges and Docker Desktop

- The IMS containers get no capability and no tools. Only the throw-away `fault-tools` container has `NET_ADMIN`, in the node's namespace, for one command.
- A network fault on a capture service (`capture`, and `capture-peer` and `capture-nni` of `deploy/interconnect`) is refused: those containers share the host's network, and the rules would change the host.
- The loss and partition rules use the egress hook of nftables, which needs a kernel built with `CONFIG_NETFILTER_EGRESS`. If `fault.sh loss` fails on the `egress` chain, the kernel lacks it. On Docker Desktop, the node faults (`stop`, `kill`, `pause`, `crash`) use only Docker commands. The network faults depend on the kernel of the Linux virtual machine that Docker Desktop runs, which this guide has not tried; the two errors above tell whether that kernel lacks the egress hook or netem.

## The fault agent

`fault.sh` breaks a node from a terminal. The fault agent does the same for the dashboard, so that a learner can break a node from its page and watch the result next to the signalling: it runs the HTTP API of `internal/fault` in a container of its own (`agent.yaml`), and applies each fault to the containers of the Compose project, as `fault.sh` does. Every fault it starts has a duration and ends by itself when the time is up.

### Why it starts only when you ask for it

The agent talks to the Docker Engine through the Docker socket, `/var/run/docker.sock`. Whoever can use that socket can start a container with the host's disk and network mounted, which is as much as root on the host. So:

- Only `agent.yaml` mounts the socket, and only into the agent. `compose.yaml` never names `agent.yaml`, and no IMS node gets the socket. `go test ./internal/fault` fails when another service of `compose.yaml` or of an override under `deploy/` mounts it.
- The agent is on a network of its own, `chaos` (172.28.9.0/24), which it shares with the dashboard only. The IMS nodes cannot reach it, and its HTTP does not show up in the capture of `ims0`.
- The API has no password: whoever reaches it can stop any container of the project. Its port is therefore published on the host's loopback address only, `127.0.0.1:8070`, for `curl`.
- The agent refuses a fault on a container that holds the Docker socket, itself included: a stopped or frozen agent would leave its faults without an end. Like `fault.sh`, it refuses a network fault on the capture containers.

### Starting and stopping it

From the root of the repository, build the tools image once, and start the IMS with the agent:

```sh
docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools
docker compose -f compose.yaml -f deploy/chaos/agent.yaml up -d
curl http://127.0.0.1:8070/fault/v1/faults
```

On a running IMS, the same `up -d` starts the agent and recreates the dashboard only, which joins the network `chaos`. Name both files on every `docker compose` command of this stack, or set them once: `export COMPOSE_FILE=compose.yaml:deploy/chaos/agent.yaml`.

The agent keeps its faults in its memory. Clear them, or let them end, before you stop it: a fault that it applied stays when it stops, and `deploy/chaos/fault.sh heal` then undoes it. To stop the agent and keep the IMS running, start the stack again without `agent.yaml`, which removes the agent and takes the dashboard off the network `chaos`, and remove the network:

```sh
docker compose -f compose.yaml up -d --remove-orphans
docker network rm ims-practice_chaos
```

`docker compose -f compose.yaml -f deploy/chaos/agent.yaml down` stops everything.

The agent's configuration is `fault-agent.yaml`: the address it listens on, where the socket is, the Compose project whose containers it may touch (`ims-practice`, the name in `compose.yaml`), and the tools image. With another project name (`docker compose -p`), change `compose_project` there.

### Breaking a node is not taking it out of service

Draining a node, so that new registrations and calls go to the other instances while the calls in progress go on, is an operation of maintenance, not a fault. It belongs to the OAM, which sets the node's administrative state (`internal/oam`). The fault agent has no drain, and the OAM starts no fault.

### The HTTP API

The dashboard reaches the API at `http://172.28.9.10:8080`, and the host at `http://127.0.0.1:8070`. The bodies are JSON, and every answer has `Content-Type: application/json`. Times are RFC 3339 in UTC, such as `"2026-11-20T23:57:48.449Z"`. Durations are strings in the form of Go, such as `"200ms"`, `"20s"` or `"5m0s"`.

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /fault/v1/faults` | | 200 and an array of faults, the oldest first. An ended fault stays in the list for an hour after it ended |
| `POST /fault/v1/faults` | A request (below) | 201 and the fault in state `active`, with `Location: /fault/v1/faults/{id}` |
| `GET /fault/v1/faults/{id}` | | 200 and the fault |
| `DELETE /fault/v1/faults/{id}` | | 200 and the fault in state `cleared`. A fault that has ended already is answered as it is, with 200 |

The request of `POST`:

| Field | Type | For the kinds | Meaning |
| --- | --- | --- | --- |
| `target` | string | all | A Compose service, such as `pcscf` or `ibcf-a`, or one container of a service that runs several, such as `agw:2`. Lower-case letters, digits, `_`, `.` and `-` |
| `kind` | string | | `stop`, `kill`, `pause`, `crash`, `recreate`, `delay`, `loss` or `partition`, as in [The faults](#the-faults) |
| `network` | string | `delay`, `loss`, `partition` | The Compose network, such as `nni`, on whose interface the fault sits. Left out, every interface of the target |
| `delay` | duration | `delay` | Above 0 and at most `10s` |
| `jitter` | duration | `delay` | From 0 to `delay` |
| `loss_percent` | integer | `loss` | From 1 to 100: the chance that each packet sent or received is dropped |
| `peers` | array of strings | `partition` | Services, containers (`agw:2`), patterns (`scscf-*`) or IPv4 addresses that the target can no longer reach nor hear. Left out, everything. No peer twice, and not the target itself |
| `duration` | duration | the faults that last | From `1s` to `1h`. Left out, `5m`. A `crash` and a `recreate` happen once and take none |

A field of another kind, or a field that the table does not have, is refused. For example:

```sh
curl -X POST -d '{"target":"pcscf","kind":"pause","duration":"20s"}' http://127.0.0.1:8070/fault/v1/faults
curl -X POST -d '{"target":"agw","kind":"loss","loss_percent":10,"duration":"2m"}' http://127.0.0.1:8070/fault/v1/faults
curl -X POST -d '{"target":"ibcf-a","kind":"partition","network":"nni","duration":"1m"}' http://127.0.0.1:8070/fault/v1/faults
```

A fault is the request, with `duration` filled in, and these fields:

| Field | Meaning |
| --- | --- |
| `id` | 8 hexadecimal digits |
| `state` | `applying` and `clearing` while the agent works on it; `active` while it is in place; `done` (it happened and is over), `cleared` (by `DELETE`), `expired` (its time was up) and `failed` at its end |
| `error` | Why it failed. Only in state `failed` |
| `created_at`, `expires_at` | When it was asked for, and when it ends by itself. A fault that happens once has no `expires_at` |
| `ended_at` | When it ended. Only at its end |

```json
{"id":"113e54e0","target":"pcscf","kind":"pause","duration":"5s","state":"active",
 "created_at":"2026-11-20T23:57:48.449036798Z","expires_at":"2026-11-20T23:57:53.449036798Z"}
```

A fault that happens once is answered in the state `done`: there is nothing to clear, so `DELETE` on it only reads it back, and it stays in the list for an hour like any other ended fault.

```json
{"id":"7c2f1a04","target":"scscf","kind":"crash","state":"done",
 "created_at":"2026-11-20T23:59:02.117Z","ended_at":"2026-11-20T23:59:02.265Z"}
```

The agent answers `POST` and `DELETE` once the change is done. `stop` waits until the node has exited, at most its `stop_grace_period` of `compose.yaml` (5 s). A network fault runs a few throw-away containers in the node's namespace, which takes about a second. Keep the request open until the answer comes. The agent looks for faults whose time is up once a second, so a fault ends up to a second after its `expires_at`; `ended_at` says when.

Every error has the body `{"error": "..."}`:

| Status | When |
| --- | --- |
| 400 | The request is not one that the table allows: an unknown kind, a field of another kind, a value out of range, an unknown field, a body that is not one JSON object, or a body over 16 KiB |
| 404 | No fault has the id, or it ended more than an hour ago. Also every other path under `/fault/v1/` |
| 405 | A method that the path does not take. The header `Allow` lists those it takes |
| 409 | `POST`: the target has a fault that is not over and that this one would meet. `stop`, `kill` and `pause` meet each other, since a stopped node can be neither killed nor paused; `delay`, `loss` and `partition` each meet only another of their kind. A `crash` and a `recreate` meet nothing, because they are over before the next request comes. `DELETE`: the fault is being applied or cleared |
| 500 | Docker could not do it. The body also has `fault`, the fault in state `failed`, which stays in the list. For example: no container of the target, a service with several containers and no number, a network fault on a capture container, a fault on the agent, the tools image not built, a kernel without netem (`delay`) or without the egress hook (`loss`, `partition`). When `DELETE` fails, the fault may still be in place: `deploy/chaos/fault.sh heal` undoes it |

```text
{"error":"executor failed: no container of the service \"nosuch\" in the project \"ims-practice\"",
 "fault":{"id":"febb1f33","target":"nosuch","kind":"stop","duration":"5s","state":"failed",
  "error":"apply: no container of the service \"nosuch\" in the project \"ims-practice\"", ...}}
```

`fault.sh recreate` recreates the container with `docker compose up -d --no-deps`, which reads the Compose file again. The agent has no Compose file: it copies the configuration of the container it replaces, which is the one Compose gave it, so the new container has the same name, address and image. It replaces the one container of the target, where `fault.sh` leaves the service to Compose.

The agent makes the new container before the old one goes, under a name of its own (`ims-practice-scscf-1-replacing-<id>`, which `docker ps` shows for a moment), and gives it the name of the old one once that is removed. So a recreate that the Engine cannot carry out leaves the node running, and the answer says so. The address and the names on the network belong to whichever of the two is running, because the Engine takes them when a container starts.
