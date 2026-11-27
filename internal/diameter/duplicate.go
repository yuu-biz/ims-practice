package diameter

import (
	"strconv"
	"strings"
	"time"
)

// A request that a sender transmits again, after its first transmission
// got no answer, keeps its End-to-End Identifier and carries the T flag
// (RFC 6733 3, 5.5.4). It may reach another instance of the server than
// the first one, and that one may have taken the first copy all the
// same. Origin-Host and the End-to-End Identifier together are how a
// server tells such a duplicate from a new request (RFC 6733 3). The T
// flag only says that a request may be one: RFC 6733 Appendix C has a
// server that checks for duplicates find them also when the first copy
// arrives after the second, so the check cannot wait for the flag.

// DuplicateWindow is how long a server keeps the record of a request
// that tells a later copy of it from a new request. A sender keeps each
// End-to-End Identifier unique for at least 4 minutes (RFC 6733 3); a
// record kept longer could take a new request that reuses the
// identifier for a copy of an old one.
const DuplicateWindow = 4 * time.Minute

// DuplicateKey names a request for the check: its Origin-Host, which is
// an FQDN and compares without case, and its End-to-End Identifier
// (RFC 6733 3).
func DuplicateKey(originHost string, endToEnd uint32) string {
	return strings.ToLower(originHost) + ";" + strconv.FormatUint(uint64(endToEnd), 16)
}
