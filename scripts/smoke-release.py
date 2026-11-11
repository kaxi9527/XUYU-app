"""Start the packaged desktop binary using an isolated, empty home directory."""
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path

binary = Path(sys.argv[1]).resolve()
with tempfile.TemporaryDirectory(prefix="xuyu-smoke-") as directory:
    home = Path(directory) / "home"
    home.mkdir()
    env = os.environ.copy()
    env.update(HOME=str(home), USERPROFILE=str(home), WORKSHELF_CONFIG=str(home / "config.json"), WORKSHELF_DISABLE_NOTIFICATIONS="1")
    with (Path(directory) / "output.log").open("w+") as log:
        process = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=subprocess.STDOUT)
        try:
            time.sleep(8)
            if process.poll() is not None:
                log.seek(0)
                raise SystemExit(f"Application exited during startup ({process.returncode}):\n{log.read()[-4000:]}")
            print("Application remained running with an isolated home directory")
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
