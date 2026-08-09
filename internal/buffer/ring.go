package buffer

import "sync"

type Ring struct {
	mu      sync.RWMutex
	entries []string
	next    int
	full    bool
}

func New(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{entries: make([]string, capacity)}
}

func (r *Ring) Add(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[r.next] = value
	r.next = (r.next + 1) % len(r.entries)
	if r.next == 0 {
		r.full = true
	}
}

func (r *Ring) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.entries)
	r.next = 0
	r.full = false
}

func (r *Ring) Values() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.full {
		return append([]string(nil), r.entries[:r.next]...)
	}
	values := make([]string, 0, len(r.entries))
	values = append(values, r.entries[r.next:]...)
	values = append(values, r.entries[:r.next]...)
	return values
}
