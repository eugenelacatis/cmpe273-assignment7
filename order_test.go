package main

import (
	"slices"
	"testing"
)

func quiet(string) {}

func TestOutOfOrderAndDuplicate(t *testing.T) {
	s := NewStore(quiet)
	if r := s.Deliver(Event{"o", 2, "PAID"}); r != Buffered {
		t.Fatalf("v2 first: got %s, want BUFFERED", r)
	}
	if r := s.Deliver(Event{"o", 1, "CREATED"}); r != Applied {
		t.Fatalf("v1: got %s, want APPLIED", r)
	}
	if r := s.Deliver(Event{"o", 2, "PAID"}); r != Duplicate {
		t.Fatalf("v2 again: got %s, want DUPLICATE", r)
	}
	status, version, pending, _ := s.Snapshot("o")
	if status != "PAID" || version != 2 || len(pending) != 0 {
		t.Fatalf("got %s v%d pending=%v, want PAID v2 pending=[]", status, version, pending)
	}
}

func TestMissingEventDoesNotJumpAhead(t *testing.T) {
	s := NewStore(quiet)
	s.Deliver(Event{"o", 1, "CREATED"})
	if r := s.Deliver(Event{"o", 3, "SHIPPED"}); r != Buffered {
		t.Fatalf("v3 with v2 missing: got %s, want BUFFERED", r)
	}
	status, version, pending, gaps := s.Snapshot("o")
	if status != "CREATED" || version != 1 {
		t.Fatalf("jumped ahead: got %s v%d", status, version)
	}
	if !slices.Equal(pending, []int{3}) || !slices.Equal(gaps, []int{2}) {
		t.Fatalf("pending=%v missing=%v, want [3] [2]", pending, gaps)
	}
}

func TestStaleTokenRejected(t *testing.T) {
	s := NewStore(quiet)
	locks := &LockService{}
	s.Deliver(Event{"o", 1, "CREATED"})
	old, newer := locks.Acquire(), locks.Acquire()
	if r := s.DeliverWithToken(Event{"o", 2, "PAID"}, newer); r != Applied {
		t.Fatalf("newer token: got %s, want APPLIED", r)
	}
	if r := s.DeliverWithToken(Event{"o", 3, "SHIPPED"}, old); r != Rejected {
		t.Fatalf("older token: got %s, want REJECTED", r)
	}
	if status, _, _, _ := s.Snapshot("o"); status != "PAID" {
		t.Fatalf("stale write leaked: status=%s", status)
	}
}
