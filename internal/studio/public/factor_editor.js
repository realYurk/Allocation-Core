// Finance-friendly factor editor. UI percentage is exact 0.0001% units;
// server validates the final six-decimal shares using the authoritative engine.
function financeShareUnits(raw) {
 const v=String(raw==null||raw===''?'1':raw).trim();
 if(!/^(?:0|1)(?:\.\d{0,6})?$/.test(v))throw Error('规则原始比例格式错误：'+v);
 const [whole,frac='']=v.split('.');
 const n=Number(whole)*1000000+Number(frac.padEnd(6,'0')||0);
 if(n>1000000)throw Error('规则原始比例大于100%');
 return n;
}
function financePercentUnits(raw){
 const v=String(raw).trim().replace(/%$/,'').trim();
 if(!/^(?:0|[1-9]\d{0,2})(?:\.\d{1,4})?$/.test(v))throw Error('请输入 0～100 的百分比，最多四位小数，例如33.3333');
 const [whole,frac='']=v.split('.');
 const n=Number(whole)*10000+Number(frac.padEnd(4,'0')||0);
 if(n>1000000)throw Error('分摊比例不能超过100%');
 return n;
}
function financePercentText(n){
 const i=Math.floor(n/10000),f=String(n%10000).padStart(4,'0').replace(/0+$/,'');
 return String(i)+(f?'.'+f:'');
}
function financeUnitsShare(n){
 if(n===1000000)return '1';
 if(n===0)return '0';
 return '0.'+String(n).padStart(6,'0').replace(/0+$/,'');
}
function financeReadableError(value){
 const s=String(value??'');
 if(/driver denominator must be positive|no eligible drivers/i.test(s))return '当前科目和期间没有可用的正向分摊因子，请检查Driver数据、维度匹配和权重是否为零。';
 if(/allocation shares must total exactly 1|分摊比例合计/i.test(s))return '同一计算阶段的分摊比例必须合计100%，请逐项检查。';
 if(/negative driver requires ALLOW_SIGNED|negative driver without explicit permission/i.test(s))return '检测到负分摊因子。普通成本分摊默认不允许；确需冲销时，应在规则设计器登记业务用途。';
 if(/configuration changed|科目规则或因子配置已被修改/i.test(s))return '配置已被其他页面修改。请重新打开科目并核对，避免覆盖最新版本。';
 return '';
}
let financeFactorReadToken=0,financeFactorSaving=false;
function financeFactorElement(tag,text,cls){
 const e=document.createElement(tag);if(text!=null)e.textContent=text;if(cls)e.className=cls;return e;
}
async function financeFactorQuick(){
 const a=document.getElementById('quickAccount')?.value;
 if(!a)return message('请先选择科目');
 if(assignmentChanges().length)return message('当前有未保存的规则分派，请先保存再配置该科目因子');
 return editFactors(a);
}
async function financeFactorOpen(account){
 const project=active,token=++financeFactorReadToken;
 const panel=document.getElementById('factorEditor'),target=document.getElementById('factorRows');
 panel.style.display='block';
 target.replaceChildren(financeFactorElement('p','正在读取已保存的因子配置…','muted'));
 document.getElementById('factorSaveButton').disabled=true;
 try{
  const v=await api('/api/multi/factor?name='+encodeURIComponent(project)+'&account='+encodeURIComponent(account));
  if(token!==financeFactorReadToken||project!==active)return;
  if(!v.versions?.assignments||!v.versions?.template||!v.versions?.overrides)throw Error('后端缺少配置版本，请使用最新程序');
  factorContext={...v,project};
  document.getElementById('factorTitle').textContent='独立因子配置 · '+account;
  target.replaceChildren();
  const drivers=Object.keys(v.rule?.datasets?.drivers||{}).sort();
  for(const st of v.rule?.stages||[]){
   if(st.enabled===false||(st.operation&&st.operation!=='ALLOCATE'))continue;
   const block=financeFactorElement('section',null,'finance-factor-stage');block.dataset.stage=st.id;
   block.appendChild(financeFactorElement('h3','计算阶段：'+st.id));
   for(const a of st.allocations||[]){
    const old=(v.overrides||[]).find(x=>x.stage===st.id&&x.allocation===a.id)||{};
    const row=financeFactorElement('div',null,'finance-factor-line');
    row.dataset.stage=st.id;row.dataset.allocation=a.id;row.dataset.enabled=String(a.enabled!==false);
    const name=financeFactorElement('label',a.id,'finance-factor-name');
    const driver=financeFactorElement('select');driver.dataset.key='driver_dataset';
    for(const d of drivers){const op=financeFactorElement('option',d);op.value=d;driver.appendChild(op)}
    driver.value=old.driver_dataset||a.driver_dataset||'';
    driver.setAttribute('aria-label',a.id+' Driver 数据集');
    const field=financeFactorElement('input');field.dataset.key='weight_field';
    field.value=old.weight_field||a.weight_field||'';field.placeholder='权重字段';
    field.setAttribute('aria-label',a.id+' 权重字段');
    const percent=financeFactorElement('input');percent.dataset.key='percent';
    percent.setAttribute('inputmode','decimal');percent.setAttribute('aria-label',a.id+' 分摊比例百分比');
    percent.value=financePercentText(financeShareUnits(old.share!==''&&old.share!=null?old.share:a.share));
    const percentBox=financeFactorElement('label',null,'finance-factor-percent');
    percentBox.append(percent,financeFactorElement('span','%'));
    row.append(name,driver,field,percentBox);
    if(a.enabled===false){
     driver.disabled=field.disabled=percent.disabled=true;
     row.append(financeFactorElement('small','已停用，不计入100%','muted'));
    }else for(const control of [driver,field,percent])control.addEventListener('input',financeFactorValidate);
    block.appendChild(row);
   }
   block.appendChild(financeFactorElement('p','', 'finance-factor-total'));
   target.appendChild(block);
  }
  if(!target.children.length)target.appendChild(financeFactorElement('p','此规则没有可调整的分摊阶段','muted'));
  financeFactorValidate();panel.scrollIntoView({behavior:'smooth',block:'nearest'});
 }catch(e){
  if(token!==financeFactorReadToken)return;
  target.replaceChildren(financeFactorElement('p','无法读取因子配置：'+e.message,'bad'));message(e.message);
 }
}
function financeFactorCollect(){
 if(!factorContext||factorContext.project!==active)throw Error('项目已变化，请重新打开因子编辑');
 let valid=true;const problems=[],rows=[];
 const stages=document.querySelectorAll('#factorRows .finance-factor-stage');
 for(const stage of stages){
  let total=0,enabled=0;
  for(const line of stage.querySelectorAll('.finance-factor-line')){
   const get=k=>line.querySelector('[data-key="'+k+'"]').value.trim();
   let units;
   try{units=financePercentUnits(get('percent'))}
   catch(e){valid=false;problems.push(stage.dataset.stage+' / '+line.dataset.allocation+'：'+e.message);continue}
   if(line.dataset.enabled==='true'){
    total+=units;enabled++;
    if(!get('driver_dataset')||!get('weight_field')){
     valid=false;problems.push(stage.dataset.stage+' / '+line.dataset.allocation+'：Driver 或权重字段不能为空');
    }
   }
   rows.push({account:factorContext.account,stage:stage.dataset.stage,allocation:line.dataset.allocation,
    driver_dataset:get('driver_dataset'),weight_field:get('weight_field'),share:financeUnitsShare(units)});
  }
  const line=stage.querySelector('.finance-factor-total');
  line.textContent='本阶段已启用比例合计 '+financePercentText(total)+'%（要求100%）';
  line.className='finance-factor-total '+(total===1000000&&enabled?'good':'bad');
  if(!enabled||total!==1000000){
   valid=false;problems.push(stage.dataset.stage+'：合计'+financePercentText(total)+'%，必须恰好100%');
  }
 }
 if(!stages.length){valid=false;problems.push('没有可配置的计算阶段')}
 return {valid,problems,rows};
}
function financeFactorValidate(){
 const node=document.getElementById('factorValidation');
 try{
  const v=financeFactorCollect();
  node.textContent=v.valid?'比例校验通过。保存后仍须输入体检、计算和独立对账。':v.problems.join('\n');
  node.className=v.valid?'good':'bad';
  document.getElementById('factorSaveButton').disabled=!v.valid||financeFactorSaving;
  return v;
 }catch(e){node.textContent=e.message;node.className='bad';document.getElementById('factorSaveButton').disabled=true;
  return {valid:false,problems:[e.message],rows:[]};
 }
}
async function financeFactorSave(){
 const check=financeFactorValidate();
 if(!check.valid||financeFactorSaving)return;
 const ctx=factorContext;
 if(!ctx||ctx.project!==active)return message('项目已变化，请重新打开因子配置');
 financeFactorSaving=true;document.getElementById('factorSaveButton').disabled=true;
 try{
  await api('/api/multi/factor',{name:active,account:ctx.account,versions:ctx.versions,rows:check.rows});
  resetRuleExplanation();
  document.getElementById('inspectResult').textContent='因子已修改，必须重新体检。';
  await financeFactorOpen(ctx.account);
  message('因子比例已保存。请重新体检、执行分摊，并与独立参考结果对账。',true);
 }catch(e){message(e.message)}
 finally{financeFactorSaving=false;financeFactorValidate()}
}
