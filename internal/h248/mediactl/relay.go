package mediactl

import (
	"context"
	"errors"

	"github.com/yuu-biz/ims-practice/internal/agw"
)

// Relay is what a Gateway of Iq carries out the commands of its
// controller with: the media relay of the IMS-AGW. Its operations and
// types are those with which the P-CSCF controls its media relay, so
// that *agw.Client is one. The Gateway asks for a context with the two
// terminations agw.Access, which faces the UE, and agw.Core, which
// faces the rest of the network; media entering one leaves through the
// other. Add fails as a whole: it makes both terminations or none.
type Relay interface {
	Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error)
	Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error
	Subtract(ctx context.Context, id string) error
}

// ErrUnavailable means that Controller has no media gateway to send the
// command to: none has registered, or the one that holds the context
// has stopped answering or is out of service.
var ErrUnavailable = errors.New("mediactl: no media gateway available")
