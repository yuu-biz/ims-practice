package mediactl

import (
	"errors"
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/h248"
)

// CommandRejected is the error for a reply that carries an Error
// descriptor: the procedure Command Rejected of TS 29.334 5.17.3.11.
// errors.Is matches it with ErrNoContext, ErrNoTermination,
// ErrInsufficientResources and ErrUnavailable where its code means one
// of them.
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
		return target == ErrNoContext
	case 430, 435:
		return target == ErrNoContext || target == ErrNoTermination
	case 510:
		return target == ErrInsufficientResources
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
