// Driver setup explains the existing Core contract; no weights are generated.
function renderDriverContract(){
 const box=document.getElementById('driverRequirementCards');if(!box)return;
 box.replaceChildren();
 const req=driverNeedsState,need=driverCurrentNeed();
 const text=(tag,value)=>{const el=document.createElement(tag);el.textContent=value;box.appendChild(el);return el};
 if(!req||!need){text('p','选择项目、规则和 Driver 后，查看必需字段及导出清单。');return}
 const names={MISSING_FILE:'缺少 Driver 文件',INVALID_FILE:'文件不可用',INVALID_HEADER:'表头不可用',MISSING_COLUMNS:'缺少必需字段',HEADER_ONLY:'只有表头，没有权重',SCHEMA_READY:'字段已准备，尚未验证计算',SCOPE_REVIEW:'需确认来源粒度',DIMENSION_REVIEW:'需明确业务维度',DATA_REVIEW:'需完整体检',UNUSED:'当前未使用'};
 text('strong',need.alias+' · '+(names[need.status]||'尚未检查'));
 text('p','当前引用文件：'+need.current_file);
 text('p','必需目标/过滤维度：'+((need.required_dimensions||[]).join('、')||'规则未要求固定目标列，仍需至少一个业务维度'));
 text('p','必需权重：'+need.weight_fields.join('、')+'；使用六位定点，超精度会阻断，不自动舍入。');
 if((need.missing_columns||[]).length)text('p','缺少字段：'+need.missing_columns.join('、'));
 text('p','来源匹配维度：'+((need.source_dimensions||[]).join('、')||'无')+'。只有在 Driver 提供这些列时，Core 才按这些来源坐标匹配权重。');
 if((need.broadcast_dimensions||[]).length)text('p','未提供的来源维度：'+need.broadcast_dimensions.join('、')+'。请确认是否允许跨期间、科目或其他来源池复用，不能默认认为正确。');
 for(const use of need.uses||[])text('p','使用步骤：'+use.stage+' / '+use.allocation+'；配置份额 '+use.share+'；负权重政策 '+use.negative_policy);
 text('p','下一步：'+(need.next_step||'准备真实权重后执行完整体检。'));
 if((need.uses||[]).length){
  const link=text('a','下载空表头 CSV 模板（不含模拟或真实权重）');
  const query=new URLSearchParams({name:req.project,template:req.template,alias:need.alias,download:'template',base_sha256:req.sha256});
  link.href='/api/multi/driver-needs?'+query.toString();
 }
 text('p','导出时请同时保留期间/场景/版本、来源池和目标粒度、权重单位、来源系统及快照说明。字段齐全仅表示结构准备，不等于权重正确或生产验收通过。');
}
