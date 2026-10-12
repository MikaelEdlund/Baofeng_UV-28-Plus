package radio

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// BlockSize is the payload size of one read/write command.
const BlockSize = 0x80

// Region is a contiguous range of radio memory that is read and written.
type Region struct {
	Start int
	Size  int
}

// Regions are exactly the ranges the vendor app reads and writes for this
// radio family (InterphoneUtil.getValidAddr, device id 16/19).
var Regions = []Region{
	{0x0000, 0x7D00}, // 1000 channels x 32 bytes
	{0x8000, 0x0080}, // VFO A, VFO B, ...
	{0x9000, 0x0080}, // general settings
	{0xA000, 0x0380}, // ANI, DTMF/PTT-ID list, zone names
}

// MemSize is the size of the address space covered by Image.
const MemSize = 0xB000

// Blocks returns every block start address in transfer order.
func Blocks() []int {
	var out []int
	for _, r := range Regions {
		for a := r.Start; a < r.Start+r.Size; a += BlockSize {
			out = append(out, a)
		}
	}
	return out
}

// InRegions reports whether [addr, addr+n) lies within one region.
func InRegions(addr, n int) bool {
	for _, r := range Regions {
		if addr >= r.Start && addr+n <= r.Start+r.Size {
			return true
		}
	}
	return false
}

// Image is a full memory image of the radio.
type Image struct {
	Mem      [MemSize]byte
	Ident    string    // model identifier the radio reported, e.g. "UV28PRO"
	Info     string    // hex of the radio's reply to 'F'
	Source   string    // "radio", "file:<name>", ...
	Captured time.Time // when it was read from the radio
}

// NewBlankImage returns an image filled with 0xFF (erased).
func NewBlankImage() *Image {
	im := &Image{}
	for i := range im.Mem {
		im.Mem[i] = 0xFF
	}
	return im
}

// Clone returns a deep copy.
func (im *Image) Clone() *Image {
	c := *im
	return &c
}

// Block returns a copy of the block at addr.
func (im *Image) Block(addr int) []byte {
	b := make([]byte, BlockSize)
	copy(b, im.Mem[addr:addr+BlockSize])
	return b
}

// SetBlock stores a block.
func (im *Image) SetBlock(addr int, data []byte) {
	copy(im.Mem[addr:addr+BlockSize], data)
}

// DiffBlocks lists blocks that differ between two images.
func DiffBlocks(a, b *Image) []int {
	var out []int
	for _, addr := range Blocks() {
		if string(a.Mem[addr:addr+BlockSize]) != string(b.Mem[addr:addr+BlockSize]) {
			out = append(out, addr)
		}
	}
	return out
}

// ---- file format ----

const fileFormat = "uv28prog-image"

type fileRegion struct {
	Start int    `json:"start"`
	Data  string `json:"data"`
}

type fileImage struct {
	Format   string       `json:"format"`
	Version  int          `json:"version"`
	Model    string       `json:"model"`
	Info     string       `json:"info,omitempty"`
	Captured string       `json:"captured,omitempty"`
	Saved    string       `json:"saved"`
	Note     string       `json:"note"`
	Regions  []fileRegion `json:"regions"`
}

// MarshalFile serialises the image to the JSON file format.
func (im *Image) MarshalFile() ([]byte, error) {
	f := fileImage{
		Format:  fileFormat,
		Version: 1,
		Model:   im.Ident,
		Info:    im.Info,
		Saved:   time.Now().Format(time.RFC3339),
		Note:    "Baofeng UV-28 Plus/Pro memory image. Regions are raw (decrypted) radio memory, base64 encoded.",
	}
	if !im.Captured.IsZero() {
		f.Captured = im.Captured.Format(time.RFC3339)
	}
	for _, r := range Regions {
		f.Regions = append(f.Regions, fileRegion{
			Start: r.Start,
			Data:  base64.StdEncoding.EncodeToString(im.Mem[r.Start : r.Start+r.Size]),
		})
	}
	return json.MarshalIndent(f, "", "  ")
}

// UnmarshalFile parses the JSON file format.
func UnmarshalFile(data []byte) (*Image, error) {
	var f fileImage
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("not a valid image file: %w", err)
	}
	if f.Format != fileFormat {
		return nil, errors.New("not a UV-28 programmer image file")
	}
	im := NewBlankImage()
	im.Ident = f.Model
	im.Info = f.Info
	if t, err := time.Parse(time.RFC3339, f.Captured); err == nil {
		im.Captured = t
	}
	seen := map[int]bool{}
	for _, fr := range f.Regions {
		raw, err := base64.StdEncoding.DecodeString(fr.Data)
		if err != nil {
			return nil, fmt.Errorf("region 0x%04X: %w", fr.Start, err)
		}
		ok := false
		for _, r := range Regions {
			if r.Start == fr.Start && r.Size == len(raw) {
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("unexpected region 0x%04X (%d bytes)", fr.Start, len(raw))
		}
		copy(im.Mem[fr.Start:], raw)
		seen[fr.Start] = true
	}
	for _, r := range Regions {
		if !seen[r.Start] {
			return nil, fmt.Errorf("image file is missing region 0x%04X", r.Start)
		}
	}
	return im, nil
}
