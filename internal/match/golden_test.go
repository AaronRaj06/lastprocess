package match

import (
	"bytes"
	"flag"
	"os"
	"runtime"
	"testing"

	"github.com/AaronRaj06/lastprocess/internal/replay"
)

var update = flag.Bool("update", false, "rewrite testdata/example.replay")

// The committed replay was recorded on darwin/arm64; CI runs on linux/amd64.
// Replaying it there, and re-running every bot (RL float inference and Lua included),
// checks that the sim and the bots are deterministic across architectures.
const goldenFile = "testdata/example.replay"

var goldenLineup = []string{"rl", "baseline", "lua:../../bots/examples/chaser.lua", "random", "rl", "baseline", "lua:../../bots/examples/turret.lua", "random"}

func TestGoldenReplayAcrossArch(t *testing.T) {
	g, err := NewGameFromLineup(7, goldenLineup)
	if err != nil {
		t.Fatal(err)
	}
	g.Run()
	got := g.Log.Encode()
	if *update {
		if err := os.WriteFile(goldenFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s on %s/%s: %d ticks, hash %016x", goldenFile, runtime.GOOS, runtime.GOARCH, g.Log.Ticks(), g.Log.Hash)
		return
	}
	raw, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatal(err)
	}
	want, err := replay.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := want.Verify(); err != nil {
		t.Fatalf("committed replay does not re-simulate on %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	if want.Hash != g.Log.Hash || !bytes.Equal(want.Actions, g.Log.Actions) {
		t.Fatalf("bots chose different actions on %s/%s: hash %016x, recorded %016x", runtime.GOOS, runtime.GOARCH, g.Log.Hash, want.Hash)
	}
	t.Logf("%d ticks reproduced bit-identically on %s/%s", want.Ticks(), runtime.GOOS, runtime.GOARCH)
}
