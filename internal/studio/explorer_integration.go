package studio

// Generic Explorer companion is served through the SAME loopback Studio host.
// This gives users a single workspace and avoids cross-origin upload failures.
import (
	"allocationcore/internal/explorer"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func startExplorerCompanion(home, data string) (http.Handler, func(), error) {
	dir := filepath.Join(data, "cube-explorer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, nil, err
	}
	handler, err := explorer.Handler(dir)
	if err != nil {
		return nil, nil, err
	}
	return http.StripPrefix("/explorer", handler), func() {}, nil
}
func (a *Application) workspace(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/workspace" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Allocation Workbench / 通用分摊工作台</title><style>*{box-sizing:border-box}body{margin:0;font:14px system-ui,Arial,sans-serif;color:#17304d;background:#f4f7fc}header{height:67px;background:#fff;border-bottom:1px solid #dbe5f2;display:flex;align-items:center;justify-content:space-between;padding:0 22px}strong{font-size:19px}button,a{border:1px solid #c5d8ee;padding:10px 15px;border-radius:8px;text-decoration:none;background:white;color:#174b96;cursor:pointer;font:inherit;margin-right:8px}button.active{background:#175cd3;color:#fff}iframe{border:0;width:100%;height:calc(100vh - 68px)}small{color:#64748b}@media(max-width:900px){small{display:none}header{padding:10px;height:auto;flex-wrap:wrap}iframe{height:calc(100vh - 120px)}}</style><header><div><strong>Allocation Workbench / 分摊工作台</strong><div><small>Generic rules · Calculation · Cube comparison / 规则 · 计算 · 数据对比</small></div></div><nav><button id="multi" class="active" onclick="show('multi')">① 规则与执行 / Run</button><button id="cube" onclick="show('cube')">② Cube 对比 / Compare</button><a href="/rules">③ 规则转换 / Rule Kit</a></nav></header><iframe id="view" title="Workbench" src="/multi"></iframe><script>function show(x){document.getElementById('view').src=x==='cube'?'/explorer/':'/multi';document.getElementById('cube').className=x==='cube'?'active':'';document.getElementById('multi').className=x==='multi'?'active':''}if(location.hash==='#compare')show('cube')</script></html>`))
}
func (a *Application) ruleKitPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/rules" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Rule Kit / 通用规则转换</title><style>body{font:15px system-ui,sans-serif;max-width:960px;margin:48px auto;color:#23334f;line-height:1.8}code,pre{background:#edf3ff;padding:3px 7px}a{color:#1859c7}</style><a href="/workspace">← Workbench / 返回工作台</a><h1>Rule Kit / 通用规则转换</h1><p>Use two generic files: <code>rule_meta.json</code> (dimensions, datasets, precision) and <code>stages.csv</code> (stage, inputs, filters, targets, weights). Use <code>AllocationWorkbench.exe rule</code> to produce a Core <code>rule.json</code>.</p><p>使用两个通用文件：<code>rule_meta.json</code>（维度、数据集、精度）与 <code>stages.csv</code>（阶段、输入、筛选、目标、因子），运行独立规则转换器生成 <code>rule.json</code>。</p><pre>AllocationWorkbench.exe rule --meta rule_meta.json --stages stages.csv --out rule.json
AllocationWorkbench.exe core validate rule.json</pre><p><b>Safety / 安全：</b>No business assumptions. Conversion checks fractions, driver names and dimensions; Core validation is mandatory. Do not upload business rules to GitHub. / 不推断任何企业业务；转换会检查比例、因子与维度，仍必须通过 Core 语义校验。企业规则不得上传 GitHub。</p><p>See / 详见 <code>docs/VNext8_GENERIC_RULES_AND_OPERATIONS_BILINGUAL.md</code>.</p></html>`))
}
func explorerUpload(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/explorer/api/upload") && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;")
}
