package bp35a1

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (d *Device) command(ctx context.Context, cmd string, params []string, timeout time.Duration, expectEcho bool) (string, error) {
	return d.exec(ctx, cmd, params, nil, timeout, expectEcho)
}

func (d *Device) sendUDP(ctx context.Context, params []string, payload []byte) (string, error) {
	return d.exec(ctx, cmdSKSENDTO, params, payload, time.Second, false)
}

func (d *Device) exec(ctx context.Context, cmd string, params []string, data []byte, timeout time.Duration, expectEcho bool) (string, error) {
	d.cmdMu.Lock()
	defer d.cmdMu.Unlock()

	d.drainResponses()

	newline := crlf
	state := stateNormal
	switch cmd {
	case cmdROPT, cmdWOPT, cmdRUART, cmdWUART:
		newline = cr
		if cmd == cmdROPT {
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

	if expectEcho {
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
		if strings.HasPrefix(res, "FAIL") {
			return "", commandError(res)
		}
		return d.collectResponses(), nil
	}
}

func (d *Device) skipEcho(sent string) {
	select {
	case <-time.After(time.Second):
	case <-d.closed:
	case line := <-d.responses:
		if line == sent {
			return
		}
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
	for {
		select {
		case <-d.results:
		case <-d.responses:
		default:
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
