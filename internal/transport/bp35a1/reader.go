package bp35a1

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

// readLoop の連続読み取りエラーがこの回数に達したらポートが死んだと判断して終了する。
const maxConsecutiveReadErrors = 10

func (d *Device) readLoop() {
	defer d.Close()

	chunk := make([]byte, 512)
	errCount := 0
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}
		n, err := d.port.Read(chunk)
		if err != nil {
			// 自分でシャットダウンした場合は正常終了。
			select {
			case <-d.ctx.Done():
				return
			case <-d.closed:
				return
			default:
			}
			// Windows ではボーレート検出時の ResetInputBuffer(PurgeComm/PURGE_RXABORT)が
			// 進行中の Read を中断し ERROR_OPERATION_ABORTED("Port has been closed")を返す。
			// ポート自体は有効で次の Read で復帰できるため、致命扱いせず継続する。
			// ポートが本当に失われた場合に備え、連続失敗が続いたら打ち切る。
			errCount++
			if errCount >= maxConsecutiveReadErrors {
				d.log.Warn("serial read error; giving up", "err", err, "consecutive", errCount)
				return
			}
			d.log.Debug("serial read error; retrying", "err", err, "consecutive", errCount)
			select {
			case <-d.ctx.Done():
				return
			case <-d.closed:
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		errCount = 0
		if n == 0 {
			continue
		}
		d.feed(chunk[:n])
	}
}

func (d *Device) feed(chunk []byte) {
	d.bufMu.Lock()
	d.buf = append(d.buf, chunk...)
	for {
		nl := []byte(d.currentNewline())
		i := bytes.Index(d.buf, nl)
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(d.buf[:i]))
		d.buf = d.buf[i+len(nl):]
		d.bufMu.Unlock()
		if line != "" {
			d.traceSerialLine("rx", line)
			d.processLine(line)
		}
		d.bufMu.Lock()
	}
	d.bufMu.Unlock()
}

func (d *Device) processLine(line string) {
	switch d.currentState() {
	case stateNormal:
		d.processNormal(line)
	case stateEpandesc:
		d.processEpandesc(line)
	case stateSKLL64:
		// SKLL64 は IPv6 アドレスを OK/FAIL なしで返す。
		if strings.HasPrefix(line, "FAIL") {
			d.send(d.results, line)
		} else {
			d.send(d.responses, line)
			d.send(d.results, "OK")
		}
		d.setState(stateNormal)
	case stateProductRead:
		// ROPT は "OK 01" のように結果と値を 1 行で返す。
		if strings.HasPrefix(line, "OK") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				d.send(d.responses, parts[1])
			}
			d.send(d.results, parts[0])
		} else {
			d.send(d.results, line)
		}
		d.setState(stateNormal)
	}
}

func (d *Device) processNormal(line string) {
	switch {
	case strings.HasPrefix(line, "ERXUDP"):
		d.handleERXUDP(line)
	case strings.HasPrefix(line, "EVENT"):
		d.handleEvent(line)
	case line == "EPANDESC":
		d.epan = Epan{}
		d.epanSeen = make(map[string]bool, 6)
		d.setState(stateEpandesc)
	case strings.HasPrefix(line, "OK"), strings.HasPrefix(line, "FAIL"):
		d.send(d.results, line)
	default:
		d.send(d.responses, line)
	}
}

func (d *Device) handleERXUDP(line string) {
	f := strings.Split(line, " ")
	if len(f) < 9 {
		d.log.Warn("malformed ERXUDP", "fields", len(f))
		return
	}
	dstPort, err := strconv.ParseInt(f[4], 16, 32)
	if err != nil {
		d.log.Warn("bad ERXUDP dst port", "err", err)
		return
	}
	if int(dstPort) != echonetPort {
		return // ECHONET Lite 以外は無視
	}
	payload, err := hexToBytes(f[8])
	if err != nil {
		d.log.Warn("bad ERXUDP payload", "err", err)
		return
	}
	select {
	case d.rxudp <- payload:
	case <-d.closed:
	}
}

func (d *Device) handleEvent(line string) {
	f := strings.Split(line, " ")
	if len(f) < 3 {
		return
	}
	code, err := strconv.ParseInt(f[1], 16, 32)
	if err != nil {
		return
	}
	switch int(code) {
	case evPANAConnectOK:
		d.txAllowed.Store(true)
	case evLifetimeExpire:
		d.txAllowed.Store(false)
	}
	ev := skEvent{code: int(code), sender: f[2]}
	select {
	case d.events <- ev:
	default:
		d.log.Debug("event channel full, dropping", "code", code)
	}
}

func (d *Device) processEpandesc(line string) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)

	switch key {
	case "Channel":
		d.epan.Channel = parseHex(value)
	case "Channel Page":
		d.epan.ChannelPage = parseHex(value)
	case "Pan ID":
		d.epan.PanID = parseHex(value)
	case "Addr":
		d.epan.MACAddress = value
	case "LQI":
		d.epan.LQI = parseHex(value)
	case "PairID":
		d.epan.PairID = value
	default:
		return
	}
	d.epanSeen[key] = true

	if len(d.epanSeen) == 6 {
		d.setState(stateNormal)
		select {
		case d.epans <- d.epan:
		case <-d.closed:
		}
	}
}

// send は内部チャネルへノンブロッキングで送る(満杯時はログのみ)。
func (d *Device) send(ch chan string, v string) {
	select {
	case ch <- v:
	case <-d.closed:
	default:
		d.log.Debug("internal channel full, dropping line", "line", v)
	}
}

// --- モード/状態のアクセサ ---

func (d *Device) setMode(newline string, state rxState) {
	d.rxMu.Lock()
	d.newline = newline
	d.state = state
	d.rxMu.Unlock()
}

func (d *Device) setState(s rxState) {
	d.rxMu.Lock()
	d.state = s
	d.rxMu.Unlock()
}

func (d *Device) currentState() rxState {
	d.rxMu.Lock()
	defer d.rxMu.Unlock()
	return d.state
}

func (d *Device) currentNewline() string {
	d.rxMu.Lock()
	defer d.rxMu.Unlock()
	return d.newline
}

func (d *Device) clearBuffer() {
	d.bufMu.Lock()
	d.buf = d.buf[:0]
	d.bufMu.Unlock()
}

// --- ヘルパ ---

func parseHex(s string) int {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 16, 64)
	return int(v)
}

func hexToBytes(s string) ([]byte, error) {
	b := make([]byte, len(s)/2)
	for i := range b {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		b[i] = byte(v)
	}
	return b, nil
}
