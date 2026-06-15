package echonet

import (
	"fmt"
	"log/slog"
	"strings"
)

// String は ESV を "GetRes(0x72)" のような可読表現にする。
func (e ESV) String() string {
	name := map[ESV]string{
		ESVSetISNA: "SetI_SNA",
		ESVSetCSNA: "SetC_SNA",
		ESVGetSNA:  "Get_SNA",
		ESVINFSNA:  "INF_SNA",
		ESVSetI:    "SetI",
		ESVSetC:    "SetC",
		ESVGet:     "Get",
		ESVINFREQ:  "INF_REQ",
		ESVSetRes:  "Set_Res",
		ESVGetRes:  "Get_Res",
		ESVINF:     "INF",
		ESVINFC:    "INFC",
		ESVINFCRes: "INFC_Res",
	}[e]
	if name == "" {
		name = "UNKNOWN"
	}
	return fmt.Sprintf("%s(%#02x)", name, byte(e))
}

// String は EOJ を "02:88:01" 形式で返す。
func (o EOJ) String() string {
	return fmt.Sprintf("%02X:%02X:%02X", o[0], o[1], o[2])
}

// String は Property を "E7=000001F4" 形式で返す(EDT 無しは "E7=")。
func (p Property) String() string {
	return fmt.Sprintf("%02X=%X", p.EPC, p.EDT)
}

// LogValue は slog でフレームを構造化ログとして展開する(slog.LogValuer 実装)。
func (f Frame) LogValue() slog.Value {
	props := make([]string, len(f.Props))
	for i, p := range f.Props {
		props[i] = p.String()
	}
	return slog.GroupValue(
		slog.Int("tid", int(f.TID)),
		slog.String("esv", f.ESV.String()),
		slog.String("seoj", f.SEOJ.String()),
		slog.String("deoj", f.DEOJ.String()),
		slog.Int("opc", len(f.Props)),
		slog.String("props", strings.Join(props, " ")),
	)
}
