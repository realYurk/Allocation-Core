# -*- coding: utf-8 -*-
"""Allocation Core HTTP client: Python 3 standard library, no pip required.

Use after starting `alloc serve`. It never calculates or modifies allocation
business logic; the same Go engine is shared by all callers.
"""
import argparse
import json
import time
import urllib.error
import urllib.request


def request(url, method="GET", body=None, token=""):
    payload = json.dumps(body, ensure_ascii=False).encode("utf-8") if body is not None else None
    headers = {"Content-Type": "application/json; charset=utf-8"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=payload, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=60) as response:
        return json.loads(response.read().decode("utf-8"))


def main():
    ap = argparse.ArgumentParser(description="Submit a configurable allocation job to local Core V13")
    ap.add_argument("--base", default="http://127.0.0.1:8787")
    ap.add_argument("--rule", default="manufacturing/rule.json")
    ap.add_argument("--token", default="")
    ap.add_argument("--driver-disk-index", action="store_true")
    args = ap.parse_args()
    base = args.base.rstrip("/")
    job = request(base + "/v1/jobs", "POST", {"rule": args.rule, "workers": 2,
        "disk_partitions": 8, "driver_disk_index": args.driver_disk_index}, args.token)
    print("Submitted job:", job["id"])
    while True:
        status = request(base + "/v1/jobs/" + job["id"], token=args.token)
        if status["status"] in ("PASS", "FAILED"):
            break
        time.sleep(0.3)
    print(json.dumps(status, indent=2, ensure_ascii=False))
    if status["status"] != "PASS":
        raise SystemExit(1)
    print("Download results from:", base + "/v1/jobs/" + job["id"] + "/results.csv")


if __name__ == "__main__":
    main()
