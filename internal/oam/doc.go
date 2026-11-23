// Package oam is the OAM server: it operates every node of the network
// together through the maintenance API of each node (package
// maintenance).
// The oam role of cmd/ims runs it: it serves Handler under PathPrefix
// and the OAM screen (UIHandler) on the same listener.
//
// The OAM is the element manager of TS 32.101 and TS 32.102: the
// interface between it and the nodes is this project's own, as TS
// 32.600 4.2.1.3 leaves that interface to each product. It keeps no
// state of the nodes and none of the subscribers: every answer comes
// from the nodes when it is asked for.
//
// The nodes are an inventory that the configuration gives (Endpoint):
// a name, the roles and the URL of the maintenance listener. Several
// nodes may have the same role, so that a role can run as several
// instances. An operation on a role goes to
// every node of the role, at most Config.NodeConcurrency at a time and
// each within Config.NodeTimeout, and its answer (FanOut) has one entry
// for each node, with the node's answer or why it failed. A failed node
// is never left out, and the answer is 200 even when some nodes failed:
// the client reads succeeded and failed.
//
// Several OAM instances may run side by side. The handlers
// keep nothing between requests; what the OAM keeps is in its two
// stores, the configuration history (ChangeStore) and the backups
// (BackupStore), which the instances then share: the file stores of
// this package on one shared volume, or a shared database behind the
// same interfaces. A drain, a configuration change and a restore that
// two instances run at once are settled by the nodes and the HSS, which
// check the generation, the administrative state and each record.
//
// The bodies are JSON, and every error is maintenance.ErrorBody,
// {"error": "..."}, as in the maintenance API. Times are RFC 3339.
//
//	GET  /oam/v1/inventory
//	     Inventory: the nodes of the configuration.
//	GET  /oam/v1/state
//	     FanOut of maintenance.NodeState for every node.
//	GET  /oam/v1/nodes/{node}/state
//	GET  /oam/v1/roles/{role}/state
//	     The same for one node, or for every node of a role.
//	PUT  /oam/v1/nodes/{node}/functions/{role}/administrative-state
//	PUT  /oam/v1/roles/{role}/administrative-state
//	     maintenance.AdministrativeStateRequest -> FanOut of
//	     maintenance.FunctionState. SHUTTINGDOWN starts the drain,
//	     UNLOCKED cancels it, LOCKED locks.
//	GET  /oam/v1/nodes/{node}/log-level
//	GET  /oam/v1/roles/{role}/log-level
//	     FanOut of maintenance.LogLevel.
//	PUT  /oam/v1/nodes/{node}/log-level
//	PUT  /oam/v1/roles/{role}/log-level
//	     maintenance.LogLevelRequest -> FanOut of
//	     maintenance.LogLevelChange. The level belongs to the node, so
//	     a node with several roles changes for all of them.
//	GET  /oam/v1/nodes/{node}/config
//	GET  /oam/v1/roles/{role}/config
//	     FanOut of maintenance.Configuration, secrets redacted.
//
// The alarms are the alarm lists of the nodes (TS 28.111 6.5),
// which the OAM reads when it is asked and keeps none of. The answer
// has an entry for every node, as a FanOut, and the records of all the
// nodes merged, the most severe first, then the newest first. A node
// that does not answer is a failed entry: the OAM makes up no alarm for
// it (Alarms). The filter goes to every node as it is.
//
//	GET  /oam/v1/alarms[?cleared=true|false][&min_severity=S]
//	     Alarms. The filter is that of the maintenance API; a bad one
//	     is 400 before any node is asked.
//	PUT  /oam/v1/nodes/{node}/alarms/{id}/ack-state
//	     maintenance.AckStateRequest -> FanOut of
//	     maintenance.AlarmRecord: acknowledges an alarm of the node, or
//	     takes the acknowledgement back. An id that the node does not
//	     have is the node's 404 in the entry.
//
// A configuration change goes to one node or every node of a role, all
// with one new generation above the highest that they have, so that
// the nodes of a role keep one generation, and the OAM records what each
// node changed in its history (Config.History): the operator, the time
// and
// the value of each setting before and after. A rollback puts the
// previous values back as a new change with a new generation, so the
// history keeps both (TS 32.600 7.1 d). It is refused with 409, naming
// each setting, when a later change of the same node touched one of the
// settings, or when the node no longer has the value that the change
// set, unless the request forces it.
//
//	PATCH /oam/v1/nodes/{node}/config
//	PATCH /oam/v1/roles/{role}/config
//	      maintenance.ConfigurationChangeRequest without a generation ->
//	      FanOut of ChangeRecord.
//	GET   /oam/v1/nodes/{node}/config/history[?limit=n]
//	      History of the node, the newest first.
//	GET   /oam/v1/config/history/{id}
//	      ChangeRecord.
//	POST  /oam/v1/config/history/{id}/rollback
//	      RollbackRequest (or no body) -> ChangeRecord of the rollback.
//	POST  /oam/v1/config/change-sets/{id}/rollback
//	      RollbackRequest (or no body) -> FanOut of ChangeRecord: each
//	      record of one change of a role, rolled back.
//
// Subscriptions go to the HSS provisioning API
// (Config.HSSProvisioning), which the OAM calls as a client; the OAM
// keeps none. A bulk provisioning is a stream of SubscriptionRecord,
// one JSON object per line, and its answer a stream of StreamLine
// (application/x-ndjson): the outcome of each record, in the order of
// the lines, then the summary. The OAM reads, sends and reports a
// record at a time, at most Config.ProvisioningConcurrency at once, so
// a file of any length takes bounded memory. Each outcome is created,
// updated, unchanged, deleted, rejected (the record is wrong; the reason
// says why) or failed (the HSS did not take it; sending it again may
// succeed). Sending the same file again changes nothing: every record is
// unchanged. A dry run asks the HSS what it would do and changes
// nothing. The records reach the HSS in no set order, so a file names
// each subscription once.
//
//	POST   /oam/v1/provisioning[?dry_run=true]
//	       stream of SubscriptionRecord -> stream of StreamLine.
//	GET    /oam/v1/subscriptions/{impi}
//	       Subscription, as the HSS shows it, without credentials.
//	PUT    /oam/v1/subscriptions/{impi}[?dry_run=true]
//	       SubscriptionRecord -> Outcome: 201 created, 200 updated or
//	       unchanged.
//	DELETE /oam/v1/subscriptions/{impi}[?dry_run=true]
//	       Outcome: 200 deleted, 404 when there is none.
//	PUT    /oam/v1/subscriptions/{impi}/customized-alerting-tone
//	       AlertingToneRequest -> AlertingTone: the tone that the
//	       callers of the user hear while it rings (CAT, TS 24.182
//	       4.5.2), which the operator provisions and the user has no
//	       standard way to set (4.8). The rest of the subscription
//	       stays as it is.
//
// Every identity of a record must be test data, so that no real
// subscriber's data enters the network: a number is
// a test IMSI of PLMN 001/01 or a fictional NANP number of 555-0100 to
// 555-0199. The credentials of a record (password, ut_password, aka.k,
// aka.opc) go to the HSS and nowhere else: a report, an error or a log
// line never shows them, even when the HSS quotes them in a refusal.
//
// Test users are an attribute of the subscription in the HSS,
// and the HSS gives each subscription a service profile: the one named
// on it, else the test users' one for a test user, else the default
// one. A feature is enabled for test users only by making a profile
// that has it the test users' one, a configuration change of every HSS
// node, in the history like any other. Admitting test users only is the
// HSS setting hss.admission. The answer to "is this feature active for
// this subscriber" comes from the subscription and the configuration of
// an HSS node, never from a walk over the subscribers.
//
//	GET  /oam/v1/test-users
//	     TestUsers: the admission, the two profiles, the features of
//	     each profile.
//	PUT  /oam/v1/test-users/service-profile
//	     ServiceProfileRequest -> FanOut of ChangeRecord.
//	PUT  /oam/v1/test-users/admission
//	     AdmissionRequest -> FanOut of ChangeRecord.
//	PUT  /oam/v1/subscriptions/{impi}/test-user
//	     TestUserRequest -> Subscription.
//	GET  /oam/v1/subscriptions/{impi}/features/{feature}
//	     FeatureState, with the reason when it is not active.
//
// A backup holds the persistent data of the network, section by
// section (Section): the HSS subscriptions, credentials and SQN_HE
// included, through a paged export of the HSS provisioning API. It is
// one archive of JSON lines in the BackupStore, with a manifest that
// records each step. A restore checks the archive whole first, then
// either says what each record would do (a dry run, the diff) or puts
// each back through the same PUT as a provisioning. Restoring twice
// changes nothing the second time. A restored subscription is Not
// Registered, and its SQN_HE is the one of the backup. Registrations,
// dialogs and other state that lives only while a node runs are not
// backed up: the UEs and the restoration procedures rebuild them.
//
//	POST   /oam/v1/backups
//	       BackupRequest (or no body) -> stream of StreamLine: each
//	       step, then the Manifest.
//	GET    /oam/v1/backups
//	       Backups, the newest first.
//	GET    /oam/v1/backups/{id}
//	       Manifest, with each restore from it.
//	POST   /oam/v1/backups/{id}/restore[?dry_run=true]
//	       RestoreRequest (or no body) -> stream of StreamLine: the
//	       steps, an outcome per record, then the Summary. 409 for a
//	       backup that did not complete.
//
// Status codes: 400 for a body that does not parse, has an unknown
// field or a value out of range; 404 for a node, role, subscription,
// record, backup or feature that does not exist, or an unknown path;
// 405 with Allow for a method that a path does not take; 409 for a
// record that the HSS refuses because one of its identities belongs to
// another subscription, a rollback in conflict (with
// ErrorBody.settings) or a restore of a backup that did not complete;
// 502 when the HSS or a node does not answer or fails; 503 when the OAM
// has no HSS provisioning API, history, backup store or HSS node.
//
// SIMPLIFIED: TS 32.600 4.2.1.1. Only operators with the right
// authorisation may change the configuration of the network. The OAM
// API has no authentication and no authorisation, and runs over
// cleartext HTTP like every interface here, so that a capture shows
// the requests, as the maintenance
// API does. It relies on the OAM listener being reachable from the
// internal network only, never published on the host or to the
// internet; operator in a request is what the caller says, not a
// checked identity.
package oam
