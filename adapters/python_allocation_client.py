# -*- coding: utf-8 -*-
"""Python 3.6+ standard library client for the standalone allocation executable."""
import json
import subprocess
import sys

def run_allocation(executable, rule_json, output_dir, workers=1):
    command=[executable, 'run', rule_json, output_dir, '--workers', str(workers)]
    process=subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                           universal_newlines=True, check=False)
    if process.returncode:
        raise RuntimeError('Allocation failed (exit {}): {}'.format(process.returncode, process.stderr))
    result=json.loads(process.stdout)
    if result.get('status')!='PASS' or result.get('input_amount')!=result.get('output_amount'):
        raise RuntimeError('Allocation did not pass conservation checks')
    return result

if __name__=='__main__':
    if len(sys.argv) not in (4,5):
        sys.exit('usage: python python_allocation_client.py executable rule.json output_dir [workers]')
    args=sys.argv[1:]
    if len(args)==4:
        args[3]=int(args[3])
    print(json.dumps(run_allocation(*args), ensure_ascii=False, indent=2))
