// Package maintenance is the part of the maintenance API of a node
// that every role shares: the node's state, the administrative state
// of each role with the drain, the node's configuration with the
// settings that change while it runs, the level of the node's log, and
// the node's alarm list. The OAM server uses it through Client. A node
// serves it on its maintenance listener (cmd/ims) for the roles that
// supply a Function, which cmd/ims gathers from each role it runs.
//
// The API is this project's own. 3GPP standardises the management
// interface above the element manager (Itf-N), and leaves the one
// between the element manager and the network element to each product
// (TS 32.600 4.2.1.3). It borrows the state attributes of TS 28.622:
// the node is a ManagedElement (4.3.3), each role a ManagedFunction
// (4.3.4) with the administrativeState of 5.3.1 and the
// operationalState of 5.3.3. The transitions of the administrative
// state are those of ITU-T X.731 7.1.3, on which TS 28.622 builds.
//
// What differs between the roles comes from callbacks that the role
// supplies (Function): its operational state, how many users it still
// serves, a summary of its own, and the news of each transition. The
// node supplies the view of its configuration, in which every secret is
// a Secret, and the settings that may change (NewSetting). The node
// holds only the administrative state of its roles and the
// configuration generation; the role reads the administrative state
// with ManagedFunction.AdministrativeState before it takes a new
// registration or session.
//
// The bodies are JSON, and every error is ErrorBody,
// {"error": "..."}. Times are RFC 3339 in UTC. openapi.yaml, next to
// this file, describes each path with its bodies and answers in OpenAPI
// 3.1, and the tests check the handler against it.
//
//	GET  /maintenance/v1/state
//	     NodeState: the node, its build, start time, configuration
//	     generation and log level, and FunctionState for each role.
//	PUT  /maintenance/v1/functions/{role}/administrative-state
//	     AdministrativeStateRequest -> FunctionState.
//	     UNLOCKED unlocks, SHUTTINGDOWN shuts down (the drain, with an
//	     optional timeout_seconds), LOCKED locks. Asking for the
//	     present state changes nothing. 404 for a role that the node
//	     does not run, 409 for a shut down of a LOCKED role.
//	GET  /maintenance/v1/config
//	     Configuration: the effective configuration as the node's view
//	     shows it, secrets redacted, and the settings that may change
//	     while the node runs. 500 when the view holds a secret.
//	PATCH /maintenance/v1/config
//	     ConfigurationChangeRequest -> ConfigurationChange.
//	     All the settings change, or none; 400 names each refused
//	     setting in ErrorBody.settings. 409 for a generation that is
//	     not above the node's.
//	GET  /maintenance/v1/log-level
//	     LogLevel.
//	PUT  /maintenance/v1/log-level
//	     LogLevelRequest -> LogLevelChange.
//
//	GET  /maintenance/v1/alarms[?cleared=true|false][&min_severity=S]
//	     AlarmList: the alarm list of the node, the most severe first
//	     and the newest first. cleared=false leaves out the cleared
//	     records, cleared=true keeps only those; min_severity (CRITICAL,
//	     MAJOR, MINOR or WARNING) keeps that severity and the more
//	     severe ones, and INDETERMINATE. An unknown parameter is 400.
//	PATCH /maintenance/v1/alarms
//	     AckStatesRequest -> AckStatesChange. The acknowledgement of
//	     several alarms of the list at once, each as in the PUT below:
//	     all of them change, or none. 404 names an id that is not in
//	     the list.
//	PUT  /maintenance/v1/alarms/{id}/ack-state
//	     AckStateRequest -> AlarmRecord. ACKNOWLEDGED or
//	     UNACKNOWLEDGED, with the operator's name and the system the
//	     operator works through. 404 for an id that is not in the list.
//
// The alarm list follows TS 28.111 6 and 7.3: an alarm is a record of
// the attributes of 7.4.1, the node, its role and a peer as the object
// instance (AlarmIdentity). The role raises an alarm when it detects a
// fault and clears it when the condition is gone (RaiseAlarm,
// ClearAlarm); the operator only reads and acknowledges. A cleared
// alarm stays until it is acknowledged, an acknowledged one leaves when
// it clears (6.14), and the list is bounded (Config.MaxAlarmRecords).
// A cleared alarm that nobody acknowledges within
// Config.ClearedAlarmRetention the node acknowledges itself, as
// RetentionAckUserID.
// An alarm never names a subscriber, so the list grows with the links
// of the node and not with its users. The list lives in the
// process, like the administrative state: a restarted node starts with
// none, and its roles raise again what they still detect.
//
// A request body with an unknown field or a value out of range is 400.
// An unknown path under /maintenance/v1/ is 404 and a method that a
// path does not take is 405 with Allow, both with ErrorBody too.
//
// The package does not serve /metrics: the node mounts Handler and
// metrics.NewHandler on the same maintenance listener (see the
// example).
//
// SIMPLIFIED: TS 32.600 4.2.1.1. Only operators with the right
// authorisation may change the configuration of a network element.
// This API has no authentication and no authorisation, and runs over
// cleartext HTTP like every interface here, so that a capture shows
// the requests. It relies on the
// maintenance listener being reachable from the internal network of
// the nodes and the OAM server only, never published on the host or
// to the internet; Operator in a request is what the caller says, not
// a checked identity.
package maintenance
