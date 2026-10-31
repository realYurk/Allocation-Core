// Historical run-to-run comparison: all values from immutable, successful jobs.
// No financial computation or causal attribution is performed in the browser.
let runCompareToken=0,runCompareReport=null;
const runCmpEl=id=>document.getElementById(id);
function clearRunComparison(){
 runCompareToken++;runCompareReport=null;
 const e=runCmpEl('runCompareRows');if(e)e.replaceChildren();
 const status=runCmpEl('runCompareStatus');if(status)status.textContent='请选择两个同项目的历史成功任务进行对比。';
 const dl=runCmpEl('runCompareDownload');if(dl)dl.hidden=true;
}
function refreshRunCompareJobs(jobs){
 clearRunComparison();
 for(const id of ['runCompareBefore','runCompareAfter']){
  const el=runCmpEl(id),selected=el.value;el.replaceChildren();
  for(const job of jobs.filter(j=>j.case===active&&j.status==='PASS')){
   const op=document.createElement('option');op.value=job.id;op.textContent=job.start+' · '+job.id.slice(0,8);el.appendChild(op);
  }
  if([...el.options].some(op=>op.value===selected))el.value=selected;
 }
 const before=runCmpEl('runCompareBefore'),after=runCmpEl('runCompareAfter');
 if(before.options.length>1&&after.value===before.value)after.selectedIndex=1;
}
function runCmpText(tag,value){const el=document.createElement(tag);el.textContent=String(value??'');return el}
function renderRunCompareAccounts(){
 const t=runCmpEl('runCompareRows');t.replaceChildren();
 const report=runCompareReport;if(!report)return;
 const search=runCmpEl('runCompareSearch').value.toLowerCase().trim();
 const mode=runCmpEl('runCompareFilter').value;
 const accounts=report.coordinate_comparison.accounts||[];
 let count=0;
 for(const a of accounts){
  const issues=a.different+a.missing+a.extra;
  if(search&&!a.account.toLowerCase().includes(search))continue;
  if(mode==='issues'&&!issues||mode==='match'&&issues)continue;
  const tr=document.createElement('tr');
  for(const value of [a.account,a.matched,a.different,a.missing,a.extra,issues?'存在差异':'一致'])tr.appendChild(runCmpText('td',value));
  t.appendChild(tr);count++;
 }
 runCmpEl('runCompareCount').textContent='显示 '+count+' / '+accounts.length+' 个科目。';
}
async function compareHistoricalRuns(){
 const before=runCmpEl('runCompareBefore').value,after=runCmpEl('runCompareAfter').value,project=active;
 if(!project||!before||!after||before===after){runCmpEl('runCompareStatus').textContent='请先选择两个不同的历史成功任务。';return}
 const token=++runCompareToken,button=runCmpEl('runCompareBtn');button.disabled=true;
 runCmpEl('runCompareDownload').hidden=true;runCmpEl('runCompareStatus').textContent='正在按完整业务坐标比较两次历史结果；大文件可能需要较长时间…';
 try{
  const report=await api('/api/multi/compare-runs',{name:project,before,after});
  if(token!==runCompareToken||project!==active||before!==runCmpEl('runCompareBefore').value||after!==runCmpEl('runCompareAfter').value)return;
  runCompareReport=report;
  const c=report.coordinate_comparison;
  const changed=report.input_changes||[],rules=report.rule_changes||[];
  const summary=[
   '旧任务 '+before.slice(0,8)+' → 新任务 '+after.slice(0,8),
   '最终逐坐标：'+c.status+'；一致 '+c.matched+'；金额不同 '+c.different+'；新任务缺少 '+c.missing+'；新任务新增 '+c.extra,
   '金额总额：'+c.reference_total+' → '+c.actual_total+'（两次金额相同也可能有坐标差异）',
   '输入文件指纹变化 '+changed.length+' 处；生效规则变化 '+rules.length+' 个科目',
   ...changed.slice(0,12).map(item=>'文件变动：'+item.file+' ['+item.type+']'),
   ...(changed.length>12?['其余 '+(changed.length-12)+' 个文件变化已记录在报告中']:[]),
   ...rules.slice(0,12).map(item=>'规则变化科目：'+item.account),
   ...(rules.length>12?['其余 '+(rules.length-12)+' 个科目规则变化已记录在报告中']:[]),
   '两个结果文件的 SHA-256：'+c.reference_sha256+' / '+c.actual_sha256,
   report.scope
  ];
  runCmpEl('runCompareStatus').textContent=summary.join('\n');
  runCmpEl('runCompareDownload').href='/api/multi/compare-runs?name='+encodeURIComponent(project)+'&id='+encodeURIComponent(report.id)+'&download=csv';
  runCmpEl('runCompareDownload').hidden=false;
  renderRunCompareAccounts();
 }catch(e){if(token===runCompareToken){runCompareReport=null;runCmpEl('runCompareRows').replaceChildren();runCmpEl('runCompareStatus').textContent='历史对比失败：'+e.message}}
 finally{button.disabled=false}
}
