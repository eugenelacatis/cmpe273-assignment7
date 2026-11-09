# CMPE 273 Lab 7: Event Ordering, Delayed Messages, and Stale Workers

A small Go simulation of an order that moves through versioned states:

```
CREATED (v1) -> PAID (v2) -> SHIPPED (v3)
```

Events can arrive late, out of order, or more than once. Workers that update orders carry fencing tokens so a paused worker can't overwrite newer state when it comes back.

## Running it

Requires Go 1.21 or newer.

```bash
git clone https://github.com/eugenelacatis/cmpe259-assignment7.git
cd cmpe259-assignment7
go run .        # runs all three scenarios
go test -v ./...  # runs the unit tests
```

## How it works

| File | Contents |
| --- | --- |
| `order.go` | `Store` (per-order state, pending buffer, fencing check) and `LockService` (issues increasing tokens) |
| `main.go` | The three scenarios printed to the terminal |
| `order_test.go` | Unit tests asserting the same behavior |
| `output.txt`, `test_output.txt` | Captured output from `go run .` and `go test -v` |

Every event carries `order_id`, `version`, and `status`. The store tracks the last applied version for each order and handles an incoming event as follows:

- `version <= current`: already applied, ignored as a duplicate.
- `version` already in the pending buffer: ignored as a duplicate.
- `version > current + 1`: there's a gap, so the event is buffered and the missing versions are logged.
- `version == current + 1`: applied. The store then drains the buffer for as long as the next version is waiting there.

For worker writes, `DeliverWithToken` compares the worker's token with the highest token the order has seen. A lower token is rejected before any version logic runs. Otherwise the order records the token as its new highest and the event goes through the normal versioned path.

## Results

Full output from `go run .` (also saved in [`output.txt`](output.txt)).

### 1. Correct event ordering and duplicate handling

Delivery order is v2, v1, v2 again.

```
=== Test 1: versioned ordering (deliver v2, v1, v2 again) ===
recv order-1 v2 PAID
  BUFFER  order-1 v2 PAID     waiting for missing [1]
recv order-1 v1 CREATED
  APPLY   order-1 v1 CREATED  NONE -> CREATED
  APPLY   order-1 v2 PAID     CREATED -> PAID
recv order-1 v2 PAID
  IGNORE  order-1 v2 PAID     already applied (current v2)
state order-1: status=PAID version=2 pending=[] missing=[]
```

v2 arrives first and is held because v1 is missing. Once v1 arrives, the store applies v1 and then the buffered v2, in that order. The second copy of v2 is ignored because the order is already at v2.

### 2. Missing event

v1 is delivered, then v3. v2 never arrives.

```
=== Test 2: missing event (deliver v1, then v3; v2 never arrives) ===
recv order-2 v1 CREATED
  APPLY   order-2 v1 CREATED  NONE -> CREATED
recv order-2 v3 SHIPPED
  BUFFER  order-2 v3 SHIPPED  waiting for missing [2]
state order-2: status=CREATED version=1 pending=[3] missing=[2]
```

The order stays at `CREATED` v1 and does not skip to `SHIPPED`. v3 sits in the pending buffer, and v2 is recorded as missing.

### 3. Stale worker rejection

```
=== Test 3: stale worker with fencing tokens ===
  APPLY   order-3 v1 CREATED  NONE -> CREATED
worker-A acquires lease, token=1
worker-A pauses (GC / network partition); lease expires
worker-B acquires lease, token=2
worker-B writes v2 PAID
  APPLY   order-3 v2 PAID     CREATED -> PAID
worker-A wakes up, still thinks it holds the lease, writes v3 SHIPPED
  REJECT  order-3 v3 SHIPPED  token=1 < highest=2 (stale worker)
worker-B writes v3 SHIPPED
  APPLY   order-3 v3 SHIPPED  PAID -> SHIPPED
state order-3: status=SHIPPED version=3 pending=[] missing=[]
```

Worker A tries to write v3, which is a valid next version, so the version check would have let it through. The token check rejects it because the order has already seen token 2 from worker B. Worker B's writes with the newer token succeed.

### Unit tests

From [`test_output.txt`](test_output.txt):

```
=== RUN   TestOutOfOrderAndDuplicate
--- PASS: TestOutOfOrderAndDuplicate (0.00s)
=== RUN   TestMissingEventDoesNotJumpAhead
--- PASS: TestMissingEventDoesNotJumpAhead (0.00s)
=== RUN   TestStaleTokenRejected
--- PASS: TestStaleTokenRejected (0.00s)
PASS
ok  	github.com/eugenelacatis/cmpe259-assignment7	0.182s
```

## Reflection

Why are timestamps alone not enough to guarantee correct event ordering in a distributed system?

Each machine has its own clock, and clocks drift apart even with NTP, so a later event from one node can carry an earlier timestamp than an event from another node. A timestamp also can't tell you whether something is missing: if you see 10:00:01 and then 10:00:05, you have no way to know whether an event happened in between, while versions 1 and 3 make the gap obvious. Network delays and retries mean arrival order says nothing about when an event happened, and a duplicate with the same timestamp is hard to tell apart from a different event that happened in the same millisecond. Per-order version numbers and fencing tokens come from one counter that only goes up, so the receiver can apply events in order, detect gaps, drop duplicates, and reject stale writers without trusting anyone's clock.
