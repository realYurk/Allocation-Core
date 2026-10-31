// Read-only source-stage Driver matching; never modify project rules or results.
let coverageReport=null,coverageToken=0;
const coverageEl=id=>document.getElementById(id);
function resetCoverage(){coverageReport=null;coverageToken++;const p=coverageEl('coverageResult');if(p)p.hidden=true}
function coverageCell(tag,value){const e=document.createElement(tag);e.textContent=String(value??'');return e}
function coverageStatus(status){return ({BLOCK:'发现匹配问题',REVIEW:'需核对负权重',UNVERIFIED:'尚未验证',SOURCE_MATCHED:'首阶段已匹配'})[status]||status}
async function inspectCoverage(){
 const project=active,btn=coverageEl('coverageBtn'),token=++coverageToken;
 if(!project){message('请先选择项目');return}
 btn.disabled=true;btn.textContent='正在检查业务坐标与 Driver...';
 try {
   const result=await api('/api/multi/driver-coverage',{name:project});
   if(project!==active||token!==coverageToken)return;
   coverageReport=result;coverageEl('coverageResult').hidden=false;
   coverageEl('coverageSummary').textContent=coverageStatus(result.status)+' · 已检查来源池/因子 '+result.checked_pool_factors+
    ' · 确认不匹配 '+result.blocked_pool_factors+' · 尚未验证科目 '+result.unverified_accounts;
   coverageEl('coverageScope').textContent=result.scope+' 本检查是只读诊断，不代表分摊计算或参考系统对账已经通过。';
   const dest=coverageEl('coverageFindings');dest.replaceChildren();
   for(const f of result.findings||[]){
     const item=document.createElement('div');item.className='coverage-finding';
     item.appendChild(coverageCell('strong',coverageStatus(f.level)+(f.account?' · '+f.account:'')+(f.stage?' / '+f.stage:'')));
     item.appendChild(coverageCell('p',f.reason));
     if(f.pool && Object.keys(f.pool).length){item.appendChild(coverageCell('p','来源池：'+Object.entries(f.pool).map(([k,v])=>k+'='+v).join(' · ')))}
     if(f.driver)item.appendChild(coverageCell('p','检查 '+f.driver+(f.field?' / '+f.field:'')));
     if(f.advice)item.appendChild(coverageCell('p','建议：'+f.advice));
     dest.appendChild(item);
   }
   if(result.truncated_findings)dest.appendChild(coverageCell('p','其余 '+result.truncated_findings+' 条诊断未在页面展开，请缩小业务切面后检查。'));
   if(!(result.findings||[]).length)dest.appendChild(coverageCell('p','本轮可检查的首阶段来源池已匹配；仍须运行完整计算并独立对账。'));
   renderCoverageAccounts();
 } catch(e){if(token===coverageToken)message('Driver 匹配检查失败：'+e.message)}
 finally{btn.disabled=false;btn.textContent='检查 Driver 是否匹配业务坐标'}
}
function coverageFiltered(){
 if(!coverageReport)return [];
 const term=coverageEl('coverageSearch').value.trim().toLowerCase(),status=coverageEl('coverageFilter').value;
 return (coverageReport.accounts||[]).filter(x=>(!status||x.status===status)&&(!term||x.account.toLowerCase().includes(term)));
}
function renderCoverageAccounts(){
 const rows=coverageEl('coverageRows');if(!rows)return;rows.replaceChildren();
 const filtered=coverageFiltered();for(const a of filtered.slice(0,250)){
  const tr=document.createElement('tr');for(const v of [a.account,coverageStatus(a.status),a.checked_pool_factors,a.problems])tr.appendChild(coverageCell('td',v));rows.appendChild(tr);
 }
 coverageEl('coverageCount').textContent='显示 '+Math.min(250,filtered.length)+' / '+filtered.length+' 科目，完整清单可导出。';
}
function downloadCoverageAccounts(){
 if(!coverageReport)return;
 exportCSV((active||'project')+'_driver_coverage.csv',['account','status','checked_pool_factors','issues'],
   (coverageReport.accounts||[]).map(x=>[x.account,x.status,x.checked_pool_factors,x.problems]));
}
