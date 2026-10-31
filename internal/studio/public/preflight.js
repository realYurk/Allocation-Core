// Read-only finance diagnostics, never infer target-level Driver eligibility.
let financeReadinessReport=null,financeReadinessRequest=0;
const fr=id=>document.getElementById(id);
function resetFinanceReadiness(){
 financeReadinessRequest++;financeReadinessReport=null;
 if(fr('financeReadiness'))fr('financeReadiness').hidden=true;
 if(fr('financeReadinessRows'))fr('financeReadinessRows').replaceChildren();
}
function financeText(tag,value,className){
 const e=document.createElement(tag);
 e.textContent=String(value==null?'':value);
 if(className)e.className=className;
 return e;
}
function renderFinanceReadiness(report){
 financeReadinessReport=report;
 fr('financeReadiness').hidden=false;
 fr('financeReadinessSummary').textContent=
 (report.status==='PASS'?'输入格式通过':'输入检查被阻断')+
 ' · 已分派科目 '+report.accounts+
 ' · 需关注科目 '+(report.review_accounts||0)+
 ' · 阻断问题 '+(report.findings||[]).filter(x=>x.level==='BLOCK').length;
 const container=fr('financeReadinessFindings');container.replaceChildren();
 for(const f of report.findings||[]){
  const item=document.createElement('div');item.className='finance-check-finding';
  item.appendChild(financeText('strong',(f.level==='BLOCK'?'阻断：':'需核对：')+f.title));
  item.appendChild(financeText('p',f.detail));
  item.appendChild(financeText('p','建议操作：'+f.next_step,'finance-check-action'));
  if(f.affected_accounts)item.appendChild(financeText('small',
   '关联 '+f.affected_accounts+' 个科目，例如：'+(f.account_examples||[]).join('、')+
   '。关联不代表每个科目都实际命中了该因子。'));
  container.appendChild(item);
 }
 if(!(report.findings||[]).length)
  container.appendChild(financeText('p','文件结构和数值格式未发现问题，可以进入真实计算验证。'));
 const drivers=fr('financeDriverSummary');drivers.replaceChildren();
 const table=document.createElement('table');
 const head=document.createElement('thead'),headers=document.createElement('tr');
 for(const h of ['Driver 文件','字段','正数行','零值行','负数行','关联科目'])headers.appendChild(financeText('th',h));
 head.appendChild(headers);table.appendChild(head);
 const body=document.createElement('tbody');
 let fieldCount=0;
 for(const d of report.drivers||[]){
  for(const sig of d.weight_signals||[]){
   if(fieldCount++>=100)break;
   const tr=document.createElement('tr');
   for(const val of [d.name,sig.field,sig.positive_rows,sig.zero_rows,sig.negative_rows,sig.affected_accounts])
    tr.appendChild(financeText('td',val));
   body.appendChild(tr);
  }
 }
 if(fieldCount>0){
  drivers.appendChild(financeText('p','Driver 字段整体分布（非目标坐标匹配结果）','finance-check-subhead'));
  table.appendChild(body);drivers.appendChild(table);
 }
 fr('financeReadinessScope').textContent='本次只检查原始金额、科目规则及 Driver 文件整体数值分布。'+
  '“输入正常”不等于 Driver 按期间/工厂/车型匹配成功，也不等于分摊结果或参考系统一致。请运行后逐坐标对账。';
 showFinanceReadinessAccounts();
}
function filteredFinanceReadiness(){
 if(!financeReadinessReport)return [];
 const term=fr('financeReadinessSearch').value.trim().toLowerCase();
 const status=fr('financeReadinessStatus').value;
 return (financeReadinessReport.account_details||[]).filter(a=>
  (!status||a.status===status)&&
  (!term||[a.account,a.template,...(a.notes||[])].some(x=>String(x).toLowerCase().includes(term))));
}
function showFinanceReadinessAccounts(){
 const body=fr('financeReadinessRows');body.replaceChildren();
 const filtered=filteredFinanceReadiness();
 for(const a of filtered.slice(0,300)){
  const tr=document.createElement('tr');
  const label=a.status==='BLOCK'?'缺少数据':a.status==='CHECK_DRIVER'?'需核对因子':'输入正常';
  for(const v of [a.account,a.template,a.input_rows,label,(a.notes||[]).join('；')||'—'])
   tr.appendChild(financeText('td',v));
  body.appendChild(tr);
 }
 fr('financeReadinessCount').textContent='显示 '+Math.min(300,filtered.length)+' / '+filtered.length+' 科目；可导出完整明细。';
}
function downloadFinanceReadinessAccounts(){
 if(!financeReadinessReport)return;
 exportCSV((active||'project')+'_input_readiness.csv',
  ['account','template','input_rows','input_check_status','notes'],
  (financeReadinessReport.account_details||[]).map(a=>
   [a.account,a.template,a.input_rows,a.status,(a.notes||[]).join('; ')]));
}
