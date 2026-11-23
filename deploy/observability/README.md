# Metrics with Prometheus and Grafana

[日本語](README.ja.md)

This directory adds [Prometheus](https://github.com/prometheus/prometheus) and [Grafana](https://github.com/grafana/grafana) to the Docker Compose project, so that the counts of the IMS nodes can be seen over time with widely used tools. Of the IMS nodes, only the P-CSCF, the I-CSCF, the S-CSCF and the HSS serve their metrics (see [Limits](#limits)), so the dashboard shows the others as down. The metric names follow the measurements of TS 32.409, and `internal/metrics` writes them.

| Path | What it is |
| --- | --- |
| `compose.observability.yaml` | The two services, as a file to add to the root `compose.yaml` |
| `prometheus/prometheus.yml` | What Prometheus scrapes, and how often. The `ims` job in it is generated |
| `prometheus.go`, `genprometheus/` | The generator of the `ims` job, from the services of the root `compose.yaml` |
| `grafana/provisioning/` | The Prometheus data source and the dashboard folder, loaded when Grafana starts |
| `grafana/dashboards/ims-overview.json` | One dashboard: node up, registrations, sessions, Diameter results |
| `observability_test.go` | Checks that `prometheus.yml` is up to date with `compose.yaml` and that the dashboard uses the provisioned data source |

## Start and stop

Run from the root of the repository. The second `-f` adds the two services to the IMS; `compose.yaml` itself is not changed.

```sh
docker compose -f compose.yaml -f deploy/observability/compose.observability.yaml up -d
```

- Prometheus: <http://localhost:9090>. The page <http://localhost:9090/targets> lists every node with the result of its last scrape.
- Grafana: <http://localhost:3000>. The home page is the dashboard **IMS overview**. You can look without signing in; to change things, sign in as `admin` with the password `admin` and choose a new one.

Both ports are published on `127.0.0.1` only, because neither tool asks for a password here.

Register a UE to see a count:

```sh
docker compose run --rm ue register -deregister-after 5s
```

Prometheus scrapes every 15 seconds. Within that time the panel **REGISTER attempts and successes** shows the registration and the deregistration that the P-CSCF and the S-CSCF counted, each under its own `role`, **Initial registration success ratio** the share of the initial registrations that succeeded, and **Initial registration set-up time** how long the initial registration took at the P-CSCF. A call, as in [Follow a voice call through the IMS](../../docs/guide/en/call.md), shows in the panels **Session attempts and answers** and **Answered sessions**: the P-CSCF counts the call on both its legs, and the I-CSCF counts it once, as the terminating session establishment that enters the home network. The panel **Diameter answers by result** follows the same registration over Cx: the I-CSCF asks the HSS where the user may register (UAR) and the S-CSCF asks for the authentication vector (MAR) and tells the HSS that it serves the user (SAR), each under its own `role` and with the result the HSS answered. The panel **Diameter peers** shows the connections that each node opens towards the peers of its configuration, 1 while the connection takes requests; it is read from the state of each connection at the scrape, so a node whose HSS is out of service drops to 0 and comes back by itself. The panel **Registered identities** follows the same registration from the HSS: the SIP URI and the tel URI of the subscriber enter the registered state together and leave it at the deregistration, while **Provisioned subscriptions and identities** shows what the HSS holds whether anyone is registered or not.

To stop and remove everything, including the pulled images and Prometheus's data:

```sh
docker compose -f compose.yaml -f deploy/observability/compose.observability.yaml down --rmi all -v --remove-orphans
```

## After changing compose.yaml

The `ims` job of `prometheus/prometheus.yml`, between the lines `# BEGIN generated` and `# END generated`, is made from the root `compose.yaml`. After adding or removing a node there, run from the root of the repository:

```sh
go generate ./deploy/observability/...
```

and commit the changed `prometheus.yml`. `go generate ./deploy/...` does the same and also makes the copies of `deploy/k8s` (`go generate ./deploy/k8s/...`), so one command brings both up to date. Until then, `observability_test.go` fails and says which command to run.

A service counts as a node when it runs the image built from the `Dockerfile` at the root of the repository (the image that runs the IMS roles, which the service with `build: .` builds and the others run by its name) and is in no profile, so that `docker compose up` starts it. No list of nodes is kept by hand. The UE simulators use the same image but only start in a profile, so they are left out. Everything outside the two marker lines, such as `global`, is written by hand and kept as it is.

## Limits

The P-CSCF, the I-CSCF, the S-CSCF, the HSS, the MMTel AS, the IP-SM-GW and the IBCF count with `internal/metrics`. The three CSCFs and the HSS of the Compose project serve their counts at `http://pcscf:8085/metrics`, `http://icscf:8085/metrics`, `http://scscf:8085/metrics` and `http://hss:8085/metrics`, on the listener that the `maintenance` section of each of `deploy/compose/pcscf.yaml`, `deploy/compose/icscf.yaml`, `deploy/compose/scscf.yaml` and `deploy/compose/hss.yaml` names. The HSS node serves its metrics there and no maintenance API, since the HSS holds no administrative state that an operator sets. The panel **Dropped sessions** stays empty: neither the P-CSCF nor the I-CSCF lets a session expire, so neither has one to count. The other nodes serve no metrics: the MMTel AS and the IP-SM-GW count the Diameter they speak over Sh, and the node of the IBCF, in `deploy/interconnect`, has no such listener, so those counts stay inside the process. `prometheus/prometheus.yml` is written for nodes that serve their metrics at `http://<service name>:8085/metrics`, on the listener of their maintenance API. No specification gives a port for this; 8085 is this project's choice, kept in `MetricsPort` in `prometheus.go`. To use another port, change `MetricsPort` and run the command above.

`ims_diameter_peer_up` covers the peers that a node is configured to connect to. The connections that a node accepts, those of the HSS and of the PCRF, are not among them: a node learns of such a peer when it connects, so there is no fixed set of them to name in a label, and the node that opened the connection reports it from its own side.

The metrics are written by `internal/metrics` in the Prometheus text format, version 0.0.4. Its labels take values from a closed set only, so no series is ever made per subscriber, and the number of series stays the same however many subscribers the network has.

The code leaves out these things that the Prometheus documents and TS 32.409 describe. Each place has a `SIMPLIFIED:` comment.

| Where | What is left out | Why |
| --- | --- | --- |
| `metrics.NewHandler`, prometheus/docs instrumenting/content_negotiation.md | Choosing among the formats in the Accept header, and gzip. The handler always answers in the text format 0.0.4, uncompressed. | Every Prometheus asks for the text format, and gzip is optional. The other formats carry nothing these metrics use. |
| `metrics.Histogram`, prometheus/docs instrumenting/exposition_formats.md | A consistent snapshot of the buckets, the sum and the count while observations go on. The "+Inf" bucket always equals `_count`, but `_sum` may already include an observation that the buckets do not, until the next scrape. | The snapshot needs a second set of counters and a swap. The mean is off by at most one observation, for one scrape. |
| P-CSCF, TS 32.409 4.1.1.1.1 and 4.1.1.1.3 | A REGISTER that the P-CSCF answers before it has read the IP association of the UE counts in no registration measurement: one that it cannot read, which gets 400, one that has used up its Max-Forwards, which gets 483, and one whose association it cannot read, which gets 500. | Whether a REGISTER starts an initial registration or a re-registration depends on whether the UE has an IP association with the P-CSCF. |
| I-CSCF, TS 32.409 4.1.2.12 and 4.1.2.8 | The I-CSCF counts no successful terminating session establishments and no dropped sessions. | No panel reads the successes, as none reads those of the P-CSCF and the S-CSCF. The I-CSCF does not record-route, so it keeps no session that could expire. |
| I-CSCF, TS 32.409 4.1.2.11 | An initial INVITE that the I-CSCF refuses while it is shut down or locked counts in no measurement. | Its sender tries the next I-CSCF, which counts it, so the instances add up to the attempts of the network. |
| S-CSCF, TS 32.409 4.1.1.1.1 and 4.1.1.1.3 | A REGISTER that the S-CSCF answers before it has read what it holds for the user counts in no registration measurement: one that it cannot read, which gets 400, one whose user it cannot lock, which gets 500, and one that it refuses while the operator has it shut down or locked, which gets 503. | Whether a REGISTER is an initial registration or a re-registration depends on whether the S-CSCF holds a registration of the user. |
| HSS, TS 32.409 4.2.1.1.2, 4.2.1.1.5, 4.2.1.1.6 and 4.2.1.1.7 | The HSS counts neither the provisioned Private User Identities nor the Private and Public Service Identities. | A subscription here has exactly one private user identity, so that number would be the subscriptions under a second name. The Public Service Identities are wildcarded ranges, one of which stands for every conference URI of a subdomain, so the HSS holds no number of them. |
| HSS, TS 32.409 4.2.1.2.2 | The HSS does not count the Public User Identities in the unregistered state, those whose profile an S-CSCF holds for services to a user who is not registered. | No panel reads them. |
| Diameter, TS 32.409 4.2.12.1 and 4.2.12.2 | The HSS counts neither the requests it receives nor the answers it sends. A role counts the requests that it sends to its peers and the answers that come back to them. | A request that arrives goes to the handler of the node, which answers for every Diameter role of the node at once, so no one role of the node is the one that received it. |
| HSS, TS 32.409 4.2.1.1 and 4.2.1.2 | An HSS whose subscribers live in a store outside its process serves none of the database usage gauges, so `http://hss:8085/metrics` then holds no `ims_hss_` series and the two HSS panels stay empty. | The numbers belong to that store, which every instance shares. An instance that starts and finds the subscribers of its configuration already there has added none of them, so it cannot work the numbers out from what it did itself. |

## Things to know

- **The scrapes show in the capture.** Prometheus joins the `ims` network, so every 15 seconds it sends one HTTP request to each node across the `ims0` bridge. In Wireshark, hide them with the display filter `!(tcp.port == 8085)`.
- **Target label `node`, not `role`.** The nodes put a `role` label on their own metrics, because one node can have several roles. Prometheus adds `job` and `instance` to every series; `prometheus.yml` adds `node` as well.
- **A failover counts twice.** `ims_diameter_requests_total` counts every attempt that goes out, which is where TS 32.409 counts a request: on its transmission. A request that the first instance of the HSS does not answer, or answers with DIAMETER_TOO_BUSY, goes to the next one and counts again, and `ims_diameter_answers_total` counts each answer that came back. The requests of a command less its answers are therefore the attempts that got no answer at all, which TS 32.409 4.1.6.3 calls the cause 'No Reply'.
- **An answer is counted under the command of its request.** The answer to a UAR counts under `command="UAR"`, not "UAA", so that the answers of a command divide into its requests. The `result` label names the result rather than numbering it, because a 3GPP application carries its own codes in Experimental-Result: 2001 there is DIAMETER_FIRST_REGISTRATION, while 2001 in Result-Code is DIAMETER_SUCCESS.
- **Counters do not go back to zero.** A Cumulative Counter of TS 32.401 starts from 0 at each granularity period. A Prometheus counter only grows, and Prometheus works out the increase over a period (`increase()`, `rate()`). The dashboard divides two increases to get the success ratio of TS 32.454.
- **Grafana does not phone home.** The environment variables turn off usage reporting, update checks and the news feed.

## Licences

| Image | Licence | Where it was checked |
| --- | --- | --- |
| `prom/prometheus:v3.15.1` | Apache-2.0 | `LICENSE` in the prometheus repository at the tag `v3.15.1`, and the label `org.opencontainers.image.licenses` in its `Dockerfile`. The footer of every page of prometheus.io says that all components are under the Apache 2 License |
| `grafana/grafana:13.3.0` | AGPL-3.0-only, with some directories under Apache-2.0 | `LICENSE` and `LICENSING.md` in the grafana repository at the tag `v13.3.0`, and the page "Licensing" on grafana.com |

**Which edition the Grafana image is.** The page "Run Grafana Docker image" in the Grafana documentation names two editions: `grafana/grafana` is Grafana Open Source and `grafana/grafana-enterprise` is Grafana Enterprise. The same page says that `grafana/grafana-oss` is no longer updated from 12.4.0 on and held the same images as `grafana/grafana`; Docker Hub has no tag `13.3.0` in `grafana/grafana-oss`. So `grafana/grafana:13.3.0` is the open source (AGPL) edition. The page calls Enterprise the recommended and default edition; it is free to run without a licence key. The FAQ of the page "Licensing" calls the Enterprise download a free-to-use binary under a proprietary licence, offered to users who do not mean to change Grafana's code. This directory uses the open source edition so that what runs is the code of the public repository.

**Base images.** The images also contain the packages of their base images, whose licences were not checked one by one. The layers listed on Docker Hub show that `prom/prometheus:v3.15.1` is built on a BusyBox root file system (BusyBox 1.38.0 with uClibc, made with Buildroot), and that `grafana/grafana:13.3.0` (amd64) is built on Alpine Linux 3.24.3 with a glibc added; the page "Configure a Grafana Docker image" says that the default images use Alpine. The project only runs the images and does not change or ship Grafana's code.

These are facts from public pages, not legal advice.
