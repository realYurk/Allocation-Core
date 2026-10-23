"""Generate synthetic (never real-company) two-stage volume test. Python is needed
only by engineers generating the benchmark, not by users running Windows EXE."""
import csv, json, sys
from pathlib import Path
root=Path(sys.argv[1]); accounts=int(sys.argv[2]) if len(sys.argv)>2 else 100
skus=int(sys.argv[3]) if len(sys.argv)>3 else 1000
if not (1<=accounts<=500 and 2<=skus<=10000):raise ValueError('accounts 1..500; skus 2..10000')
root.mkdir(parents=True,exist_ok=True)
dims=['period','plant','brand','family','sku','account','currency']
periods=[f'2026-{m:02}' for m in range(1,13)]
def put(name,head,records):
 with (root/name).open('w',encoding='utf-8',newline='') as f:
  writer=csv.writer(f);writer.writerow(head);writer.writerows(records)
put('amounts.csv',dims+['amount'],((month,'工厂一','待分配品牌','待分配系列','待分配',f'科目{i:03}','CNY','1000.00') for month in periods for i in range(1,accounts+1)))
put('labor.csv',['period','plant','family','hours'],((month,'工厂一',family,'100') for month in periods for family in ['系列甲','系列乙']))
put('sales.csv',['period','plant','brand','family','sku','units'],((month,'工厂一','品牌甲',['系列甲','系列乙'][i%2],f'商品{i:05}','1') for month in periods for i in range(1,skus+1)))
origin={'period':'AS_IS','plant':'AS_IS','brand':'TOTAL','family':'TOTAL','sku':'TOTAL','account':'AS_IS','currency':'AS_IS'}
targ1={d:{'mode':'SOURCE'} for d in dims}
targ1.update({'brand':{'mode':'ELEMENT','value':'品牌甲'},'family':{'mode':'INCLUDE','include':['系列甲','系列乙']},'sku':{'mode':'ELEMENT','value':'待分配'}})
origin2={d:('TOTAL' if d=='sku' else 'AS_IS') for d in dims}
targ2={d:{'mode':'SOURCE'} for d in dims};targ2['sku']={'mode':'INCLUDE','include':[f'商品{i:05}' for i in range(1,skus+1)]}
rule={'schema':'allocation-ir/2.0','name':f'Synthetic 2-stage {accounts} accounts * 12 months * {skus} SKUs','precision':2,'dimensions':dims,'immutable_dimensions':['period','currency'],'hierarchies':{},'datasets':{'amounts':{'amounts':'amounts.csv'},'drivers':{'labor':'labor.csv','sales':'sales.csv'}},'stages':[
{'id':'stage1','inputs':[{'from':'amounts','share':'1'}],'source':{'grouping':origin,'filters':{}},'target':targ1,'conservation_keys':['period','plant','account','currency'],'allocations':[{'id':'labor','share':'1','driver_dataset':'labor','weight_field':'hours'}]},
{'id':'stage2','inputs':[{'from':'stage1','share':'1'}],'source':{'grouping':origin2,'filters':{}},'target':targ2,'conservation_keys':['period','plant','brand','family','account','currency'],'allocations':[{'id':'sales','share':'1','driver_dataset':'sales','weight_field':'units'}]}
]}
(root/'rule.json').write_text(json.dumps(rule,indent=2,ensure_ascii=False),encoding='utf-8')
print(f'Generated synthetic {accounts*12} source pools, {accounts*12*skus:,} expected terminal SKU rows')
