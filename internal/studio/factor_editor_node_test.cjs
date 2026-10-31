// Run with node --test; UI percentage calculation does not require a browser.
const test=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync(__dirname+'/public/factor_editor.js','utf8');
const ctx=vm.createContext({});
vm.runInContext(source,ctx);

test('percent conversion preserves six-decimal fractions exactly',()=>{
 for(const [share,percent] of [
  ['1','100'],['0','0'],['0.75','75'],['0.333333','33.3333'],
  ['0.000001','0.0001'],['0.000010','0.001']
 ]){
  assert.equal(ctx.financePercentText(ctx.financeShareUnits(share)),percent);
  assert.equal(ctx.financeUnitsShare(ctx.financePercentUnits(percent)),
   share.replace(/0+$/,'').replace(/\.$/,'')||'0');
 }
});
test('invalid or imprecise percentages are rejected, never rounded',()=>{
 for(const s of ['-1','101','12.12345','1e2','33,3','','.1','NaN'])
  assert.throws(()=>ctx.financePercentUnits(s));
});
test('33.3333 + 33.3333 + 33.3334 is exactly 100 percent',()=>{
 const result=['33.3333','33.3333','33.3334'].reduce((n,s)=>n+ctx.financePercentUnits(s),0);
 assert.equal(result,1000000);
 assert.equal(ctx.financeUnitsShare(result),'1');
});