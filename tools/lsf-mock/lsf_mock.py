#!/usr/bin/env python3
"""Small local LSF simulator. Requires Linux, Python 3.9+, and tcsh."""

import argparse
import getpass
import socket
from contextlib import contextmanager
import fcntl
import json
import os
import pwd
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time


ROOT = Path(os.environ.get("LSF_MOCK_STATE_DIR", Path.home() / ".local/state/lsf-mock")).resolve()
TERMINAL = {"DONE", "EXIT"}


@contextmanager
def locked():
    ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (ROOT / ".lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield


def read(job_id):
    if not str(job_id).isdigit() or int(job_id) < 1:
        raise ValueError("Job ID must be a positive integer")
    try:
        return json.loads((ROOT / f"{int(job_id)}.json").read_text())
    except FileNotFoundError:
        raise ValueError(f"Job <{job_id}> is not found") from None


def save(job):
    path = ROOT / f"{job['id']}.json"
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(job, indent=2) + "\n")
    temporary.replace(path)


def output_path(value, job_id, default):
    return str(Path((value or str(ROOT / default)).replace("%J", str(job_id))).resolve())


def terminate_session(process):
    # tcsh creates additional process groups for background jobs, even without a tty.
    # The unreaped direct child owns this session ID, so it cannot be reused.
    groups = set()
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fields = (entry / "stat").read_text().rsplit(")", 1)[1].split()
            if int(fields[3]) == process.pid:
                groups.add(int(fields[2]))
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            continue
    for group in sorted(groups - {process.pid}) + [process.pid]:
        try:
            os.killpg(group, signal.SIGKILL)
        except ProcessLookupError:
            pass


def submit(argv):
    parser = argparse.ArgumentParser(prog="bsub", description=__doc__, allow_abbrev=False)
    parser.add_argument("-q", default="normal")
    parser.add_argument("-J", default="")
    parser.add_argument("-n", type=int, default=1)
    parser.add_argument("-R", default="")
    parser.add_argument("-m", default="")
    parser.add_argument("-env", choices=("all", "none"), default="all")
    parser.add_argument("-cwd", default=os.getcwd())
    parser.add_argument("-K", action="store_true")
    out = parser.add_mutually_exclusive_group()
    out.add_argument("-o")
    out.add_argument("-oo")
    err = parser.add_mutually_exclusive_group()
    err.add_argument("-e")
    err.add_argument("-eo")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    shell = shutil.which("tcsh")
    if not shell:
        parser.error("tcsh is required; install it and add it to PATH")
    if args.n < 1:
        parser.error("-n must be positive")
    if not Path(args.cwd).is_dir():
        parser.error("-cwd must be an existing directory")
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if command:
        # One argument is a shell expression; multiple arguments preserve argv.
        script = command[0] if len(command) == 1 else shlex.join(command)
    else:
        if sys.stdin.isatty():
            parser.error("a command or a script on stdin is required")
        script = sys.stdin.read()
    if not script.strip():
        parser.error("empty command")
    with locked():
        counter = ROOT / "counter"
        job_id = int(counter.read_text()) + 1 if counter.exists() else 1
        counter.write_text(str(job_id))
        job = dict(id=job_id, user=getpass.getuser(), host=socket.gethostname(), status="PEND", queue=args.q, name=args.J,
                   slots=args.n, resources=args.R, hosts=args.m, cwd=str(Path(args.cwd).resolve()),
                   command=script, shell=shell, cancel=False, exit_code=None, environment=args.env,
                   stdout=output_path(args.o or args.oo, job_id, "%J.out"),
                   stderr=output_path(args.e or args.eo, job_id, "%J.err"),
                   stdout_mode="w" if args.oo else "a",
                   stderr_mode="w" if args.eo else "a")
        save(job)
    try:
        subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "_worker", str(job_id)],
                         stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                         stderr=subprocess.DEVNULL, start_new_session=True,
                         env=dict(os.environ, LSF_MOCK_STATE_DIR=str(ROOT)))
    except OSError as error:
        with locked():
            job.update(status="EXIT", exit_code=127, error=str(error))
            save(job)
        raise
    print(f"Job <{job_id}> is submitted to queue <{args.q}>.", flush=True)
    if args.K:
        while True:
            job = read(job_id)
            if job["status"] in TERMINAL:
                return job["exit_code"]
            time.sleep(0.05)
    return 0


def worker(job_id):
    process = None
    code = 127
    error = None
    try:
        job = read(job_id)
        env = dict(os.environ if job.get("environment", "all") == "all" else {}, LSB_JOBID=str(job_id), LSB_JOBNAME=job["name"],
                   LSB_QUEUE=job["queue"], LSB_DJOB_NUMPROC=str(job["slots"]))
        if job.get("environment") == "none":
            # LSF still supplies execution-account/job state with -env none.
            # Allow tests to model a node HOME distinct from the submit host.
            account = pwd.getpwuid(os.getuid())
            env.update(HOME=os.environ.get("LSF_MOCK_EXEC_HOME", account.pw_dir),
                       USER=account.pw_name, PWD=job["cwd"])
        with open(job["stdout"], job["stdout_mode"]) as out, open(job["stderr"], job["stderr_mode"]) as err:
            # A script file avoids command-length limits and executes stdin scripts with tcsh too.
            with tempfile.TemporaryFile(mode="w+") as script:
                script.write(job["command"] + "\n")
                script.seek(0)
                with locked():
                    job = read(job_id)
                    if job["cancel"]:
                        code = 137
                    else:
                        process = subprocess.Popen([job["shell"], "-f"], stdin=script,
                                                   stdout=out, stderr=err, cwd=job["cwd"],
                                                   env=env, start_new_session=True)
                        job.update(status="RUN", pid=process.pid)
                        save(job)
                if process is not None:
                    while True:
                        # Only this worker signals its own unreaped child's session.
                        if read(job_id)["cancel"]:
                            terminate_session(process)
                            process.wait()
                            code = 137
                            break
                        result = process.poll()
                        if result is not None:
                            code = result if result >= 0 else 128 - result
                            break
                        time.sleep(0.05)
    except Exception as exc:
        error = str(exc)
        if process is not None and process.returncode is None:
            try:
                terminate_session(process)
            except ProcessLookupError:
                pass
            process.wait()
    finally:
        with locked():
            job = read(job_id)
            if job["cancel"]:
                code = 137
            job.update(status="DONE" if code == 0 else "EXIT", exit_code=code)
            if error:
                job["error"] = error
            save(job)
    return 0


def kill(argv):
    parser = argparse.ArgumentParser(prog="bkill", description="Cancel local mock jobs by ID")
    parser.add_argument("ids", nargs="+", type=int)
    args = parser.parse_args(argv)
    result = 0
    for job_id in args.ids:
        try:
            with locked():
                job = read(job_id)
                if job["status"] in TERMINAL:
                    raise ValueError(f"Job <{job_id}> has already finished")
                job["cancel"] = True
                save(job)
            print(f"Job <{job_id}> is being terminated")
        except ValueError as exc:
            print(exc, file=sys.stderr)
            result = 1
    return result


def jobs(argv):
    parser = argparse.ArgumentParser(prog="bjobs", description="List local mock jobs")
    parser.add_argument("-a", action="store_true", help="include finished jobs")
    parser.add_argument("-l", action="store_true", help="long job details")
    parser.add_argument("-json", action="store_true", help="mock-specific JSON output")
    parser.add_argument("-noheader", action="store_true")
    parser.add_argument("-o", choices=("stat exit_code", "jobid stat", "jobid stat job_name", "jobid stat job_name:250"))
    parser.add_argument("ids", nargs="*", type=int)
    args = parser.parse_args(argv)
    with locked():
        records = [read(i) for i in args.ids] if args.ids else [
            read(p.stem) for p in ROOT.glob("*.json")]
    records = sorted((j for j in records if args.ids or args.a or j["status"] not in TERMINAL),
                     key=lambda j: j["id"])
    if args.json:
        print(json.dumps(records, indent=2))
    elif args.l:
        for job in records:
            print(f"Job <{job['id']}>, Job Name <{job['name']}>, User <{job.get('user', 'unknown')}>, Status <{job['status']}>, Queue <{job['queue']}>, Command <{job['command']}>")
            print(f"Submitted from host <{job.get('host', 'unknown')}>, CWD <{job['cwd']}>, Output File <{job['stdout']}>, Error File <{job['stderr']}>;")
            if job.get("pid"):
                print(f"Started on <{job.get('host', 'unknown')}>, Execution CWD <{job['cwd']}>;")
    elif args.o:
        if not args.noheader:
            print("JOBID STAT JOB_NAME" if args.o in ("jobid stat job_name", "jobid stat job_name:250") else "JOBID STAT" if args.o == "jobid stat" else "STAT EXIT_CODE")
        for job in records:
            if args.o in ("jobid stat job_name", "jobid stat job_name:250"):
                print(f"{job['id']} {job['status']} {job['name']}")
            elif args.o == "jobid stat":
                print(f"{job['id']} {job['status']}")
            else:
                print(f"{job['status']} {job['exit_code'] if job['exit_code'] is not None else '-'}")
    else:
        print("JOBID\tSTAT\tQUEUE\tJOB_NAME")
        for job in records:
            print(f"{job['id']}\t{job['status']}\t{job['queue']}\t{job['name']}")
    return 0


if __name__ == "__main__":
    try:
        action = sys.argv[1]
        handlers = {"bsub": submit, "bkill": kill, "bjobs": jobs,
                    "_worker": lambda args: worker(int(args[0]))}
        sys.exit(handlers[action](sys.argv[2:]))
    except (OSError, ValueError) as exc:
        print(f"lsf-mock: {exc}", file=sys.stderr)
        sys.exit(1)
