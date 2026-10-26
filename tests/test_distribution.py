import pathlib, subprocess, sys, tempfile, unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
VERIFY = ROOT / 'tools' / 'verify_distribution.py'

class DistributionTests(unittest.TestCase):
    def test_full_source_windows_executables(self):
        p = subprocess.run([sys.executable, str(VERIFY), str(ROOT), '--all-platforms'], capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)

    def test_missing_executable_is_an_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            p = subprocess.run([sys.executable, str(VERIFY), tmp], capture_output=True, text=True)
            self.assertNotEqual(p.returncode, 0)
            self.assertIn('MISSING', p.stdout + p.stderr)

if __name__ == '__main__': unittest.main()
