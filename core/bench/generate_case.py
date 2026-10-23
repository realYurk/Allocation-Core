"""Synthetic performance dataset only. Generates 100 accounts × 12 periods × 1000 SKUs.
Developer-only generator, not required to run the compiled allocation executable.
"""
import csv,json,sys,os
out=sys.argv[1] if len(sys.argv)>1 else 'bench/generated'
os.makedirs(out,exist_ok=True)
dims=['period','region','brand','account','sku','currency']
def dump(path,cols,iterable):
 with open(os.path.join(out,path),'w',newline='',encoding='utf-8') as f:
  w=csv.writer(f);w.writerow(cols)
  for r in iterable:w.writerow(r)
dump('amounts.csv',dims+['amount','row_id'],((f'2026-{month:02}', '东区','品牌甲',f'科目{i:03}','待分摊','CNY','5000.00',f'SRC-{month}-{i}') for month in range(1,13) for i in range(1,101)))
dump('drivers.csv',['period','region','brand','sku','units'],((f'2026-{month:02}','东区','品牌甲',f'商品{j:04}',str(1+j%20)) for month in range(1,13) for j in range(1,1001)))
dimsScope={'period':'AS_IS','region':'AS_IS','brand':'AS_IS','account':'AS_IS','sku':'TOTAL','currency':'AS_IS'}
target={d:{'mode':'SOURCE'} for d in dims};target['sku']={'mode':'INCLUDE','include':[f'商品{j:04}' for j in range(1,1001)]}
spec={'schema':'allocation-ir/2.0','name':'100科目×12个月×1000SKU，完全合成的性能验证','precision':2,'dimensions':dims,'immutable_dimensions':['period','currency'],'hierarchies':{},'datasets':{'amounts':{'expenses':'amounts.csv'},'drivers':{'sales':'drivers.csv'}},'stages':[{'id':'按商品销量分摊','inputs':[{'from':'expenses','share':'1'}],'source':{'grouping':dimsScope,'filters':{}},'target':target,'conservation_keys':['period','region','brand','account','currency'],'allocations':[{'id':'销量占比','share':'1','driver_dataset':'sales','weight_field':'units','negative_driver_policy':'REJECT'}]}]}
with open(os.path.join(out,'rule.json'),'w',encoding='utf-8') as f:json.dump(spec,f,ensure_ascii=False,indent=2)
print('Generated 1,200 pools, 12,000 driver rows, expected 1,200,000 output rows')
