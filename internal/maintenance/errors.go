package maintenance

import "errors"

// ErrInvalidRequest is the answer to a request that the API cannot
// take as it is: a body that does not parse, an unknown value, or a
// timeout that is out of range.
var ErrInvalidRequest = errors.New("invalid request")
