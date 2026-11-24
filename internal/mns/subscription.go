package mns

// NtfSubscriptionControlSingle is one NtfSubscriptionControl: a
// subscription to notifications that a consumer creates below the
// SubNetwork (TS 28.622 4.3.22; the schema
// NtfSubscriptionControl-Single of TS28623_SubscriptionControlNrm.yaml).
// It is the Resource of the PUT that creates it and of the GET that
// reads it; deleting the object ends the subscription.
//
// SIMPLIFIED: TS 28.622 V20.4.0 4.3.21. The HeartbeatControl that a
// subscription may contain, which asks for heartbeat notifications, is
// left out: fault supervision does not need it.
type NtfSubscriptionControlSingle struct {
	Top
	Attributes *NtfSubscriptionControlAttributes `json:"attributes,omitempty"`
}

// NtfSubscriptionControlAttributes are the attributes of an
// NtfSubscriptionControl (TS 28.622 4.3.22.2). The file has four of the
// eight of 4.3.22.2: lastSequenceNo, administrativeState,
// operationalState and availabilityStatus are not in it.
type NtfSubscriptionControlAttributes struct {
	// NotificationRecipientAddress is the URI that the producer posts
	// the notifications to. It is mandatory in 4.3.22.2.
	NotificationRecipientAddress string `json:"notificationRecipientAddress"`
	// NotificationTypes are the kinds of notifications to send; none
	// means every kind the producer can send.
	NotificationTypes []NotificationType `json:"notificationTypes,omitempty"`
	// Scope selects the objects whose notifications are sent, below the
	// object that contains the subscription; none means all of them.
	Scope *Scope `json:"scope,omitempty"`
	// NotificationFilter is a Jex conditions expression (TS 32.161 7.6)
	// that a notification must meet to be sent; none sends all.
	NotificationFilter string `json:"notificationFilter,omitempty"`
}

// Scope selects the objects below a base object (TS 28.622 4.3.23,
// TS 32.158 6.1.2). It is the schema Scope of both
// TS28623_SubscriptionControlNrm.yaml, for the scope of a
// subscription, and TS28532_ProvMnS.yaml, for the scope query of a
// GET; there it is a JSON object in the query.
//
// SIMPLIFIED: TS 28.622 V20.4.0 4.3.23. The second form of the scope
// of a subscription, a dataNodeSelector, is left out: the subscriptions
// here select by notificationFilter.
type Scope struct {
	ScopeType ScopeType `json:"scopeType,omitempty"`
	// ScopeLevel is the level below the base object, for
	// BASE_NTH_LEVEL and BASE_SUBTREE.
	ScopeLevel *int `json:"scopeLevel,omitempty"`
}

// ScopeType is how far below the base object a scope reaches (TS
// 32.158 6.1.2; the schema ScopeType of TS28532_ProvMnS.yaml). No
// scopeType means BASE_ONLY.
//
// SIMPLIFIED: TS 32.158 V20.1.0 6.1.2. BASE_NTH_LEVEL and BASE_SUBTREE
// are left out: the alarm list and the subscriptions contain no
// objects, so a scope is either the object alone (BASE_ONLY) or all
// below the SubNetwork (BASE_ALL).
type ScopeType string

const (
	BaseOnly ScopeType = "BASE_ONLY"
	BaseAll  ScopeType = "BASE_ALL"
)
