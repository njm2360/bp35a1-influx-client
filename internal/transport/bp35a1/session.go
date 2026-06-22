package bp35a1

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

func (d *Device) setup(ctx context.Context) error {
	if err := d.initModule(ctx); err != nil {
		return err
	}
	stack, app, err := d.getVersions(ctx)
	if err != nil {
		return err
	}
	d.log.Info("module version", "stack", stack, "app", app)
	epan, err := d.establish(ctx)
	if err != nil {
		return err
	}
	go d.manage(epan)
	return nil
}

func (d *Device) getVersions(ctx context.Context) (stack, app string, err error) {
	resp, err := d.command(ctx, cmdSKVER, nil, time.Second)
	if err != nil {
		return "", "", fmt.Errorf("bp35a1: read stack version: %w", err)
	}
	stack = strings.TrimSpace(strings.TrimPrefix(resp, "EVER"))

	resp, err = d.command(ctx, cmdSKAPPVER, nil, time.Second)
	if err != nil {
		return "", "", fmt.Errorf("bp35a1: read app version: %w", err)
	}
	app = strings.TrimSpace(strings.TrimPrefix(resp, "EAPPVER"))

	return stack, app, nil
}

func (d *Device) establish(ctx context.Context) (Epan, error) {
	epan, cached := loadEpan(d.epanCache)
	if cached {
		d.log.Info("using cached EPAN", "channel", epan.Channel, "pan_id", epan.PanID)
	} else {
		var err error
		if epan, err = d.scanAndCache(ctx); err != nil {
			return Epan{}, err
		}
	}

	ip, err := d.connect(ctx, epan)
	if err != nil && cached {
		d.log.Warn("connect with cached EPAN failed; rescanning once", "err", err)
		if epan, err = d.scanAndCache(ctx); err != nil {
			return Epan{}, err
		}
		ip, err = d.connect(ctx, epan)
	}
	if err != nil {
		return Epan{}, err
	}
	d.setIP(ip)
	d.log.Info("PANA connected", "ip", ip)
	return epan, nil
}

func (d *Device) signalReconnect() {
	select {
	case d.reconnectCh <- struct{}{}:
	default:
	}
}

func (d *Device) manage(epan Epan) {
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.closed:
			return
		case <-d.reconnectCh:
			d.log.Warn("PANA session expired; reconnecting")
			d.sessionEst.Store(false)
			next, ok := d.reconnect(epan)
			if !ok {
				return
			}
			epan = next
		case <-d.events:
		}
	}
}

const (
	rescanAfterFailures = 5
	resetAfterFailures  = 20
)

type recoveryStep int

const (
	stepNone recoveryStep = iota
	stepRescan
	stepReset
)

func recoveryFor(failures int) recoveryStep {
	switch {
	case failures > 0 && failures%resetAfterFailures == 0:
		return stepReset
	case failures > 0 && failures%rescanAfterFailures == 0:
		return stepRescan
	}
	return stepNone
}

func (d *Device) reestablish(epan Epan) (Epan, bool) {
	const maxBackoff = time.Minute

	backoff := 5 * time.Second
	for failures := 0; ; failures++ {
		if d.ctx.Err() != nil {
			return epan, false
		}

		step := recoveryFor(failures)
		if step == stepReset {
			d.log.Warn("resetting BP35A1 module", "failures", failures)
			if err := d.initModule(d.ctx); err != nil {
				d.log.Warn("module reinit during reconnect failed", "err", err)
			}
		}
		if step != stepNone {
			if next, err := d.scanAndCache(d.ctx); err != nil {
				d.log.Warn("rescan during reconnect failed", "err", err)
			} else {
				epan = next
			}
		}

		ip, err := d.connect(d.ctx, epan)
		if err == nil {
			d.setIP(ip)
			d.log.Info("PANA reconnected", "ip", ip, "attempt", failures+1)
			return epan, true
		}
		if d.ctx.Err() != nil {
			return epan, false
		}
		d.log.Warn("reconnect attempt failed", "attempt", failures+1, "err", err, "backoff", backoff)

		select {
		case <-d.ctx.Done():
			return epan, false
		case <-d.closed:
			return epan, false
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (d *Device) scanAndCache(ctx context.Context) (Epan, error) {
	scanned, err := d.scan(ctx, 6)
	if err != nil {
		return Epan{}, err
	}
	if err := saveEpan(d.epanCache, *scanned); err != nil {
		d.log.Warn("failed to cache EPAN", "err", err)
	}
	return *scanned, nil
}

func (d *Device) initModule(ctx context.Context) error {
	d.echo.Store(true)

	if err := d.correctBaudrate(ctx, d.baud); err != nil {
		return err
	}
	if _, err := d.command(ctx, cmdSKRESET, nil, 3*time.Second); err != nil {
		return err
	}
	if _, err := d.command(ctx, cmdSKSREG, []string{"SFE", "0"}, time.Second); err != nil {
		return err
	}
	d.echo.Store(false)

	opt, err := d.command(ctx, cmdROPT, nil, time.Second)
	if err != nil {
		return err
	}
	if opt != "01" {
		if _, err := d.command(ctx, cmdWOPT, []string{"01"}, time.Second); err != nil {
			return err
		}
	}

	rbid := strings.ReplaceAll(d.routeBID, "-", "")
	if _, err := d.command(ctx, cmdSKSETRBID, []string{rbid}, time.Second); err != nil {
		return err
	}
	pwd := d.password
	if _, err := d.command(ctx, cmdSKSETPWD, []string{fmt.Sprintf("%X", len(pwd)), pwd}, time.Second); err != nil {
		return err
	}
	return nil
}

func (d *Device) correctBaudrate(ctx context.Context, preferred int) error {
	bauds := append([]int{preferred}, candidateBauds...)

	// タイミングによりSKVERが稀にFAILするため2ループ試す
	for range 2 {
		for _, b := range bauds {
			if err := d.port.SetMode(&serial.Mode{BaudRate: b}); err != nil {
				continue
			}
			d.log.Debug("testing baudrate", "baud", b)

			d.clearBuffer()
			d.traceSerial("tx", []byte(crlf))
			_, _ = d.port.Write([]byte(crlf))
			_ = d.port.ResetInputBuffer()
			_ = d.port.ResetOutputBuffer()

			resp, err := d.command(ctx, cmdSKVER, nil, time.Second)
			if err == nil && strings.HasPrefix(resp, "EVER") {
				d.log.Info("baudrate detected", "baud", b)
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return errors.New("bp35a1: no valid baudrate found")
}

func (d *Device) scan(ctx context.Context, initDuration int) (*Epan, error) {
	d.log.Info("scanning for smart meter")
	for duration := initDuration; duration <= 7; duration++ {
		if _, err := d.command(ctx, cmdSKSCAN, []string{"2", "FFFFFFFF", strconv.Itoa(duration)}, time.Second); err != nil {
			return nil, err
		}
		var found *Epan
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-d.closed:
				return nil, ErrClosed
			case e := <-d.epans:
				found = &e
			case ev := <-d.events:
				if ev.code == evActiveScanOK {
					found = drainEpan(d.epans, found)
					break wait
				}
			case <-time.After(30 * time.Second):
				break wait
			}
		}
		if found != nil {
			return found, nil
		}
	}
	return nil, errors.New("bp35a1: EPAN not found")
}

func drainEpan(ch <-chan Epan, cur *Epan) *Epan {
	for {
		select {
		case e := <-ch:
			cur = &e
		default:
			return cur
		}
	}
}

func (d *Device) connect(ctx context.Context, epan Epan) (string, error) {
	if _, err := d.command(ctx, cmdSKSREG, []string{"S2", fmt.Sprintf("%X", epan.Channel)}, time.Second); err != nil {
		return "", err
	}
	if _, err := d.command(ctx, cmdSKSREG, []string{"S3", fmt.Sprintf("%X", epan.PanID)}, time.Second); err != nil {
		return "", err
	}
	ip, err := d.command(ctx, cmdSKLL64, []string{epan.MACAddress}, time.Second)
	if err != nil {
		return "", err
	}
	if _, err := d.command(ctx, cmdSKJOIN, []string{ip}, time.Second); err != nil {
		return "", err
	}

	d.log.Info("waiting for PANA connection", "ip", ip)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-d.closed:
			return "", ErrClosed
		case ev := <-d.events:
			switch ev.code {
			case evPANAConnectOK:
				return ip, nil
			case evPANAConnectErr:
				return "", ErrPANAConnect
			}
		case <-time.After(30 * time.Second):
			return "", errors.New("bp35a1: PANA connect timeout")
		}
	}
}
