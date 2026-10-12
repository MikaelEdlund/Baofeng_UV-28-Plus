package radio

import (
	"bytes"
	"testing"
)

// simRadio is an in-memory stand-in for the radio + BLE bridge. It implements
// the other half of the protocol so the Driver can be exercised end to end:
// module auth, the clone handshake, and encrypted block read/write, with
// responses fragmented into <=20-byte notifications like a real module.
type simRadio struct {
	mem    [MemSize]byte
	events chan []byte
	cmd    []byte // accumulates data-characteristic writes
	writes int
	reads  int
}

func newSimRadio() *simRadio {
	s := &simRadio{events: make(chan []byte, 4096)}
	for i := range s.mem {
		s.mem[i] = 0xFF
	}
	return s
}

func (s *simRadio) emit(p []byte) {
	for off := 0; off < len(p); off += 20 {
		end := off + 20
		if end > len(p) {
			end = len(p)
		}
		cp := make([]byte, end-off)
		copy(cp, p[off:end])
		s.events <- cp
	}
}

func (s *simRadio) WriteControl(p []byte) error {
	// Module challenge -> derived key (first variant).
	if len(p) == 20 && p[0] == 0x3F {
		k1, _ := AuthResponses(p)
		s.emit(k1)
	}
	return nil
}

func (s *simRadio) WriteData(p []byte) error {
	s.cmd = append(s.cmd, p...)
	for {
		if len(s.cmd) == 0 {
			return nil
		}
		want := cmdLen(s.cmd[0])
		if want == 0 || len(s.cmd) < want {
			return nil
		}
		frame := s.cmd[:want]
		s.cmd = s.cmd[want:]
		s.handle(frame)
	}
}

func cmdLen(first byte) int {
	switch first {
	case 'P': // PROGRAMCOLORPROU
		return 16
	case 'F', 'M':
		return 1
	case 'S': // SEND! + 20
		return 25
	case cmdRead:
		return 4
	case cmdWrite:
		return 4 + BlockSize
	}
	return 0
}

func (s *simRadio) handle(f []byte) {
	switch f[0] {
	case 'P':
		s.emit([]byte{ack})
	case 'F':
		s.emit([]byte("INFO0123456789AB")[:16])
	case 'M':
		s.emit([]byte("UV28PRO"))
	case 'S':
		s.emit([]byte{ack})
	case cmdRead:
		addr := int(f[1])<<8 | int(f[2])
		resp := []byte{cmdRead, f[1], f[2], blockLenByte}
		resp = append(resp, Crypt(BlockKeyIndex, s.mem[addr:addr+BlockSize])...)
		s.reads++
		s.emit(resp)
	case cmdWrite:
		addr := int(f[1])<<8 | int(f[2])
		copy(s.mem[addr:addr+BlockSize], Crypt(BlockKeyIndex, f[4:4+BlockSize]))
		s.writes++
		s.emit([]byte{ack})
	}
}

func (s *simRadio) Events() <-chan []byte { return s.events }
func (s *simRadio) Close() error          { return nil }

func TestDriverReadWriteAgainstSim(t *testing.T) {
	sim := newSimRadio()

	// Seed the simulated radio with a couple of channels by encoding into a
	// scratch image and copying the raw bytes into the sim's memory.
	seed := NewBlankImage()
	_ = seed.SetChannel(Channel{Number: 1, Name: "SIMCALL", RXFreq: "145.50000", TXFreq: "145.50000", RXTone: "88.5", TXTone: "OFF", Power: "High", Scan: true, SigCode: 1, PTTID: "Off"})
	_ = seed.SetChannel(Channel{Number: 2, Name: "RPT", RXFreq: "438.80000", TXFreq: "431.20000", RXTone: "D023N", TXTone: "67.0", Power: "Low", Narrow: true, SigCode: 2, PTTID: "Both"})
	copy(sim.mem[:], seed.Mem[:])

	d := NewDriver(sim, nil)
	im, err := d.ReadImage(nil)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if im.Ident != "UV28PRO" {
		t.Fatalf("ident = %q", im.Ident)
	}
	if n := len(Blocks()); sim.reads != n {
		t.Fatalf("expected %d block reads, got %d", n, sim.reads)
	}
	// The read-back image must match the seeded regions exactly.
	for _, r := range Regions {
		if !bytes.Equal(im.Mem[r.Start:r.Start+r.Size], seed.Mem[r.Start:r.Start+r.Size]) {
			t.Fatalf("region 0x%04X mismatch after read", r.Start)
		}
	}
	if got := im.GetChannel(2); got.Name != "RPT" || got.RXFreq != "438.80000" || got.TXTone != "67.0" {
		t.Fatalf("channel 2 decoded wrong: %+v", got)
	}

	// Now edit and write back to a fresh sim; verify the bytes land correctly.
	sim2 := newSimRadio()
	d2 := NewDriver(sim2, nil)
	im.SetChannel(Channel{Number: 3, Name: "NEW", RXFreq: "446.00625", TXFreq: "446.00625", RXTone: "OFF", TXTone: "OFF", Power: "Mid", SigCode: 1, PTTID: "Off"})
	if err := d2.WriteImage(im, nil, nil); err != nil {
		t.Fatalf("WriteImage: %v", err)
	}
	if n := len(Blocks()); sim2.writes != n {
		t.Fatalf("expected %d block writes, got %d", n, sim2.writes)
	}
	// Read the second sim back and confirm it equals what we wrote.
	back, err := NewDriver(sim2, nil).ReadImage(nil)
	if err != nil {
		t.Fatalf("read-back: %v", err)
	}
	for _, r := range Regions {
		if !bytes.Equal(back.Mem[r.Start:r.Start+r.Size], im.Mem[r.Start:r.Start+r.Size]) {
			t.Fatalf("region 0x%04X mismatch after write/read-back", r.Start)
		}
	}
	if got := back.GetChannel(3); got.Name != "NEW" || got.Power != "Mid" {
		t.Fatalf("channel 3 wrong after write: %+v", got)
	}
}
