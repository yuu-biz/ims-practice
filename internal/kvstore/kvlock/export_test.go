package kvlock

// Queued is how many users have a queue in the process: someone holds
// their lock or waits for it.
func (l *Locker) Queued() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}
