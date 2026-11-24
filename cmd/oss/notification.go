package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// headerRequired are the attributes of NotificationHeader that its
// schema in TS28623_ComDefs.yaml requires (TS 28.532 11.0.2, 12.0.2.1).
var headerRequired = []string{"href", "notificationId", "notificationType", "eventTime", "systemDN"}

// bodyRequired are, for each notification the OAM sends, the
// attributes besides those of the header that the schema of its body
// in TS28111_FaultNrm.yaml requires. A notificationType missing here,
// such as notifyNewSecAlarm, is one of the oneOf of
// TS28111_FaultNotifications.yaml that the OAM never sends.
var bodyRequired = map[mns.NotificationType][]string{
	mns.NotifyNewAlarmType:     {"alarmId", "alarmType", "probableCause", "perceivedSeverity"},
	mns.NotifyClearedAlarmType: {"alarmId", "alarmType", "probableCause", "perceivedSeverity"},
	// The schema does not require perceivedSeverity, but TS 28.111
	// 8.4.2 makes it mandatory, and the OAM always writes it.
	mns.NotifyChangedAlarmGeneralType:      {"alarmId", "alarmType", "probableCause", "perceivedSeverity"},
	mns.NotifyAckStateChangedType:          {"alarmId", "alarmType", "probableCause", "perceivedSeverity", "ackState", "ackUserId"},
	mns.NotifyPotentialFaultyAlarmListType: {"reason"},
	mns.NotifyAlarmListRebuiltType:         {"reason"},
}

// The values of the enumerations of TS28111_FaultNrm.yaml that the
// notifications carry, as internal/mns has them.
var (
	alarmTypes = []mns.AlarmType{
		mns.CommunicationsAlarm, mns.QualityOfServiceAlarm, mns.ProcessingErrorAlarm,
		mns.EquipmentAlarm, mns.EnvironmentalAlarm, mns.OtherAlarm,
	}
	perceivedSeverities = []mns.PerceivedSeverity{
		mns.Indeterminate, mns.Critical, mns.Major, mns.Minor, mns.Warning, mns.Cleared,
	}
	ackStates          = []mns.AckState{mns.Acknowledged, mns.Unacknowledged}
	alignmentRequireds = []mns.AlarmListAlignmentRequirement{mns.AlignmentRequired, mns.AlignmentNotRequired}
)

// checkNotification reads the body of a POST as one of the bodies of
// TS28111_FaultNotifications.yaml and says what is wrong with it, if
// anything. The notificationType picks the body, which then decodes
// into its internal/mns type by decodeStrictly, with no attribute the
// type does not have: an attribute of another body, such as the reason
// of notifyAlarmListRebuilt in a notifyNewAlarm, is refused, and so is
// null. Each attribute the schema requires must be there, each
// enumeration must hold one of its values, and the href and systemDN
// of the header must be a canonical URI and a DN.
func checkNotification(body []byte) (mns.NotificationType, error) {
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(body, &attributes); err != nil {
		return "", fmt.Errorf("the body is not a JSON object: %v", err)
	}
	var notificationType mns.NotificationType
	if err := json.Unmarshal(attributes["notificationType"], &notificationType); err != nil || notificationType == "" {
		return "", errors.New("the body has no notificationType")
	}
	required, ok := bodyRequired[notificationType]
	if !ok {
		return notificationType, fmt.Errorf("%q is not a notification of fault supervision that the OAM sends", notificationType)
	}
	for _, name := range slices.Concat(headerRequired, required) {
		if _, ok := attributes[name]; !ok {
			return notificationType, fmt.Errorf("%s has no %s, which its schema requires", notificationType, name)
		}
	}
	header, err := decodeBody(notificationType, body)
	if err != nil {
		return notificationType, fmt.Errorf("%s: %w", notificationType, err)
	}
	if err := checkHeader(header); err != nil {
		return notificationType, fmt.Errorf("%s: %w", notificationType, err)
	}
	return notificationType, nil
}

// decodeBody decodes the body of the given notificationType into its
// internal/mns type, checks the attributes of the body, and returns its
// header.
func decodeBody(notificationType mns.NotificationType, body []byte) (mns.NotificationHeader, error) {
	switch notificationType {
	case mns.NotifyNewAlarmType:
		var n mns.NotifyNewAlarm
		if err := decodeStrictly(body, &n); err != nil {
			return n.NotificationHeader, err
		}
		return n.NotificationHeader, checkAlarm(n.AlarmId, n.AlarmType, n.PerceivedSeverity)
	case mns.NotifyClearedAlarmType:
		var n mns.NotifyClearedAlarm
		if err := decodeStrictly(body, &n); err != nil {
			return n.NotificationHeader, err
		}
		// TS 28.111 8.3.2.
		if n.PerceivedSeverity != mns.Cleared {
			return n.NotificationHeader, fmt.Errorf("perceivedSeverity is %q, not %q", n.PerceivedSeverity, mns.Cleared)
		}
		return n.NotificationHeader, checkAlarm(n.AlarmId, n.AlarmType, n.PerceivedSeverity)
	case mns.NotifyChangedAlarmGeneralType:
		var n mns.NotifyChangedAlarmGeneral
		if err := decodeStrictly(body, &n); err != nil {
			return n.NotificationHeader, err
		}
		// TS 28.111 8.4.2: a change to CLEARED is a notifyClearedAlarm.
		if n.PerceivedSeverity == mns.Cleared {
			return n.NotificationHeader, fmt.Errorf("perceivedSeverity is %q", mns.Cleared)
		}
		return n.NotificationHeader, checkAlarm(n.AlarmId, n.AlarmType, n.PerceivedSeverity)
	case mns.NotifyAckStateChangedType:
		var n mns.NotifyAckStateChanged
		if err := decodeStrictly(body, &n); err != nil {
			return n.NotificationHeader, err
		}
		if err := oneOf("ackState", n.AckState, ackStates); err != nil {
			return n.NotificationHeader, err
		}
		return n.NotificationHeader, checkAlarm(n.AlarmId, n.AlarmType, n.PerceivedSeverity)
	case mns.NotifyPotentialFaultyAlarmListType:
		var n mns.NotifyPotentialFaultyAlarmList
		err := decodeStrictly(body, &n)
		return n.NotificationHeader, err
	case mns.NotifyAlarmListRebuiltType:
		var n mns.NotifyAlarmListRebuilt
		if err := decodeStrictly(body, &n); err != nil {
			return n.NotificationHeader, err
		}
		// alarmListAlignmentRequirement is optional (TS 28.111 8.5.2).
		if n.AlarmListAlignmentRequirement != "" {
			return n.NotificationHeader, oneOf("alarmListAlignmentRequirement", n.AlarmListAlignmentRequirement, alignmentRequireds)
		}
		return n.NotificationHeader, nil
	}
	return mns.NotificationHeader{}, fmt.Errorf("no body for %q", notificationType)
}

// checkAlarm checks the attributes that the four notifications about
// one alarm record share.
func checkAlarm(alarmId string, alarmType mns.AlarmType, perceivedSeverity mns.PerceivedSeverity) error {
	if alarmId == "" {
		return errors.New("alarmId is empty")
	}
	if err := oneOf("alarmType", alarmType, alarmTypes); err != nil {
		return err
	}
	return oneOf("perceivedSeverity", perceivedSeverity, perceivedSeverities)
}

func oneOf[T ~string](name string, value T, values []T) error {
	if !slices.Contains(values, value) {
		return fmt.Errorf("%s is %q, which is none of %q", name, value, values)
	}
	return nil
}

// checkHeader checks that href is the canonical URI of an object (TS
// 32.158 4.2.4): an http or https URI whose path is the URI-LDN of a DN,
// with nothing after the path; that systemDN is a DN; and that the
// object of href is the object of systemDN or one below it, since the
// producer notifies about the objects of its own tree.
func checkHeader(h mns.NotificationHeader) error {
	href, err := url.Parse(h.Href)
	switch {
	case err != nil:
		return fmt.Errorf("href: %v", err)
	case href.Scheme != "http" && href.Scheme != "https", href.Host == "":
		return fmt.Errorf("href %q is not an http or https URI", h.Href)
	case href.RawQuery != "" || href.ForceQuery || href.Fragment != "" || href.User != nil:
		return fmt.Errorf("href %q has more than a scheme, a host and a path", h.Href)
	}
	object, err := mns.ParseURILDN(href.EscapedPath())
	if err != nil {
		return fmt.Errorf("href: %v", err)
	}
	system, err := mns.ParseDN(h.SystemDN)
	if err != nil {
		return fmt.Errorf("systemDN: %v", err)
	}
	if len(object) < len(system) || !slices.Equal(object[:len(system)], system) {
		return fmt.Errorf("href %q names %s, which is not below the systemDN %s", h.Href, object, system)
	}
	return nil
}
