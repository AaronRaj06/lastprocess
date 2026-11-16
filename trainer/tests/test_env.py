import json
import sys
from pathlib import Path

import numpy as np
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from lps_env import ROOT, LPSEnv, LPSVecEnv  # noqa: E402


def test_gymnasium_api():
    from gymnasium.utils.env_checker import check_env
    env = LPSEnv()
    try:
        check_env(env, skip_render_check=True)
    finally:
        env.close()


def rollout(seed: int, steps: int = 300):
    env = LPSVecEnv(4, seed=seed)
    try:
        rng = np.random.default_rng(0)
        out = [env.reset()]
        for _ in range(steps):
            obs, rewards, dones, _ = env.step(rng.integers(0, 13, 4))
            out += [obs, rewards, dones]
        return out
    finally:
        env.close()


def test_same_seed_same_trajectory():
    a, b, c = rollout(7), rollout(7), rollout(8)
    assert all(np.array_equal(x, y) for x, y in zip(a, b))
    assert not all(np.array_equal(x, y) for x, y in zip(a, c))


def test_episodes_end_and_report():
    env = LPSVecEnv(8, seed=1)
    try:
        env.reset()
        episodes = 0
        for _ in range(3000):
            obs, rewards, dones, infos = env.step(np.zeros(8, dtype=np.int64))  # stand still: the zone kills
            assert obs.shape == (8, env.observation_space.shape[0]) and np.isfinite(obs).all()
            episodes += sum("episode" in i for i in infos)
        assert episodes >= 8
    finally:
        env.close()


def test_onnx_matches_exported_vectors():
    """The committed policy must give the same logits in onnxruntime as PyTorch did at export."""
    onnx_path = ROOT / "internal" / "bot" / "policy.onnx"
    parity = ROOT / "internal" / "bot" / "testdata" / "parity.json"
    if not parity.exists() or onnx_path.stat().st_size == 0:
        pytest.skip("no trained policy yet")
    import onnxruntime as ort
    data = json.loads(parity.read_text())
    sess = ort.InferenceSession(str(onnx_path))
    got = sess.run(None, {"obs": np.array(data["obs"], dtype=np.float32)})[0]
    np.testing.assert_allclose(got, np.array(data["logits"]), atol=1e-4)


def test_expert_labels_are_valid_and_repeatable():
    from lps_env import SimClient
    c = SimClient()
    try:
        c.reset(7, 4)
        a = c.expert()
        c.reset(7, 4)
        assert a.shape == (4,) and (a < c.num_actions).all() and (a == c.expert()).all()
    finally:
        c.close()
