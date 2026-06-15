package echonet

import (
	"encoding/binary"
	"fmt"
	"time"
)

const (
	EPCOperationStatus byte = 0x80 // 動作状態
	EPCInstallLocation byte = 0x81 // 設置場所
	EPCStandardVersion byte = 0x82 // 規格Version情報
	EPCFaultStatus     byte = 0x88 // 異常発生状態
	EPCMakerCode       byte = 0x8A // メーカコード
	EPCSerialNumber    byte = 0x8D // 製造番号
	EPCCurrentTime     byte = 0x97 // 現在時刻設定
	EPCCurrentDate     byte = 0x98 // 現在年月日設定
	EPCStatusChangeMap byte = 0x9D // 状変アナウンスプロパティマップ
	EPCSetPropertyMap  byte = 0x9E // Setプロパティマップ
	EPCGetPropertyMap  byte = 0x9F // Getプロパティマップ
)

// 0x80 動作状態
func DecodeOperationStatus(edt []byte) (on bool, err error) {
	if len(edt) != 1 {
		return false, fmt.Errorf("echonet: operation status expects 1 byte, got %d", len(edt))
	}
	switch edt[0] {
	case 0x30:
		return true, nil
	case 0x31:
		return false, nil
	default:
		return false, fmt.Errorf("echonet: invalid operation status %#x", edt[0])
	}
}

// 0x81 設置場所
func DecodeInstallLocation(edt []byte) (code byte, err error) {
	if len(edt) < 1 {
		return 0, fmt.Errorf("echonet: install location empty")
	}
	return edt[0], nil
}

// 0x88 異常発生状態
func DecodeFaultStatus(edt []byte) (fault bool, err error) {
	if len(edt) != 1 {
		return false, fmt.Errorf("echonet: fault status expects 1 byte, got %d", len(edt))
	}
	switch edt[0] {
	case 0x41:
		return true, nil
	case 0x42:
		return false, nil
	default:
		return false, fmt.Errorf("echonet: invalid fault status %#x", edt[0])
	}
}

// 0x97 現在時刻設定
func DecodeCurrentTime(edt []byte) (hour, min int, err error) {
	if len(edt) != 2 {
		return 0, 0, fmt.Errorf("echonet: current time expects 2 bytes, got %d", len(edt))
	}
	return int(edt[0]), int(edt[1]), nil
}

// 0x98 現在年月日設定
func DecodeCurrentDate(edt []byte) (year int, month time.Month, day int, err error) {
	if len(edt) != 4 {
		return 0, 0, 0, fmt.Errorf("echonet: current date expects 4 bytes, got %d", len(edt))
	}
	return int(binary.BigEndian.Uint16(edt[0:2])), time.Month(edt[2]), int(edt[3]), nil
}

// 0x9D/0x9E/0x9F プロパティマップ
func DecodePropertyMap(edt []byte) ([]byte, error) {
	if len(edt) < 1 {
		return nil, fmt.Errorf("echonet: property map empty")
	}
	n := int(edt[0])
	if n < 16 {
		if len(edt) < 1+n {
			return nil, fmt.Errorf("echonet: property map truncated (n=%d, len=%d)", n, len(edt))
		}
		out := make([]byte, n)
		copy(out, edt[1:1+n])
		return out, nil
	}
	if len(edt) < 17 {
		return nil, fmt.Errorf("echonet: property map bitmap truncated (len=%d)", len(edt))
	}
	var out []byte
	for i := range 16 {
		b := edt[1+i]
		for bit := range 8 {
			if b&(1<<uint(bit)) != 0 {
				out = append(out, byte((bit+8)<<4)|byte(i))
			}
		}
	}
	return out, nil
}
