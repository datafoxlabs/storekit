#!/usr/bin/env python3
"""Run canonical RepoFoundry validators from a pinned release commit."""

from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SOURCE = "https://github.com/XiaoWeiKIN/RepoFoundryAI.git"
REVISION = "f2f2d07f5c11138996154472fef7d1851aab3a40"  # v0.15.0


def main():
    with tempfile.TemporaryDirectory(prefix="storekit-harness-") as temporary:
        checkout = Path(temporary)
        subprocess.run(["git", "init", "--quiet", str(checkout)], check=True)
        subprocess.run(["git", "-C", str(checkout), "fetch", "--quiet", "--depth=1", SOURCE, REVISION], check=True)
        subprocess.run(["git", "-C", str(checkout), "checkout", "--quiet", "--detach", "FETCH_HEAD"], check=True)
        actual = subprocess.check_output(["git", "-C", str(checkout), "rev-parse", "HEAD"], text=True).strip()
        if actual != REVISION:
            raise RuntimeError("RepoFoundry revision mismatch")
        print(f"RepoFoundry v0.15.0: {actual}", flush=True)
        command = ["python3", str(checkout / "scripts/foundryctl.py"), "--repo", str(ROOT)]
        for action in (["validate", "--harness"], ["spec", "validate"]):
            subprocess.run(command + action, check=True)
        subprocess.run(["python3", str(checkout / "engineering-execution-plan/scripts/epctl.py"),
                        "--repo", str(ROOT), "validate"], check=True)


if __name__ == "__main__":
    main()
