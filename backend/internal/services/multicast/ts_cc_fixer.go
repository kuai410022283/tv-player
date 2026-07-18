package multicast

import "sync"

// TS packet structure (188 bytes each):
//   Byte 0: Sync byte (0x47)
//   Byte 1: TEI(1) | PUSI(1) | TP(1) | PID(5 high bits)
//   Byte 2: PID(8 low bits)
//   Byte 3: TSC(2) | AFC(2) | CC(4)
//
// Continuity Counter (CC) is a 4-bit counter (0-15) that increments by 1 for each
// TS packet with the same PID. When RTP packets are lost (sequence number gap),
// the TS packets inside them are also lost, causing the CC to jump.
// ExoPlayer's TsExtractor freezes when it detects a CC discontinuity.
//
// TsCCFixer repairs the CC by tracking the expected CC per PID and fixing
// discontinuities when packet loss is detected.

// TsCCFixer tracks and repairs MPEG-TS Continuity Counters across RTP packet loss.
type TsCCFixer struct {
	mu     sync.Mutex
	lastCC map[uint16]uint8 // pid → last seen CC
}

// NewTsCCFixer creates a new TsCCFixer.
func NewTsCCFixer() *TsCCFixer {
	return &TsCCFixer{
		lastCC: make(map[uint16]uint8),
	}
}

// Process iterates over TS packets in the payload and fixes CC discontinuities
// when packetLossDetected is true. It always updates the internal CC tracker.
//
// The payload is modified in-place. It must contain complete TS packets (188 bytes each).
// If the payload does not start with a sync byte (0x47), it is scanned byte by byte
// to find the first sync byte before processing.
func (f *TsCCFixer) Process(payload []byte, packetLossDetected bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	offset := 0
	for offset+188 <= len(payload) {
		// Scan for next sync byte 0x47
		if payload[offset] != 0x47 {
			offset++
			continue
		}
		if offset+188 > len(payload) {
			break
		}

		ts := payload[offset : offset+188]

		// Parse PID (13 bits: byte1 low 5 bits + byte2)
		pid := uint16(ts[1]&0x1F)<<8 | uint16(ts[2])

		// Parse current CC (byte3 low 4 bits)
		currentCC := ts[3] & 0x0F

		if packetLossDetected {
			if expectedCC, ok := f.lastCC[pid]; ok {
				expected := (expectedCC + 1) & 0x0F
				if currentCC != expected {
					// Fix CC: preserve high 4 bits (TSC + AFC), replace low 4 bits
					ts[3] = (ts[3] & 0xF0) | expected
				}
			}
		}

		// Update last CC for this PID (read again in case we just fixed it)
		f.lastCC[pid] = ts[3] & 0x0F

		offset += 188
	}
}

// Reset clears all tracked CC state. Should be called on seek/connection reset.
func (f *TsCCFixer) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCC = make(map[uint16]uint8)
}