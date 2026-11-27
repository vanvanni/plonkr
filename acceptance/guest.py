#!/usr/bin/env python3
"""Real-SSH acceptance scenarios, run only inside the disposable suite VM.

The Multipass harness owns VM disposal. This script deliberately creates Linux
accounts and installs a unit inside that VM. All credentials are generated test
fixtures; reports contain outcomes and diagnostics, never private key contents.
"""

import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import shlex
import shutil
import signal
import socket
import stat
import struct
import subprocess
import sys
import tempfile
import termios
import time


ROOT = Path(__file__).resolve().parents[1]
BINARY = ROOT / "plonk"
RESULTS = []


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def run(*args, expected=0, timeout=90, env=None, cwd=None, input=None):
    process = subprocess.run(
        [str(arg) for arg in args], input=input, text=True,
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        timeout=timeout, env=env, cwd=cwd,
    )
    if expected is not None and process.returncode != expected:
        raise AssertionError(
            f"{args[0]} returned {process.returncode}, expected {expected}:\n"
            + process.stdout[-12000:]
        )
    return process


def case(name, function):
    started = time.monotonic()
    result = {"name": name}
    try:
        function()
        result["status"] = "passed"
        print("PASS " + name, flush=True)
    except Exception as error:
        result.update(status="failed", error=str(error))
        print("FAIL " + name + ": " + str(error), flush=True)
    result["seconds"] = round(time.monotonic() - started, 3)
    RESULTS.append(result)
    (ROOT / "acceptance-results.json").write_text(json.dumps(RESULTS, indent=2) + "\n")


def quote(value):
    # JSON string syntax is also valid for the fixture's HCL string values.
    return json.dumps(str(value)).replace("${", "$${").replace("%{", "%%{")


def task(name, commands, order=0, extra=""):
    return ('  task ' + quote(name) + ' {\n    order = ' + str(order)
            + '\n' + extra + '    script = ['
            + ', '.join(quote(c) for c in commands) + ']\n  }\n')


class Suite:
    def __init__(self, scratch):
        self.scratch = scratch
        self.profiles = scratch / "profiles"
        self.profiles.mkdir()
        self.logs = scratch / "logs"
        self.env = dict(os.environ, PLONK_PROFILES_DIR=str(self.profiles),
                        PLONK_LOG_DIR=str(self.logs), TERM="xterm-256color")
        self.env.pop("NO_COLOR", None)
        self.control = scratch / "control"
        self.deploy = scratch / "deploy"
        self.host = "plonkr-acceptance-target"
        self.repo_url = "ssh://fixturegit@127.0.0.1/srv/plonkr-git/repo.git"

    def write(self, name, text):
        target = self.profiles / (name + ".hcl")
        target.write_text(text)
        return target

    def cli(self, *args, expected=0, env=None):
        return run(BINARY, *args, expected=expected, env=env or self.env)

    def apply(self, name, *flags, expected=0):
        return self.cli("run", self.host, name, "--no-tui", *flags, expected=expected)

    def shell(self, script, expected=0):
        return run("sudo", "-n", "sh", "-ec", script, expected=expected)

    def bootstrap(self):
        run("sudo", "-n", "true")
        for key in (self.control, self.deploy):
            run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
        self.source_digest = hashlib.sha256(self.deploy.read_bytes()).hexdigest()
        ssh_dir = Path.home() / ".ssh"
        ssh_dir.mkdir(mode=0o700, exist_ok=True)
        auth = ssh_dir / "authorized_keys"
        with auth.open("a") as stream:
            stream.write(self.control.with_suffix(".pub").read_text())
        auth.chmod(0o600)
        host_key = run("sudo", "-n", "cat", "/etc/ssh/ssh_host_ed25519_key.pub").stdout.strip()
        self.known_host = "127.0.0.1 " + host_key
        self.known = self.scratch / "known_hosts"
        self.known.write_text(self.known_host + "\n")
        # Only the disposable VM's ubuntu account is modified. The host account
        # and Multipass's authorized login key remain untouched.
        with (ssh_dir / "config").open("a") as stream:
            stream.write(f"\nHost {self.host}\n  HostName 127.0.0.1\n  User ubuntu\n"
                         f"  IdentityFile {self.control}\n  IdentitiesOnly yes\n"
                         f"  UserKnownHostsFile {self.known}\n"
                         "  StrictHostKeyChecking yes\n  BatchMode yes\n  ConnectTimeout 10\n")
        (ssh_dir / "config").chmod(0o600)
        run("ssh", self.host, "true")
        self.shell("useradd -u 29001 -m -d /srv/plonkr-git -s /bin/sh fixturegit\n"
                   "install -d -m 700 -o fixturegit -g fixturegit /srv/plonkr-git/.ssh")
        public_key = self.deploy.with_suffix(".pub").read_text()
        self.shell("printf '%s\\n' " + shlex.quote(public_key.strip())
                   + " > /srv/plonkr-git/.ssh/authorized_keys\n"
                   "chown fixturegit:fixturegit /srv/plonkr-git/.ssh/authorized_keys\n"
                   "chmod 600 /srv/plonkr-git/.ssh/authorized_keys")
        seed = self.scratch / "seed"
        run("git", "init", "-b", "main", seed)
        run("git", "-C", seed, "config", "user.email", "fixture@example.invalid")
        run("git", "-C", seed, "config", "user.name", "Plonkr acceptance fixture")
        (seed / "README").write_text("Private repository fixture\n")
        run("git", "-C", seed, "add", "README")
        run("git", "-C", seed, "commit", "-m", "Fixture")
        run("git", "-C", seed, "tag", "fixture-v1")
        self.commit = run("git", "-C", seed, "rev-parse", "HEAD").stdout.strip()
        self.shell("git clone --bare " + shlex.quote(str(seed))
                   + " /srv/plonkr-git/repo.git\n"
                   "chown -R fixturegit:fixturegit /srv/plonkr-git/repo.git")

    def examples_validate(self):
        examples = sorted((ROOT / "examples").glob("*.hcl"))
        check(bool(examples), "No example profiles found")
        executed_examples = {"minimal.hcl", "app-machine.hcl"}
        check({example.name for example in examples} == executed_examples,
              "Example list changed: add/remove its execution scenario and update executed_examples")
        for example in examples:
            self.cli("validate", example, "--profiles-dir", ROOT / "examples")
            preview = self.cli("run", self.host, example, "--profiles-dir", ROOT / "examples", "--dry-run")
            check("planned" in preview.stdout, f"No planned steps in {example.name}")
        check(re.fullmatch(r"plonk [1-9][0-9]{3}\.(?:0[1-9]|1[0-2])\.(?:0|[1-9][0-9]*)(?:-(?:dev|(?:alpha|beta|rc)\.(?:0|[1-9][0-9]*)))?\s*",
                           self.cli("version").stdout) is not None,
              "Version output must use YYYY.MM.PATCH CalVer")
        self.cli("--help")
        self.cli("validate", "--help")

    def minimal_example(self):
        result = self.cli("run", self.host, ROOT / "examples/minimal.hcl", "--no-tui")
        check("Complete." in result.stdout, "Minimal example did not complete")
        check("UTC" in run("timedatectl", "show", "-p", "Timezone", "--value").stdout, "Timezone was not applied")

    def profile_discovery(self):
        name = "acceptance-discovery"
        baseline = 'tasks {\n' + task("Discovery", ["echo DEFAULT_LOCATION"]) + '}\n'
        self.shell("test ! -e /etc/plonkr/acceptance-discovery.hcl\n"
                   "install -d -m 755 /etc/plonkr\n"
                   "printf '%s' " + shlex.quote(baseline)
                   + " > /etc/plonkr/acceptance-discovery.hcl")
        default_env = dict(self.env)
        default_env.pop("PLONK_PROFILES_DIR", None)
        check("DEFAULT_LOCATION" in self.cli("validate", name, env=default_env).stdout,
              "Default /etc/plonkr discovery did not resolve the profile")
        self.write(name, baseline.replace("DEFAULT_LOCATION", "ENV_LOCATION"))
        check("ENV_LOCATION" in self.cli("validate", name).stdout,
              "PLONK_PROFILES_DIR did not override default profile lookup")
        explicit = self.scratch / "explicit-profiles"
        explicit.mkdir()
        (explicit / (name + ".hcl")).write_text(baseline.replace("DEFAULT_LOCATION", "EXPLICIT_LOCATION"))
        result = self.cli("validate", name, "--profiles-dir", explicit)
        check("EXPLICIT_LOCATION" in result.stdout and "ENV_LOCATION" not in result.stdout,
              "Explicit --profiles-dir did not override PLONK_PROFILES_DIR")
        result = self.cli("run", self.host, name, "--profiles-dir", explicit, "--no-tui")
        check("EXPLICIT_LOCATION" in result.stdout and "Complete." in result.stdout,
              "Run command did not honor the explicit profile directory")

    def deferred_accounts(self):
        self.write("deferred", '''configuration {
  groups = [{ name = "deferredgrp", create = false }]
  users = [{ name = "deferreduser", groups = ["deferredgrp"], create = false }]
}
tasks {
  task "Leave deferred" {
    order = 0
    script = ["true"]
  }
}
''')
        self.apply("deferred")
        run("getent", "group", "deferredgrp", expected=2)
        run("getent", "passwd", "deferreduser", expected=2)
        text = (self.profiles / "deferred.hcl").read_text().replace('script = ["true"]', 'setup = [group("deferredgrp"), user("deferreduser")]')
        self.write("explicit", text)
        self.apply("explicit")
        check("deferredgrp" in run("id", "-nG", "deferreduser").stdout, "Explicit user group missing")

    def app_example(self):
        shutil.copytree(ROOT / "examples/files", self.profiles / "files")
        shutil.copytree(ROOT / "examples/snippets", self.profiles / "snippets")
        text = (ROOT / "examples/app-machine.hcl").read_text()
        text = text.replace("git@example.com:team/your-repo.git", self.repo_url)
        text = text.replace("/home/operator/.ssh/your-repo", str(self.deploy))
        text = re.sub(r'known_hosts\s*=\s*\[[^\]]*\]', "known_hosts = [" + quote(self.known_host) + "]", text)
        self.write("app-example", text)
        # /srv is root-owned and the app account is created by Plonkr itself, so the
        # checkout directory must be prepared for it.
        check(self.shell("stat -c %U /srv").stdout.strip() == "root", "Fixture expects a root-owned /srv")
        result = self.apply("app-example", "--vm", "--verbose")
        check("guest-agent-ready" in result.stdout and "Application setup complete" in result.stdout,
              "Enabled snippets or log message did not execute")
        check(run("getent", "group", "app").stdout.split(":")[2] == "1001", "Explicit GID not honored")
        check("app" in run("id", "-nG", "app").stdout, "App group membership missing")
        check("reports" in run("id", "-nG", "reporter").stdout, "Explicit reporter setup missing")
        check("/var/lib/app:/usr/sbin/nologin" in run("getent", "passwd", "app").stdout, "Service account defaults differ")
        check("America/Chicago" in run("timedatectl", "show", "-p", "Timezone", "--value").stdout, "Example timezone missing")
        installed = "/var/lib/app/.ssh/plonkr-your-repo.key"
        check(self.shell("sha256sum " + installed).stdout.split()[0] == self.source_digest, "Installed key bytes changed")
        check(hashlib.sha256(self.deploy.read_bytes()).hexdigest() == self.source_digest, "Source key changed")
        check(self.shell("stat -c '%a:%U' " + installed).stdout.strip() == "600:app", "Target key mode/owner differ")
        ssh_config = self.shell("cat /var/lib/app/.ssh/config").stdout
        check("Host your-repo.127.0.0.1" in ssh_config and
              "HostName 127.0.0.1" in ssh_config and
              "IdentityFile ~/.ssh/plonkr-your-repo.key" in ssh_config,
              "Repository-specific SSH alias missing")
        check(self.shell("stat -c '%a:%U' /var/lib/app/.ssh/config").stdout.strip() == "600:app", "SSH config mode/owner differ")
        origin = self.shell("sudo -n -H -u app git -C /srv/your-repo remote get-url origin").stdout
        check("your-repo.127.0.0.1" in origin, "Git does not use repository SSH alias")
        self.shell("test ! -e /var/lib/app/.ssh/viac; test ! -e /var/lib/app/.ssh/plonkr")
        check(self.shell("sudo -n -H -u app git -C /srv/your-repo rev-parse HEAD").stdout.strip() == self.commit, "Clone HEAD mismatch")
        unit = Path("/etc/systemd/system/example.service").read_text()
        check("User=app" in unit and "WorkingDirectory=/srv/your-repo" in unit and "${" not in unit,
              "Unit template was not rendered")
        check(self.shell("stat -c '%a:%U' /etc/systemd/system/example.service").stdout.strip() == "644:root", "Unit permissions differ")
        self.shell("grep -q example-only-replace-me /home/reporter/.ssh/authorized_keys")

    def rerun_and_pull(self):
        result = self.apply("app-example")
        check("already present and verified" in result.stdout, "Existing checkout was not verified")
        config = self.shell("cat /var/lib/app/.ssh/config").stdout
        check(config.count("Host your-repo.127.0.0.1\n") == 1, "Repeated run duplicated SSH alias")
        check("skipped" in result.stdout, "Disabled optional example flags not skipped")
        self.shell("sudo -n -H -u app git -C /srv/your-repo pull --ff-only")
        self.shell("test $(grep -c example-only-replace-me /home/reporter/.ssh/authorized_keys) = 1")

    def repository_profile(self, name, *, key=None, ref="main", path=None, known=None, setup_extra="", script_before=""):
        return self.write(name, f'''repository "{name}" {{
  url = {quote(self.repo_url)}
  path = {quote(path or '/srv/' + name)}
  user = "app"
  key = {quote(key or self.deploy)}
  ref = {quote(ref)}
  known_hosts = [{quote(self.known_host if known is None else known)}]
}}
tasks {{
{script_before}  task "Clone" {{
    order = 10
    {setup_extra}
    setup = [repo("{name}")]
  }}
}}
''')

    def repository_refs(self):
        for name, ref in [("commit-fixture", self.commit), ("tag-fixture", "fixture-v1")]:
            self.repository_profile(name, ref=ref)
            self.apply(name)
            self.apply(name)
            check(self.shell(f"sudo -n -H -u app git -C /srv/{name} rev-parse HEAD").stdout.strip() == self.commit,
                  f"{name} resolved to wrong commit")

    def preflight_keys(self):
        encrypted = self.scratch / "encrypted"
        run("ssh-keygen", "-q", "-t", "ed25519", "-N", "fixture-passphrase", "-f", encrypted)
        crlf = self.scratch / "crlf-key"
        crlf.write_bytes(self.deploy.read_bytes().replace(b"\n", b"\r\n"))
        crlf.chmod(0o600)
        for name, key, diagnostic in [("missing-key", self.scratch / "absent", "inspect key source"),
                                      ("encrypted-key", encrypted, "passphrase-protected"),
                                      ("crlf-key", crlf, "CRLF")]:
            marker = self.scratch / (name + "-executed")
            self.repository_profile(name, key=key, script_before=task("Before clone", ["touch " + str(marker)]))
            self.apply(name, "--dry-run")
            result = self.apply(name, expected=1)
            check(diagnostic in result.stdout, f"Wrong {name} error: {result.stdout}")
            check(not marker.exists(), "Key preflight happened after remote execution")
            self.apply(name, "--bypass", "Clone")
            check(marker.exists(), "Bypassing clone did not avoid unnecessary key read")

    def key_conflict_and_trust(self):
        name = "conflict-fixture"
        self.repository_profile(name)
        installed = "/var/lib/app/.ssh/plonkr-" + name + ".key"
        self.shell("printf existing-fixture > " + installed + "; chown app:app " + installed)
        before = self.shell("sha256sum " + installed).stdout.split()[0]
        result = self.apply(name, expected=1)
        check("Warning:" in result.stdout and "not approved" in result.stdout, "Key conflict was not reported")
        check(self.shell("sha256sum " + installed).stdout.split()[0] == before, "Conflicting key was overwritten")
        self.shell("test -z \"$(ls -A /srv/conflict-fixture 2>/dev/null)\"")  # nothing was cloned
        code, output = self.terminal(name, ["--no-tui"], answer="no\n")
        check(code == 1 and "declined" in output, "Declining replacement was not reported")
        check(self.shell("sha256sum " + installed).stdout.split()[0] == before, "Declining changed the key")
        code, output = self.terminal(name, ["--no-tui"], answer="yes\n")
        check(code == 0 and "Replaced deploy key" in output, "Approved replacement was not performed: " + output[-2000:])
        check(self.shell("sha256sum " + installed).stdout.split()[0] == self.source_digest, "Approved key bytes differ")
        self.shell("printf another-fixture > " + installed)
        result = self.apply(name, "--auto-approve")
        check("Warning:" in result.stdout and "approved by --auto-approve" in result.stdout, "Automatic approval missed warning")
        check(self.shell("sha256sum " + installed).stdout.split()[0] == self.source_digest, "Automatic replacement bytes differ")
        # Use an independent user/home so the known host from the successful
        # repository cannot satisfy this intentionally incorrect trust case.
        self.shell("useradd -m -s /bin/sh trustfixture; install -d -o trustfixture -g trustfixture /srv/trust-fixture")
        wrong = self.control.with_suffix(".pub").read_text().strip()
        path = self.repository_profile("trust-fixture", path="/srv/trust-fixture/repo", known="127.0.0.1 " + wrong)
        path.write_text(path.read_text().replace('user = "app"', 'user = "trustfixture"'))
        self.apply("trust-fixture")
        self.shell("test -f /srv/trust-fixture/repo/README")
        # No configured known_hosts entry must also work.
        path = self.repository_profile("unknown-host-fixture", path="/srv/trust-fixture/unknown")
        text = path.read_text().replace('user = "app"', 'user = "trustfixture"')
        text = re.sub(r'known_hosts\s*=\s*\[[^\]]*\]', '', text)
        path.write_text(text)
        self.apply("unknown-host-fixture")

    def flags_bypass_and_logs(self):
        enabled = self.scratch / "enabled"
        bypassed = self.scratch / "bypassed"
        source = 'tasks {\n' + '''  task "Conditions" {
    order = 0
    script = [flag("outer", flag("inner", COMMAND)), log("Local message", "green"), PRINT]
  }
'''.replace("COMMAND", quote("touch " + str(enabled))).replace("PRINT", quote(r"printf '\033[31mRED\033[0m\rNEXT\n'"))
        source += task("Configured bypass", ["touch " + str(bypassed)], 1, "    bypass = true\n")
        source += task("CLI bypass", ["touch " + str(bypassed)], 2) + "}\n"
        self.write("conditions", source)
        help_text = self.cli("run", self.host, "conditions", "--help").stdout
        check("--outer" in help_text and "--inner" in help_text, "Dynamic flags missing from help")
        result = self.apply("conditions", "--bypass", "CLI bypass", "--outer")
        check("1 skipped" in result.stdout and "2 bypassed" in result.stdout, "Status counts differ")
        check(not enabled.exists() and not bypassed.exists(), "Skipped or bypassed action executed")
        result = self.apply("conditions", "--bypass", "CLI bypass", "--outer", "--inner")
        check(enabled.exists() and not bypassed.exists(), "Nested flag conjunction failed")
        self.apply("conditions", "--bypass", "unknown-task", expected=1)
        self.apply("conditions", "--not-a-profile-flag", expected=1)
        check("\x1b" not in result.stdout, "Plain output retained terminal escapes")
        logs = sorted(self.logs.glob("*.log"), key=lambda p: p.stat().st_mtime_ns)
        check(bool(logs), "No run logs created")
        latest = logs[-1]
        log = latest.read_text()
        check("\x1b" not in log and "RED\nNEXT" in log and "Local message" in log, "Log sanitation/message output failed")
        check(stat.S_IMODE(latest.stat().st_mode) == 0o600, "Log file is not private")
        for logfile in logs:
            check("BEGIN OPENSSH PRIVATE KEY" not in logfile.read_text(), "Private key leaked into log")

    def chaining_and_failure(self):
        order_file = self.scratch / "order"
        self.write("child", 'tasks {\n' + task("Child", [f"printf child >> {order_file}"]) + '}\n')
        (self.profiles / "snippets/nested.hcl").write_text('''snippet "inner" {
  commands = [COMMAND]
}
snippet "outer" {
  commands = [run("inner")]
}
'''.replace("COMMAND", quote(f"printf snippet >> {order_file}")))
        self.write("parent", 'include = ["child"]\ntasks {\n  task "Parent" {\n    order = 0\n'
                   '    script = [run("outer"), ' + quote(f"printf parent >> {order_file}") + ']\n  }\n}\n')
        self.apply("parent")
        check(order_file.read_text() == "childsnippetparent", "Profile/snippet execution order differs")
        marker = self.scratch / "after-failure"
        self.write("failure", 'tasks {\n' + task("Fails", ["exit 7", "touch " + str(marker)])
                   + task("Pending", ["touch " + str(marker)], 1) + '}\n')
        result = self.apply("failure", expected=7)
        check("1 failed" in result.stdout and "2 pending" in result.stdout, "Failure/pending result counts differ")
        check(not marker.exists(), "Execution continued after failure")

    def task_locks(self):
        state_dir = "/etc/plonkr/.states"

        def lines(path):
            return len(path.read_text().splitlines()) if path.exists() else 0

        def state_file(profile):
            return state_dir + "/" + hashlib.sha256(profile.encode()).hexdigest()[:16] + ".json"

        tool = self.scratch / "lock-tool"
        version = self.scratch / "lock-version"
        runs = self.scratch / "lock-runs"
        after = self.scratch / "lock-after"
        version.write_text("v1\n")
        lock = ('    lock {\n      exists = [' + quote(tool) + ']\n      eval = [' + quote("cat " + str(version))
                + ']\n    }\n')
        self.write("locks", 'tasks {\n' + task("Locked", ["echo run >> " + str(runs)], 0, lock)
                   + task("After", ["echo after >> " + str(after)], 1) + '}\n')
        held = "bypassed by lock: exists and eval unchanged"

        # With no recorded state the task runs, and the state it leaves is recorded.
        result = self.apply("locks")
        check(lines(runs) == 1 and held not in result.stdout, "Locked task did not run on the first run")
        # Once the path exists and eval matches that state, the task is bypassed
        # and the next task still runs.
        tool.write_text("x")
        result = self.apply("locks")
        check(lines(runs) == 1 and held in result.stdout, "Held lock did not bypass the task")
        check("1 bypassed" in result.stdout and "1 completed" in result.stdout, "Bypassed lock counts differ")
        check(lines(after) == 2, "Following task did not run after a bypassed lock")

        # A changed eval result runs the task again and is then recorded.
        version.write_text("v2\n")
        self.apply("locks")
        check(lines(runs) == 2, "Changed eval result did not run the task")
        self.apply("locks")
        check(lines(runs) == 2, "Recorded eval result did not hold")

        # --force runs a held task; --dry-run never connects or probes.
        self.apply("locks", "--force")
        check(lines(runs) == 3, "--force did not run a held task")
        result = self.apply("locks", "--dry-run")
        check("lock is evaluated when the task runs" in result.stdout and lines(runs) == 3, "Dry run lock note differs")
        self.apply("locks")
        check(lines(runs) == 3, "State recorded by --force did not hold")

        # --bypass wins over --force.
        self.apply("locks", "--bypass", "Locked", "--force")
        check(lines(runs) == 3, "Explicit bypass did not win over --force")

        # State is private, root-owned and written through sudo by the non-root login.
        mode = self.shell("stat -c '%a %U' " + state_dir + " " + shlex.quote(state_file("locks"))).stdout.split()
        check(mode == ["700", "root", "600", "root"], f"State ownership or mode differs: {mode}")
        check("tmp." not in self.shell("ls -A " + state_dir).stdout, "Temporary state file left behind")

        # exists alone keeps no state.
        self.write("lock-exists", 'tasks {\n' + task("Exists only", ["echo run >> " + str(runs)], 0,
                   '    lock {\n      exists = [' + quote(tool) + ']\n    }\n') + '}\n')
        self.apply("lock-exists")
        check(lines(runs) == 3, "exists-only lock did not bypass")
        self.shell("test ! -e " + shlex.quote(state_file("lock-exists")))
        tool.unlink()
        self.apply("lock-exists")
        check(lines(runs) == 4, "exists-only lock held without the path")

        # A failed task records nothing, so it is never wrongly bypassed.
        self.write("lock-fail", 'tasks {\n' + task("Fails", ["exit 3"], 0,
                   '    lock {\n      eval = [' + quote("cat " + str(version)) + ']\n    }\n') + '}\n')
        self.apply("lock-fail", expected=3)
        self.shell("test ! -e " + shlex.quote(state_file("lock-fail")))

    def edge_cases(self):
        def lines(path):
            return len(path.read_text().splitlines()) if path.exists() else 0

        # A profile reached through more than one include path runs once.
        order_file = self.scratch / "edge-order"
        self.write("edge-shared", 'tasks {\n' + task("Shared", [f"printf shared >> {order_file}"]) + '}\n')
        self.write("edge-left", 'include = ["edge-shared"]\ntasks {\n' + task("Left", [f"printf left >> {order_file}"]) + '}\n')
        self.write("edge-right", 'include = ["edge-shared"]\ntasks {\n' + task("Right", [f"printf right >> {order_file}"]) + '}\n')
        self.write("edge-diamond", 'include = ["edge-left", "edge-right"]\ntasks {\n' + task("Top", [f"printf top >> {order_file}"]) + '}\n')
        self.apply("edge-diamond")
        check(order_file.read_text() == "sharedleftrighttop", f"Shared include did not run once: {order_file.read_text()!r}")
        self.write("edge-duplicate", 'include = ["edge-shared", "edge-shared"]\n')
        check("more than once" in self.cli("validate", "edge-duplicate", expected=1).stdout, "Duplicate include accepted")

        # A locked task in a shared profile is valid, and its lock works through the diamond.
        marker, locked_runs = self.scratch / "edge-lock-marker", self.scratch / "edge-lock-runs"
        locked = 'tasks {\n' + task("Shared locked", [f"touch {marker}", f"echo run >> {locked_runs}"], 0,
                                    '    lock {\n      exists = [' + quote(marker) + ']\n    }\n') + '}\n'
        self.write("edge-locked", locked)
        self.write("edge-lock-left", 'include = ["edge-locked"]\n')
        self.write("edge-lock-right", 'include = ["edge-locked"]\n')
        self.write("edge-lock-top", 'include = ["edge-lock-left", "edge-lock-right"]\n')
        self.apply("edge-lock-top")
        self.apply("edge-lock-top")
        check(lines(locked_runs) == 1, "Locked task in a shared profile did not run exactly once across two runs")

        # Built-in flag names cannot be redefined by a profile, and never crash the CLI.
        self.write("edge-force", 'tasks {\n' + task("Forced", ["true"]).replace('"true"', 'flag("force", "true")') + '}\n')
        result = self.apply("edge-force", expected=1)
        check("reserved flag name" in result.stdout and "panic" not in result.stdout.lower(), f"Reserved flag name not rejected cleanly: {result.stdout}")

        # A deploy key never applies to an https:// repository.
        self.write("edge-https-key", f'''repository "web" {{
  url = "https://example.invalid/team/web.git"
  path = "/srv/edge-web"
  user = "app"
  key = {quote(self.deploy)}
}}
tasks {{
  task "Clone" {{
    order = 0
    setup = [repo("web")]
  }}
}}
''')
        check("key applies only to SSH" in self.cli("validate", "edge-https-key", expected=1).stdout, "https URL with a key accepted")

        # Lock paths and task names are data: quotes, substitutions and unicode stay inert.
        evidence = self.scratch / "EDGE-EVIDENCE"
        hostile = self.scratch / "it's $(touch EDGE-EVIDENCE) `touch EDGE-EVIDENCE` a b"
        hostile.write_text("x")
        hostile_runs = self.scratch / "edge-hostile-runs"
        self.write("edge-hostile", 'tasks {\n' + task("Hostile path", [f"echo run >> {hostile_runs}"], 0,
                   '    lock {\n      exists = [' + quote(hostile) + ']\n    }\n') + '}\n')
        result = self.apply("edge-hostile")
        check("bypassed by lock: exists" in result.stdout and lines(hostile_runs) == 0, "Hostile lock path was not held")
        check(not evidence.exists(), "A lock path was executed as shell code")
        hostile.unlink()
        self.apply("edge-hostile")
        check(lines(hostile_runs) == 1, "Missing hostile lock path did not run the task")

        version, named_runs = self.scratch / "edge-name-version", self.scratch / "edge-name-runs"
        version.write_text("v1\n")
        weird_name = 'Zażółć "quoted" \'single\' $x ✓'
        self.write("edge-name", 'name = "Name with \'quotes\' and \\"double\\" ✓"\ntasks {\n'
                   + task(weird_name, [f"echo run >> {named_runs}"], 0, '    lock {\n      eval = [' + quote("cat " + str(version)) + ']\n    }\n') + '}\n')
        self.apply("edge-name")
        self.apply("edge-name")
        check(lines(named_runs) == 1, "Lock state for a task with quotes and unicode in its name was lost")
        version.write_text("v2\n")
        self.apply("edge-name")
        check(lines(named_runs) == 2, "Changed eval result did not rerun a task with an unusual name")

        # A profile edited on Windows (CRLF) never leaks carriage returns into remote commands.
        crlf_file = self.scratch / "edge-crlf-output"
        text = ('tasks {\n  task "CRLF" {\n    order = 0\n    script = [<<-EOT\n      printf \'one\\n\' > ' + str(crlf_file)
                + '\n      printf \'two\\n\' >> ' + str(crlf_file) + '\n    EOT\n    ]\n  }\n}\n')
        (self.profiles / "edge-crlf.hcl").write_bytes(text.replace("\n", "\r\n").encode())
        self.apply("edge-crlf")
        check(crlf_file.read_bytes() == b"one\ntwo\n", f"CRLF leaked into a remote command: {crlf_file.read_bytes()!r}")

        # A command's own output can be large without stalling the run or the log.
        self.write("edge-large", 'tasks {\n' + task("Large output", ["head -c 3000000 /dev/zero | tr '\\0' x; echo; seq 1 50000"]) + '}\n')
        result = self.apply("edge-large")
        check("Complete." in result.stdout and "\n50000\n" in result.stdout, "Large output run did not complete")
        largest = max(self.logs.glob("*.log"), key=lambda p: p.stat().st_mtime_ns)
        check(largest.stat().st_size > 3_000_000 and stat.S_IMODE(largest.stat().st_mode) == 0o600, "Large output log is missing or not private")

        # An unreachable host is a connection failure (255), reported as failed and logged.
        result = self.cli("run", "plonkr-no-such-host.invalid", "edge-shared", "--no-tui", expected=255)
        check("Failed." in result.stdout, "Unreachable host was not reported as failed")

        # An interrupted SSH config update leaves a lock that must be diagnosed, not ignored.
        self.repository_profile("stale-lock-fixture")
        lock = "/var/lib/app/.ssh/.plonkr-config.lock"
        self.shell("install -d -m 700 -o app -g app " + lock)
        result = self.apply("stale-lock-fixture", expected=1)
        check("Another SSH config update is active" in result.stdout and "remove " + lock in result.stdout,
              f"Stale SSH config lock was not explained: {result.stdout[-1500:]}")
        self.shell("test -d " + lock)
        self.shell("rmdir " + lock)
        self.apply("stale-lock-fixture")
        self.shell("test -f /srv/stale-lock-fixture/README; test ! -e " + lock)

    def invalid_plan_prevents_changes(self):
        marker = self.scratch / "invalid-plan-executed"
        self.write("invalid", 'tasks {\n' + task("First", ["touch " + str(marker)])
                   + '  task "Wrong phase" {\n    order = 1\n    script = [user("absent")]\n  }\n}\n')
        result = self.apply("invalid", expected=1)
        check("only allowed in setup" in result.stdout, "Phase validation error missing")
        check(not marker.exists(), "Invalid plan executed an earlier action")
        self.write("cycle-a", 'include = ["cycle-b"]\n')
        self.write("cycle-b", 'include = ["cycle-a"]\n')
        check("cycle" in self.cli("validate", "cycle-a", expected=1).stdout, "Include cycle accepted")

    def terminal(self, name, flags=(), cancel=False, answer=None):
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 110, 0, 0))
        process = subprocess.Popen([str(BINARY), "run", self.host, name, *flags],
                                   stdin=slave, stdout=slave, stderr=slave,
                                   env=self.env, start_new_session=True)
        os.close(slave)
        data = bytearray()
        sent = False
        deadline = time.monotonic() + 30
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError as error:
                        if error.errno == errno.EIO:
                            break
                        raise
                    if not chunk:
                        break
                    data.extend(chunk)
                    if answer is not None and not sent and b"Replace this key?" in data:
                        os.write(master, answer.encode())
                        sent = True
                elif process.poll() is not None:
                    break
                # A displayed command can contain CANCEL_READY before SSH starts.
                # Require a file created by the real remote command instead.
                if cancel and not sent and (self.scratch / "cancel-ready").exists():
                    os.write(master, b"q")
                    sent = True
            try:
                code = process.wait(timeout=5)
            except subprocess.TimeoutExpired as error:
                raise AssertionError("PTY run did not finish within 35 seconds") from error
            if cancel:
                check(sent, "Cancellation readiness marker was not observed")
            return code, data.decode(errors="replace")
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
            os.close(master)

    def terminal_paths(self):
        self.write("terminal", 'tasks {\n' + task("Terminal", ["echo TERMINAL_READY; sleep 1"]) + '}\n')
        code, output = self.terminal("terminal")
        check(code == 0 and "TERMINAL_READY" in output and "Complete." in output, "Live TUI run failed")
        check("\x1b[?1049h" in output, "TUI did not use alternate screen")
        self.write("tty", 'tasks {\n' + task("TTY", ["test -t 0; echo REMOTE_TTY_READY"]) + '}\n')
        code, output = self.terminal("tty", ["--tty"])
        check(code == 0 and "REMOTE_TTY_READY" in output, "Remote PTY was not allocated")
        self.write("cancel", 'tasks {\n' + task("Cancellation", ["touch " + str(self.scratch / "cancel-ready") + "; echo CANCEL_READY; sleep 20"])
                   + task("Unreached", ["echo SHOULD_NOT_RUN"], 1) + '}\n')
        code, output = self.terminal("cancel", cancel=True)
        check(code == 130 and "Cancelled." in output, f"Cancellation result differs: code={code}, output={output[-2000:]}")


def main():
    if os.environ.get("PLONKR_ACCEPTANCE_VM") != "1" or os.getuid() == 0:
        sys.exit("Run only as ubuntu inside the disposable Multipass suite VM, with PLONKR_ACCEPTANCE_VM=1.")
    if not Path("/run/cloud-init").exists():
        sys.exit("Expected a cloud-init disposable VM.")
    if not socket.gethostname().startswith("plonkr-suite-"):
        sys.exit("Expected a disposable VM named plonkr-suite-*. Refusing target changes.")
    with tempfile.TemporaryDirectory(prefix="plonkr-acceptance-") as temp:
        suite = Suite(Path(temp))
        try:
            suite.bootstrap()
        except Exception as error:
            case("fixture bootstrap", lambda: (_ for _ in ()).throw(error))
            return 1
        for name, function in [
            ("all shipped examples validate and preview", suite.examples_validate),
            ("minimal example over real SSH", suite.minimal_example),
            ("default, environment and explicit profile directory discovery", suite.profile_discovery),
            ("application example provisions accounts, private clone, key and unit", suite.app_example),
            ("deferred and explicit user/group creation", suite.deferred_accounts),
            ("repeat application and independent git pull", suite.rerun_and_pull),
            ("commit and tag repository references", suite.repository_refs),
            ("missing/encrypted key preflight, preview and bypass", suite.preflight_keys),
            ("key replacement approval and automatic repository host acceptance", suite.key_conflict_and_trust),
            ("dynamic nested flags, both bypass forms, plain output and logs", suite.flags_bypass_and_logs),
            ("profile/snippet chaining and fail-fast result propagation", suite.chaining_and_failure),
            ("task locks: exists, eval state, force, dry run and private state", suite.task_locks),
            ("edge cases: include once, reserved flags, hostile names, CRLF, large output, failures", suite.edge_cases),
            ("whole-plan validation before remote changes", suite.invalid_plan_prevents_changes),
            ("real PTY TUI, remote tty and cancellation", suite.terminal_paths),
        ]:
            case(name, function)
    return int(any(result["status"] != "passed" for result in RESULTS))


if __name__ == "__main__":
    sys.exit(main())
