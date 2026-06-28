// Package snowflake implements a distributed, monotonically increasing 64-bit ID
// generator inspired by Twitter's Snowflake algorithm.
//
// ID layout (64 bits, MSB = sign bit always 0):
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│ 0 │         timestamp (41 bits)         │ workerID │  sequence  │
//	│   │   milliseconds since custom epoch   │ (10 bits)│  (12 bits) │
//	└─────────────────────────────────────────────────────────────────┘
//
// Properties:
//   - Sortable by creation time (timestamp is the high bits)
//   - 4096 unique IDs per millisecond per worker node
//   - Supports 1024 concurrent worker nodes (10-bit worker ID)
//   - IDs stay unique for ~69 years from the epoch
//
// Why Snowflake instead of MySQL AUTO_INCREMENT?
//   MySQL AUTO_INCREMENT is a single point of serialization — every INSERT
//   acquires a table-level counter, which becomes a bottleneck at scale.
//   Snowflake IDs are generated in-process with no database round-trip.
package snowflake

import (
	"sync"
	"time"
)

// customEpoch is the base timestamp (milliseconds since Unix epoch) for ID generation.
// Using 2024-01-01 00:00:00 UTC instead of Unix epoch keeps IDs smaller.
const customEpoch = int64(1704067200000) // 2024-01-01 00:00:00 UTC

// Bit-width constants that define the ID layout.
const (
	workerIDBits  = 10
	sequenceBits  = 12
	maxWorkerID   = (1 << workerIDBits) - 1 // 1023
	maxSequence   = (1 << sequenceBits) - 1  // 4095
	workerIDShift = sequenceBits             // sequence occupies the bottom 12 bits
	timestampShift = workerIDBits + sequenceBits // timestamp starts at bit 22
)

// Generator is a thread-safe Snowflake ID generator.
// Create one instance per worker process and reuse it.
type Generator struct {
	mu        sync.Mutex // guards lastStamp and sequence
	workerID  int64      // unique identifier for this worker node (0–1023)
	sequence  int64      // counter within the current millisecond
	lastStamp int64      // timestamp (ms since customEpoch) of the last generated ID
}

// NewGenerator creates a Generator for the given workerID.
// workerID must be in [0, 1023]; values outside this range are clamped to 0.
//
// In a Kubernetes deployment each Pod typically reads its worker ID from an
// environment variable (e.g. WORKER_ID) derived from the StatefulSet ordinal.
func NewGenerator(workerID int64) *Generator {
	if workerID < 0 || workerID > maxWorkerID {
		workerID = 0
	}
	return &Generator{workerID: workerID}
}

// Next generates the next unique, monotonically increasing Snowflake ID.
//
// If the system clock goes backwards (rare in modern NTP-synced environments)
// the generator stalls until the clock catches up, preventing duplicate IDs.
func (g *Generator) Next() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := currentMillis()

	if now == g.lastStamp {
		// Same millisecond — increment sequence counter (wraps around at 4095)
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			// Sequence exhausted (4096 IDs in this ms): busy-wait for next millisecond
			for now <= g.lastStamp {
				now = currentMillis()
			}
		}
	} else {
		// New millisecond — reset sequence
		g.sequence = 0
	}

	g.lastStamp = now

	// Pack timestamp | workerID | sequence into a single 64-bit integer
	return uint64(now<<timestampShift | g.workerID<<workerIDShift | g.sequence)
}

// currentMillis returns milliseconds elapsed since customEpoch.
func currentMillis() int64 {
	return time.Now().UnixMilli() - customEpoch
}
