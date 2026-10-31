package studio

import (
 "allocationcore/internal/multi"
 "errors"
 "net/http"
)

// A read-only endpoint: the Multi runner supplies the already-overridden,
// validated effective plan. No financial amounts, file contents or raw paths.
func (x *MultiApp) accountPlan(w http.ResponseWriter, r *http.Request) {
 if r.Method != http.MethodGet {w.WriteHeader(http.StatusMethodNotAllowed);return}
 if r.URL.Query().Get("account") == "" {
  fail(w,http.StatusBadRequest,errors.New("account is required"));return
 }
 x.mu.Lock()
 defer x.mu.Unlock()
 dir,err:=x.projectDir(r.URL.Query().Get("name"))
 if err!=nil{fail(w,http.StatusNotFound,err);return}
 view,err:=multi.ExplainAccount(dir,r.URL.Query().Get("account"))
 if err!=nil{fail(w,http.StatusUnprocessableEntity,err);return}
 jsonOut(w,http.StatusOK,view)
}
