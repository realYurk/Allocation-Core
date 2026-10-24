import importlib.util
import hashlib
import json
import pathlib
import unittest
p=pathlib.Path(__file__).resolve().parents[1]/'integration'/'python'/'sql_bulk_publish.py'
spec=importlib.util.spec_from_file_location('sql_bulk_publish',p)
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class IntegrityTests(unittest.TestCase):
    def test_canonical_input_hash_not_result_hash(self):
        s={'data_sha256':{'z.csv':'a'*64,'a.csv':'b'*64}}
        expected=hashlib.sha256(json.dumps(s['data_sha256'],ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()).hexdigest()
        self.assertEqual(m.input_fingerprint(s),expected)
        self.assertNotEqual(m.input_fingerprint(s),'a'*64)
        self.assertEqual(m.input_fingerprint(s),m.input_fingerprint({'data_sha256':{'a.csv':'b'*64,'z.csv':'a'*64}}))
    def test_missing_hash_fail_closed(self):
        for s in ({},{'data_sha256':{}},{'data_sha256':{'x.csv':'0'*63}},{'data_sha256':{'x.csv':'wrong'}}):
            with self.assertRaises(ValueError):m.input_fingerprint(s)
if __name__=='__main__':unittest.main()
