package accessapi

import "strings"

// MessageType is the name of a NAS message. Each stub defines the names
// of its own messages. On the event stream the name is the event name.
type MessageType string

// Path is where a UE posts a message of type t, such as
// /ues/001010000000001/attach-request.
func Path(imsi string, t MessageType) string {
	return "/ues/" + imsi + "/" + strings.ReplaceAll(strings.ToLower(string(t)), " ", "-")
}

// EventsPath is the event stream of a UE: Server-Sent Events whose name
// is a MessageType and whose data is the message as JSON.
func EventsPath(imsi string) string { return "/ues/" + imsi + "/events" }

// SystemInformationPath is where a UE reads the identity of the cell it
// camps on.
const SystemInformationPath = "/system-information"
