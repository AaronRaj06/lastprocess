"""Train a PPO bot against the scripted baseline and export it to ONNX.

    uv run python train.py                 # train with the default config
    uv run python train.py --steps 200000  # quick smoke run

Everything that affects the result is in CONFIG or on the command line and is
written to runs/<name>/config.json next to the learning curves (progress.csv).
"""
from __future__ import annotations

import argparse
import json
from collections import deque
from pathlib import Path

import numpy as np
import torch
from stable_baselines3 import PPO
from stable_baselines3.common.callbacks import BaseCallback
from stable_baselines3.common.logger import configure

from lps_env import ROOT, LPSVecEnv

CONFIG = dict(
    seed=1,
    steps=16_000_000,
    n_envs=64,
    opponents="baseline",  # 1v1 duels against the scripted baseline
    n_steps=256,
    batch_size=2048,
    n_epochs=4,
    gamma=0.995,
    gae_lambda=0.95,
    learning_rate=3e-4,  # decays linearly to zero
    ent_coef=0.005,
    net_arch=[256, 256],
    bc_samples=0,     # >0: imitation warm start, this many baseline-labelled states per DAgger round
    bc_rounds=3,
    freeze_steps=0,   # PPO steps with the actor frozen, so the fresh critic cannot wreck the cloned policy
)


class WinRate(BaseCallback):
    """Logs the win rate over the last 500 finished training matches."""

    def __init__(self):
        super().__init__()
        self.recent = deque(maxlen=500)

    def _on_step(self) -> bool:
        for info in self.locals["infos"]:
            if "won" in info:
                self.recent.append(info["won"])
        if self.recent:
            self.logger.record("rollout/win_rate", float(np.mean(self.recent)))
        return True


class Actor(torch.nn.Module):
    """The deterministic part of the policy: observation -> action logits."""

    def __init__(self, policy):
        super().__init__()
        self.net = torch.nn.Sequential(*policy.mlp_extractor.policy_net, policy.action_net)

    def forward(self, obs):
        return self.net(obs)


def clone_baseline(model: PPO, env: LPSVecEnv, samples: int, rounds: int) -> None:
    """DAgger: label visited states with the baseline's action and fit the actor to them.

    Round 0 follows the baseline, later rounds follow the learner, always with
    10% random actions so the data covers states the baseline never reaches.
    """
    actor = Actor(model.policy)
    opt = torch.optim.Adam(actor.parameters(), lr=1e-3)
    rng = np.random.default_rng(model.seed)
    xs, ys = [], []
    obs = env.reset()
    for rnd in range(rounds):
        for _ in range(samples // env.num_envs):
            label = env.client.expert()
            xs.append(obs)
            ys.append(label)
            act = label
            if rnd > 0:
                with torch.no_grad():
                    act = actor(torch.from_numpy(obs)).argmax(1).numpy()
            act = np.where(rng.random(env.num_envs) < 0.1, rng.integers(0, env.action_space.n, env.num_envs), act)
            obs = env.client.step(act)[0]
        x, y = torch.from_numpy(np.concatenate(xs)), torch.from_numpy(np.concatenate(ys))
        for _ in range(8):
            for idx in torch.randperm(len(x)).split(4096):
                loss = torch.nn.functional.cross_entropy(actor(x[idx]), y[idx])
                opt.zero_grad()
                loss.backward()
                opt.step()
        with torch.no_grad():
            acc = (actor(x[-samples:]).argmax(1) == y[-samples:]).float().mean().item()
        print(f"bc round {rnd}: {len(x)} samples, loss {loss.item():.3f}, agreement on newest data {acc:.3f}")


def export_onnx(model: PPO, path: Path) -> None:
    actor = Actor(model.policy).eval()
    dummy = torch.zeros(1, model.observation_space.shape[0])
    # The legacy exporter emits plain Gemm/Tanh nodes, which is all the Go runner supports.
    torch.onnx.export(actor, dummy, str(path), input_names=["obs"], output_names=["logits"],
                      dynamic_axes={"obs": {0: "batch"}}, opset_version=17, dynamo=False)
    # Test vectors so the Go runner can be checked against PyTorch.
    rng = np.random.default_rng(0)
    obs = rng.uniform(-1, 1, (16, dummy.shape[1])).astype(np.float32)
    with torch.no_grad():
        logits = actor(torch.from_numpy(obs)).numpy()
    parity = path.parent / "testdata" / "parity.json"
    parity.parent.mkdir(exist_ok=True)
    parity.write_text(json.dumps({"obs": obs.tolist(), "logits": logits.tolist()}))


def main() -> None:
    ap = argparse.ArgumentParser()
    for key, value in CONFIG.items():
        if not isinstance(value, list):
            ap.add_argument(f"--{key}", type=type(value), default=value)
    ap.add_argument("--name", default="ppo")
    ap.add_argument("--out", default=str(ROOT / "internal" / "bot" / "policy.onnx"))
    args = ap.parse_args()
    cfg = {**CONFIG, **{k: v for k, v in vars(args).items() if k in CONFIG}}

    run = Path(__file__).parent / "runs" / args.name
    run.mkdir(parents=True, exist_ok=True)
    (run / "config.json").write_text(json.dumps(cfg, indent=2))

    torch.manual_seed(cfg["seed"])
    torch.set_num_threads(4)
    env = LPSVecEnv(cfg["n_envs"], cfg["opponents"], seed=cfg["seed"])
    model = PPO(
        "MlpPolicy", env, seed=cfg["seed"], device="cpu", verbose=1,
        n_steps=cfg["n_steps"], batch_size=cfg["batch_size"], n_epochs=cfg["n_epochs"],
        gamma=cfg["gamma"], gae_lambda=cfg["gae_lambda"], learning_rate=lambda left: cfg["learning_rate"] * left,
        ent_coef=cfg["ent_coef"], policy_kwargs=dict(net_arch=cfg["net_arch"]),
    )
    model.set_logger(configure(str(run), ["stdout", "csv"]))
    if cfg["bc_samples"]:
        clone_baseline(model, env, cfg["bc_samples"], cfg["bc_rounds"])
    if cfg["freeze_steps"]:
        actor = list(Actor(model.policy).parameters())
        for p in actor:
            p.requires_grad_(False)
        model.learn(total_timesteps=cfg["freeze_steps"], callback=WinRate(), log_interval=10)
        for p in actor:
            p.requires_grad_(True)
    if cfg["steps"]:
        model.learn(total_timesteps=cfg["steps"], callback=WinRate(), log_interval=10, reset_num_timesteps=False)
    model.save(run / "model")
    export_onnx(model, Path(args.out))
    env.close()
    print(f"exported {args.out}")


if __name__ == "__main__":
    main()
