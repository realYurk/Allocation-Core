import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from allocation_core_client import Client, CoreError

EXE = os.environ.get("ALLOCATION_TEST_EXE", "")
ROOT = Path(__file__).resolve().parents[2]


@unittest.skipUnless(EXE, "set ALLOCATION_TEST_EXE to a locally built executable")
class SyntheticClientTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = Path(self.tmp.name)
        source = ROOT / "examples" / "manufacturing"
        for name in ("rule.json", "amounts.csv", "labor.csv", "sales.csv"):
            shutil.copyfile(source / name, self.path / name)
        self.client = Client(EXE, timeout=30)

    def tearDown(self):
        self.tmp.cleanup()

    def test_inspect_memory_disk_and_reference(self):
        rule = self.path / "rule.json"
        self.assertEqual(self.client.inspect(rule)["status"], "PASS")
        reference = self.path / "reference.csv"
        reference.write_text("period,plant,brand,family,sku,account,currency,amount\n"
            "2026-01,工厂甲,品牌甲,系列甲,Item-A,Cost-A,CUR,100.00\n"
            "2026-01,工厂甲,品牌甲,系列甲,Item-B,Cost-A,CUR,300.00\n"
            "2026-01,工厂甲,品牌甲,系列乙,商品C,Cost-A,CUR,400.00\n"
            "2026-01,工厂甲,品牌甲,系列乙,商品D,Cost-A,CUR,400.00\n",encoding="utf-8")
        for mode in ("memory", "disk"):
            out = self.path / mode
            kwargs = {"disk_partitions": 4, "driver_disk_index": True} if mode == "disk" else {}
            summary = self.client.run(rule, out, workers=2, **kwargs)
            self.assertEqual((summary["status"], summary["output_amount"]), ("PASS", "1200.00"))
            compared = self.client.compare(out / "results.csv", reference, self.path / (mode + "-reference"), precision=2, reject_reference_duplicates=True)
            self.assertEqual((compared["status"], compared["matched"]), ("MATCH", 4))
        changed = self.path / "wrong.csv"
        changed.write_text(reference.read_text(encoding="utf-8").replace("100.00", "101.00"), encoding="utf-8")
        compared = self.client.compare(self.path / "memory" / "results.csv", changed, self.path / "difference", precision=2)
        self.assertEqual(compared["status"], "DIFFERENCES")

    def test_failure_has_report_and_no_publication(self):
        (self.path / "amounts.csv").write_text("period,plant,brand,family,sku,account,currency,amount\n2026-01,P,U,U,U,E1,CUR,1.001\n", encoding="utf-8")
        with self.assertRaises(CoreError) as caught:
            self.client.inspect(self.path / "rule.json")
        self.assertEqual(caught.exception.report["status"], "BLOCK")
        with self.assertRaises(CoreError):
            self.client.run(self.path / "rule.json", self.path / "invalid")
        self.assertFalse((self.path / "invalid").exists())

    def test_reconciliation_coordinate_limit(self):
        data = "account,sku,amount\n" + "".join(f"A,SKU-{i:02d},1.00\n" for i in range(33))
        source = self.path / "many.csv"
        source.write_text(data, encoding="utf-8")
        out = self.path / "limited"
        with self.assertRaises(CoreError) as caught:
            self.client.compare(source, source, out, precision=2, max_partition_coordinates=1)
        self.assertIn("max partition coordinates", str(caught.exception))
        self.assertFalse(out.exists())


class ClientValidationTest(unittest.TestCase):
    def test_timeout_and_disk_options(self):
        client = Client(sys.executable, timeout=1)
        with patch("allocation_core_client.subprocess.run", side_effect=subprocess.TimeoutExpired("core", 1)):
            with self.assertRaises(CoreError):
                client.inspect("rule.json")
        with self.assertRaises(ValueError):
            client.run("rule.json", "out", driver_disk_index=True)
        with self.assertRaises(ValueError):
            client.run("rule.json", "out", resume=True)


if __name__ == "__main__":
    unittest.main()
