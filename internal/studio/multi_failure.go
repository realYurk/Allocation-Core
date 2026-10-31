package studio
// Only transfer authoritative Multi process diagnostics, never infer the reason
// using a project's mutable current rules after a run fails.
import ("allocationcore/internal/multi";"bytes";"encoding/json")
const multiFailureMarker="ALLOCATION_FAILURE_V1:"
func extractMultiFailure(raw []byte)(*multi.FailureDiagnosis,string){
 var d *multi.FailureDiagnosis
 var log bytes.Buffer
 for _,line:=range bytes.Split(raw,[]byte{'\n'}){
  if bytes.HasPrefix(line,[]byte(multiFailureMarker)){
   value:=bytes.TrimSpace(bytes.TrimPrefix(line,[]byte(multiFailureMarker)))
   if len(value)>0&&len(value)<=8192{
    var item multi.FailureDiagnosis
    if json.Unmarshal(value,&item)==nil&&item.GroupID!=""&&item.Code!=""&&len(item.NextSteps)>0{d=&item}
   }
   continue
  }
  if log.Len()>=12000{break}
  log.Write(line);log.WriteByte('\n')
 }
 return d,log.String()
}
