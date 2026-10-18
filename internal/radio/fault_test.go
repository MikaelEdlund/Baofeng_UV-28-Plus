package radio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// faultyLink sits between the Driver and simRadio and injects one transport
// fault, the kind of thing a real BLE link does now and then.
type faultyLink struct {
	*simRadio

	// Lose one 20-byte chunk of the write frame for block writeAddr, either
	// silently (as when the module's UART buffer overflows) or by returning
	// an error from WriteData.
	writeAddr  int
	loseChunk  int // 0-based chunk index within the 132-byte frame
	withError  bool
	inFrame    int // chunk index inside the target frame, -1 outside it
	writeFired bool

	// Lose the first notification of the reply to a read of block readAddr.
	readAddr  int
	readFired bool
}

func newFaultyLink(s *simRadio) *faultyLink {
	return &faultyLink{simRadio: s, writeAddr: -1, readAddr: -1, inFrame: -1}
}

func isFrameStart(p []byte, cmd byte, addr int) bool {
	return len(p) >= 4 && p[0] == cmd && p[3] == blockLenByte && int(p[1])<<8|int(p[2]) == addr
}

func (f *faultyLink) WriteData(p []byte) error {
	if f.writeAddr >= 0 && !f.writeFired {
		if f.inFrame < 0 && len(p) == chunkSize && isFrameStart(p, cmdWrite, f.writeAddr) {
			f.inFrame = 0
		} else if f.inFrame >= 0 {
			f.inFrame++
		}
		if f.inFrame == f.loseChunk {
			f.writeFired = true
			if f.withError {
				return errors.New("simulated BLE write failure")
			}
			return nil // never reaches the radio
		}
	}
	if err := f.simRadio.WriteData(p); err != nil {
		return err
	}
	if f.readAddr >= 0 && !f.readFired && isFrameStart(p, cmdRead, f.readAddr) {
		// simRadio replies synchronously; drop the first notification.
		f.readFired = true
		<-f.simRadio.events
	}
	return nil
}

func testImage() *Image {
	im := NewBlankImage()
	im.Ident = "UV28PRO"
	for n := 1; n <= 4; n++ {
		_ = im.SetChannel(Channel{Number: n, Name: "FIRST", RXFreq: "145.50000", TXFreq: "145.50000", RXTone: "OFF", TXTone: "OFF", Power: "High", SigCode: 1, PTTID: "Off"})
	}
	for n := 997; n <= 1000; n++ {
		_ = im.SetChannel(Channel{Number: n, Name: "LAST", RXFreq: "438.80000", TXFreq: "431.20000", RXTone: "88.5", TXTone: "88.5", Power: "Low", SigCode: 1, PTTID: "Off"})
	}
	return im
}

func channelOnlyBlocks() []int {
	var out []int
	for _, a := range Blocks() {
		if a < 0x7D00 {
			out = append(out, a)
		}
	}
	return out
}

// A write that loses part of a frame must fail and must not leave a
// misaligned block on the radio. (Retrying the frame used to make the radio
// commit a corrupted block and ACK it, which was reported as success.)
func TestWriteLostChunkFailsWithoutCorruptingRadio(t *testing.T) {
	// A few blocks keep the test fast; writing all 250 adds nothing here.
	all := channelOnlyBlocks()
	head, tail := all[:3], all[len(all)-3:]
	cases := []struct {
		name      string
		blocks    []int
		addr      int
		loseChunk int
		withError bool
	}{
		{"last block, last chunk lost silently", tail, tail[2], 6, false},
		{"last block, WriteData fails on chunk 3", tail, tail[2], 3, true},
		{"first block, chunk 3 lost silently", head, head[0], 3, false},
	}
	for _, c := range cases {
		blocks := c.blocks
		t.Run(c.name, func(t *testing.T) {
			sim := newSimRadio()
			link := newFaultyLink(sim)
			link.writeAddr, link.loseChunk, link.withError = c.addr, c.loseChunk, c.withError

			err := NewDriver(link, nil).WriteImage(testImage(), blocks, false, nil)
			if err == nil {
				t.Fatal("WriteImage succeeded despite a lost chunk")
			}
			if !strings.Contains(err.Error(), "write 0x") {
				t.Errorf("error should name the failing block: %v", err)
			}
			// The radio started blank; the failed block must still be blank.
			for i, b := range sim.mem[c.addr : c.addr+BlockSize] {
				if b != 0xFF {
					t.Fatalf("block 0x%04X was modified on the radio (byte %d = 0x%02X)", c.addr, i, b)
				}
			}
			wantWrites := 0
			for _, a := range blocks {
				if a == c.addr {
					break
				}
				wantWrites++
			}
			if sim.writes != wantWrites {
				t.Fatalf("radio committed %d blocks, want %d (only those before the failure)", sim.writes, wantWrites)
			}
		})
	}
}

// Reads are retried: a lost notification costs one timeout, not the session.
func TestReadRetriesLostNotification(t *testing.T) {
	sim := newSimRadio()
	copy(sim.mem[:], testImage().Mem[:])
	link := newFaultyLink(sim)
	link.readAddr = 0x0080

	im, err := NewDriver(link, nil).ReadImage(nil)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if !link.readFired {
		t.Fatal("fault was not injected")
	}
	if want := len(Blocks()) + 1; sim.reads != want {
		t.Fatalf("radio served %d reads, want %d (one retry)", sim.reads, want)
	}
	for _, r := range Regions {
		if !bytes.Equal(im.Mem[r.Start:r.Start+r.Size], sim.mem[r.Start:r.Start+r.Size]) {
			t.Fatalf("region 0x%04X differs after a retried read", r.Start)
		}
	}
}

func TestWriteImageModelCheck(t *testing.T) {
	im := testImage()
	blocks := channelOnlyBlocks()[:2]

	im.Ident = "UV21PRO" // the simulated radio reports UV28PRO
	sim := newSimRadio()
	err := NewDriver(sim, nil).WriteImage(im, blocks, false, nil)
	if err == nil || !strings.Contains(err.Error(), "model mismatch") {
		t.Fatalf("expected a model mismatch error, got %v", err)
	}
	if sim.writes != 0 {
		t.Fatalf("radio was written (%d blocks) despite the mismatch", sim.writes)
	}

	sim = newSimRadio()
	if err := NewDriver(sim, nil).WriteImage(im, blocks, true, nil); err != nil {
		t.Fatalf("forceMismatch=true: %v", err)
	}
	if sim.writes != len(blocks) {
		t.Fatalf("forced write committed %d blocks, want %d", sim.writes, len(blocks))
	}

	for _, ident := range []string{"uv28pro", ""} { // case-insensitive; empty skips the check
		im.Ident = ident
		sim = newSimRadio()
		if err := NewDriver(sim, nil).WriteImage(im, blocks, false, nil); err != nil {
			t.Fatalf("ident %q: %v", ident, err)
		}
	}
}
