package bp35a1

import (
	"encoding/json"
	"os"
)

// Epan は SKSCAN のアクティブスキャンで得られる PAN 記述子(EPANDESC)。
type Epan struct {
	Channel     int    `json:"channel"`
	ChannelPage int    `json:"channelPage"`
	PanID       int    `json:"panId"`
	MACAddress  string `json:"macAddress"`
	LQI         int    `json:"lqi"`
	PairID      string `json:"pairId"`
}

// loadEpan はキャッシュファイルから Epan を読み込む。存在しない/不正なら ok=false。
func loadEpan(path string) (Epan, bool) {
	if path == "" {
		return Epan{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Epan{}, false
	}
	var e Epan
	if err := json.Unmarshal(b, &e); err != nil {
		return Epan{}, false
	}
	if e.MACAddress == "" {
		return Epan{}, false
	}
	return e, true
}

// saveEpan は Epan をキャッシュファイルへ保存する(path が空なら何もしない)。
func saveEpan(path string, e Epan) error {
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
