# -*- coding: utf-8 -*-
# Python 3.6+ standard-library adapter. Reorders/renames exported CSV columns.
# Usage: python csv_mapping_adapter.py column_mapping.json
from __future__ import print_function
import csv, json, os, sys

def run(config_path):
    base=os.path.dirname(os.path.abspath(config_path))
    project=os.path.dirname(base)
    with open(config_path,'r',encoding='utf-8') as f: cfg=json.load(f)
    for group in ('amounts','drivers'):
        for name,setting in sorted(cfg.get(group,{}).items()):
            input_path=setting['input_csv']
            if not os.path.isabs(input_path): input_path=os.path.join(project,input_path)
            output_path=os.path.join(project,setting['output_csv'])
            if not os.path.isfile(input_path):
                raise IOError('Please set input_csv for '+group+'/'+name+': '+input_path)
            columns=setting['fields']  # {canonical_name: upstream_export_column}
            if os.path.abspath(input_path)==os.path.abspath(output_path):
                raise ValueError('input and output must differ')
            with open(input_path,'r',newline='',encoding='utf-8-sig') as fin:
                reader=csv.DictReader(fin)
                for field in columns.values():
                    if field not in (reader.fieldnames or []): raise ValueError('missing field '+field)
                with open(output_path,'w',newline='',encoding='utf-8-sig') as fout:
                    writer=csv.DictWriter(fout,fieldnames=list(columns.keys()))
                    writer.writeheader()
                    for row in reader:
                        writer.writerow({canonical:row[upstream] for canonical,upstream in columns.items()})
            print('Wrote',output_path)

if __name__=='__main__':
    if len(sys.argv)!=2: raise SystemExit('usage: python csv_mapping_adapter.py column_mapping.json')
    run(sys.argv[1])
