# Taking a node out for maintenance, and losing it

When an operator wants to stop a node for maintenance, the first step is to take the node out of service (shut down). A node taken out refuses new work and carries the work it has begun to its end. The senders of the refused work send it to another instance. The answer to the operator's request comes at once, but it means "the node has started to go out of service". Whether the work in progress has finished shows afterwards, in whether the state of the node becomes `LOCKED`. Even then, `LOCKED` means the end of the work only when no deadline cut it.

When the process of a node is stopped, when it disappears all at once, or when the store is lost, the node has no choice about its work. What is left depends on where the state of each piece of work lives. State in a store that the instances share can be taken over by another instance. State in the process goes with the process. State that the other node holds comes back when that node sends again.

This chapter compares four cases for five kinds of work: new registrations, new calls, calls in progress, Diameter requests in flight, and short messages held for delivery. The four cases are: taking the node out for maintenance, stopping it in order, its process disappearing, and losing the store.

## The setups and what this chapter assumes

There are two setups. In `deploy/compose` each role runs on one node and keeps its state in the memory of the process. With `deploy/redundancy/redundancy.yaml` on top, the roles that hold state keep it in a shared Valkey store, and a node created again under the same name finds there what its earlier process left.

Every node of the Compose project serves a maintenance API on port 8085. The OAM server can send the same request to every node of a role. The API is this project's own: the interface between the manager and the managed nodes is left to each product (TS 32.600 4.2.1.3).

Where state lives and how several instances take it over is in [What several nodes and failures hand over](design-shared-state.md); what happens when an application server does not answer is in When an application server is not available; and retransmitted and duplicated short messages are in Retransmitted and duplicated SMS.

## Taking a node out of service, and the answer

Each role has two states. The administrative state is set by the operator, and the operational state by the role itself. The values are those of TS 28.622 5.3.1 and 5.3.3, and the transitions follow ITU-T X.731 7.1.3.

| Administrative state | Meaning | When this implementation enters it |
| --- | --- | --- |
| `UNLOCKED` | May serve its users | At start, and on an unlock |
| `SHUTTINGDOWN` | May serve only the users it has | On a shut down, while users are left |
| `LOCKED` | Serves no one | On a lock; when a `SHUTTINGDOWN` role has no user left; when the deadline of a shut down has passed |

The operational state is `ENABLED` or `DISABLED`. The S-CSCF and the I-CSCF, for example, are `DISABLED` while no instance of the HSS takes their Cx requests.

A role that gets a shut down changes its state like this.

[internal/maintenance/state.go](../../../internal/maintenance/state.go)

```go
	m.shutdowns++
	if users == 0 {
		m.enter(Locked)
		m.dispatchAndUnlock()
		return nil
	}
	m.enter(ShuttingDown)
	if timeout > 0 {
		m.deadline = m.clock.Now().Add(timeout)
		shutdown := m.shutdowns
		m.timer = m.clock.AfterFunc(timeout, func() { m.deadlinePassed(shutdown) })
	}
	m.dispatchAndUnlock()
```

`users` is the number of users that the role counted just before. With none, the role is `LOCKED` at once. With some, it becomes `SHUTTINGDOWN`, and when the request has a deadline (`timeout_seconds`), a timer makes it `LOCKED` at that time. From then on the role reports each piece of work it ends, and it becomes `LOCKED` when the count reaches zero. The state changes on the spot, so a role that reads the state on each request refuses new work from its next request on. `dispatchAndUnlock` tells the change to the roles that want to hear of it, such as the HSS and the SC.

The answer to the operator's request carries the state at that moment.

| State in the answer | What it tells | What it does not tell |
| --- | --- | --- |
| `SHUTTINGDOWN` | The node took the request and has started to refuse new work. Work it counts is left | When it will end |
| `LOCKED` (the last piece of work ended) | No work that this process counts is left | Work it does not count; the work of other instances |
| `LOCKED` (the deadline passed, or a lock) | It serves no one any more | The work that was left has been cut |

So the success of the operation and the end of the work are two things. After a `SHUTTINGDOWN`, the operator keeps reading the state until it is `LOCKED`. As `LOCKED` comes in two kinds, whether a deadline cut the work is told by whether one was set, and by the log. When the OAM server sends a request to each node of a role, the answers come back per node too: one node of a role may fail to answer while the others succeed.

What `LOCKED` promises depends on what the role counts as its users. It counts what this process holds, not by searching the store, so that nothing walks all the subscribers.

| Role | What it counts as users |
| --- | --- |
| P-CSCF | The IP associations of the UEs that use this instance |
| I-CSCF, S-CSCF | The transactions in progress |
| HSS | The Cx and Sh requests it is answering |
| MMTel AS | The calls it serves, and the Ut requests it is answering |
| IP-SM-GW | The deliveries it has in hand |
| Service Centre (SC) | The OFRs and ALRs it is answering, and the recipients it holds a lease on |
| Media relay | The contexts it holds |

The `LOCKED` of an S-CSCF says only that no transaction is in progress. The registrations are in the store that every instance shares, so an S-CSCF instance does not count them as its users. The `LOCKED` of a media relay says more: no RTP passes through this process any more. Stopping the node then cuts no audio.

The administrative state lives in the process. A node that starts again starts `UNLOCKED`.

## What each role refuses and what it carries on

What a role refuses and what it carries on while out of service depends on what its work is tied to.

| Role | New work it refuses | Work it carries on | How the sender learns |
| --- | --- | --- | --- |
| P-CSCF | The REGISTERs that start or refresh a registration | The calls of the UEs registered through this instance, the requests within a dialog, the deregistrations | 503, Retry-After 300 s |
| I-CSCF | Every request | A CANCEL for an INVITE it is forwarding (the transaction takes it) | 503, Retry-After 30 s |
| S-CSCF | Requests outside a dialog (REGISTER and a new INVITE) | Requests within a dialog, and requests that an AS sends back with the original dialog identifier | 503, Retry-After 30 s |
| HSS | New connections (it stops listening) | The requests on its connections while `SHUTTINGDOWN`; at `LOCKED` it closes them | The DPR when it becomes `LOCKED` (Disconnect-Cause `REBOOTING`) |
| MMTel AS | The INVITE of a new call, an INVITE that creates a conference, new Ut requests | Requests within a dialog, the diversion of a call in progress, joining a conference it is the focus of | 503, Retry-After 30 s |
| IP-SM-GW | Third-party REGISTERs, short messages from a UE | Delivery reports, the NOTIFYs of reg event, the deliveries that the SC hands it | 503, Retry-After 30 s |
| SC | New connections (it stops listening), deliveries it would start itself | OFRs and ALRs that arrive on its connections, the delivery attempts in hand | The DPR when it becomes `LOCKED` |
| Media relay | `Add`, which makes a context | `Modify`, `Subtract` and the like on contexts that exist | 503 over the control API (error 502 over H.248) |

At the S-CSCF the decision is a few lines where requests come in.

[internal/scscf/scscf.go](../../../internal/scscf/scscf.go)

```go
func (s *SCSCF) OnRequest(st *sip.ServerTransaction) {
	s.transactions.Add(1)
	defer s.transactionEnded()
	if !s.unlocked() && !s.carriesOn(st.Request) {
		s.refuse(st)
		return
	}
	if st.Request.Method != sip.REGISTER {
		s.session(st)
		return
	}
	s.register(st)
```

Each request first counts as a transaction in progress. When it ends, `transactionEnded` takes it off the count and tells whoever waits for the shut down. When the S-CSCF is not `UNLOCKED` and the request does not carry on with something begun, the S-CSCF refuses it with 503. `carriesOn` tells which requests carry on.

[internal/scscf/maintenance.go](../../../internal/scscf/maintenance.go)

```go
func (s *SCSCF) carriesOn(req *sip.Message) bool {
	return ims.IsInDialog(req) || s.sentBack(req)
}
```

These are the requests within a dialog and the requests that an AS sends back. The S-CSCF serves both even after it is `LOCKED`: refusing the BYE of a call in progress would keep that call from ending properly. A refresh of a registration is a new REGISTER outside a dialog, so it is refused. The S-CSCF instances share the registrations, so when there are several, the I-CSCF sends the refused REGISTER to the next one (RFC 3263 4.3), and that one accepts it.

The P-CSCF refuses REGISTERs only, and among them lets the deregistrations through. The INVITEs of calls and the requests within a dialog are not refused while it is out of service.

[internal/pcscf/pcscf.go](../../../internal/pcscf/pcscf.go)

```go
	if !p.unlocked() && procedure.kind != ims.DeRegistration {
		respond(p.refusal(req, log))
		return
	}
```

A UE sends every request to the P-CSCF that it registered through, so the users of a P-CSCF do not move to another instance by themselves. A P-CSCF taken out refuses the REGISTERs that start or refresh a registration with 503, and waits for its UEs to register through another P-CSCF. Its Retry-After of 300 s is longer than the UE's Timer F (128 s), because TS 24.229 5.1.1.2.1 has a UE that gets a 503 with a Retry-After longer than that treat its P-CSCF as unavailable. No specification gives the 300 s; it is this implementation's choice.

The HSS cannot refuse in a response as SIP does. The CSCFs and the application servers reach the HSS over Diameter connections, so the HSS acts on the connections instead.

[internal/hss/maintenance.go](../../../internal/hss/maintenance.go)

```go
		AdministrativeStateChanged: func(_, to maintenance.AdministrativeState) {
			d.Change(to == maintenance.Unlocked, to == maintenance.Locked)
		},
```

`d` is the part that handles the Diameter listener. The first argument says whether to listen, the second whether to end the connections the node has. `SHUTTINGDOWN` stops taking new connections and keeps the ones it has. `LOCKED` sends a DPR to each peer and closes the connection.

## Five kinds of work in four cases

The question of this chapter is answered in a table. The sections after it give the reason for each cell.

| Work | Taken out for maintenance | Stopped in order | Process disappears | Store lost |
| --- | --- | --- | --- | --- |
| New registrations | Refused with 503; the sender sends them to the next instance | Not taken; the sender goes to the next instance once it learns there is no answer | The same | 500 while the store cannot be read. When its contents are lost, the registration is gone, and calls to the UE do not arrive until the UE sends REGISTER again |
| New calls | Refused with 503; the sender sends them to the next instance. When an AS refuses, the S-CSCF tries the next AS, and with none left follows the DefaultHandling of the iFC | Not taken | The same | 500 while the store cannot be read |
| Calls in progress | Served to their end | The state of the proxies (P-CSCF, S-CSCF) is in the store, and with a shared store another instance can carry on. The calls of the B2BUAs (MMTel AS, IBCF) stop with the process | The same, with no DPR and no BYE | For a call whose record is lost, the node cannot release the relay context and the rest, even when the BYE passes |
| Diameter requests in flight | While `SHUTTINGDOWN`, answered before the DPR. A lock or a deadline drops the answers and closes | A DPR at the end closes the connection | The connection breaks; the sender sends the requests to the next instance | The HSS answers DIAMETER_UNABLE_TO_COMPLY |
| Short messages held | They stay in the store, and an instance in service delivers them | The attempts are cut short, and their recipients go at once to an instance in service | Another instance takes over once the lease has run out | What was held is gone |

### New registrations and new calls

A node taken out of service refuses new work. In SIP that is a 503 with Retry-After, which asks the client not to send that server another request for that many seconds (RFC 3261 21.5.4). A sender that gets a 503 sends the request to the next server that DNS gave it (RFC 3263 4.3).

When the sender is a proxy, it does not pass the 503 upstream (RFC 3261 16.7 step 6): passed on, the 503 would say that the proxy itself takes no request. This network has no other instance to try. So what the UE receives is the 500 that the I-CSCF sends in place of the 503 of the S-CSCF, or the 504 of a P-CSCF that could forward the REGISTER to no I-CSCF (TS 24.229 5.2.2.1 item 7).

The 503 of the MMTel AS goes back to the S-CSCF. The S-CSCF tries the next instance of the AS, and with none left follows the DefaultHandling of the iFC: SESSION_CONTINUED goes on with the call without the AS, and SESSION_TERMINATED ends the call.

A node stopped in order, and a node whose process has disappeared, answer nothing. The sender tries the next instance once it learns that it cannot connect or that no answer comes (RFC 3263 4.3). The difference is how long the sender waits. A refusing 503 arrives at once; with no answer over UDP, the sender waits until the timer of its transaction runs out.

### Calls in progress

A node taken out of service serves its calls in progress to their end. The difference shows when the node is lost, and it depends on where the state of the call lives.

- **The P-CSCF and the S-CSCF are proxies.** What they need to handle a call (for the P-CSCF the record of the dialog and the ID of the relay context; for the S-CSCF the registration and the continuation of the iFC evaluation) is behind a `Store`. With a shared store, another instance reached by the same name can handle the next request. With the state in the memory of the process, it goes with the process.
- **The MMTel AS is a B2BUA.** The two dialogs and the transactions of a call are in the process of the instance that took its INVITE, and the call stops when that process stops. An instance created again under the same name does not rebuild the dialogs from the call records in the store. It answers each request within such a call with 481 (Call/Transaction Does Not Exist), and the parties then end the call (RFC 3261 12.2.1.2). This is a simplification, written in a `SIMPLIFIED:` of the code. Stopped in order, the AS also just closes its UA, with no BYE for its calls.
- **The IBCF is a B2BUA too.** The difference is that an instance created again under the same name reads the call records that its earlier process left in the store, sends a BYE in each of the two dialogs, and releases the TrGW context (TS 24.229 5.10.2.4, 5.10.3.4). The records last only as long as the store that holds them. With the records in the memory of the process, the new IBCF ends nothing, and the TrGW context stays.
- **The media relay holds the RTP sockets.** When its process stops, the audio of the call stops. The P-CSCF has no way of learning this, and does not end the call. A relay that comes back at the same address and opens the contexts again from its store carries the audio on. One that does not come back leaves only the signalling until a party hangs up. When the next offer finds the context gone, the P-CSCF releases the call there ([When a media renegotiation fails](design-media-rollback.md)).

### Diameter requests in flight

After a shut down, a `SHUTTINGDOWN` HSS keeps its connections and answers the requests that arrive. When no request is being answered, it becomes `LOCKED`, and the DPR goes out behind that last answer. A DPR says that the node is about to disconnect, not that it drops what it holds (RFC 6733 5.4).

Its peers have not been told to stop, though, and go on sending requests. While registrations keep arriving, the count may never reach zero. The deadline bounds that. When the deadline passes, or on a lock, the HSS closes its connections with requests still being answered. An answer that was on its way out is dropped, and the sender gets none.

A sender left with a request that has no answer sends it again to the next instance once it learns that the connection has gone. That is the failover of RFC 6733 5.5.4, and the request sent again carries the T flag. `diameter.PeerGroup` of this project does so. A request that names one instance in Destination-Host cannot be sent elsewhere. The UAR, the LIR, the MAR, the SAR for an unregistered user and the Sh requests carry only a Destination-Realm, so any instance of the realm may take them.

The Disconnect-Cause of the DPR is `REBOOTING`, which lets the peer connect again later (RFC 6733 5.4.3). An HSS that is unlocked gets its peers back at their next attempt.

A node stopped in order also closes its Diameter connections with a DPR, last. When the process disappears, there is no DPR. The peer goes on using the connection until the kernel closes the TCP connection, or until the watchdog of the connection (DWR and DWA) notices that no answer comes. Either way, the requests without an answer go to the next instance by failover. The same request may then arrive twice, and the receiver tells duplicates apart by the End-to-End Identifier and the Origin-Host (RFC 6733 5.5.4).

When the HSS cannot read its store, it answers each request with DIAMETER_UNABLE_TO_COMPLY. It does not stop the role as a whole.

### Short messages held

The SC holds its short messages in the store, per recipient. Any instance takes a short message, and any instance delivers it. What ties a piece of work to one instance is only the lease on a recipient that an instance holds while it attempts a delivery.

An SC taken out of service finishes the delivery attempt it is making. After that it claims neither the next short message of that recipient nor any other recipient. In the store that recipient is due at once, and an instance in service delivers the rest. A `LOCKED` SC still takes an OFR that arrives on a connection it has, and puts the short message in the store; an instance in service delivers it. When no other instance shares the store, the SC holds what arrives until it is unlocked.

On a lock, and when the deadline of a shut down passes, the node closes its connections, so a TFR that waits for its TFA gets no answer. The SC takes that as a system failure that may pass, and keeps the short message for its next retry. Stopped in order, the SC cuts its attempts short and hands their recipients at once to an instance in service. When its process disappears, nobody claims the recipient until the lease runs out; then another instance takes it over.

In each case the outcome of the attempt in progress is not known, the short message stays in the store, and it goes out again. But the serving node may have delivered it already, and the recipient then gets the same short message twice. That is the subject of Retransmitted and duplicated SMS.

When the contents of the store are lost, the short messages that were held are gone. Nobody delivers them, and the sender hears of no outcome.

## An orderly stop is not a drain

A node that gets SIGTERM stops in this order.

[cmd/ims/main.go](../../../cmd/ims/main.go)

```go
func stopNode(work, stacks []func(), stopDiameter func()) {
	var stopping sync.WaitGroup
	for _, stop := range work {
		stopping.Go(stop)
	}
	stopping.Wait()
	for _, stop := range stacks {
		stop()
	}
	stopDiameter()
}
```

`work` is the work that the roles do of their own accord: the sweeps of the registration expiries, and the deliveries of the SC. It stops first, and the node waits for all of it. A sweep claims no new expiry and finishes the NOTIFYs and SARs of the expiries it has claimed; an expiry that falls due from then on belongs to another instance. The SC cuts its attempts short. Then the SIP stacks close. The transactions still running end, and the roles take that for no answer of the peer. Diameter closes last, since the requests that came over SIP may still be using it.

This order keeps intact the state that the instances share. It is not an order that carries work to its end: an orderly stop refuses no new work and waits for no call in progress. To stop a node once its work is done, shut it down first, wait for `LOCKED`, and then stop it.

## The ways compared

The ways to take a node out fall into four. This implementation combines them per role.

| Way | What it does | When it fits | What it gives up | Roles that use it here |
| --- | --- | --- | --- | --- |
| Drain | Refuse new work and stop once the work in progress has ended | The work is tied to the instance and cannot move | No bound on the time: the P-CSCF waits until its UEs register again, the AS until its calls end. A deadline cuts the work left | P-CSCF, MMTel AS, media relay |
| Refuse new work only | Refusing is enough, and the node can stop at once | The instance holds nothing of its own, or everything it holds is in a shared store | The time the refused senders take to send again | I-CSCF, S-CSCF |
| Hand over | Another instance reads the state of the work and carries it on | The state is in a shared store, and every instance is reached by the same name | An attempt in progress is made again, and duplicates can happen | SC, IP-SM-GW, the expiries of the S-CSCF's registrations |
| Let the senders retry | Refuse, or close the connection, and the sender picks the next instance | The sender knows a next instance (DNS SRV, a list of peers, a list of relays) | With no next instance, the user sees the failure | SIP's 503, Diameter's DPR, the relay's 503 to `Add` |

**Refusing new work is enough** for the I-CSCF and the S-CSCF. The I-CSCF keeps nothing from one request to the next and does not record-route, so no later request comes back to it. The S-CSCF shares its registrations in the store, so only its transactions in progress are tied to the instance.

**A drain is needed** where the work cannot move off the instance. A context of the media relay exists only in the process that opened its ports. A call of the MMTel AS is dialogs in the process of that instance. The users of a P-CSCF stay until the UE itself registers again.

**Handing over** needs the state to be in a store that every instance can read. For another instance to take over a call of a B2BUA, it would have to accept a dialog that it did not set up, go on with CSeq numbers above any that the earlier process used, and set up the relaying of both legs again. The MMTel AS and the IBCF of this implementation do not go that far. The IBCF ends the calls of its earlier process instead of taking them over.

**Letting the senders retry** has a constraint from the standard on how to refuse. An HSS that answered every new request with DIAMETER_TOO_BUSY can be imagined, but that answer may be used only when a request names one server and that server cannot serve it (RFC 6733 7.1.3). Most requests to the HSS name no host, so the answer would be wrong. That is why the HSS and the SC close their connections with a DPR.

## The choice of this implementation and its limits

- **The HSS and the SC do not listen on port 3868 while out of service.** RFC 6733 2.1 requires a node to take connections on this port at all times. This implementation departs from that, only while the node is out of service. The `REBOOTING` it sent says that the node is about to reboot (5.4.3), and a rebooting node holds no socket either, so a refused connection and another attempt after Tc are what the peer expects. It is a deliberate departure from the standard, not a choice the standard leaves open.
- **The media relay tells no controller that it is out of service.** A real IMS-AGW or MRFP tells its controllers with a ServiceChange with the method `GRACEFUL`. Here a controller learns of it when its next `Add` is refused (a `SIMPLIFIED:` in the code; TS 29.334 5.17.3.2, TS 23.333 8.30).
- **An MMTel AS created again does not carry on the calls of its earlier process.** It answers the requests within them with 481 (a `SIMPLIFIED:` in the code; RFC 3261 12.2.2).
- **The IBCF ends the calls of an earlier process only in an instance that comes back under the same name.** The records of an instance that never comes back stay in the store (a `SIMPLIFIED:` in the code; TS 24.229 5.10.2.4, 5.10.3.4).
- **The administrative state lives in the process.** A node that was taken out and is created again starts `UNLOCKED`, and takes new work at once.
- **This network has one instance of each role.** A refused sender has no next instance, so while a node is out, users see failures.
- **The Compose scenarios check the behaviour with no work in progress.** `scripts/e2e-maintenance-sip.sh`, `scripts/e2e-maintenance-hss.sh` and `scripts/e2e-maintenance-sms.sh` take a node out while it has nothing in progress, and check in the capture that the node, `LOCKED` at once, refuses new work, and that it comes back after the unlock. `scripts/e2e-fault.sh` stops, kills and recreates the MMTel AS node, which holds no registration and no call, and checks that the fault agent works. What happens with work left is what the Go tests below check.

## What this explanation rests on

The explanation of this chapter was checked against the following code and tests. Each test was run and passes as a Go test of this repository. The Compose scenarios were read, not run, for this chapter.

- `TestShutDownWaitsForTheLastUserToQuit`, `TestShutDownWithoutUsersLocksAtOnce` and `TestShutDownDeadlineLocksWithUsersLeft` in `internal/maintenance/state_test.go`. With users left the role is `SHUTTINGDOWN` and becomes `LOCKED` when the last one quits; with none it is `LOCKED` at once; when the deadline passes it is `LOCKED` with one user still left.
- `TestAShutDownSCSCFRefusesOnlyWhatIsNew` and `TestAShutDownWaitsForTheTransactionsInProgress` in `internal/scscf/maintenance_test.go`. A new REGISTER and a new INVITE get 503 with Retry-After 30 while a BYE within a dialog goes through; a REGISTER in progress gets its answer before the S-CSCF becomes `LOCKED`.
- `TestAShutDownPCSCFRefusesOnlyTheRegistrations` in `internal/pcscf/maintenance_test.go`. A REGISTER gets 503 with Retry-After and is not forwarded; the requests and the deregistration of the registered UE go through; the P-CSCF is `LOCKED` once the IP association is gone.
- `TestAShutDownICSCFRefusesEveryRequest` in `internal/icscf/maintenance_test.go`. Every request gets 503, and nothing is forwarded.
- `TestPeerGroupMovesToTheSecondHSSWhenTheFirstIsDrained` and `TestShutDownWaitsForTheRequestsItTook` in `internal/hss/maintenance_test.go`. The HSS taken out closes its connections with a DPR, the client's next request goes to the second instance, and after the unlock the first is used again; a request taken before the shut down is answered before the connection closes.
- `TestAShutDownASRefusesANewCall`, `TestAShutDownASServesTheDiversionOfACallInProgress` and `TestAShutDownWaitsForTheCallsOfTheAS` in `internal/mmtel/maintenance_test.go`. A new call is refused, the diversion of a call in progress is served, and the AS stays `SHUTTINGDOWN` until its call has ended.
- `TestAShutDownSCFinishesTheAttemptInHandAndLeavesTheQueue` and `TestALockedSCLeavesEveryDeliveryToAnInstanceInService` in `internal/smsc/maintenance_test.go`. The SC finishes the attempt in hand before it is `LOCKED`, and another instance delivers the rest; a `LOCKED` SC takes OFRs and ALRs but delivers nothing itself.
- `TestAShutDownRelayMakesNoContext`, `TestTheDrainWaitsForTheContextsOfTheRelay` and `TestTheContextsOfAnEarlierProcessCountAsUsers` in `internal/agw/maintenance_test.go`. The relay makes no new context, stays `SHUTTINGDOWN` until its context is subtracted, and counts the contexts it opened again from its store as users.
- `TestAnExpiryThatComesWhileANodeStopsGoesToAnotherInstance` in `cmd/ims/stop_test.go`. An S-CSCF node that is stopping claims no expiry, and another instance on the same store handles it.
- `TestRestartedInstanceEndsTheCallsItLeft` in `internal/ibcf/network_test.go`. An IBCF instance that starts again sends a BYE in both dialogs of a call of its earlier process and releases the TrGW context.
- `TestACallGoesOnWhenItsRecordCannotBeWritten` and `TestAnInstanceThatCannotReadItsCallsDoesNotStart` in `internal/ibcf/storefailure_test.go`. A call goes on when the records after its answer cannot be written, and an instance that cannot read the records of its earlier process does not start.
- `TestRegisterWhenTheStoreFails` in `internal/scscf/storefailure_test.go` and in `internal/pcscf/storefailure_test.go`, and `TestInviteWhenTheStoreFails` in `internal/pcscf/storefailure_test.go`. While the store cannot be used, a REGISTER, and an INVITE from the UE or to it, get 500 and go no further.
- `TestRequestsWhoseSubscriberCannotBeRead` in `internal/hss/storefailure_test.go`. An HSS that cannot read its store answers each request with DIAMETER_UNABLE_TO_COMPLY.

## If you want to run it yourself

You do not need to run anything to read this chapter. The steps to take each node out of service, and what a capture shows, are in these guide sections: [Take the S-CSCF out of service](../../guide/en/registration.md#take-the-s-cscf-out-of-service), [Take the P-CSCF out of service](../../guide/en/registration.md#take-the-p-cscf-out-of-service), [Take the I-CSCF out of service](../../guide/en/registration.md#take-the-i-cscf-out-of-service), [Take the HSS out of service](../../guide/en/registration.md#take-the-hss-out-of-service), [Take the MMTel AS out of service](../../guide/en/mmtel.md#take-the-mmtel-as-out-of-service), [Take the IP-SM-GW out of service](../../guide/en/sms.md#take-the-ip-sm-gw-out-of-service), [Take the Service Centre out of service](../../guide/en/sms.md#take-the-service-centre-out-of-service) and [Take a media relay out of service](../../guide/en/call.md#take-a-media-relay-out-of-service). What an IBCF instance that starts again does is in [When an IBCF instance starts again](../../guide/en/interconnect.md#when-an-ibcf-instance-starts-again). How to stop, kill and recreate a node with a fault is in [the README of deploy/chaos](../../../deploy/chaos/README.md).
