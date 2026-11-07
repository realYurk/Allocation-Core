"""Small standard-library process client for an installed Allocation Core.

This client never implements allocation or edits source data. It requires a
platform-specific Core executable and does not start a server or open a port.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
from typing import Any


class CoreError(RuntimeError):
    def __init__(self, message: str, *, exit_code: int | None = None, report: dict[str, Any] | None = None):
        super().__init__(message)
        self.exit_code = exit_code
        self.report = report


class Client:
    def __init__(self, executable: str | os.PathLike[str], *, timeout: float | None = None):
        self.executable = str(Path(executable).resolve(strict=True))
        if timeout is not None and timeout <= 0:
            raise ValueError("timeout must be positive")
        self.timeout = timeout

    def _call(self, args: list[str], *, allow_differences: bool = False) -> dict[str, Any]:
        try:
            proc = subprocess.run([self.executable, *args], stdin=subprocess.DEVNULL,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                  timeout=self.timeout, check=False)
        except subprocess.TimeoutExpired as exc:
            raise CoreError("Core process timed out; inspect temporary files before retrying") from exc
        except OSError as exc:
            raise CoreError(f"could not start Core: {exc}") from exc
        if proc.returncode not in ((0, 3) if allow_differences else (0,)):
            message = proc.stderr.decode("utf-8", "replace").strip()
            try:
                report = json.loads(proc.stdout.decode("utf-8"))
                if not isinstance(report, dict):
                    report = None
            except (UnicodeError, ValueError):
                report = None
            raise CoreError(message or "Core process failed", exit_code=proc.returncode, report=report)
        try:
            result = json.loads(proc.stdout.decode("utf-8"))
        except (UnicodeError, ValueError) as exc:
            raise CoreError("Core returned invalid UTF-8 JSON", exit_code=proc.returncode) from exc
        if not isinstance(result, dict):
            raise CoreError("Core returned a non-object response", exit_code=proc.returncode)
        return result

    def inspect(self, rule: str | os.PathLike[str]) -> dict[str, Any]:
        return self._call(["core", "inspect", str(Path(rule).resolve()), "--json"])

    def run(self, rule: str | os.PathLike[str], output: str | os.PathLike[str], *,
            workers: int = 1, disk_partitions: int | None = None,
            driver_disk_index: bool = False, driver_cache_dir: str | os.PathLike[str] | None = None,
            checkpoint_dir: str | os.PathLike[str] | None = None, resume: bool = False,
            evidence_gzip: bool = False) -> dict[str, Any]:
        if not 1 <= workers <= 128:
            raise ValueError("workers must be 1..128")
        if disk_partitions is not None and not 2 <= disk_partitions <= 64:
            raise ValueError("disk_partitions must be 2..64")
        if (driver_disk_index or driver_cache_dir is not None or checkpoint_dir is not None) and disk_partitions is None:
            raise ValueError("disk options require disk_partitions")
        if driver_cache_dir is not None and not driver_disk_index:
            raise ValueError("driver_cache_dir requires driver_disk_index")
        if resume and checkpoint_dir is None:
            raise ValueError("resume requires checkpoint_dir")
        args = ["core", "run", str(Path(rule).resolve()), str(Path(output).resolve()), "--workers", str(workers)]
        if disk_partitions is not None:
            args += ["--disk-partitions", str(disk_partitions)]
        if driver_disk_index:
            args += ["--driver-disk-index"]
        if driver_cache_dir is not None:
            args += ["--driver-cache-dir", str(Path(driver_cache_dir).resolve())]
        if checkpoint_dir is not None:
            args += ["--checkpoint-dir", str(Path(checkpoint_dir).resolve())]
        if resume:
            args += ["--resume"]
        if evidence_gzip:
            args += ["--evidence-gzip"]
        return self._call(args)

    def compare(self, actual: str | os.PathLike[str], reference: str | os.PathLike[str],
                output: str | os.PathLike[str], *, precision: int,
                tolerance: str = "0", reject_reference_duplicates: bool = False,
                reject_actual_duplicates: bool = False) -> dict[str, Any]:
        if not 0 <= precision <= 6:
            raise ValueError("precision must be 0..6")
        args = ["reconcile", "--actual", str(Path(actual).resolve()), "--reference", str(Path(reference).resolve()),
                "--out", str(Path(output).resolve()), "--precision", str(precision), "--tolerance", tolerance]
        if reject_reference_duplicates:
            args += ["--reject-reference-duplicates"]
        if reject_actual_duplicates:
            args += ["--reject-actual-duplicates"]
        return self._call(args, allow_differences=True)
