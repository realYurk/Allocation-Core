"""Synthetic multi-stage benchmark with 24 accounts x 12 months x 500 SKUs. No dependencies."""
import csv,json,sys,os
from pathlib import Path
root=Path(sys.argv[1] if len(sys.argv)>1 else 'bench/two_stage_synthetic')
root.mkdir(parents=True,exist_ok=True)
dims=['period','plant','brand','family','sku','account','currency']
def write(name,headers,rows):
 with open(root/name,'w',newline='',encoding='utf8') as f:
  w=csv.writer(f);w.writerow(headers);w.writerows(rows)
periods=[f'2026-{m:02}' for m in range(1,13)]
write('amounts.csv',dims+['amount'],((month,'工厂一','待分配品牌','待分配系列','待分配',f'科目{i:03}','CNY','2200.00') for month in periods for i in range(1,25)))
families=['品类一','品类二']
write('labor.csv',['period','plant','family','hours'],((month,'工厂一',family,'100') for month in periods for family in families))
write('sales.csv',['period','plant','brand','family','sku','units'],((month,'工厂一','品牌甲',families[i%2],f'商品{i:04}','1') for month in periods for i in range(1,501)))
source1={d:('AS_IS' if d in ['period','plant','account','currency'] else 'TOTAL') for d in dims}
target1={d:{'mode':'SOURCE'} for d in dims}
target1.update({'brand':{'mode':'ELEMENT','value':'品牌甲'},'family':{'mode':'INCLUDE','include':families},'sku':{'mode':'ELEMENT','value':'待分配'}})
source2={d:('TOTAL' if d=='sku' else 'AS_IS') for d in dims}
target2={d:{'mode':'SOURCE'} for d in dims};target2['sku']={'mode':'INCLUDE','include':[f'商品{i:04}' for i in range(1,501)]}
rule={
 'schema':'allocation-ir/2.0','name':'two-stage synthetic 24 accounts 12 periods 500 SKUs','precision':2,'dimensions':dims,
 'immutable_dimensions':['period','currency'], 'hierarchies':{},
 'datasets':{'amounts':{'amounts':'amounts.csv'},'drivers':{'labor':'labor.csv','sales':'sales.csv'}},
 'stages':[
 {'id':'stage1','inputs':[{'from':'amounts','share':'1'}],'source':{'grouping':source1,'filters':{}},'target':target1,
 'conservation_keys':['period','plant','account','currency'], 'allocations':[{'id':'labor','share':'1','driver_dataset':'labor','weight_field':'hours'}]},
 {'id':'stage2','inputs':[{'from':'stage1','share':'1'}],'source':{'grouping':source2,'filters':{}},'target':target2,
 'conservation_keys':['period','plant','brand','family','account','currency'], 'allocations':[{'id':'sales','share':'1','driver_dataset':'sales','weight_field':'units'}]}
 ]}
(root/'rule.json').write_text(json.dumps(rule,indent=2,ensure_ascii=False),encoding='utf8')
print('288 input pools x 2 family x 250 SKUs => 144,000 terminal rows')
