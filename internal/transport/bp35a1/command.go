package bp35a1

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (d *Device) command(ctx context.Context, cmd string, params []string, timeout time.Duration) (string, error) {
	return d.exec(ctx, cmd, params, nil, timeout)
}

func (d *Device) sendUDP(ctx context.Context, handle uint8, sec SecOption, payload []byte) (string, error) {
	params := []string{
		fmt.Sprintf("%d", handle),
		d.getIP(),
		fmt.Sprintf("%04X", echonetPort),
		fmt.Sprintf("%d", sec),
		fmt.Sprintf("%04X", len(payload)),
	}
	return d.exec(ctx, cmdSKSENDTO, params, payload, time.Second)
}

func (d *Device) exec(ctx context.Context, cmd string, params []string, data []byte, timeout time.Duration) (string, error) {
	d.cmdMu.Lock()
	defer d.cmdMu.Unlock()
	start := time.Now()

	d.drainResponses()

	newline := crlf
	state := stateNormal
	switch cmd {
	case cmdROPT, cmdWOPT, cmdRUART, cmdWUART:
		newline = cr
		if cmd == cmdROPT || cmd == cmdRUART {
			state = stateProductRead
		}
	case cmdSKLL64:
		state = stateSKLL64
	}
	d.setMode(newline, state)

	line := cmd
	if len(params) > 0 {
		line += " " + strings.Join(params, " ")
	}
	sent := []byte(line)
	if data != nil {
		sent = append(sent, ' ')
		sent = append(sent, data...)
	} else {
		sent = append(sent, newline...)
	}
	d.traceSerial("tx", sent)
	if _, err := d.port.Write(sent); err != nil {
		return "", fmt.Errorf("bp35a1: write %s: %w", cmd, err)
	}

	if d.echo.Load() {
		d.skipEcho(line)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-d.closed:
		return "", ErrClosed
	case <-timer.C:
		return "", fmt.Errorf("bp35a1: %s result timeout", cmd)
	case res := <-d.results:
		elapsed := time.Since(start).Round(time.Millisecond)
		if strings.HasPrefix(res, "FAIL") {
			d.log.Debug("command failed", "cmd", cmd, "result", res, "elapsed", elapsed)
			return "", commandError(res)
		}
		d.log.Debug("command ok", "cmd", cmd, "elapsed", elapsed)
		return d.collectResponses(), nil
	}
}

func (d *Device) skipEcho(sent string) {
	select {
	case <-time.After(time.Second):
		d.log.Debug("echo timeout", "expected", sent)
	case <-d.closed:
	case line := <-d.responses:
		if line == sent {
			return
		}
		d.log.Debug("echo mismatch", "expected", sent, "got", line)
		select {
		case d.responses <- line:
		default:
		}
	}
}

func (d *Device) collectResponses() string {
	var lines []string
	for {
		select {
		case r := <-d.responses:
			lines = append(lines, r)
		default:
			return strings.Join(lines, crlf)
		}
	}
}

func (d *Device) drainResponses() {
	var n int
	for {
		select {
		case <-d.results:
			n++
		case <-d.responses:
			n++
		default:
			if n > 0 {
				d.log.Debug("drained stale responses", "count", n)
			}
			return
		}
	}
}

func commandError(result string) error {
	code := ""
	if len(result) > 5 {
		code = strings.TrimSpace(result[5:])
	}
	var msg string
	switch code {
	case "ER04":
		msg = "command not supported"
	case "ER05":
		msg = "wrong number of arguments"
	case "ER06":
		msg = "argument format or value out of range"
	case "ER09":
		msg = "UART input error"
	case "ER10":
		msg = "command accepted but execution failed"
	default:
		msg = "unknown command error"
	}
	return fmt.Errorf("bp35a1: %s (%s)", msg, code)
}
