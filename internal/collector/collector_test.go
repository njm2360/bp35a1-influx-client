package collector

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"main/internal/config"
	"main/internal/echonet"
	"main/internal/model"
)

type fakeELClient struct {
	edt map[byte][]byte
	inf chan echonet.Frame
}

func (s *fakeELClient) Get(_ context.Context, epcs ...byte) (echonet.Frame, error) {
	f := echonet.Frame{ESV: echonet.ESVGetRes}
	for _, e := range epcs {
		if v, ok := s.edt[e]; ok {
			f.Props = append(f.Props, echonet.Property{EPC: e, EDT: v})
		}
	}
	return f, nil
}

func (s *fakeELClient) INF() <-chan echonet.Frame { return s.inf }

type fakeWriter struct {
	mu       sync.Mutex
	power    []model.Power
	total    []model.EnergyTotal
	min1     []model.Energy1Min
	min30    []model.Energy30Min
	statuses []model.Status
	metas    []model.Meta
}

func (w *fakeWriter) WritePower(_ context.Context, m model.Power) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.power = append(w.power, m)
	return nil
}
func (w *fakeWriter) WriteEnergyTotal(_ context.Context, m model.EnergyTotal) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total = append(w.total, m)
	return nil
}
func (w *fakeWriter) WriteEnergy1Min(_ context.Context, m model.Energy1Min) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.min1 = append(w.min1, m)
	return nil
}
func (w *fakeWriter) WriteEnergy30Min(_ context.Context, m model.Energy30Min) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.min30 = append(w.min30, m)
	return nil
}
func (w *fakeWriter) WriteStatus(_ context.Context, m model.Status) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statuses = append(w.statuses, m)
	return nil
}
func (w *fakeWriter) WriteMeta(_ context.Context, m model.Meta) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.metas = append(w.metas, m)
	return nil
}
func (w *fakeWriter) Close() error { return nil }

func testConfig() config.Config {
	return config.Config{
		GetTimeout:     time.Second,
		GetTimeoutLong: time.Second,
		Location:       time.FixedZone("JST", 9*3600),
	}
}

func newTestCollector(s *fakeELClient, w *fakeWriter) *Collector {
	return New(s, w, testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPollPower(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		echonet.EPCInstantPower:   {0x00, 0x00, 0x02, 0x00}, // 512W
		echonet.EPCInstantCurrent: {0x00, 0x19, 0x7F, 0xFE}, // R=2.5A, T 未計測
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	if err := c.pollPower(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.power) != 1 {
		t.Fatalf("want 1 power write, got %d", len(w.power))
	}
	p := w.power[0]
	if !p.HasWatt || p.Watt != 512 || !p.HasCurrentR || p.CurrentR != 2.5 || p.HasCurrentT {
		t.Fatalf("unexpected power: %+v", p)
	}
}

func TestPollPowerNoData(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		echonet.EPCInstantPower:   {0x7F, 0xFF, 0xFF, 0xFE}, // no-data
		echonet.EPCInstantCurrent: {0x00, 0x19, 0x7F, 0xFE}, // R=2.5A, T 未計測
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	if err := c.pollPower(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.power) != 1 {
		t.Fatalf("want 1 power write, got %d", len(w.power))
	}
	p := w.power[0]
	// 電力は欠測 → HasWatt=false(0W として記録しない)。電流は有効。
	if p.HasWatt {
		t.Fatalf("HasWatt should be false on no-data, got %+v", p)
	}
	if !p.HasCurrentR || p.CurrentR != 2.5 {
		t.Fatalf("current should still be recorded: %+v", p)
	}
}

func TestPollEnergyMinuteAppliesParams(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		echonet.EPCCumulativeFwd: {0x00, 0x01, 0x86, 0xA0}, // 100000
		echonet.EPCCumulative1Min: {
			0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00,
			0x00, 0x01, 0x86, 0xA0, // 正方向 100000
			0x00, 0x00, 0x00, 0x00, // 逆方向
		},
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	c.params.Store(&model.MeterParams{Coefficient: 1, UnitKWh: 0.1})
	c.propMap = map[byte]struct{}{
		echonet.EPCCumulativeFwd:  {},
		echonet.EPCCumulative1Min: {},
	}

	if err := c.pollEnergyMinute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.total) != 1 || w.total[0].KWh != 10000 || w.total[0].Raw != 100000 {
		t.Fatalf("unexpected total: %+v", w.total)
	}
	if len(w.min1) != 1 || w.min1[0].KWh != 10000 || w.min1[0].Raw != 100000 {
		t.Fatalf("unexpected min1: %+v", w.min1)
	}
}

func TestPollEnergyMinuteSkipsUnsupported(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		echonet.EPCCumulativeFwd: {0x00, 0x01, 0x86, 0xA0},
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	c.params.Store(&model.MeterParams{Coefficient: 1, UnitKWh: 0.1})
	c.propMap = map[byte]struct{}{echonet.EPCCumulativeFwd: {}}

	if err := c.pollEnergyMinute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.total) != 1 {
		t.Fatalf("want 1 total write, got %d", len(w.total))
	}
	if len(w.min1) != 0 {
		t.Fatalf("min1 should be skipped (D0 not in propMap), got %d", len(w.min1))
	}
}

// pollEnergy1Min 単体の換算・時刻処理を検証する。
func TestPollEnergy1Min(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		// 0xD0 は 15byte: 年月日+時分秒+正方向(100000)+逆方向
		echonet.EPCCumulative1Min: {
			0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00,
			0x00, 0x01, 0x86, 0xA0, // 正方向 100000
			0x00, 0x00, 0x00, 0x00, // 逆方向
		},
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	params := model.MeterParams{Coefficient: 1, UnitKWh: 0.1}

	if err := c.pollEnergy1Min(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if len(w.min1) != 1 || w.min1[0].KWh != 10000 || w.min1[0].Raw != 100000 {
		t.Fatalf("unexpected min1: %+v", w.min1)
	}
	// 計測時刻は EDT 埋め込み値(2026-06-15 10:30:00)を使う。
	want := time.Date(2026, 6, 15, 10, 30, 0, 0, c.cfg.Location)
	if !w.min1[0].Time.Equal(want) {
		t.Fatalf("min1 time want %v, got %v", want, w.min1[0].Time)
	}
}

func TestEnergyNoDataSkipped(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		echonet.EPCCumulativeFwd: {0xFF, 0xFF, 0xFF, 0xFE}, // noData
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	c.params.Store(&model.MeterParams{Coefficient: 1, UnitKWh: 0.1})
	c.propMap = map[byte]struct{}{echonet.EPCCumulativeFwd: {}}
	if err := c.pollEnergyMinute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.total) != 0 {
		t.Fatalf("noData should be skipped, got %d writes", len(w.total))
	}
}

func TestRefreshMetaSetsParams(t *testing.T) {
	s := &fakeELClient{edt: map[byte][]byte{
		echonet.EPCMakerCode:   {0x00, 0x00, 0x16},
		echonet.EPCCoefficient: {0x00, 0x00, 0x00, 0x01},
		echonet.EPCUnit:        {0x01}, // 0.1 kWh
		echonet.EPCDigits:      {0x08},
	}}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	if err := c.refreshMeta(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := c.loadParams()
	if !p.Valid() || p.Coefficient != 1 || p.UnitKWh != 0.1 {
		t.Fatalf("params not set: %+v", p)
	}
	if len(w.metas) != 1 || w.metas[0].Digits != 8 {
		t.Fatalf("unexpected meta: %+v", w.metas)
	}
}

func TestHandleINFScheduled30(t *testing.T) {
	s := &fakeELClient{}
	w := &fakeWriter{}
	c := newTestCollector(s, w)
	c.params.Store(&model.MeterParams{Coefficient: 1, UnitKWh: 0.1})

	edt := []byte{0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00, 0x00, 0x01, 0x86, 0xA0} // 100000
	c.handleINFFrame(context.Background(), echonet.Frame{
		ESV:   echonet.ESVINF,
		Props: []echonet.Property{{EPC: echonet.EPCScheduledFwd, EDT: edt}},
	})
	if len(w.min30) != 1 || w.min30[0].KWh != 10000 {
		t.Fatalf("unexpected energy30: %+v", w.min30)
	}
	want := time.Date(2026, 6, 15, 10, 30, 0, 0, c.cfg.Location)
	if !w.min30[0].Time.Equal(want) {
		t.Fatalf("ts want %v, got %v", want, w.min30[0].Time)
	}
}

func TestNextMinuteMark(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 20, 0, 0, time.UTC)
	got := nextMinuteMark(now, []int{5, 35})
	want := time.Date(2026, 6, 15, 10, 35, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	// :40 のときは次の時間の :05。
	got = nextMinuteMark(time.Date(2026, 6, 15, 10, 40, 0, 0, time.UTC), []int{5, 35})
	want = time.Date(2026, 6, 15, 11, 5, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}
