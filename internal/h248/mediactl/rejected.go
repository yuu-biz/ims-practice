package mediactl

import (
	"errors"
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// CommandRejected is the error for a reply that carries an Error
// descriptor: the procedure Command Rejected of TS 29.334 5.17.3.11.
// errors.Is matches it with agw.ErrNoContext, agw.ErrNoTermination,
// agw.ErrNoPort, agw.ErrUnknownAnnouncement and ErrUnavailable where
// its code means one of them, so that a caller of the Controller tells
// the errors apart as it does for the HTTP client of the media node.
// The codes are those that a Gateway answers for these errors of its
// relay or processor.
type CommandRejected struct {
	ErrorDescriptor h248.ErrorDescriptor
}

func (e *CommandRejected) Error() string {
	text := fmt.Sprintf("mediactl: command rejected with error %d", e.ErrorDescriptor.ErrorCode)
	if e.ErrorDescriptor.ErrorText != "" {
		text += " " + e.ErrorDescriptor.ErrorText
	}
	return text
}

func (e *CommandRejected) Is(target error) bool {
	switch e.ErrorDescriptor.ErrorCode {
	case 411:
		// H.248.36 (03/2013) 5.6.1: the codes that tell that one side
		// has no record of a context or termination.
		return target == agw.ErrNoContext
	case 430, 435:
		return target == agw.ErrNoContext || target == agw.ErrNoTermination
	case 510:
		// H.248.8 (03/2013) 4.2.49, Insufficient Resources: the media
		// node has no free port for another context.
		return target == agw.ErrNoPort
	case 449:
		// An unknown parameter value, which an MRFP answers for an
		// announcement it does not hold (TS 29.333 5.17.1). The
		// Controller checks the other parameters of what it sends
		// (playSignal), so that is what a 449 means to its caller.
		return target == agw.ErrUnknownAnnouncement
	case 505:
		// H.248.8 (03/2013) 4.2.46: the gateway is registering.
		return target == ErrUnavailable
	}
	return false
}

// rejection returns the first error of a reply, whether it stands for
// the whole transaction, an action or a command, or nil.
func rejection(reply h248.TransactionReply) error {
	if reply.Error != nil {
		return &CommandRejected{ErrorDescriptor: *reply.Error}
	}
	for _, a := range reply.Actions {
		for _, c := range a.Commands {
			if c.Error != nil {
				return &CommandRejected{ErrorDescriptor: *c.Error}
			}
		}
		if a.Error != nil {
			return &CommandRejected{ErrorDescriptor: *a.Error}
		}
	}
	return nil
}

// errMalformedReply is returned for a reply that lacks what the request
// asked for.
var errMalformedReply = errors.New("mediactl: the reply does not answer the request")
