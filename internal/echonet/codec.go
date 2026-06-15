package echonet

import (
	"encoding/binary"
	"fmt"
)

const noData32 = 0xFFFFFFFE // 計測データなし(積算電力量プロパティ)

func DecodeU32(edt []byte) (v uint32, noData bool, err error) {
	if len(edt) != 4 {
		return 0, false, fmt.Errorf("echonet: u32 expects 4 bytes, got %d", len(edt))
	}
	v = binary.BigEndian.Uint32(edt)
	if v == noData32 {
		return v, true, nil
	}
	return v, false, nil
}

func DecodeS32(edt []byte) (v int32, noData bool, err error) {
	if len(edt) != 4 {
		return 0, false, fmt.Errorf("echonet: s32 expects 4 bytes, got %d", len(edt))
	}
	u := binary.BigEndian.Uint32(edt)
	if u == 0x7FFFFFFE || u == 0x80000000 {
		return int32(u), true, nil
	}
	return int32(u), false, nil
}

func DecodeString(edt []byte) string {
	end := len(edt)
	for end > 0 && edt[end-1] == 0x00 {
		end--
	}
	return string(edt[:end])
}
