package echonet

import (
	"encoding/binary"
	"fmt"
)

const (
	EHD1 byte = 0x10 // ECHONET Lite 規定
	EHD2 byte = 0x81 // 形式1(規定電文形式)
)

type ESV byte

const (
	ESVSetISNA ESV = 0x50 // プロパティ値書き込み要求不可応答(応答不要)
	ESVSetCSNA ESV = 0x51 // プロパティ値書き込み要求不可応答
	ESVGetSNA  ESV = 0x52 // プロパティ値読み出し不可応答
	ESVINFSNA  ESV = 0x53 // プロパティ値通知不可応答
	ESVSetI    ESV = 0x60 // プロパティ値書き込み要求(応答不要)
	ESVSetC    ESV = 0x61 // プロパティ値書き込み要求(応答要)
	ESVGet     ESV = 0x62 // プロパティ値読み出し要求
	ESVINFREQ  ESV = 0x63 // プロパティ値通知要求
	ESVSetRes  ESV = 0x71 // プロパティ値書き込み応答
	ESVGetRes  ESV = 0x72 // プロパティ値読み出し応答
	ESVINF     ESV = 0x73 // プロパティ値通知
	ESVINFC    ESV = 0x74 // プロパティ値通知(応答要)
	ESVINFCRes ESV = 0x7A // プロパティ値通知応答
)

func (e ESV) IsResponse() bool {
	switch e {
	case ESVGetRes, ESVGetSNA, ESVSetRes, ESVSetCSNA:
		return true
	}
	return false
}

func (e ESV) IsRequest() bool {
	_, ok := e.SNAResponse()
	return ok
}

func (e ESV) SNAResponse() (ESV, bool) {
	switch e {
	case ESVSetI:
		return ESVSetISNA, true
	case ESVSetC:
		return ESVSetCSNA, true
	case ESVGet:
		return ESVGetSNA, true
	case ESVINFREQ:
		return ESVINFSNA, true
	}
	return 0, false
}

type EOJ [3]byte

var (
	EOJMeter      = EOJ{0x02, 0x88, 0x01} // 低圧スマート電力量メータ
	EOJController = EOJ{0x05, 0xFF, 0x01} // コントローラ
)

type Frame struct {
	TID   uint16
	SEOJ  EOJ
	DEOJ  EOJ
	ESV   ESV
	Props []Property
}

type Property struct {
	EPC byte
	EDT []byte
}

func (f Frame) Encode() []byte {
	// 固定ヘッダ 11byte(EHD2+TID2+SEOJ3+DEOJ3+ESV1) + OPC 1byte + 各プロパティ(EPC+PDC+EDT)
	size := 11 + 1
	for _, p := range f.Props {
		size += 2 + len(p.EDT)
	}
	b := make([]byte, 0, size)
	b = append(b, EHD1, EHD2)
	b = binary.BigEndian.AppendUint16(b, f.TID)
	b = append(b, f.SEOJ[:]...)
	b = append(b, f.DEOJ[:]...)
	b = append(b, byte(f.ESV))
	b = append(b, byte(len(f.Props)))
	for _, p := range f.Props {
		b = append(b, p.EPC, byte(len(p.EDT)))
		b = append(b, p.EDT...)
	}
	return b
}

func Decode(b []byte) (Frame, error) {
	if len(b) < 12 {
		return Frame{}, fmt.Errorf("echonet: frame too short: %d bytes", len(b))
	}
	if b[0] != EHD1 || b[1] != EHD2 {
		return Frame{}, fmt.Errorf("echonet: unexpected EHD %#x %#x", b[0], b[1])
	}
	var f Frame
	f.TID = binary.BigEndian.Uint16(b[2:4])
	copy(f.SEOJ[:], b[4:7])
	copy(f.DEOJ[:], b[7:10])
	f.ESV = ESV(b[10])
	opc := int(b[11])

	off := 12
	f.Props = make([]Property, 0, opc)
	for i := range opc {
		if off+2 > len(b) {
			return Frame{}, fmt.Errorf("echonet: truncated property header at prop %d", i)
		}
		epc := b[off]
		pdc := int(b[off+1])
		off += 2
		if off+pdc > len(b) {
			return Frame{}, fmt.Errorf("echonet: truncated EDT at prop %d (pdc=%d)", i, pdc)
		}
		edt := make([]byte, pdc)
		copy(edt, b[off:off+pdc])
		off += pdc
		f.Props = append(f.Props, Property{EPC: epc, EDT: edt})
	}
	return f, nil
}
