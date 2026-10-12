package radio

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var csvHeader = []string{
	"Channel", "Name", "RXFreq", "TXFreq", "RXTone", "TXTone",
	"Power", "Narrow", "Scan", "BusyLock", "FreqHop", "SigCode", "PTTID",
}

func boolToCSV(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func csvToBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

// ExportCSV writes every non-empty channel as a CSV row.
func (im *Image) ExportCSV(w io.Writer, includeEmpty bool) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for n := 1; n <= ChannelCount; n++ {
		c := im.GetChannel(n)
		if c.Empty && !includeEmpty {
			continue
		}
		row := []string{
			strconv.Itoa(c.Number), c.Name, c.RXFreq, c.TXFreq,
			string(c.RXTone), string(c.TXTone), c.Power,
			boolToCSV(c.Narrow), boolToCSV(c.Scan), boolToCSV(c.BusyLock),
			boolToCSV(c.FreqHop), strconv.Itoa(c.SigCode), c.PTTID,
		}
		if c.Empty {
			row = []string{strconv.Itoa(c.Number)}
			for i := 1; i < len(csvHeader); i++ {
				row = append(row, "")
			}
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ImportCSV updates the image from a CSV produced by ExportCSV (or edited by
// hand). Channels present in the CSV replace those in the image; channels not
// mentioned are left untouched. A row with an empty RXFreq clears that
// channel.
func (im *Image) ImportCSV(r io.Reader) (updated int, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	need := func(name string) (int, bool) { i, ok := col[strings.ToLower(name)]; return i, ok }
	chCol, ok := need("Channel")
	if !ok {
		return 0, fmt.Errorf("CSV is missing a 'Channel' column")
	}
	get := func(row []string, name string) string {
		i, ok := need(name)
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	for ln, row := range rows[1:] {
		if len(row) == 0 || (len(row) == 1 && row[0] == "") {
			continue
		}
		numStr := ""
		if chCol < len(row) {
			numStr = strings.TrimSpace(row[chCol])
		}
		if numStr == "" {
			continue
		}
		num, perr := strconv.Atoi(numStr)
		if perr != nil {
			return updated, fmt.Errorf("line %d: bad channel number %q", ln+2, numStr)
		}
		c := Channel{Number: num}
		c.Name = get(row, "Name")
		c.RXFreq = get(row, "RXFreq")
		c.TXFreq = get(row, "TXFreq")
		if c.RXFreq == "" {
			c.Empty = true
		}
		c.RXTone = Tone(firstNonEmpty(get(row, "RXTone"), "OFF"))
		c.TXTone = Tone(firstNonEmpty(get(row, "TXTone"), "OFF"))
		c.Power = firstNonEmpty(get(row, "Power"), "High")
		c.Narrow = csvToBool(get(row, "Narrow"))
		c.Scan = csvToBool(get(row, "Scan"))
		c.BusyLock = csvToBool(get(row, "BusyLock"))
		c.FreqHop = csvToBool(get(row, "FreqHop"))
		if sc := get(row, "SigCode"); sc != "" {
			c.SigCode, _ = strconv.Atoi(sc)
		}
		c.PTTID = firstNonEmpty(get(row, "PTTID"), "Off")
		if err := im.SetChannel(c); err != nil {
			return updated, fmt.Errorf("line %d: %w", ln+2, err)
		}
		updated++
	}
	return updated, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
