package main

import "fmt"

func main() {
	store := NewStore(func(s string) { fmt.Println(s) })

	header("Test 1: versioned ordering (deliver v2, v1, v2 again)")
	for _, e := range []Event{
		{"order-1", 2, "PAID"},
		{"order-1", 1, "CREATED"},
		{"order-1", 2, "PAID"},
	} {
		fmt.Printf("recv %s v%d %s\n", e.OrderID, e.Version, e.Status)
		store.Deliver(e)
	}
	state(store, "order-1")

	header("Test 2: missing event (deliver v1, then v3; v2 never arrives)")
	for _, e := range []Event{
		{"order-2", 1, "CREATED"},
		{"order-2", 3, "SHIPPED"},
	} {
		fmt.Printf("recv %s v%d %s\n", e.OrderID, e.Version, e.Status)
		store.Deliver(e)
	}
	state(store, "order-2")

	header("Test 3: stale worker with fencing tokens")
	locks := &LockService{}
	store.Deliver(Event{"order-3", 1, "CREATED"})

	tokenA := locks.Acquire()
	fmt.Printf("worker-A acquires lease, token=%d\n", tokenA)
	fmt.Println("worker-A pauses (GC / network partition); lease expires")

	tokenB := locks.Acquire()
	fmt.Printf("worker-B acquires lease, token=%d\n", tokenB)
	fmt.Println("worker-B writes v2 PAID")
	store.DeliverWithToken(Event{"order-3", 2, "PAID"}, tokenB)

	fmt.Println("worker-A wakes up, still thinks it holds the lease, writes v3 SHIPPED")
	store.DeliverWithToken(Event{"order-3", 3, "SHIPPED"}, tokenA)

	fmt.Println("worker-B writes v3 SHIPPED")
	store.DeliverWithToken(Event{"order-3", 3, "SHIPPED"}, tokenB)
	state(store, "order-3")
}

func header(title string) {
	fmt.Printf("\n=== %s ===\n", title)
}

func state(s *Store, id string) {
	status, version, pending, gaps := s.Snapshot(id)
	fmt.Printf("state %s: status=%s version=%d pending=%v missing=%v\n", id, status, version, pending, gaps)
}
