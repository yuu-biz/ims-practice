package kvlock

import "strings"

// Queued is how many users have a queue in the process: someone holds
// their lock or waits for it.
func (l *Locker) Queued() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}

// Users is how many goroutines of the process hold the lock of user or
// wait for it.
func (l *Locker) Users(user string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if q, ok := l.queue[strings.ToLower(user)]; ok {
		return q.users
	}
	return 0
}
