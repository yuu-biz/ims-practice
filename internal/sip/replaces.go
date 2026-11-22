package sip

// ReplacedDialog is RFC 3891 3 at the UAS for an INVITE with a Replaces
// header field: the dialog of this UA that the new call is to take the
// place of. It returns nil and 0 for an INVITE without Replaces, and
// nil with the status code of the refusal when the INVITE cannot
// replace anything:
//
//   - 400 (Bad Request) for more than one Replaces, or one that does
//     not parse;
//   - 481 (Call/Transaction Does Not Exist) when it names no dialog of
//     an INVITE at this UA, or an early dialog that this UA did not
//     start;
//   - 486 (Busy Here) when it names a confirmed dialog but asks to
//     replace an early one only;
//   - 603 (Decline) when it names a dialog that has ended, so that the
//     user is not alerted for a call that is no longer wanted. The UA
//     knows of an ended dialog for 64*T1 (UA.forget), and a Replaces
//     that comes later finds no dialog.
//
// The user of the call checks that whoever sent
// the INVITE may replace the dialog, for instance by its Referred-By,
// accepts the new INVITE, and then ends the replaced dialog: with BYE
// when it is confirmed, with CANCEL of its INVITE when it is early.
func (c *IncomingCall) ReplacedDialog() (*CallDialog, int) {
	values := c.Request.GetAll("Replaces")
	switch len(values) {
	case 0:
		return nil, 0
	case 1:
	default:
		return nil, 400
	}
	r, err := ParseReplaces(values[0])
	if err != nil {
		return nil, 400
	}
	d, ok := c.ua.dialog(r.DialogID())
	switch {
	case !ok && c.ua.hasEnded(r.DialogID()):
		return nil, 603
	case !ok || d == &c.CallDialog:
		return nil, 481
	}
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	switch {
	case d.dialog.State == DialogConfirmed && r.EarlyOnly:
		return nil, 486
	case d.dialog.State == DialogEarly && d.in != nil:
		return nil, 481
	}
	return d, 0
}
