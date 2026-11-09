package main

import (
	"fmt"
	"sort"
	"sync"
)

// Event is one versioned update for an order.
type Event struct {
	OrderID string
	Version int
	Status  string
}

// Result describes what the store did with an event.
type Result string

const (
	Applied   Result = "APPLIED"
	Buffered  Result = "BUFFERED"  // arrived early, waiting for missing versions
	Duplicate Result = "DUPLICATE" // already applied or already buffered
	Rejected  Result = "REJECTED"  // stale fencing token
)

type order struct {
	status       string
	version      int           // last applied version (0 = nothing applied yet)
	pending      map[int]Event // events that arrived ahead of a gap
	highestToken int64         // highest fencing token seen for this order
}

// Store applies versioned events per order, strictly in version order.
type Store struct {
	mu     sync.Mutex
	orders map[string]*order
	log    func(string)
}

func NewStore(log func(string)) *Store {
	return &Store{orders: map[string]*order{}, log: log}
}

func (s *Store) get(id string) *order {
	o, ok := s.orders[id]
	if !ok {
		o = &order{status: "NONE", pending: map[int]Event{}}
		s.orders[id] = o
	}
	return o
}

// Deliver handles an event coming off the network (possibly late, early, or repeated).
func (s *Store) Deliver(e Event) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deliver(s.get(e.OrderID), e)
}

// DeliverWithToken is Deliver guarded by a fencing token. Any token lower than
// the highest one already seen for this order is rejected.
func (s *Store) DeliverWithToken(e Event, token int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.get(e.OrderID)
	if token < o.highestToken {
		s.log(fmt.Sprintf("  REJECT  %s v%d %-8s token=%d < highest=%d (stale worker)",
			e.OrderID, e.Version, e.Status, token, o.highestToken))
		return Rejected
	}
	o.highestToken = token
	return s.deliver(o, e)
}

func (s *Store) deliver(o *order, e Event) Result {
	if e.Version <= o.version {
		s.log(fmt.Sprintf("  IGNORE  %s v%d %-8s already applied (current v%d)", e.OrderID, e.Version, e.Status, o.version))
		return Duplicate
	}
	if _, ok := o.pending[e.Version]; ok {
		s.log(fmt.Sprintf("  IGNORE  %s v%d %-8s already pending", e.OrderID, e.Version, e.Status))
		return Duplicate
	}
	if e.Version > o.version+1 {
		o.pending[e.Version] = e
		s.log(fmt.Sprintf("  BUFFER  %s v%d %-8s waiting for missing %v", e.OrderID, e.Version, e.Status, missing(o)))
		return Buffered
	}

	s.apply(o, e)
	// Drain any buffered events that are now next in line.
	for {
		next, ok := o.pending[o.version+1]
		if !ok {
			break
		}
		delete(o.pending, next.Version)
		s.apply(o, next)
	}
	return Applied
}

func (s *Store) apply(o *order, e Event) {
	prev := o.status
	o.status, o.version = e.Status, e.Version
	s.log(fmt.Sprintf("  APPLY   %s v%d %-8s %s -> %s", e.OrderID, e.Version, e.Status, prev, e.Status))
}

// missing lists versions between the last applied one and the highest pending one.
func missing(o *order) []int {
	maxV := o.version
	for v := range o.pending {
		maxV = max(maxV, v)
	}
	var out []int
	for v := o.version + 1; v < maxV; v++ {
		if _, ok := o.pending[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}

// Snapshot returns the order's current state for display and tests.
func (s *Store) Snapshot(id string) (status string, version int, pending []int, gaps []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.get(id)
	for v := range o.pending {
		pending = append(pending, v)
	}
	sort.Ints(pending)
	return o.status, o.version, pending, missing(o)
}

// LockService hands out leases with strictly increasing fencing tokens.
type LockService struct {
	mu   sync.Mutex
	next int64
}

func (l *LockService) Acquire() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	return l.next
}
