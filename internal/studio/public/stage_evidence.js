// View historical Core ledger evidence only. No business allocation is done here.
let stageEvidenceToken=0,stageEvidenceJob='';
const stageEl=id=>document.getElementById(id);
function resetStageEvidence(){
 stageEvidenceToken++;stageEvidenceJob='';
 const box=stageEl('stageEvidence');if(box)box.hidden=true;
 const body=stageEl('stageEvidenceRows');if(body)body.replaceChildren();
 const notice=stageEl('stageEvidenceNotice');if(notice)notice.textContent='';
 const dl=stageEl('stageEvidenceDownload');if(dl)dl.hidden=true;
}
function setStageEvidenceJob(job,summary){
 resetStageEvidence();
 if(!job||summary?.status!=='PASS'||!Array.isArray(summary.details))return;
 stageEvidenceJob=job;stageEl('stageEvidence').hidden=false;
 const select=stageEl('stageEvidenceAccount');select.replaceChildren();
 for(const a of summary.details){
  if(!a.account)continue;
  const option=document.createElement('option');option.value=a.account;option.textContent=a.account;
  select.appendChild(option);
 }
 stageEl('stageEvidenceNotice').textContent='请选择科目。此处查看的是历史成功任务实际产生的 Ledger，并非当前项目修改后的规则。';
}
function invalidateStageEvidence(){stageEvidenceToken++;stageEl('stageEvidenceRows').replaceChildren();stageEl('stageEvidenceDownload').hidden=true;stageEl('stageEvidenceNotice').textContent='科目或期间已改变，请重新读取该条件下的历史证据。'}
function stageEvidenceText(tag,value){const e=document.createElement(tag);e.textContent=String(value??'');return e}
async function loadStageEvidence(){
 const job=stageEvidenceJob,project=active,account=stageEl('stageEvidenceAccount').value,period=stageEl('stageEvidencePeriod').value.trim();
 const token=++stageEvidenceToken,btn=stageEl('stageEvidenceBtn'),dl=stageEl('stageEvidenceDownload');
 if(!job||!account)return message('请选择成功任务和科目');
 btn.disabled=true;dl.hidden=true;
 stageEl('stageEvidenceNotice').textContent='正在读取历史运行 Ledger，较大的任务可能需要扫描较多记录…';
 const query='name='+encodeURIComponent(project)+'&job='+encodeURIComponent(job)+'&account='+encodeURIComponent(account)+'&period='+encodeURIComponent(period);
 try{
  const r=await api('/api/multi/stage-evidence?'+query);
  if(token!==stageEvidenceToken||project!==active||job!==stageEvidenceJob||account!==stageEl('stageEvidenceAccount').value)return;
  const body=stageEl('stageEvidenceRows');body.replaceChildren();
  let measured=0;
  for(const item of r.lines||[]){
   const tr=document.createElement('tr');
   for(const v of [item.stage,item.allocation,item.driver_dataset+' / '+item.weight_field,item.allocated_amount,item.output_rows])tr.appendChild(stageEvidenceText('td',v));
   const td=document.createElement('td');
   if(item.sample_pools?.length){
    const details=document.createElement('details'),title=stageEvidenceText('summary','来源池样例（最多 3 个）');
    details.appendChild(title);
    for(const pool of item.sample_pools)details.appendChild(stageEvidenceText('p',Object.entries(pool).map(([k,v])=>k+'='+v).join(' · ')));
    td.appendChild(details);
   }else td.textContent='无分摊流水';
   tr.appendChild(td);body.appendChild(tr);measured+=item.output_rows;
  }
  stageEl('stageEvidenceNotice').textContent='已读取 '+measured+' 条匹配流水 · 规则组 '+r.group_id+' · 证据'+(r.manifest_verified?'文件指纹核对通过':'缺少清单，不能证明完整性')+'。\n'+r.scope;
  dl.href='/api/multi/stage-evidence?'+query+'&download=csv';dl.hidden=false;
 }catch(e){if(token===stageEvidenceToken){stageEl('stageEvidenceRows').replaceChildren();stageEl('stageEvidenceNotice').textContent='读取历史证据失败：'+e.message}}
 finally{btn.disabled=false}
}
