package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"net"

	"github.com/AaronRaj06/lastprocess/internal/bot"
	"github.com/AaronRaj06/lastprocess/internal/match"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// The RL environment protocol (see docs/decisions.md D11). One TCP connection
// drives N environments at once; all numbers are little-endian.
//
//	server -> client on connect:  u32 obsLen, u32 numActions
//	client -> server 'R': u64 seed, u32 n      reply: n*obsLen f32
//	client -> server 'S': n action bytes       reply: n*obsLen f32, n f32 rewards, n u8 done, n u8 won
//
// The learner is always slot 0; the opponents are the rest of the lineup.
// A finished environment resets itself; the observation returned with done=1
// is the first one of the next episode.

// Reward shaping: damage dealt and HP changes count 0.01 per point, the end of
// the episode counts +1 for a win and -1 otherwise.
const (
	rewardPerHP = 0.01
	rewardEnd   = 1
)

type env struct {
	game     *match.Game
	action   world.Action
	seed     uint64
	last     world.Combatant
	opponent []string
}

func (e *env) reset() {
	e.seed++
	lineup := append([]string{"random"}, e.opponent...)
	g, err := match.NewGameFromLineup(e.seed, lineup)
	if err != nil {
		panic(err) // the lineup was validated at startup
	}
	g.Slots[0].Ctl = bot.Func(func(*world.Observation) world.Action { return e.action })
	e.game = g
	e.last, _ = g.Sim.Stats(g.IDs[0])
}

func (e *env) step(a world.Action) (reward float32, done, won bool) {
	e.action = a
	e.game.Tick()
	c, _ := e.game.Sim.Stats(e.game.IDs[0])
	reward = rewardPerHP * float32(c.Dealt-e.last.Dealt+c.HP-e.last.HP)
	e.last = c
	if c.HP > 0 && !e.game.Sim.Done() {
		return reward, false, false
	}
	won = e.game.Sim.Done() && e.game.Winner() == 0
	if won {
		return reward + rewardEnd, true, true
	}
	return reward - rewardEnd, true, false
}

func (e *env) observe(dst []float32) {
	var o world.Observation
	e.game.Sim.Observe(e.game.IDs[0], &o)
	o.Vector(dst)
}

func serveEnv(addr string, opponents []string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("env server on %s, opponents %v", ln.Addr(), opponents)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer func() { _ = conn.Close() }()
			if err := serveConn(conn, opponents); err != io.EOF {
				log.Printf("env client: %v", err)
			}
		}()
	}
}

func serveConn(conn net.Conn, opponents []string) error {
	r, w := bufio.NewReader(conn), bufio.NewWriterSize(conn, 1<<16)
	le := binary.LittleEndian
	if err := binary.Write(w, le, [2]uint32{world.ObsLen, uint32(world.NumActions)}); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	var envs []*env
	var obs []float32
	var out, actions []byte
	for {
		op, err := r.ReadByte()
		if err != nil {
			return err
		}
		switch op {
		case 'R':
			var hdr struct {
				Seed uint64
				N    uint32
			}
			if err := binary.Read(r, le, &hdr); err != nil {
				return err
			}
			if hdr.N == 0 || hdr.N > 4096 {
				return fmt.Errorf("bad env count %d", hdr.N)
			}
			envs = make([]*env, hdr.N)
			for i := range envs {
				// Spread the seeds so parallel envs never replay each other's maps.
				envs[i] = &env{seed: hdr.Seed + uint64(i)<<32, opponent: opponents}
				envs[i].reset()
			}
			obs = make([]float32, int(hdr.N)*world.ObsLen)
			actions = make([]byte, hdr.N)
			out = out[:0]
		case 'S':
			if envs == nil {
				return fmt.Errorf("step before reset")
			}
			if _, err := io.ReadFull(r, actions); err != nil {
				return err
			}
			n := len(envs)
			tail := make([]byte, 6*n) // rewards, then done flags, then win flags
			for i, e := range envs {
				a := world.Action(actions[i])
				if !a.Valid() {
					a = world.Stay
				}
				reward, done, won := e.step(a)
				le.PutUint32(tail[4*i:], math.Float32bits(reward))
				if done {
					tail[4*n+i] = 1
					e.reset()
				}
				if won {
					tail[5*n+i] = 1
				}
			}
			out = tail
		default:
			return fmt.Errorf("unknown op %q", op)
		}
		for i, e := range envs {
			e.observe(obs[i*world.ObsLen : (i+1)*world.ObsLen])
		}
		if err := binary.Write(w, le, obs); err != nil {
			return err
		}
		if _, err := w.Write(out); err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
}
