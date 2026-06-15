package echonet

import (
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	in := Frame{
		TID:  0x1234,
		SEOJ: EOJController,
		DEOJ: EOJMeter,
		ESV:  ESVGetRes,
		Props: []Property{
			{EPC: EPCInstantPower, EDT: []byte{0x00, 0x00, 0x02, 0x00}},
			{EPC: EPCInstantCurrent, EDT: []byte{0x00, 0x19, 0x7F, 0xFE}},
		},
	}
	got, err := Decode(in.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.TID != in.TID || got.ESV != in.ESV || got.SEOJ != in.SEOJ || got.DEOJ != in.DEOJ {
		t.Fatalf("header mismatch: %+v vs %+v", got, in)
	}
	if len(got.Props) != 2 {
		t.Fatalf("want 2 props, got %d", len(got.Props))
	}
	if got.Props[0].EPC != EPCInstantPower || string(got.Props[1].EDT) != string(in.Props[1].EDT) {
		t.Fatalf("props mismatch: %+v", got.Props)
	}
}

func TestDecodeGetRequestNoEDT(t *testing.T) {
	in := Frame{
		TID: 1, SEOJ: EOJController, DEOJ: EOJMeter, ESV: ESVGet,
		Props: []Property{{EPC: EPCInstantPower}},
	}
	b := in.Encode()
	// Get 要求は PDC=0。エンコード長 = ヘッダ12(OPC含む) + EPC1 + PDC1 = 14。
	if len(b) != 14 {
		t.Fatalf("want 14 bytes, got %d", len(b))
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.Props) != 1 || len(got.Props[0].EDT) != 0 {
		t.Fatalf("unexpected props: %+v", got.Props)
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := map[string][]byte{
		"too short":     {0x10, 0x81, 0x00},
		"bad EHD":       {0x20, 0x81, 0, 0, 2, 0x88, 1, 5, 0xFF, 1, byte(ESVGet), 0},
		"truncated EDT": {0x10, 0x81, 0, 1, 2, 0x88, 1, 5, 0xFF, 1, byte(ESVGetRes), 1, 0xE7, 0x04, 0x00},
	}
	for name, b := range tests {
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestIsResponse(t *testing.T) {
	for _, e := range []ESV{ESVGetRes, ESVGetSNA, ESVSetRes, ESVSetCSNA} {
		if !e.IsResponse() {
			t.Errorf("%#x should be response", byte(e))
		}
	}
	for _, e := range []ESV{ESVINF, ESVINFC, ESVGet, ESVSetC} {
		if e.IsResponse() {
			t.Errorf("%#x should not be response", byte(e))
		}
	}
}

func TestIsRequest(t *testing.T) {
	for _, e := range []ESV{ESVSetI, ESVSetC, ESVGet, ESVINFREQ} {
		if !e.IsRequest() {
			t.Errorf("%#x should be request", byte(e))
		}
	}
	for _, e := range []ESV{ESVGetRes, ESVINF, ESVINFC, ESVSetRes} {
		if e.IsRequest() {
			t.Errorf("%#x should not be request", byte(e))
		}
	}
}

func TestSNAResponse(t *testing.T) {
	cases := map[ESV]ESV{
		ESVSetI:   ESVSetISNA,
		ESVSetC:   ESVSetCSNA,
		ESVGet:    ESVGetSNA,
		ESVINFREQ: ESVINFSNA,
	}
	for req, wantSNA := range cases {
		got, ok := req.SNAResponse()
		if !ok || got != wantSNA {
			t.Errorf("SNAResponse(%#x) = %#x,%v want %#x", byte(req), byte(got), ok, byte(wantSNA))
		}
	}
	// 要求でない ESV は SNA を持たない。
	if _, ok := ESVGetRes.SNAResponse(); ok {
		t.Error("GetRes should not have an SNA response")
	}
}

func TestEncodeExactBytes(t *testing.T) {
	f := Frame{
		TID:   0x1234,
		SEOJ:  EOJController,
		DEOJ:  EOJMeter,
		ESV:   ESVGet,
		Props: []Property{{EPC: EPCInstantPower}},
	}
	want := []byte{
		0x10, 0x81, // EHD
		0x12, 0x34, // TID
		0x05, 0xFF, 0x01, // SEOJ コントローラ
		0x02, 0x88, 0x01, // DEOJ メータ
		0x62,       // ESV Get
		0x01,       // OPC
		0xE7, 0x00, // EPC, PDC=0
	}
	got := f.Encode()
	if string(got) != string(want) {
		t.Fatalf("encode mismatch:\n got %x\nwant %x", got, want)
	}
}

func TestDecodeOPCZero(t *testing.T) {
	f := Frame{TID: 7, SEOJ: EOJController, DEOJ: EOJMeter, ESV: ESVINF}
	got, err := Decode(f.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.Props) != 0 {
		t.Fatalf("want 0 props, got %d", len(got.Props))
	}
}

func TestDecodeTruncatedPropHeader(t *testing.T) {
	// OPC=1 だが EPC/PDC の 2byte が欠落。
	b := []byte{0x10, 0x81, 0, 1, 5, 0xFF, 1, 0x02, 0x88, 0x01, byte(ESVGetRes), 1}
	if _, err := Decode(b); err == nil {
		t.Fatal("expected truncated property header error")
	}
}

func TestDecodeMultiPropVaryingEDT(t *testing.T) {
	in := Frame{
		TID: 0xABCD, SEOJ: EOJMeter, DEOJ: EOJController, ESV: ESVGetRes,
		Props: []Property{
			{EPC: EPCCoefficient, EDT: []byte{0x00, 0x00, 0x00, 0x01}},
			{EPC: EPCUnit, EDT: []byte{0x01}},
			{EPC: EPCScheduledFwd, EDT: make([]byte, 11)},
		},
	}
	got, err := Decode(in.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.Props) != 3 {
		t.Fatalf("want 3 props, got %d", len(got.Props))
	}
	if len(got.Props[0].EDT) != 4 || len(got.Props[1].EDT) != 1 || len(got.Props[2].EDT) != 11 {
		t.Fatalf("EDT lengths wrong: %+v", got.Props)
	}
}
