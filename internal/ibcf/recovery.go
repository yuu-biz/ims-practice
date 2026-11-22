package ibcf

import (
	"context"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// staleSeqGap is how far beyond the CSeq number of a record the BYE of
// ReleaseLeftovers starts. The record is written again after each
// request that the call passes on, but a request may have been on its
// way when the process stopped; the far end takes any number above the
// last it saw (RFC 3261 12.2.2), so a large step stays above them.
const staleSeqGap = 1 << 16

// ReleaseLeftovers ends the calls of records that an earlier process of
// this instance left in the Store, which Listen found before its UAs
// started. The dialogs of those calls were in that process and are
// gone with it; the parties would keep talking into a TrGW context that
// nobody releases. For each record the IBCF releases the TrGW context,
// and sends a BYE in each of the two dialogs, from what the record
// saved of them, as TS 24.229 5.10.2.4 and 5.10.3.4 let it release a
// call by itself. It then deletes the record.
//
// A record of a call that was not answered yet has no dialogs; only
// its TrGW context is released. The caller's INVITE, which went to the
// earlier process, gets no final response from this one and times out.
//
// SIMPLIFIED: TS 24.229 5.10.2.4 and 5.10.3.4. The IBCF ends the calls
// of an earlier process of the same instance, which comes back under
// the same name. The records of an instance that never comes back stay
// in the Store, with their TrGW contexts, until an instance with that
// name starts.
func (b *IBCF) ReleaseLeftovers(records []CallRecord) {
	var wg sync.WaitGroup
	for _, r := range records {
		log := b.log.With("call_record", r.ID, "peer", r.Peer, "direction", string(r.Direction))
		log.Info("ending a call that an earlier process of this instance left")
		if r.Media != nil {
			b.releaseMedia(*r.Media, log)
		}
		for _, d := range []struct {
			s      side
			dialog *sip.Dialog
		}{{b.inside(), r.Home}, {b.outside(), r.PeerSide}} {
			if d.dialog == nil {
				continue
			}
			wg.Add(1)
			go func(s side, dialog sip.Dialog) {
				defer wg.Done()
				b.byeInLeftover(s, dialog)
			}(d.s, *d.dialog)
		}
		if err := b.store.DeleteCall(context.Background(), r.Instance, r.ID); err != nil {
			// The record stays for the next process of the instance,
			// whose BYEs get 481 and whose release finds no context.
			log.Warn("cannot remove the record of the ended call", "error", err.Error())
		}
	}
	wg.Wait()
}

// byeInLeftover sends a BYE in dialog, a dialog of side s that a record
// saved, and waits for its final response.
func (b *IBCF) byeInLeftover(s side, dialog sip.Dialog) {
	log := b.log.With("direction", string(s.direction), "call_id", dialog.ID.CallID)
	ua, err := s.agent()
	if err != nil {
		log.Warn("cannot end the dialog", "error", err.Error())
		return
	}
	dialog.LocalSeq += staleSeqGap
	ct, err := ua.SendRequest(dialog.NewRequest(sip.BYE))
	if err != nil {
		log.Warn("cannot end the dialog", "error", err.Error())
		return
	}
	for resp := range ct.Responses() {
		if resp.StatusCode >= 200 {
			log.Info("dialog ended", "status", resp.StatusCode)
			return
		}
	}
	log.Info("the BYE got no answer", "error", errText(ct.Err()))
}
