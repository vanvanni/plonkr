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
        check("0.1.0" in self.cli("version").stdout, "Version output missing version")
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
        # The published example explicitly requires a writable parent. Provision
        # it without pre-creating the app account that Plonkr must create itself.
        self.shell("install -d -m 1777 /srv")
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
        for name, key, diagnostic in [("missing-key", self.scratch / "absent", "inspect key source"),
                                      ("encrypted-key", encrypted, "passphrase-protected")]:
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
        check("Different key file already exists" in result.stdout, "Key conflict was not reported")
        check(self.shell("sha256sum " + installed).stdout.split()[0] == before, "Conflicting key was overwritten")
        self.shell("test ! -e /srv/conflict-fixture")
        # Use an independent user/home so the known host from the successful
        # repository cannot satisfy this intentionally incorrect trust case.
        self.shell("useradd -m -s /bin/sh trustfixture; install -d -o trustfixture -g trustfixture /srv/trust-fixture")
        wrong = self.control.with_suffix(".pub").read_text().strip()
        path = self.repository_profile("trust-fixture", path="/srv/trust-fixture/repo", known="127.0.0.1 " + wrong)
        path.write_text(path.read_text().replace('user = "app"', 'user = "trustfixture"'))
        result = self.apply("trust-fixture", expected=128)
        check("Host key verification failed" in result.stdout, "Host trust failure not reported")

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

    def terminal(self, name, flags=(), cancel=False):
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
            ("key conflict preservation and strict repository host trust", suite.key_conflict_and_trust),
            ("dynamic nested flags, both bypass forms, plain output and logs", suite.flags_bypass_and_logs),
            ("profile/snippet chaining and fail-fast result propagation", suite.chaining_and_failure),
            ("whole-plan validation before remote changes", suite.invalid_plan_prevents_changes),
            ("real PTY TUI, remote tty and cancellation", suite.terminal_paths),
        ]:
            case(name, function)
    return int(any(result["status"] != "passed" for result in RESULTS))


if __name__ == "__main__":
    sys.exit(main())
