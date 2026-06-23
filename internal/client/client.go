package client

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"main/internal/echonet"
	"main/internal/transport"
)

const infBuffer = 32

const defaultDeadLinkAfter = 3 * time.Minute

type Client struct {
	tr  transport.Transport
	log *slog.Logger

	sendMu sync.Mutex
	reqSem chan struct{}

	mu      sync.Mutex
	nextTID uint16
	pending map[uint16]chan echonet.Frame

	infCh chan echonet.Frame

	wdMu          sync.Mutex
	firstFailAt   time.Time
	deadLinkAfter time.Duration

	reqTimeout time.Duration
}

func New(tr transport.Transport, log *slog.Logger, reqTimeout time.Duration) *Client {
	return &Client{
		tr:            tr,
		log:           log.With("component", "echonet"),
		reqSem:        make(chan struct{}, 1),
		pending:       make(map[uint16]chan echonet.Frame),
		infCh:         make(chan echonet.Frame, infBuffer),
		deadLinkAfter: defaultDeadLinkAfter,
		reqTimeout:    reqTimeout,
	}
}

func (c *Client) INF() <-chan echonet.Frame {
	return c.infCh
}

func (c *Client) Get(ctx context.Context, epcs ...byte) (echonet.Frame, error) {
	props := make([]echonet.Property, len(epcs))
	for i, e := range epcs {
		props[i] = echonet.Property{EPC: e}
	}
	return c.request(ctx, echonet.ESVGet, props)
}

func (c *Client) SetC(ctx context.Context, props ...echonet.Property) (echonet.Frame, error) {
	return c.request(ctx, echonet.ESVSetC, props)
}

func (c *Client) request(ctx context.Context, esv echonet.ESV, props []echonet.Property) (echonet.Frame, error) {
	if _, ok := ctx.Deadline(); !ok && c.reqTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.reqTimeout)
		defer cancel()
	}

	select {
	case c.reqSem <- struct{}{}:
		defer func() { <-c.reqSem }()
	case <-ctx.Done():
		return echonet.Frame{}, ctx.Err()
	}
	start := time.Now()

	tid := c.registerPending()
	ch := c.pendingChan(tid)
	defer c.clearPending(tid)

	f := echonet.Frame{
		TID:   tid,
		SEOJ:  echonet.EOJController,
		DEOJ:  echonet.EOJMeter,
		ESV:   esv,
		Props: props,
	}
	if err := c.send(ctx, f); err != nil {
		return echonet.Frame{}, fmt.Errorf("client: send: %w", err)
	}

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.recordTimeout()
		}
		return echonet.Frame{}, ctx.Err()
	case resp := <-ch:
		c.recordSuccess()
		elapsed := time.Since(start).Round(time.Millisecond)
		switch resp.ESV {
		case echonet.ESVGetSNA, echonet.ESVSetCSNA:
			c.log.Debug("request SNA", "tid", tid, "elapsed", elapsed)
			return resp, fmt.Errorf("client: meter returned SNA (esv=%#x)", byte(resp.ESV))
		}
		c.log.Debug("request ok", "tid", tid, "elapsed", elapsed)
		return resp, nil
	}
}

func (c *Client) recordSuccess() {
	c.wdMu.Lock()
	c.firstFailAt = time.Time{}
	c.wdMu.Unlock()
}

func (c *Client) recordTimeout() {
	now := time.Now()
	c.wdMu.Lock()
	if c.firstFailAt.IsZero() {
		c.firstFailAt = now
		c.wdMu.Unlock()
		return
	}
	dead := now.Sub(c.firstFailAt)
	if dead < c.deadLinkAfter {
		c.wdMu.Unlock()
		return
	}
	c.firstFailAt = now
	c.wdMu.Unlock()

	c.log.Warn("link unresponsive; forcing reconnect", "dead", dead)
	c.tr.Reconnect()
}

func (c *Client) Run(ctx context.Context) error {
	for {
		b, err := c.tr.Recv(ctx)
		if err != nil {
			return err
		}
		f, err := echonet.Decode(b)
		if err != nil {
			c.log.Warn("decode failed", "err", err, "bytes", len(b), "raw", hex.EncodeToString(b))
			continue
		}
		c.traceFrame(ctx, "rx", f, b)
		c.dispatch(ctx, f)
	}
}

func (c *Client) dispatch(ctx context.Context, f echonet.Frame) {
	switch {
	case f.ESV.IsResponse():
		c.deliver(f)
	case f.ESV == echonet.ESVINF:
		c.notify(f)
	case f.ESV == echonet.ESVINFC:
		c.sendINFCRes(ctx, f)
		c.notify(f)
	case f.ESV.IsRequest():
		c.log.Debug("unsupported request, sending SNA", "esv", fmt.Sprintf("0x%02X", byte(f.ESV)), "tid", f.TID)
		c.sendSNA(ctx, f)
	default:
		c.log.Debug("ignoring frame", "esv", fmt.Sprintf("0x%02X", byte(f.ESV)))
	}
}

func (c *Client) deliver(f echonet.Frame) {
	c.mu.Lock()
	ch, ok := c.pending[f.TID]
	c.mu.Unlock()
	if !ok {
		c.log.Warn("response for unknown TID (late arrival?)", "tid", f.TID)
		return
	}
	select {
	case ch <- f:
		c.log.Debug("response delivered", "tid", f.TID)
	default:
		c.log.Warn("pending channel full, dropping response", "tid", f.TID)
	}
}

func (c *Client) notify(f echonet.Frame) {
	select {
	case c.infCh <- f:
		c.log.Debug("INF dispatched", "esv", fmt.Sprintf("0x%02X", byte(f.ESV)), "props", len(f.Props))
	default:
		c.log.Warn("INF channel full, dropping notification (will recover via poll)")
	}
}

func (c *Client) sendINFCRes(ctx context.Context, in echonet.Frame) {
	// 同一 EPC リストで EDT を空にして応答。宛先は通知元(in.SEOJ)。
	props := make([]echonet.Property, len(in.Props))
	for i, p := range in.Props {
		props[i] = echonet.Property{EPC: p.EPC}
	}
	resp := echonet.Frame{
		TID:   in.TID,
		SEOJ:  echonet.EOJController,
		DEOJ:  in.SEOJ,
		ESV:   echonet.ESVINFCRes,
		Props: props,
	}
	if err := c.send(ctx, resp); err != nil {
		c.log.Warn("failed to send INFC_Res", "err", err, "tid", in.TID)
	}
}

func (c *Client) sendSNA(ctx context.Context, in echonet.Frame) {
	sna, ok := in.ESV.SNAResponse()
	if !ok {
		return
	}

	resp := echonet.Frame{
		TID:   in.TID,
		SEOJ:  echonet.EOJController,
		DEOJ:  in.SEOJ,
		ESV:   sna,
		Props: in.Props,
	}
	if err := c.send(ctx, resp); err != nil {
		c.log.Warn("failed to send SNA", "err", err, "tid", in.TID, "esv", fmt.Sprintf("0x%02X", byte(sna)))
	}
}

func (c *Client) send(ctx context.Context, f echonet.Frame) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	b := f.Encode()
	c.traceFrame(ctx, "tx", f, b)
	return c.tr.Send(ctx, b)
}

// traceFrame は LOG_LEVEL=debug のとき ECHONET フレームを生バイト付きで記録する。
// hex 変換は debug 無効時には行わない。
func (c *Client) traceFrame(ctx context.Context, dir string, f echonet.Frame, raw []byte) {
	if !c.log.Enabled(ctx, slog.LevelDebug) {
		return
	}
	c.log.Debug("echonet frame", "dir", dir, "frame", f, "raw", hex.EncodeToString(raw))
}

func (c *Client) registerPending() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextTID++
	if c.nextTID == 0 {
		c.nextTID = 1
	}
	tid := c.nextTID
	c.pending[tid] = make(chan echonet.Frame, 1)
	return tid
}

func (c *Client) pendingChan(tid uint16) chan echonet.Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[tid]
}

func (c *Client) clearPending(tid uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, tid)
}
