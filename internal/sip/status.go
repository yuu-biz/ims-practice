package sip

// reasonPhrases are the reason phrases of RFC 3261 21, and of the codes
// that later RFCs add.
var reasonPhrases = map[int]string{
	100: "Trying",
	180: "Ringing",
	181: "Call Is Being Forwarded",
	182: "Queued",
	183: "Session Progress",
	199: "Early Dialog Terminated", // RFC 6228 11.1
	200: "OK",
	// RFC 3265 defined it; RFC 3428 7 has a relay that passes a MESSAGE
	// on answer with it, as the IP-SM-GW does (TS 24.341 B.5).
	202: "Accepted",
	300: "Multiple Choices",
	301: "Moved Permanently",
	302: "Moved Temporarily",
	305: "Use Proxy",
	380: "Alternative Service",
	400: "Bad Request",
	401: "Unauthorized",
	402: "Payment Required",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	406: "Not Acceptable",
	407: "Proxy Authentication Required",
	408: "Request Timeout",
	410: "Gone",
	413: "Request Entity Too Large",
	414: "Request-URI Too Long",
	415: "Unsupported Media Type",
	416: "Unsupported URI Scheme",
	420: "Bad Extension",
	421: "Extension Required",
	422: "Session Interval Too Small", // RFC 4028 6
	423: "Interval Too Brief",
	480: "Temporarily Unavailable",
	481: "Call/Transaction Does Not Exist",
	482: "Loop Detected",
	483: "Too Many Hops",
	484: "Address Incomplete",
	485: "Ambiguous",
	486: "Busy Here",
	487: "Request Terminated",
	488: "Not Acceptable Here",
	489: "Bad Event", // RFC 6665 8.3.2
	491: "Request Pending",
	493: "Undecipherable",
	500: "Server Internal Error",
	501: "Not Implemented",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Server Time-out",
	505: "Version Not Supported",
	513: "Message Too Large",
	600: "Busy Everywhere",
	603: "Decline",
	604: "Does Not Exist Anywhere",
	606: "Not Acceptable",
}

// ReasonPhrase returns the reason phrase RFC 3261 21 gives a status
// code, or "" for a code it does not define.
func ReasonPhrase(statusCode int) string { return reasonPhrases[statusCode] }
