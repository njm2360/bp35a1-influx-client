package bp35a1

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEpanSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "epan.json")
	in := Epan{Channel: 0x21, ChannelPage: 0x09, PanID: 0x8888, MACAddress: "001D129012345678", LQI: 0xE1, PairID: "00112233"}
	if err := saveEpan(path, in); err != nil {
		t.Fatalf("saveEpan: %v", err)
	}
	got, ok := loadEpan(path)
	if !ok {
		t.Fatal("loadEpan returned ok=false")
	}
	if got != in {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, in)
	}
}

func TestLoadEpanRejects(t *testing.T) {
	dir := t.TempDir()

	if _, ok := loadEpan(""); ok {
		t.Error("empty path should fail")
	}
	if _, ok := loadEpan(filepath.Join(dir, "missing.json")); ok {
		t.Error("missing file should fail")
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadEpan(bad); ok {
		t.Error("invalid JSON should fail")
	}

	noMAC := filepath.Join(dir, "nomac.json")
	if err := os.WriteFile(noMAC, []byte(`{"channel":33}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadEpan(noMAC); ok {
		t.Error("missing MACAddress should fail")
	}
}

func TestSaveEpanEmptyPathNoop(t *testing.T) {
	if err := saveEpan("", Epan{MACAddress: "x"}); err != nil {
		t.Fatalf("saveEpan with empty path should be a no-op, got %v", err)
	}
}
