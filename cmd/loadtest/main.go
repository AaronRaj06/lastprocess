// Command loadtest opens many real SSH sessions against a running server,
// plays in all of them, and reports whether the server held 20 Hz. A few
// "probe" sessions run a terminal emulator to measure input-to-frame latency:
// the time from sending a key until a frame arrives whose HUD acknowledges it.
//
// Start the server with -loadtest and raised limits first; see `make loadtest`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/AaronRaj06/lastprocess/internal/render"
	"github.com/AaronRaj06/lastprocess/internal/session"
)

var (
	addr     = flag.String("addr", "127.0.0.1:2222", "server address")
	sessions = flag.Int("sessions", 100, "concurrent SSH sessions")
	probes   = flag.Int("probes", 16, "sessions that measure input-to-frame latency")
	duration = flag.Duration("duration", 30*time.Second, "measurement time after all sessions are up")
	ackRE    = regexp.MustCompile(`ack (\d+)`)
)

func dial(i int) (*gossh.Client, error) {
	return gossh.Dial("tcp", *addr, &gossh.ClientConfig{
		User:            fmt.Sprintf("load%d", i),
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), // test tool against a local server
		Auth: []gossh.AuthMethod{gossh.KeyboardInteractive(
			func(string, string, []string, []bool) ([]string, error) { return nil, nil })},
		Timeout: 20 * time.Second,
	})
}

func stats(cmd string) (st session.Stats, err error) {
	c, err := dial(0)
	if err != nil {
		return st, err
	}
	defer func() { _ = c.Close() }()
	s, err := c.NewSession()
	if err != nil {
		return st, err
	}
	out, err := s.Output(cmd)
	if err != nil {
		return st, fmt.Errorf("%s: %w (is the server running with -loadtest?)", cmd, err)
	}
	return st, json.Unmarshal(out, &st)
}

type result struct {
	mu        sync.Mutex
	latencies []time.Duration
	failed    atomic.Int64
	bytes     atomic.Int64
}

// play runs one session until stop closes. Every session sends about five keys a second.
func play(i int, probe bool, stop <-chan struct{}, ready *sync.WaitGroup, res *result) {
	var once sync.Once
	up := func() { once.Do(ready.Done) }
	defer up()
	c, err := dial(i)
	if err != nil {
		res.failed.Add(1)
		return
	}
	defer func() { _ = c.Close() }()
	s, err := c.NewSession()
	if err != nil {
		res.failed.Add(1)
		return
	}
	in, _ := s.StdinPipe()
	out, _ := s.StdoutPipe()
	if s.RequestPty("xterm", 43, 80, gossh.TerminalModes{}) != nil || s.Shell() != nil {
		res.failed.Add(1)
		return
	}
	up()

	// acked is the newest "ack N" value on the probe's emulated screen.
	var acked atomic.Int64
	acked.Store(-1)
	go func() {
		scr := render.NewScreen(80, 43)
		buf := make([]byte, 16384)
		for {
			n, err := out.Read(buf)
			res.bytes.Add(int64(n))
			if probe && n > 0 {
				_, _ = scr.Write(buf[:n])
				if m := ackRE.FindSubmatch(scr.Rows[41]); m != nil {
					v, _ := strconv.Atoi(string(m[1]))
					acked.Store(int64(v))
				} else {
					acked.Store(-1) // dead or in the lobby: no ack counter on screen
				}
			}
			if err != nil {
				return
			}
		}
	}()

	rng := rand.New(rand.NewPCG(uint64(i), 1))
	keys := []string{"w", "a", "s", "d", "\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "W", "D"}
	for {
		select {
		case <-stop:
			return
		case <-time.After(150*time.Millisecond + time.Duration(rng.IntN(100))*time.Millisecond):
		}
		before := acked.Load()
		sent := time.Now()
		if _, err := io.WriteString(in, keys[rng.IntN(len(keys))]); err != nil {
			res.failed.Add(1)
			return
		}
		if !probe || before < 0 {
			continue
		}
		// Wait for the frame that acknowledges this key.
		for time.Since(sent) < time.Second {
			if acked.Load() > before {
				res.mu.Lock()
				res.latencies = append(res.latencies, time.Since(sent))
				res.mu.Unlock()
				break
			}
			time.Sleep(200 * time.Microsecond)
		}
	}
}

func main() {
	flag.Parse()
	res := &result{}
	stop := make(chan struct{})
	var ready, done sync.WaitGroup
	sem := make(chan struct{}, 64) // parallel SSH handshakes
	for i := range *sessions {
		ready.Add(1)
		sem <- struct{}{}
		done.Go(func() {
			go func() { ready.Wait() }()
			play(i+1, i < *probes, stop, &ready, res)
		})
		go func() { time.Sleep(20 * time.Millisecond); <-sem }()
	}
	ready.Wait()
	fmt.Fprintf(os.Stderr, "%d sessions started (%d failed); settling 8s, then measuring %s\n",
		*sessions, res.failed.Load(), *duration)
	time.Sleep(8 * time.Second) // let lobbies fill and matches start
	if _, err := stats("stats reset"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res.mu.Lock()
	res.latencies = nil
	res.mu.Unlock()
	bytes0 := res.bytes.Load()
	time.Sleep(*duration)
	st, err := stats("stats")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	recv := res.bytes.Load() - bytes0
	res.mu.Lock()
	lat := slices.Clone(res.latencies)
	res.mu.Unlock()
	close(stop)
	slices.Sort(lat)
	pct := func(p int) float64 {
		if len(lat) == 0 {
			return 0
		}
		return float64(lat[len(lat)*p/100].Microseconds()) / 1000
	}
	stable := st.LateP99 < 5000 && st.LateTicks*100 <= st.Ticks
	fmt.Printf("sessions=%d failed=%d matches=%d stable20Hz=%v | tick late p50/p99/max %.2f/%.2f/%.2f ms, late ticks %d of %d | tick work p50/p99 %.2f/%.2f ms | input-to-frame p50/p99 %.1f/%.1f ms (n=%d) | %.0f B/frame, %.1f kB/s per session\n",
		st.Sessions-1, res.failed.Load(), st.Matches, stable,
		float64(st.LateP50)/1000, float64(st.LateP99)/1000, float64(st.LateMax)/1000, st.LateTicks, st.Ticks,
		float64(st.WorkP50)/1000, float64(st.WorkP99)/1000, pct(50), pct(99), len(lat),
		float64(st.Bytes)/float64(max(st.Frames, 1)), float64(recv)/duration.Seconds()/1000/float64(*sessions))
	done.Wait()
}
