const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync(__dirname+'/public/driver_contract.js','utf8');
function view(need){
 const elements=[];
 const box={children:[],replaceChildren(){this.children=[]},appendChild(el){this.children.push(el)}};
 const ctx=vm.createContext({URLSearchParams,driverNeedsState:{project:'demo',template:'a.json',sha256:'hash'},driverCurrentNeed:()=>need,
  document:{getElementById:()=>box,createElement:tag=>{const el={tag,textContent:''};elements.push(el);return el}}});
 vm.runInContext(source,ctx);ctx.renderDriverContract();return elements;
}
test('missing Driver explains required dimensions, source scope, and empty template',()=>{
 const elements=view({alias:'<script>bad</script>',status:'MISSING_FILE',current_file:'driver.csv',required_dimensions:['sku'],weight_fields:['weight'],source_dimensions:['period'],broadcast_dimensions:['period'],missing_columns:['sku'],uses:[{stage:'s',allocation:'a',share:'1',negative_policy:'REJECT'}],next_step:'导出真实权重'});
 assert.ok(elements.some(e=>e.textContent.includes('必需目标/过滤维度：sku')));
 assert.ok(elements.some(e=>e.textContent.includes('跨期间')));
 const link=elements.find(e=>e.tag==='a');assert.ok(link.href.startsWith('/api/multi/driver-needs?'));
 assert.ok(link.href.includes('base_sha256=hash'));assert.ok(link.href.includes('%3Cscript%3E'));
 assert.equal(elements.some(e=>Object.hasOwn(e,'innerHTML')),false);
});
test('no selected Driver gives an actionable empty state',()=>{
 const elements=view(null);assert.ok(elements.some(e=>e.textContent.includes('选择项目')));assert.equal(elements.some(e=>e.tag==='a'),false);
});
