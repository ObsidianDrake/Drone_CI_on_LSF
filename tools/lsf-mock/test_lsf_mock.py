import concurrent.futures
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import unittest


BIN = Path(__file__).resolve().parent / "bin"


class MockLSFTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.env = dict(os.environ, LSF_MOCK_STATE_DIR=str(self.root / "state"))

    def tearDown(self):
        for path in (self.root / "state").glob("*.json"):
            job = json.loads(path.read_text())
            if job["status"] not in ("DONE", "EXIT"):
                self.run_cmd("bkill", str(job["id"]))
                self.wait(job["id"])
        self.temp.cleanup()

    def run_cmd(self, name, *args, input=None):
        return subprocess.run([str(BIN / name), *args], cwd=self.root, env=self.env,
                              input=input, text=True, capture_output=True, timeout=10)

    def submit(self, *args):
        result = self.run_cmd("bsub", *args)
        self.assertEqual(result.returncode, 0, result.stderr)
        return int(re.search(r"Job <(\d+)>", result.stdout)[1])

    def wait(self, job_id):
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            job = json.loads(self.run_cmd("bjobs", "-json", str(job_id)).stdout)[0]
            if job["status"] in ("DONE", "EXIT"):
                return job
            time.sleep(0.03)
        self.fail("job did not finish")

    def test_tcsh_environment_cwd_and_output(self):
        job_id = self.submit("-J", "demo", "-q", "test", "-n", "2", "-oo", "job-%J.log",
                             'set greeting = "hello world"; echo "$greeting $LSB_JOBID $LSB_QUEUE $LSB_DJOB_NUMPROC"; pwd')
        job = self.wait(job_id)
        self.assertEqual(job["status"], "DONE")
        self.assertEqual((self.root / f"job-{job_id}.log").read_text(),
                         f"hello world {job_id} test 2\n{self.root}\n")

    def test_long_job_details(self):
        job_id = self.submit("-J", "details", "-q", "test", "-oo", "out.log", "-eo", "err.log", "echo done")
        self.wait(job_id)
        result = self.run_cmd("bjobs", "-a", "-l", str(job_id))
        self.assertEqual(result.returncode, 0, result.stderr)
        for value in (f"Job <{job_id}>", "Job Name <details>", "User <", "Queue <test>", "Command <echo done>", "Started on <", f"CWD <{self.root}>", "Output File <", "Error File <"):
            self.assertIn(value, result.stdout)

    def test_stdin_failure_and_stderr(self):
        result = self.run_cmd("bsub", "-K", "-eo", "error.log",
                              input="echo problem > /dev/stderr\nexit 7\n")
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertEqual((self.root / "error.log").read_text(), "problem\n")

    def test_argv_spaces_and_append(self):
        for _ in range(2):
            self.submit("-K", "-o", "output.log", "/usr/bin/printf", "%s\\n", "a b", "c")
        self.assertEqual((self.root / "output.log").read_text(), "a b\nc\na b\nc\n")

    def test_cancel_descendants(self):
        script = self.root / "child.py"
        script.write_text("import os, time\nfrom pathlib import Path\n"
                          "Path('child.pid').write_text(str(os.getpid()))\n"
                          "while True:\n    Path('heartbeat').write_text(str(time.time_ns()))\n    time.sleep(.02)\n")
        job_id = self.submit(f"python3 {script} &\nwait")
        deadline = time.monotonic() + 3
        while not (self.root / "heartbeat").exists() and time.monotonic() < deadline:
            time.sleep(0.02)
        self.assertTrue((self.root / "heartbeat").exists())
        self.assertEqual(self.run_cmd("bkill", str(job_id)).returncode, 0)
        job = self.wait(job_id)
        self.assertEqual((job["status"], job["exit_code"]), ("EXIT", 137))
        heartbeat = (self.root / "heartbeat").read_text()
        time.sleep(0.15)
        self.assertEqual((self.root / "heartbeat").read_text(), heartbeat)
        self.assertNotEqual(self.run_cmd("bkill", str(job_id)).returncode, 0)

    def test_concurrent_submissions(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            ids = list(pool.map(lambda _: self.submit("echo ok"), range(12)))
        self.assertEqual(len(set(ids)), 12)
        for job_id in ids:
            self.assertEqual(self.wait(job_id)["status"], "DONE")

    def test_invalid_input_and_output_error(self):
        self.assertNotEqual(self.run_cmd("bsub", "-unsupported", "echo ok").returncode, 0)
        self.assertNotEqual(self.run_cmd("bkill", "99999").returncode, 0)
        self.assertNotEqual(self.run_cmd("bsub", "-K", "-oo", "missing/out", "echo ok").returncode, 0)


if __name__ == "__main__":
    unittest.main()
