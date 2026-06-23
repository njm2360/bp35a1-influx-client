package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	// BP35A1
	SerialPort string
	SerialBaud int
	BRouteID   string
	BRoutePass string
	EpanCache  string

	// 出力先: "influx"(デフォルト) または "stdout"
	Output string

	// InfluxDB v2
	InfluxURL    string
	InfluxToken  string
	InfluxOrg    string
	InfluxBucket string

	// タグ
	MeterTag string

	// ポーリング周期
	PollPower  time.Duration
	PollEnergy time.Duration

	// 応答待ちタイマー
	RequestTimeout     time.Duration
	RequestTimeoutLong time.Duration

	// 計測日時(EDT)の解釈に使うタイムゾーン
	Location *time.Location

	LogLevel string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	c := Config{
		SerialPort:         getEnv("SERIAL_PORT", "/dev/ttyUSB0"),
		SerialBaud:         getEnvInt("SERIAL_BAUD", 115200),
		BRouteID:           os.Getenv("BROUTE_ID"),
		BRoutePass:         os.Getenv("BROUTE_PASS"),
		EpanCache:          getEnv("EPAN_CACHE", "epan.json"),
		Output:             getEnv("OUTPUT", "influx"),
		InfluxURL:          os.Getenv("INFLUX_URL"),
		InfluxToken:        os.Getenv("INFLUX_TOKEN"),
		InfluxOrg:          os.Getenv("INFLUX_ORG"),
		InfluxBucket:       getEnv("INFLUX_BUCKET", "smartmeter"),
		MeterTag:           getEnv("METER_TAG", "meter01"),
		PollPower:          getEnvDuration("POLL_POWER", 10*time.Second),
		PollEnergy:         getEnvDuration("POLL_ENERGY", 60*time.Second),
		RequestTimeout:     getEnvDuration("REQUEST_TIMEOUT", 20*time.Second),
		RequestTimeoutLong: getEnvDuration("REQUEST_TIMEOUT_LONG", 60*time.Second),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
	}

	loc, err := time.LoadLocation(getEnv("METER_TIMEZONE", "Asia/Tokyo"))
	if err != nil {
		return Config{}, fmt.Errorf("config: invalid METER_TIMEZONE: %w", err)
	}
	c.Location = loc

	switch c.Output {
	case "influx", "stdout":
	default:
		return Config{}, fmt.Errorf("config: invalid OUTPUT %q (want \"influx\" or \"stdout\")", c.Output)
	}

	type envVar struct {
		name string
		val  string
	}
	required := []envVar{
		{"BROUTE_ID", c.BRouteID},
		{"BROUTE_PASS", c.BRoutePass},
	}
	// Influx 出力時のみ Influx 接続情報を必須にする。
	if c.Output == "influx" {
		required = append(required,
			envVar{"INFLUX_URL", c.InfluxURL},
			envVar{"INFLUX_TOKEN", c.InfluxToken},
			envVar{"INFLUX_ORG", c.InfluxOrg},
		)
	}

	var missing []string
	for _, kv := range required {
		if kv.val == "" {
			missing = append(missing, kv.name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("config: missing required env: %v", missing)
	}
	return c, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
