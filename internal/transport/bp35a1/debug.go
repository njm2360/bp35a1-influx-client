package bp35a1

import (
	"fmt"
	"log/slog"
	"strings"
)

// traceSerial は LOG_LEVEL=debug のときシリアルポートを流れた生バイトを記録する。
// SKSENDTO の UDP ペイロード等にバイナリが混じるため、escapeSerial で
// 印字可能文字はそのまま、制御文字・非印字バイトは \xNN にエスケープして出力する。
// escape は debug 無効時には行わない。
func (d *Device) traceSerial(dir string, b []byte) {
	if !d.log.Enabled(d.ctx, slog.LevelDebug) {
		return
	}
	d.log.Debug("serial io", "dir", dir, "len", len(b), "data", escapeSerial(b))
}

// traceSerialLine は組み立て済みの 1 行を記録する。RX は port.Read が細切れに
// 返すため、チャンク単位ではなく feed で改行区切りした行単位でトレースする。
func (d *Device) traceSerialLine(dir, line string) {
	if !d.log.Enabled(d.ctx, slog.LevelDebug) {
		return
	}
	d.log.Debug("serial io", "dir", dir, "data", escapeSerial([]byte(line)))
}

// escapeSerial はテキスト部分を可読のまま残しつつ、バイナリ・制御文字を
// \xNN 形式へエスケープした単一行文字列を返す。
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
