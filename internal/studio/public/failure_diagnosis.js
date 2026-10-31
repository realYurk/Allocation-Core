// Immutable failed-run evidence only; all dynamic content is plain text.
function renderFailureDiagnosis(job){
 const box=document.getElementById('failureDiagnosis');if(!box)return;
 box.replaceChildren();
 const d=job&&job.status==='FAILED'?job.diagnosis:null;
 box.hidden=!d;if(!d)return;
 const put=(tag,value,cls)=>{const e=document.createElement(tag);e.textContent=value==null?'':String(value);if(cls)e.className=cls;box.appendChild(e);return e};
 put('h3','计算失败 · 定位与处理建议');
 put('p',d.title+'（'+d.code+'）','diagnosis-title');
 const lines=[];
 if(d.template)lines.push('规则模板：'+d.template);
 if(d.stage)lines.push('阶段：'+d.stage);
 if(d.allocation)lines.push('分摊因子：'+d.allocation);
 if(d.driver_dataset)lines.push('Driver：'+d.driver_dataset);
 if(d.driver_file)lines.push('需要检查的文件：'+d.driver_file);
 if(d.weight_field)lines.push('权重字段：'+d.weight_field);
 if(d.account_count)lines.push('关联科目：'+d.account_count+' 个'+((d.accounts||[]).length&&d.account_count>(d.accounts||[]).length?'（显示前20个）':''));
 if(d.accounts&&d.accounts.length)lines.push('科目：'+d.accounts.join('、'));
 if(d.scope==='EXACT_POOL'&&d.pool&&Object.keys(d.pool).length){
  lines.push('本次失败的来源分摊池：');
  for(const k of Object.keys(d.pool).sort())lines.push('  '+k+' = '+(d.pool[k]==='__POOL__'?'汇总池（非业务成员）':d.pool[k]));
 }else{lines.push('定位范围：规则组（引擎没有报告可确认的完整分摊池坐标）');}
 put('pre',lines.join('\n'),'diagnosis-context');
 put('h4','建议检查');
 const steps=document.createElement('ol');
 for(const step of d.next_steps||[]){const li=document.createElement('li');li.textContent=step;steps.appendChild(li)}
 box.appendChild(steps);
 put('p',d.limitations||'本提示仅用于排查，仍需重新计算和独立对账。','diagnosis-limit');
 const link=document.createElement('a');link.href=d.driver_file?'#flow-driver':'#flow-rules';
 link.textContent=d.driver_file?'前往 Driver 数据准备':'前往规则配置';box.appendChild(link);
 put('p','此诊断在失败当时保存；项目若已修改，请按当前数据重新体检、运行。','diagnosis-limit');
}
