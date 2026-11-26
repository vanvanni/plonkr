"""Install the module's Go floor in a disposable guest, verifying its official hash."""
import hashlib
import json
import pathlib
import platform
import re
import subprocess
import sys
import urllib.request

version = sys.argv[1]
if not re.fullmatch(r"\d+\.\d+\.\d+", version):
    raise SystemExit("Expected a full Go version")
arch = {"x86_64": "amd64", "aarch64": "arm64"}[platform.machine()]
with urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=60) as response:
    releases = json.load(response)
release = next(r for r in releases if r["version"] == "go" + version)
artifact = next(f for f in release["files"] if f["os"] == "linux" and f["arch"] == arch and f["kind"] == "archive")
archive = pathlib.Path("/home/ubuntu/go-toolchain.tar.gz")
digest = hashlib.sha256()
with urllib.request.urlopen("https://go.dev/dl/" + artifact["filename"], timeout=120) as response, archive.open("wb") as output:
    while chunk := response.read(1024 * 1024):
        digest.update(chunk)
        output.write(chunk)
if digest.hexdigest() != artifact["sha256"]:
    raise SystemExit("Go toolchain SHA-256 mismatch")
subprocess.run(["sudo", "tar", "-xzf", str(archive), "-C", "/usr/local"], check=True, timeout=120)
archive.unlink()
print("Installed and verified", artifact["filename"])
