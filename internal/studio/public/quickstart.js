// Compact finance-user setup guide. Reuses optimistic saves and existing preflight.
let quickGuideToken=0;
let quickCurrentTemplate=null;
const quickEl=id=>document.getElementById(id);
function quickOption(select,value,label){
 const o=document.createElement('option');o.value=value;o.textContent=label;select.appendChild(o);
}
function quickNotice(text,type='neutral'){
 const p=quickEl('quickNotice');p.textContent=text;p.dataset.kind=type;
}
function quickGuideRefresh(){
 const project=quickEl('quickProject'),old=project.value;
 project.replaceChildren();
 const source=quickEl('project');
 for(const o of source.options)quickOption(project,o.value,o.textContent);
 if(active&&[...project.options].some(o=>o.value===active))project.value=active;
 else if([...project.options].some(o=>o.value===old))project.value=old;
 const template=quickEl('quickTemplate'),prior=template.value;
 template.replaceChildren();
 for(const name of data?.templates||[]){
  if(data?.template_columns?.[name])quickOption(template,name,name.replace(/\.json$/,''));
 }
 if([...template.options].some(o=>o.value===prior))template.value=prior;
 quickGuideAccounts();
 quickGuideLoadTemplate();
 quickNotice(active?'请选择科目和规则，先预览，再将变更保存到项目。':'先在下方创建项目或选择已有项目。');
}
async function quickProjectChanged(){
 const selected=quickEl('quickProject').value;
 if(!selected)return;
 quickEl('project').value=selected;
 busyJob='';
 await openProject();
 await loadJobHistory();
}
function quickGuideAccounts(){
 const select=quickEl('quickAccount'),old=select.value;
 const q=quickEl('quickSearch').value.trim().toLocaleLowerCase();
 const matching=(data?.assignments||[]).filter(a=>a.account.toLocaleLowerCase().includes(q));
 select.replaceChildren();
 for(const a of matching.slice(0,250))quickOption(select,a.account,a.account);
 if([...select.options].some(o=>o.value===old))select.value=old;
 quickEl('quickAccountCount').textContent='筛选 '+matching.length+' 个科目，显示前 '+Math.min(250,matching.length)+' 个。批量指定将影响全部筛选科目。';
 quickGuideImpact();
 quickEl('quickApplyFiltered').disabled=!quickCurrentTemplate||!q||!matching.length;
}
function quickGuideImpact(){
 const account=quickEl('quickAccount').value,template=quickEl('quickTemplate').value;
 const saved=(data?.assignments||[]).find(a=>a.account===account)?.template;
 const proposed=chosen?.[account];
 const pending=typeof assignmentChanges==='function'?assignmentChanges().length:0;
 const state=!account?'请先选择科目':proposed===template?'当前草稿已选此规则':'当前科目：'+(proposed||saved||'未分配')+'；拟选择：'+(template||'未选择');
 quickEl('quickImpact').textContent=state+'。全项目还有 '+pending+' 项待保存的规则变更。';
}
function quickGuideField(parent,tag,value,className){
 const el=document.createElement(tag);el.textContent=value;
 if(className)el.className=className;
 parent.appendChild(el);return el;
}
async function quickGuideLoadTemplate(){
 const token=++quickGuideToken;
 quickCurrentTemplate=null;
 const name=active,template=quickEl('quickTemplate').value;
 const box=quickEl('quickTemplateDetail');box.replaceChildren();
 quickEl('quickApplyOne').disabled=true;
 quickEl('quickApplyFiltered').disabled=true;
 quickGuideImpact();
 if(!name||!template){quickGuideField(box,'p','当前项目没有可预览的有效规则模板。','muted');return}
 quickGuideField(box,'p','正在读取规则结构…','muted');
 try{
  const guide=await api('/api/multi/template-guide?name='+encodeURIComponent(name)+'&template='+encodeURIComponent(template));
  if(token!==quickGuideToken||active!==name||quickEl('quickTemplate').value!==template)return;
  quickCurrentTemplate=guide;
  box.replaceChildren();
  quickGuideField(box,'strong',guide.title||template,'quickTitle');
  quickGuideField(box,'p',guide.stages.length+' 个计算阶段 · 金额精度 '+guide.precision+' 位小数 · 规则尾差政策：'+guide.rounding_policy,'muted');
  for(const stage of guide.stages){
   const item=document.createElement('div');item.className='quickStage';box.appendChild(item);
   quickGuideField(item,'strong',stage.id);
   const operation=stage.operation==='ALLOCATE'?'按分摊因子计算':stage.operation==='MERGE'?'汇总合并':'按原金额传递';
   quickGuideField(item,'p',operation+' · 来源：'+(stage.sources.join('、')||'无'));
   for(const factor of stage.drivers){
    quickGuideField(item,'p','分摊依据：'+factor.dataset+' / '+factor.field+' · 比例 '+factor.percent);
   }
   quickGuideField(item,'p','分配目标维度：'+(stage.targets.join('、')||'沿用来源维度')+'；保持不变：'+(stage.conserved.join('、')||'规则指定的固定维度'),'muted');
  }
  for(const warning of guide.warnings)quickGuideField(box,'p','注意：'+warning,'quickWarning');
  quickGuideField(box,'p',guide.note,'muted');
  quickEl('quickApplyOne').disabled=false;
  quickEl('quickApplyFiltered').disabled=!quickEl('quickSearch').value.trim();
 }catch(error){
  if(token!==quickGuideToken)return;
  box.replaceChildren();quickGuideField(box,'p','规则无法使用：'+error.message,'quickWarning');
  quickNotice('无法通过规则校验，不能设置为科目规则。','error');
 }
}
function quickGuideStage(all){
 if(!data||!quickCurrentTemplate||quickCurrentTemplate.template!==quickEl('quickTemplate').value){
  quickNotice('请先选择并通过规则预览。','error');return;
 }
 const template=quickEl('quickTemplate').value;
 let selected;
 if(all){
  const q=quickEl('quickSearch').value.trim().toLocaleLowerCase();
  if(!q){quickNotice('批量设置前请先输入科目筛选词，防止误改所有科目。','error');return}
  selected=data.assignments.filter(a=>a.account.toLocaleLowerCase().includes(q));
  if(!selected.length){quickNotice('当前筛选没有科目。','error');return}
  const changed=selected.filter(a=>chosen[a.account]!==template).length;
  if(changed===0){quickNotice('筛选内科目已经使用该规则。');return}
  if(!confirm('将 '+selected.length+' 个筛选科目中的 '+changed+' 个改为「'+template+'」。\n仅更新当前页面草稿，不会立即保存或执行分摊。是否继续？'))return;
 }else{
  const account=quickEl('quickAccount').value;
  if(!account||!data.assignments.some(a=>a.account===account)){
   quickNotice('请先选择一个现有科目。','error');return;
  }
  selected=[{account}];
 }
 for(const a of selected)chosen[a.account]=template;
 renderTable();quickGuideImpact();
 quickNotice('已调整 '+selected.length+' 个科目的页面草稿。请点击“确认保存规则分派”，再进行体检。','success');
 quickEl('quickSave').focus();
}
async function quickGuideSave(){
 if(!data||!active){quickNotice('请先选择项目。','error');return}
 const changes=assignmentChanges();
 if(!changes.length){quickNotice('没有待保存的规则变更。');return}
 const button=quickEl('quickSave');button.disabled=true;
 try{
  const success=await save();
  quickGuideImpact();
  if(success)quickNotice('已保存 '+changes.length+' 项规则变更。请执行输入体检。','success');
  else quickNotice('保存未完成（可能存在并发变更或取消操作），请查看页面错误提示。','error');
 }finally{button.disabled=false}
}
async function quickGuideInspect(){
 if(!data||!active){quickNotice('请先选择项目。','error');return}
 if(assignmentChanges().length){quickNotice('存在尚未保存的变更，请先保存规则分派。','error');return}
 quickEl('flow-preflight').scrollIntoView({behavior:'smooth',block:'start'});
 await inspectProject();
 const report=quickEl('inspectResult').textContent;
 const pass=report.includes('输入体检：PASS');
 quickNotice(pass?'输入体检通过。还需实际执行与参考系统逐坐标对账。':'输入体检未通过，请在下方查看错误后修正。',pass?'success':'error');
}
window.quickGuideRefresh=quickGuideRefresh;
quickGuideRefresh();
