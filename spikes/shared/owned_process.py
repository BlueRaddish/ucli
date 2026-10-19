"""Shared process ownership for disposable Python spikes, not production platform code."""
from __future__ import annotations

import ctypes
import os
from pathlib import Path
import signal
import subprocess


class OwnedProcess:
    """Kill only the owned job/process group, never names or unrelated PIDs.

    Windows assignment happens immediately after Popen. There is a startup race
    before job assignment; fixtures spawn descendants only after initialization.
    This is cleanup evidence, not proof that untrusted startup code is contained.
    """

    def __init__(self, command: list[str], cwd: Path, env: dict | None = None):
        self.job = None
        self.process = subprocess.Popen(
            command, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
            start_new_session=os.name != "nt", bufsize=0,
        )
        try:
            if os.name == "nt":
                self._assign_job()
        except Exception:
            self.process.kill()
            self.process.wait(timeout=5)
            raise

    def _assign_job(self):
        from ctypes import wintypes as wt

        class BasicLimits(ctypes.Structure):
            _fields_ = [("PerProcessUserTimeLimit", ctypes.c_int64),
                        ("PerJobUserTimeLimit", ctypes.c_int64), ("LimitFlags", wt.DWORD),
                        ("MinimumWorkingSetSize", ctypes.c_size_t), ("MaximumWorkingSetSize", ctypes.c_size_t),
                        ("ActiveProcessLimit", wt.DWORD), ("Affinity", ctypes.c_size_t),
                        ("PriorityClass", wt.DWORD), ("SchedulingClass", wt.DWORD)]

        class IoCounters(ctypes.Structure):
            _fields_ = [(name, ctypes.c_uint64) for name in
                        ("ReadOperationCount", "WriteOperationCount", "OtherOperationCount",
                         "ReadTransferCount", "WriteTransferCount", "OtherTransferCount")]

        class ExtendedLimits(ctypes.Structure):
            _fields_ = [("BasicLimitInformation", BasicLimits), ("IoInfo", IoCounters),
                        ("ProcessMemoryLimit", ctypes.c_size_t), ("JobMemoryLimit", ctypes.c_size_t),
                        ("PeakProcessMemoryUsed", ctypes.c_size_t), ("PeakJobMemoryUsed", ctypes.c_size_t)]

        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        self.kernel.CreateJobObjectW.argtypes = [ctypes.c_void_p, wt.LPCWSTR]
        self.kernel.CreateJobObjectW.restype = wt.HANDLE
        self.kernel.SetInformationJobObject.argtypes = [wt.HANDLE, ctypes.c_int, ctypes.c_void_p, wt.DWORD]
        self.kernel.AssignProcessToJobObject.argtypes = [wt.HANDLE, wt.HANDLE]
        self.kernel.CloseHandle.argtypes = [wt.HANDLE]
        job = self.kernel.CreateJobObjectW(None, None)
        if not job:
            raise OSError("owned job creation failed")
        limits = ExtendedLimits()
        limits.BasicLimitInformation.LimitFlags = 0x2000  # KILL_ON_JOB_CLOSE
        if not self.kernel.SetInformationJobObject(job, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
            self.kernel.CloseHandle(job)
            raise OSError("owned job configuration failed")
        if not self.kernel.AssignProcessToJobObject(job, wt.HANDLE(self.process._handle)):
            self.kernel.CloseHandle(job)
            raise OSError("owned job assignment failed")
        self.job = job

    def close(self):
        if self.job is not None:
            self.kernel.CloseHandle(self.job)
            self.job = None
        elif os.name != "nt":
            try:
                os.killpg(self.process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        elif self.process.poll() is None:
            self.process.kill()
        self.process.wait(timeout=5)
        for stream in (self.process.stdin, self.process.stdout, self.process.stderr):
            stream.close()
