"""Gymnasium / Stable-Baselines3 environments backed by the Go headless sim.

The sim runs in a separate process (`cmd/headless -serve`) and is driven over
one TCP socket. One message steps every environment at once, so the Python
side costs one round trip per vectorised step instead of one per environment.
Protocol: see cmd/headless/env.go.
"""
from __future__ import annotations

import os
import socket
import struct
import subprocess
import time
from pathlib import Path

import gymnasium as gym
import numpy as np
from stable_baselines3.common.vec_env import VecEnv

ROOT = Path(__file__).resolve().parent.parent


class SimClient:
    """Starts the Go env server and speaks its binary protocol."""

    def __init__(self, opponents: str = "baseline", port: int = 0):
        binary = ROOT / "bin" / "headless"
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/headless"], cwd=ROOT, check=True)
        port = port or _free_port()
        # Slot 0 is the learner; the server ignores its spec and takes actions from us.
        self.proc = subprocess.Popen(
            [str(binary), "-serve", f"127.0.0.1:{port}", "-bots", f"random,{opponents}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(100):
            try:
                self.sock = socket.create_connection(("127.0.0.1", port))
                break
            except OSError:
                time.sleep(0.05)
        else:
            raise RuntimeError("env server did not start")
        self.sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        self.obs_len, self.num_actions = struct.unpack("<II", self._read(8))
        self.n = 0

    def _read(self, n: int) -> bytes:
        buf = bytearray()
        while len(buf) < n:
            chunk = self.sock.recv(n - len(buf))
            if not chunk:
                raise ConnectionError("env server closed the connection")
            buf += chunk
        return bytes(buf)

    def _obs(self) -> np.ndarray:
        raw = self._read(4 * self.n * self.obs_len)
        return np.frombuffer(raw, dtype="<f4").reshape(self.n, self.obs_len).copy()

    def reset(self, seed: int, n: int) -> np.ndarray:
        self.n = n
        self.sock.sendall(b"R" + struct.pack("<QI", seed, n))
        return self._obs()

    def step(self, actions: np.ndarray):
        self.sock.sendall(b"S" + np.asarray(actions, dtype=np.uint8).tobytes())
        obs = self._obs()
        tail = self._read(6 * self.n)
        rewards = np.frombuffer(tail[: 4 * self.n], dtype="<f4").copy()
        dones = np.frombuffer(tail[4 * self.n: 5 * self.n], dtype=np.uint8).astype(bool)
        wins = np.frombuffer(tail[5 * self.n:], dtype=np.uint8).astype(bool)
        return obs, rewards, dones, wins

    def close(self):
        try:
            self.sock.close()
        finally:
            self.proc.terminate()
            self.proc.wait(timeout=5)


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _spaces(client: SimClient):
    obs = gym.spaces.Box(-np.inf, np.inf, (client.obs_len,), np.float32)
    return obs, gym.spaces.Discrete(client.num_actions)


class LPSEnv(gym.Env):
    """A single match as a standard Gymnasium environment."""

    metadata = {"render_modes": []}

    def __init__(self, opponents: str = "baseline"):
        self.client = SimClient(opponents)
        self.observation_space, self.action_space = _spaces(self.client)

    def reset(self, *, seed=None, options=None):
        super().reset(seed=seed)
        if seed is None:
            seed = int(self.np_random.integers(1 << 31))
        return self.client.reset(seed, 1)[0], {}

    def step(self, action):
        obs, rewards, dones, wins = self.client.step(np.array([action]))
        # The server has already reset a finished match: obs is the next episode's first.
        return obs[0], float(rewards[0]), bool(dones[0]), False, {"won": bool(wins[0])}

    def close(self):
        self.client.close()


class LPSVecEnv(VecEnv):
    """N matches stepped with one socket round trip. Used for training."""

    def __init__(self, n_envs: int, opponents: str = "baseline", seed: int = 0):
        self.client = SimClient(opponents)
        obs_space, act_space = _spaces(self.client)
        super().__init__(n_envs, obs_space, act_space)
        self._seed = seed
        self._actions = None
        self._ep_ret = np.zeros(n_envs, np.float32)
        self._ep_len = np.zeros(n_envs, np.int64)

    def seed(self, seed=None):
        self._seed = seed or 0
        return [self._seed + i for i in range(self.num_envs)]

    def reset(self):
        self._ep_ret[:] = 0
        self._ep_len[:] = 0
        return self.client.reset(self._seed, self.num_envs)

    def step_async(self, actions):
        self._actions = actions

    def step_wait(self):
        obs, rewards, dones, wins = self.client.step(self._actions)
        self._ep_ret += rewards
        self._ep_len += 1
        infos = [{} for _ in range(self.num_envs)]
        for i in np.flatnonzero(dones):
            # "episode" is what SB3's logger reads; "won" feeds our win-rate curve.
            infos[i] = {"episode": {"r": float(self._ep_ret[i]), "l": int(self._ep_len[i])},
                        "won": bool(wins[i]), "TimeLimit.truncated": False}
            self._ep_ret[i] = 0
            self._ep_len[i] = 0
        return obs, rewards, dones, infos

    def close(self):
        self.client.close()

    # The remaining VecEnv methods are not meaningful for a remote sim.
    def get_attr(self, attr_name, indices=None):
        return [getattr(self, attr_name, None)] * self.num_envs

    def set_attr(self, attr_name, value, indices=None):
        raise NotImplementedError

    def env_method(self, method_name, *args, indices=None, **kwargs):
        raise NotImplementedError

    def env_is_wrapped(self, wrapper_class, indices=None):
        return [False] * self.num_envs
