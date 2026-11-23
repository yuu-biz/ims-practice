package smsc

import "github.com/yuu-biz/ims-practice/internal/sgd"

// The clients of package sgd are what a role gives the SC to send its
// requests with. The compiler checks here that each of them still
// satisfies the interface the SC asks for, since nothing in the
// non-test code of either package names both.
var (
	_ SGdClient = (*sgd.SGdClient)(nil)
	_ S6cClient = (*sgd.S6cClient)(nil)
)
