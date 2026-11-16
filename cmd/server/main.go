// Command server is the SSH game server: `ssh -p 2222 localhost` to play.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/ssh"

	"github.com/AaronRaj06/lastprocess/internal/match"
	"github.com/AaronRaj06/lastprocess/internal/session"
	"github.com/AaronRaj06/lastprocess/internal/store"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := flag.String("addr", ":2222", "listen address")
	data := flag.String("data", "data", "directory for the database and the SSH host key")
	size := flag.Int("size", 8, "combatants per match (2-16)")
	fill := flag.String("fill", "baseline", `bots that fill empty slots, cycled, e.g. "rl:policy.onnx,baseline"`)
	maxMatches := flag.Int("max-matches", 32, "concurrent matches")
	maxSessions := flag.Int("max-sessions", 256, "concurrent SSH sessions")
	maxPerIP := flag.Int("max-per-ip", 4, "concurrent sessions per client address")
	connRate := flag.Float64("conn-rate", 1, "new sessions per second allowed per address (burst 10)")
	loadtest := flag.Bool("loadtest", false, `allow "stats reset" (for load tests only)`)
	flag.Parse()

	fillSpecs := strings.Split(*fill, ",")
	for _, spec := range fillSpecs {
		if _, err := match.NewController(spec, 0); err != nil {
			return fmt.Errorf("-fill: %w", err)
		}
	}
	if *size < 2 || *size > 16 {
		return errors.New("-size must be 2 to 16")
	}
	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	st, err := store.Open(*data + "/lastprocess.db")
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	srv := &session.Server{Store: st, MaxSessions: *maxSessions, MaxPerIP: *maxPerIP, AllowReset: *loadtest}
	srv.Mgr = match.NewManager(match.Config{
		Size: *size, Fill: fillSpecs, MaxMatches: *maxMatches,
		LobbyTicks: 3 * world.TickRate, ResultTicks: 4 * world.TickRate,
		Tick:  time.Second / world.TickRate,
		OnEnd: func(r match.Result) int64 { return saveMatch(st, r) },
	})

	s, err := session.NewSSH(srv, *addr, *data+"/host_ed25519", *connRate)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("listening on %s", *addr)
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			log.Print(err)
			stop()
		}
	}()
	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Shutdown(shut)
}

// saveMatch stores a finished match if a person or an uploaded bot took part;
// the endless bot-only matches are not worth the disk.
func saveMatch(st *store.Store, r match.Result) int64 {
	var keys []string
	winner, winnerKey := "nobody", ""
	involved := false
	for i, sl := range r.Slots {
		if sl.Owner == nil {
			continue
		}
		involved = true
		if sl.Owner.Key != "" && sl.Kind == "human" {
			keys = append(keys, sl.Owner.Key)
			if i == r.Winner {
				winnerKey = sl.Owner.Key
			}
		}
	}
	if r.Winner >= 0 {
		winner = r.Slots[r.Winner].Name
	}
	if !involved {
		return 0
	}
	id, err := st.SaveMatch(r.Ticks, winner, winnerKey, keys, r.Log.Encode())
	if err != nil {
		log.Printf("save match: %v", err)
	}
	return id
}
