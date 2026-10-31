package studio

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

// Assignment editing is a business decision: stale browser tabs must fail
// closed instead of overwriting another user's saved rule mapping.
func TestMultiAssignmentOptimisticEdit(t *testing.T) {
    root := t.TempDir()
    dir := filepath.Join(root, "finance")
    if err := os.MkdirAll(filepath.Join(dir, "templates"), 0700); err != nil { t.Fatal(err) }
    initial := []byte("account,template\nA,first.json\nB,first.json\n")
    write := func(name string, data []byte) {
        t.Helper()
        if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil { t.Fatal(err) }
    }
    write("assignments.csv",initial)
    for _, n := range []string{"first.json","second.json"} {
        if err := os.WriteFile(filepath.Join(dir,"templates",n), []byte("{}"),0600); err != nil {t.Fatal(err)}
    }
    app := &MultiApp{root:root,busy:map[string]bool{}}
    get := httptest.NewRecorder()
    app.project(get,httptest.NewRequest(http.MethodGet,"/api/multi/project?name=finance",nil))
    if get.Code!=200 {t.Fatalf("read project: %d: %s",get.Code,get.Body.String())}
    var project struct{ AssignmentsSHA256 string `json:"assignments_sha256"` }
    if err:=json.Unmarshal(get.Body.Bytes(),&project);err!=nil {t.Fatal(err)}
    sum:=sha256.Sum256(initial)
    if project.AssignmentsSHA256!=hex.EncodeToString(sum[:]) {t.Fatal("project hash differs from disk")}
    post := func(token,template string) *httptest.ResponseRecorder {
        t.Helper()
        body,_:=json.Marshal(map[string]any{"name":"finance","base_sha256":token,
            "updates":[]map[string]string{{"account":"A","template":template}}})
        rec:=httptest.NewRecorder()
        app.assign(rec,httptest.NewRequest(http.MethodPost,"/api/multi/assign",strings.NewReader(string(body))))
        return rec
    }
    if rec:=post(project.AssignmentsSHA256,"second.json");rec.Code!=200 {
        t.Fatalf("first write: %d: %s",rec.Code,rec.Body.String())
    }
    changed,err:=os.ReadFile(filepath.Join(dir,"assignments.csv"))
    if err!=nil {t.Fatal(err)}
    if !strings.Contains(string(changed),"A,second.json") {t.Fatalf("update missing: %s",changed)}
    if rec:=post(project.AssignmentsSHA256,"first.json");rec.Code!=http.StatusConflict {
        t.Fatalf("stale write should return 409: %d: %s",rec.Code,rec.Body.String())
    }
    if rec:=post("","first.json");rec.Code!=http.StatusConflict {
        t.Fatalf("unversioned write should return 409: %d: %s",rec.Code,rec.Body.String())
    }
    after,err:=os.ReadFile(filepath.Join(dir,"assignments.csv"))
    if err!=nil {t.Fatal(err)}
    if string(after)!=string(changed) {t.Fatal("conflict modified assignments")}
    nextHash:=sha256.Sum256(after)
    if rec:=post(hex.EncodeToString(nextHash[:]),"missing.json");rec.Code!=422 {
        t.Fatalf("unknown template should fail validation: %d: %s",rec.Code,rec.Body.String())
    }
    after,err=os.ReadFile(filepath.Join(dir,"assignments.csv"))
    if err!=nil {t.Fatal(err)}
    if string(after)!=string(changed) {t.Fatal("invalid template modified assignments")}
}
