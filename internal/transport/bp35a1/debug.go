package bp35a1

import (
	"fmt"
	"log/slog"
	"strings"
)

func (d *Device) traceSerial(dir string, b []byte) {
	if !d.serialLog.Enabled(d.ctx, slog.LevelDebug) {
		return
	}
	d.serialLog.Debug("serial io", "dir", dir, "len", len(b), "data", escapeSerial(b))
}

func (d *Device) traceSerialLine(dir, line string) {
	if !d.serialLog.Enabled(d.ctx, slog.LevelDebug) {
		return
	}
	d.serialLog.Debug("serial io", "dir", dir, "data", escapeSerial([]byte(line)))
}

func escapeSerial(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c == '\\':
			sb.WriteString(`\\`)
		case c == '\r':
			sb.WriteString(`\r`)
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\t':
			sb.WriteString(`\t`)
		case c >= 0x20 && c < 0x7f:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, `\x%02x`, c)
		}
	}
	return sb.String()
}
