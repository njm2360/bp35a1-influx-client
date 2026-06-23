package client

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"main/internal/echonet"
)

// fakeTransport は送信フレームを responder に渡し、その戻りを Recv で返す。
type fakeTransport struct {
	responder func(echonet.Frame) []echonet.Frame // 送信に対する応答フレーム群(空可)
	recvCh    chan []byte
	closeOnce sync.Once
	closed    chan struct{}

	reconnectMu sync.Mutex
	reconnects  int
}

func newFakeTransport(responder func(echonet.Frame) []echonet.Frame) *fakeTransport {
	return &fakeTransport{
		responder: responder,
		recvCh:    make(chan []byte, 16),
		closed:    make(chan struct{}),
	}
}

func (f *fakeTransport) Send(_ context.Context, payload []byte) error {
	frame, err := echonet.Decode(payload)
	if err != nil {
		return err
	}
	if f.responder != nil {
		for _, r := range f.responder(frame) {
			f.recvCh <- r.Encode()
		}
	}
	return nil
}

func (f *fakeTransport) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.closed:
		return nil, io.EOF
	case b := <-f.recvCh:
		return b, nil
	}
}

func (f *fakeTransport) Reconnect() {
	f.reconnectMu.Lock()
	f.reconnects++
	f.reconnectMu.Unlock()
}

func (f *fakeTransport) reconnectCount() int {
	f.reconnectMu.Lock()
	defer f.reconnectMu.Unlock()
	return f.reconnects
}

func (f *fakeTransport) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

// inject は外部(メータ)発の非同期フレームを注入する。
func (f *fakeTransport) inject(fr echonet.Frame) { f.recvCh <- fr.Encode() }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGetCorrelatesByTID(t *testing.T) {
	tr := newFakeTransport(func(req echonet.Frame) []echonet.Frame {
		// 同一 TID で Get_Res を返す。
		return []echonet.Frame{{
			TID: req.TID, SEOJ: echonet.EOJMeter, DEOJ: echonet.EOJController,
			ESV:   echonet.ESVGetRes,
			Props: []echonet.Property{{EPC: echonet.EPCInstantPower, EDT: []byte{0, 0, 2, 0}}},
		}}
	})
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)

	gctx, gcancel := context.WithTimeout(ctx, time.Second)
	defer gcancel()
	resp, err := c.Get(gctx, echonet.EPCInstantPower)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.ESV != echonet.ESVGetRes || len(resp.Props) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGetTimeout(t *testing.T) {
	tr := newFakeTransport(func(echonet.Frame) []echonet.Frame { return nil }) // 応答しない
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)

	gctx, gcancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer gcancel()
	if _, err := c.Get(gctx, echonet.EPCInstantPower); err == nil {
		t.Fatal("expected timeout error")
	}
	// タイムアウト後 pending は掃除されていること。
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending not cleaned: %d", n)
	}
}

func TestSNAReturnsError(t *testing.T) {
	tr := newFakeTransport(func(req echonet.Frame) []echonet.Frame {
		return []echonet.Frame{{
			TID: req.TID, SEOJ: echonet.EOJMeter, DEOJ: echonet.EOJController,
			ESV:   echonet.ESVGetSNA,
			Props: []echonet.Property{{EPC: echonet.EPCInstantPower}},
		}}
	})
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)
	gctx, gcancel := context.WithTimeout(ctx, time.Second)
	defer gcancel()
	if _, err := c.Get(gctx, echonet.EPCInstantPower); err == nil {
		t.Fatal("expected SNA error")
	}
}

func TestRequestsSerialized(t *testing.T) {
	var mu sync.Mutex
	var inFlight, maxInFlight int
	gate := make(chan struct{})
	tr := newFakeTransport(func(req echonet.Frame) []echonet.Frame {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		<-gate // メータが処理中の間ビジー状態を維持する
		mu.Lock()
		inFlight--
		mu.Unlock()
		return []echonet.Frame{{
			TID: req.TID, SEOJ: echonet.EOJMeter, DEOJ: echonet.EOJController,
			ESV:   echonet.ESVGetRes,
			Props: []echonet.Property{{EPC: echonet.EPCInstantPower, EDT: []byte{0, 0, 2, 0}}},
		}}
	})
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)

	const n = 3
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			gctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, _ = c.Get(gctx, echonet.EPCInstantPower)
		})
	}
	// 直列化されていれば、同時に待機中の responder は常に1つだけ。
	for range n {
		gate <- struct{}{}
	}
	wg.Wait()

	mu.Lock()
	got := maxInFlight
	mu.Unlock()
	if got != 1 {
		t.Fatalf("requests not serialized: max in-flight=%d, want 1", got)
	}
}

func TestDeadLinkWatchdog(t *testing.T) {
	tr := newFakeTransport(func(echonet.Frame) []echonet.Frame { return nil })
	c := New(tr, testLogger(), 0)

	// 単発の無応答ではタイマ起動のみ。発火しない。
	c.recordTimeout()
	if got := tr.reconnectCount(); got != 0 {
		t.Fatalf("single timeout triggered reconnect: got %d", got)
	}

	// 連続して失敗してもまだ deadLink 未満なら発火しない(バースト耐性)
	c.recordTimeout()
	c.recordTimeout()
	if got := tr.reconnectCount(); got != 0 {
		t.Fatalf("reconnect before deadLink elapsed: got %d", got)
	}

	// 最初の無応答から deadLink を超えた状態を作る。
	c.wdMu.Lock()
	c.firstFailAt = time.Now().Add(-c.deadLinkAfter - time.Second)
	c.wdMu.Unlock()
	c.recordTimeout()
	if got := tr.reconnectCount(); got != 1 {
		t.Fatalf("expected reconnect after deadLink elapsed, got %d", got)
	}

	// 発火後はウィンドウが再スタートし、次の単発無応答では再発火しない。
	c.recordTimeout()
	if got := tr.reconnectCount(); got != 1 {
		t.Fatalf("reconnect re-fired within window: got %d", got)
	}

	// 成功(SNA 含む)でタイマがリセットされ、その後の単発無応答でも発火しない。
	c.recordSuccess()
	c.recordTimeout()
	if got := tr.reconnectCount(); got != 1 {
		t.Fatalf("reconnect fired after success reset: got %d", got)
	}
}

func TestSetCCorrelates(t *testing.T) {
	tr := newFakeTransport(func(req echonet.Frame) []echonet.Frame {
		if req.ESV != echonet.ESVSetC {
			return nil
		}
		return []echonet.Frame{{
			TID: req.TID, SEOJ: echonet.EOJMeter, DEOJ: echonet.EOJController,
			ESV:   echonet.ESVSetRes,
			Props: []echonet.Property{{EPC: echonet.EPCHistoryDay1}},
		}}
	})
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)

	gctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	resp, err := c.SetC(gctx, echonet.Property{EPC: echonet.EPCHistoryDay1, EDT: []byte{0x01}})
	if err != nil {
		t.Fatalf("SetC: %v", err)
	}
	if resp.ESV != echonet.ESVSetRes {
		t.Fatalf("want SetRes, got %#x", byte(resp.ESV))
	}
}

// TestUnsupportedRequestGetsSNA はメータ発の要求(ここでは Get)に対し
// コントローラが SNA を返すこと(dispatch の IsRequest 経路 + sendSNA)を確認する。
func TestUnsupportedRequestGetsSNA(t *testing.T) {
	var mu sync.Mutex
	var sawSNA bool
	tr := newFakeTransport(func(req echonet.Frame) []echonet.Frame {
		if req.ESV == echonet.ESVGetSNA {
			mu.Lock()
			sawSNA = true
			mu.Unlock()
		}
		return nil
	})
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)

	tr.inject(echonet.Frame{
		TID: 0x1234, SEOJ: echonet.EOJMeter, DEOJ: echonet.EOJController,
		ESV:   echonet.ESVGet,
		Props: []echonet.Property{{EPC: echonet.EPCInstantPower}},
	})

	deadline := time.After(time.Second)
	for {
		mu.Lock()
		ok := sawSNA
		mu.Unlock()
		if ok {
			return
		}
		select {
		case <-deadline:
			t.Fatal("Get_SNA was not sent for unsupported request")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestINFCSendsResponseAndNotifies(t *testing.T) {
	var mu sync.Mutex
	var sawINFCRes bool
	tr := newFakeTransport(func(req echonet.Frame) []echonet.Frame {
		if req.ESV == echonet.ESVINFCRes {
			mu.Lock()
			sawINFCRes = true
			mu.Unlock()
		}
		return nil
	})
	c := New(tr, testLogger(), 0)
	ctx := t.Context()
	go c.Run(ctx)

	// メータからの INFC を注入。
	tr.inject(echonet.Frame{
		TID: 0x9999, SEOJ: echonet.EOJMeter, DEOJ: echonet.EOJController,
		ESV:   echonet.ESVINFC,
		Props: []echonet.Property{{EPC: echonet.EPCFaultStatus, EDT: []byte{0x42}}},
	})

	select {
	case f := <-c.INF():
		if f.ESV != echonet.ESVINFC {
			t.Fatalf("want INFC on stream, got %#x", byte(f.ESV))
		}
	case <-time.After(time.Second):
		t.Fatal("no INF notification received")
	}

	// INFC_Res が送られたか(少し待つ)。
	deadline := time.After(time.Second)
	for {
		mu.Lock()
		ok := sawINFCRes
		mu.Unlock()
		if ok {
			return
		}
		select {
		case <-deadline:
			t.Fatal("INFC_Res was not sent")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
