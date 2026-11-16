// Command headless runs matches with no SSH and no rendering, as fast as the
// CPU allows. It is the benchmark and evaluation tool, and with -serve it is
// the environment server for the Python trainer.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AaronRaj06/lastprocess/internal/match"
)

func main() {
	bots := flag.String("bots", "baseline,random*7", `lineup, e.g. "rl:policy.onnx,baseline*7"; kinds: random, baseline, rl:<file>, lua:<file>`)
	matches := flag.Int("matches", 1000, "number of matches")
	seed := flag.Uint64("seed", 1, "first match seed; match i uses seed+i")
	workers := flag.Int("workers", runtime.NumCPU(), "parallel matches")
	serve := flag.String("serve", "", "serve the RL environment on this TCP address instead of running matches")
	flag.Parse()

	lineup, err := match.ParseLineup(*bots)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *serve != "" {
		if err := serveEnv(*serve, lineup[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	wins := make([]atomic.Int64, len(lineup))
	var draws, ticks, next atomic.Int64
	start := time.Now()
	var wg sync.WaitGroup
	for range *workers {
		wg.Go(func() {
			for {
				i := next.Add(1) - 1
				if i >= int64(*matches) {
					return
				}
				g, err := match.NewGameFromLineup(*seed+uint64(i), lineup)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				if w := g.Run(); w >= 0 {
					wins[w].Add(1)
				} else {
					draws.Add(1)
				}
				ticks.Add(int64(g.Sim.Tick))
			}
		})
	}
	wg.Wait()
	el := time.Since(start).Seconds()

	fmt.Printf("%d matches, %d combatants, %d workers, %.2fs\n", *matches, len(lineup), *workers, el)
	fmt.Printf("%.0f matches/s, %.2fM ticks/s (%.0fx real time per core), mean %.0f ticks/match\n",
		float64(*matches)/el, float64(ticks.Load())/el/1e6,
		float64(ticks.Load())/el/20/float64(*workers), float64(ticks.Load())/float64(*matches))
	// Wins are reported per slot group so "rl,baseline*7" shows the RL bot against the field.
	for i := 0; i < len(lineup); {
		j, w := i, int64(0)
		for ; j < len(lineup) && lineup[j] == lineup[i]; j++ {
			w += wins[j].Load()
		}
		fmt.Printf("  %-28s x%-2d wins %6d  (%.1f%%)\n", lineup[i], j-i, w, 100*float64(w)/float64(*matches))
		i = j
	}
	fmt.Printf("  %-28s     %10d  (%.1f%%)\n", "no survivor", draws.Load(), 100*float64(draws.Load())/float64(*matches))
}
