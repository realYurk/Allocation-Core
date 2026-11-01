// DOM-contract tests, not a substitute for browser rendering/visual acceptance.
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'public/lab.js'),'utf8');
const html=fs.readFileSync(path.join(__dirname,'public/lab.html'),'utf8');

function harness(){
 const ids=new Map(),timers=new Map();let nextTimer=0;
 class Element{
  constructor(tag='div'){this.tagName=tag;this.children=[];this.events={};this.dataset={};this.style={};this.attributes={};this.value='';this.hidden=false;this.checked=false;this._text='';this.classes=new Set();this.classList={add:v=>this.classes.add(v),remove:v=>this.classes.delete(v),toggle:(v,on)=>on?this.classes.add(v):this.classes.delete(v)};}
  set id(id){this._id=id;ids.set(id,this)}get id(){return this._id}
  set innerHTML(v){throw new Error('Business data must use textContent, not HTML')}
  set textContent(v){this._text=String(v);this.children=[]}get textContent(){return this._text+this.children.map(c=>c.textContent).join(' ')}
  setAttribute(k,v){this.attributes[k]=v}
  append(...items){for(const item of items){item.parentElement=this;this.children.push(item)}}
  replaceChildren(...items){this._text='';this.children=[];this.append(...items)}
  addEventListener(type,fn){(this.events[type]??=[]).push(fn)}
  async dispatch(type){for(const fn of this.events[type]||[])await fn({target:this})}
  querySelectorAll(tag){return this.children.flatMap(c=>[...(c.tagName===tag?[c]:[]),...c.querySelectorAll(tag)])}
  click(){return this.dispatch('click')}
 }
 for(const m of html.matchAll(/<(\w+)[^>]*\bid="([^"]+)"[^>]*>/g)){const e=new Element(m[1]);e.id=m[2];e.parentElement=new Element();}
 const document={getElementById:id=>{if(!ids.has(id))throw new Error('Missing DOM id '+id);return ids.get(id)},createElement:tag=>new Element(tag),createElementNS:(_,tag)=>new Element(tag),body:new Element('body'),addEventListener(){}};
 const fetches=[];
 const context={document,module:{exports:{}},AbortController,URLSearchParams,Blob,URL,console,navigator:{clipboard:{writeText:async()=>{}}},setTimeout:fn=>{timers.set(++nextTimer,fn);return nextTimer},clearTimeout:id=>timers.delete(id),setInterval:fn=>{timers.set(++nextTimer,fn);return nextTimer},clearInterval:id=>timers.delete(id),fetch:(url,options)=>new Promise(resolve=>fetches.push({url,options,resolve:body=>resolve({ok:true,json:async()=>body})}))};
 ids.get('example').value='A';ids.get('demoGroup').value='model';ids.get('tableSide').value='after';ids.get('sort').value='desc';ids.get('fault').value='zero';
 vm.createContext(context);vm.runInContext(source,context);
 return{lab:context.module.exports,ids,fetches,timers};
}
function report(amount='1000000.00'){
 const row={account:'E1',model:'车型 A',amount};
 return{mode:'SYNTHETIC',status:'PASS',precision:2,dimensions:['account','model'],input:amount,output:amount,process_input:amount,process_output:amount,difference:'0.00',before:{rows:[row],groups:[{member:'未分配',amount}],count:1},after:{rows:[row],groups:[{member:'车型 A',amount}],count:1},stages:[{id:'stage',inputs:[{from:'expenses',share:'1'}],source:{grouping:{model:'TOTAL'},filters:{}},target:{model:{mode:'INCLUDE',include:['车型 A']}},allocations:[{id:'rule',driver_dataset:'hours',weight_field:'weight',share:'1',negative_driver_policy:'REJECT'}],input:amount,output:amount,status:'PASS'}],ledger:[],checks:{conservation:'PASS',reference:'NOT_TESTED',publication:'UNPUBLISHED'},note:'合成数据 / 未发布'};
}
test('exact decimal display never rounds large or negative values',()=>{
 const{lab}=harness();assert.equal(lab.fmt('9223372036854.775807'),'9,223,372,036,854.775807');assert.equal(lab.fmt('-0.01'),'-0.01');assert.equal(lab.fmt('0.000000'),'0.000000');
});
test('graph preserves branches, joins and explicit terminal edges',()=>{
 const{lab}=harness();const stages=[{id:'join',inputs:[{from:'A',share:'.5'},{from:'B',share:'1'}]},{id:'B',inputs:[{from:'source',share:'.4'}]},{id:'A',inputs:[{from:'source',share:'.6'}]}];const g=lab.graphModel(stages);
 assert.equal(g.nodes.find(n=>n.id==='join').level,2);assert.equal(g.edges.length,5);assert.equal(g.edges.filter(e=>e.to==='__lab_final__').length,1);assert(!g.edges.some(e=>e.from==='A'&&e.to==='B'));
 assert.throws(()=>lab.graphModel([{id:'A',inputs:[{from:'B'}]},{id:'B',inputs:[{from:'A'}]}]),/依赖不完整/);
});
test('stale requests cannot display results after parameters change',async()=>{
 const{lab,fetches,ids}=harness();const old=lab.runDemo();assert.equal(fetches.length,1);ids.get('amount').value='200.00';const current=lab.runDemo();assert.equal(fetches.length,2);assert.equal(JSON.parse(fetches[1].options.body).amount,'200.00');assert.equal(ids.get('report').hidden,true);
 fetches[1].resolve(report('200.00'));await current;assert.equal(ids.get('afterMoney').textContent,'200.00');fetches[0].resolve(report());await old;assert.equal(ids.get('afterMoney').textContent,'200.00');
 lab.clear();assert.equal(ids.get('report').hidden,true);assert.equal(lab.state.report,null);
});
test('playback, stepping, weights and targets are bound to working controls',async()=>{
 const{lab,fetches,ids,timers}=harness();const pending=lab.runDemo();fetches[0].resolve(report());await pending;
 await ids.get('next').click();assert.equal(lab.state.step,1);assert.match(ids.get('stageDetailBody').textContent,/hours/);
 await ids.get('play').click();assert(lab.state.playTimer);await ids.get('play').click();assert.equal(lab.state.playTimer,null);
 ids.get('weight0').value='7';await ids.get('weight0').dispatch('input');assert.equal(ids.get('report').hidden,true);assert(timers.size>0);
 ids.get('target1').checked=false;const next=lab.runDemo();const request=JSON.parse(fetches[1].options.body);assert.equal(request.weights[0],'7');assert.equal(request.targets[1],false);fetches[1].resolve(report());await next;
});
test('business text is inert; mixed currencies do not become one displayed amount',async()=>{
 const{lab,fetches,ids}=harness();const r=report();r.after.rows[0].model='<img src=x onerror=alert(1)>';r.after.mixed_currency=true;r.after.file_mixed_currency=true;const pending=lab.runDemo();fetches[0].resolve(r);await pending;
 assert.equal(ids.get('afterMoney').textContent,'混合币种');assert.match(ids.get('detailRows').textContent,/<img/);assert.match(ids.get('afterBars').textContent,/currency/);
});
test('MERGE, PASS and disabled stages remain inspectable without allocation arrays',async()=>{
 const{lab,fetches,ids}=harness();const r=report();r.stages[0].operation='MERGE';r.stages[0].allocations=null;const pending=lab.runDemo();fetches[0].resolve(r);await pending;lab.showStep(1);assert.match(ids.get('stageDetailBody').textContent,/合并上游金额/);
 r.stages[0].operation='PASS';lab.render();lab.showStep(1);assert.match(ids.get('stageDetailBody').textContent,/原样透传/);
 r.stages[0].enabled=false;lab.render();lab.showStep(1);assert.match(ids.get('stageDetailBody').textContent,/已停用/);assert.equal(ids.get('ruleCount').textContent,'0');
 const g=lab.graphModel([{id:'__lab_final__',inputs:[{from:'source',share:'1'}]}]);assert.equal(new Set(g.nodes.map(n=>n.id)).size,g.nodes.length);
});
test('exports retain decimals, quote coordinates and neutralize spreadsheet formulas',async()=>{
 const{lab,fetches}=harness();const r=report('90071992547409.91');r.after.rows[0].model='=HYPERLINK("test")';const pending=lab.runDemo();fetches[0].resolve(r);await pending;const csv=lab.exportText();assert(csv.includes('90071992547409.91'));assert(csv.includes("'=HYPERLINK"));assert(csv.includes('""test""'));
});
test('offline assets, narrow layout rules, reduced motion and original entry points exist',()=>{
 assert(!/https?:\/\//.test(html));const css=fs.readFileSync(path.join(__dirname,'public/lab.css'),'utf8');assert(css.includes('prefers-reduced-motion:reduce'));assert(css.includes('max-width:760px'));assert(css.includes('overflow:auto'));
 for(const page of ['index.html','multi.html','designer.html'])assert(fs.readFileSync(path.join(__dirname,'public',page),'utf8').includes('href="/lab"'));
});
