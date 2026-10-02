package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/drone/drone-ui/dist"
)

// The UI is distributed as an embedded, precompiled Go dependency. Adapt only
// its application bundle, never third-party JS or browser APIs. Each patch is
// checked against the pinned drone-ui v2.12.0 bundle; an upstream change fails
// at startup instead of silently shipping broken routing. See docs/base-path.md.
type uiAssets struct {
	base  string
	index []byte
	main  string
	cache sync.Map
}

var mainBundle = regexp.MustCompile(`src="(/static/js/main\.[^"]+\.js)"`)
var rootAttribute = regexp.MustCompile(`(href|src)="/([^/])`)

func newUIAssets(base string) (*uiAssets, error) {
	u := &uiAssets{base: base, index: dist.MustLookup("/index.html")}
	match := mainBundle.FindSubmatch(u.index)
	if len(match) != 2 {
		return nil, fmt.Errorf("base path: cannot identify embedded UI application bundle")
	}
	u.main = string(match[1])
	main, err := adaptMain(dist.MustLookup(u.main), base)
	if err != nil {
		return nil, err
	}
	u.cache.Store(u.main, main)
	// This embedded filename hashes the upstream bundle, not our adapted bytes.
	// Version its URL so browsers do not reuse a year-cached older adaptation.
	version := fmt.Sprintf("%x", sha256.Sum256(main))
	u.index = bytes.Replace(u.index, []byte(`src="`+u.main+`"`), []byte(`src="`+u.main+`?v=`+version+`"`), 1)
	if base == "" {
		return u, nil
	}
	u.index = rootAttribute.ReplaceAll(u.index, []byte(`${1}="`+base+`/${2}`))
	if bytes.Count(u.index, []byte(`i.p="/"`)) != 1 {
		return nil, fmt.Errorf("base path: embedded UI webpack public path changed")
	}
	public, _ := json.Marshal(base + "/")
	u.index = bytes.Replace(u.index, []byte(`i.p="/"`), []byte(`i.p=`+string(public)), 1)
	return u, nil
}

func adaptMain(data []byte, base string) ([]byte, error) {
	features := []struct{ old, replacement string }{
		{`{data:_,userIsAdminOrHasWritePerm:r,view:M}`, `{data:_,userIsAdminOrHasWritePerm:r,userIsAdmin:!!(a&&a.admin),view:M}`},
		{`{to:"/".concat(a,"/").concat(n,"/deployments"),exact:!0,label:"Deployments",tag:t?"span":l.c},`, ``},
		{`Object(He.jsx)(dh.Deployments,{path:"/:namespace/:name/deployments",componentProps:{user:t,repo:x},visibility:S,exact:!0}),`, ``},
		{`g=null;return g=s?null:c.length?`, `g=null;if(r.isError||(!s&&!Array.isArray(c)))return Object(He.jsxs)("div",{role:"alert",children:[Object(He.jsx)("p",{children:"Unable to load Secrets. "+(r.isError&&r.isError.message==="Not Implemented"?"This feature is unavailable in the current Drone OSS build.":r.isError&&r.isError.message||"Check your login and repository permissions.")}),Object(He.jsx)(Ye,{onClick:function(){l()},children:"Retry"})]});return g=s?null:c.length?`},
		{`_=null;return _=h?null:p.length?`, `_=null;if(u.isError||(!h&&!Array.isArray(p)))return Object(He.jsxs)("div",{role:"alert",children:[Object(He.jsx)("p",{children:"Unable to load Cron Jobs. "+(u.isError&&u.isError.message==="Not Implemented"?"This feature is unavailable in the current Drone OSS build.":u.isError&&u.isError.message||"Check your login and repository permissions.")}),Object(He.jsx)(Ye,{onClick:function(){j()},children:"Retry"})]});return _=h?null:p.length?`},
		{`"ignore_pull_requests","ignore_forks","protected","trusted","visibility"`, `"ignore_pull_requests","ignore_forks","protected","trusted","lsf_job_info_disabled","visibility"`},
		{`case"trusted":case"auto_cancel_running":`, `case"trusted":case"lsf_job_info_disabled":case"auto_cancel_running":`},
		{`Object(He.jsxs)("div",{className:rp("switch-row"),children:[Object(He.jsx)(pd,{id:"trusted",checked:v.trusted,onChange:y("trusted"),children:"Trusted"}),Object(He.jsx)("p",{className:rp("note"),children:"Enables privileged container settings."})]})`, `Object(He.jsxs)("div",{className:rp("switch-row"),children:[Object(He.jsx)(pd,{id:"trusted",checked:v.trusted,onChange:y("trusted"),children:"Trusted"}),Object(He.jsx)("p",{className:rp("note"),children:"Enables privileged container settings."})]}),Object(He.jsxs)("div",{className:rp("switch-row"),children:[Object(He.jsx)(pd,{id:"lsf_job_info_disabled",checked:!v.lsf_job_info_disabled,onChange:y("lsf_job_info_disabled"),children:"Show LSF job information"}),Object(He.jsx)("p",{className:rp("note"),children:"Append scheduler.out after each LSF step finishes."})]})`},
		{`{value:"promote",content:"Promote"},`, ``},
		{`,{value:"debug",content:"Debug"}`, ``},
		{`Object(He.jsx)(su,{id:"build-actions",className:pu("controls"),menuItems:[{value:"restart",content:"Restart"}],menuAlignment:"right",onMenuItemSelect:s})`, `Object(He.jsx)("div",{className:pu("controls"),children:Object(He.jsxs)("span",{style:{display:"inline-flex",gap:8},children:[Object(He.jsx)(Ye,{className:pu("cancel-button"),style:{width:88,boxSizing:"border-box"},onClick:function(){s("restart")},children:"Restart"}),e.userIsAdmin&&Object(He.jsx)(Ye,{className:pu("cancel-button"),style:{width:88,boxSizing:"border-box"},title:"Run again and retain the LSF working directory",onClick:function(){s("debug")},children:"Debug"})]})})`},
		{`Object(He.jsx)(oi.Radio,{id:"promote",name:"promote",label:"Promote",value:"promote",checked:"promote"===d.action,onChange:O("action")}),`, ``},
		{`Ps(!!R.get("target"))`, `Ps("rollback"===D&&!!R.get("target"))`},
	}

	features = append(features, []struct{ old, replacement string }{
		{`a&&O(rt(a)(["ignore_pull_requests","ignore_forks","protected","trusted","lsf_job_info_disabled","visibility","timeout","config_path","auto_cancel_pull_requests","auto_cancel_pushes","auto_cancel_running"]))`, `a&&O(Object.assign(rt(a)(["ignore_pull_requests","ignore_forks","protected","trusted","lsf_job_info_disabled","visibility","timeout","config_path","auto_cancel_pull_requests","auto_cancel_pushes","auto_cancel_running"]),{timeout_hours:a.timeout>0?a.timeout/60:1,next_build_number:a.counter+1}))`},
		{`case"timeout":`, `case"next_build_number":case"timeout_hours":case"timeout":`},
		{`Object(He.jsx)(oi.Select,{label:"Timeout",value:v.timeout,optionsList:(_=g,_.map((function(e){return{value:e,key:e>90?"".concat(e/60," hours"):"".concat(e," minutes")}}))),width:200,className:rp("timeout"),onChange:y("timeout")})`, `Object(He.jsx)(oi.Input,{label:"Timeout (hours)",name:"timeout_hours",type:"number",min:1,max:2562047,step:1,value:v.timeout_hours,width:200,className:rp("timeout"),disabled:!(t&&t.admin),onChange:y("timeout_hours")})`},
		{`timeout:+v.timeout`, `timeout:void 0,timeout_hours:Number(v.timeout_hours),next_build_number:t&&t.admin&&a.active&&a.next_build_number_editable&&Number(v.next_build_number)!==a.counter+1?Number(v.next_build_number):void 0`},
		{`onClick:w,children:"Save Changes"`, `onClick:function(){if(!/^[0-9]+$/.test(String(v.timeout_hours))||!Number.isSafeInteger(Number(v.timeout_hours))||Number(v.timeout_hours)<1||Number(v.timeout_hours)>2562047){p("Timeout must be a whole number of hours between 1 and 2562047.");return}if(t&&t.admin&&a.active&&a.next_build_number_editable&&(!/^[0-9]+$/.test(String(v.next_build_number))||!Number.isSafeInteger(Number(v.next_build_number))||Number(v.next_build_number)<1||Number(v.next_build_number)>2147483647)){p("Next build number must be a whole number between 1 and 2147483647.");return}w()},children:"Save Changes"`},
		{`Object(He.jsxs)(li,{className:rp("form-section-row","form-section-row-is-last"),children:[`, `Object(He.jsxs)(li,{className:rp("form-section-row"),title:"Build numbering",children:[Object(He.jsx)(oi.Input,{label:"Next Build Number",name:"next_build_number",type:"number",min:1,max:2147483647,step:1,value:v.next_build_number,width:200,className:rp("timeout"),disabled:!(t&&t.admin&&a.active&&a.next_build_number_editable),onChange:y("next_build_number")}),Object(He.jsx)("p",{style:{fontSize:13,lineHeight:"20px",marginTop:8,color:"var(--color-summary)"},children:"Only Drone administrators can change this for an active repository with no build records. To reset numbering, delete all build records first. For migration, enter the old server's last build number plus one."})]}),Object(He.jsxs)(li,{className:rp("form-section-row","form-section-row-is-last"),children:[`},
	}...)
	for _, patch := range features {
		if bytes.Count(data, []byte(patch.old)) != 1 {
			return nil, fmt.Errorf("UI: embedded drone-ui compatibility check failed for %q", patch.old)
		}
		data = bytes.Replace(data, []byte(patch.old), []byte(patch.replacement), 1)
	}
	var historyErr error
	data, historyErr = adaptHistory(data)
	if historyErr == nil {
		data, historyErr = adaptTrends(data)
	}
	if historyErr != nil {
		return nil, historyErr
	}
	if base == "" {
		return data, nil
	}
	quoted, _ := json.Marshal(base)
	b := string(quoted)
	patches := []struct {
		old, new string
		count    int
	}{
		{`children:Object(He.jsx)(l.a,{children:Object(He.jsx)(ph,{})})`, `children:Object(He.jsx)(l.a,{basename:` + b + `,children:Object(He.jsx)(ph,{})})`, 1},
		{`.concat(window.location.host)`, `.concat(window.location.host,` + b + `)`, 1},
		{`window.location.pathname`, `(window.location.pathname.slice(` + fmt.Sprint(len(base)) + `)||"/")`, 2},
		{`href:"/login"`, `href:` + b + `+"/login"`, 1},
		{`href:"/logout"`, `href:` + b + `+"/logout"`, 1},
		{`Object(He.jsx)("a",{href:"/".concat`, `Object(He.jsx)("a",{href:(` + b + `+"/").concat`, 1},
	}
	for _, p := range patches {
		if bytes.Count(data, []byte(p.old)) != p.count {
			return nil, fmt.Errorf("base path: embedded drone-ui v2.12.0 compatibility check failed for %q", p.old)
		}
		data = bytes.ReplaceAll(data, []byte(p.old), []byte(p.new))
	}
	return data, nil
}

func (u *uiAssets) Open(name string) (http.File, error) {
	f, err := dist.New().Open(name)
	if err != nil {
		return f, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		return f, nil
	}
	if cached, ok := u.cache.Load(name); ok {
		f.Close()
		return memoryAsset(cached.([]byte), info), nil
	}
	if u.base == "" {
		return f, nil
	}
	if name != "/manifest.json" && name != "/asset-manifest.json" && !strings.HasSuffix(name, ".css") {
		return f, nil
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(name, ".css") {
		for _, prefix := range []string{"url(/", `url("/`, `url('/`} {
			// Only root-local assets, not protocol-relative external URLs.
			data = bytes.ReplaceAll(data, []byte(prefix+"static/"), []byte(prefix+strings.TrimPrefix(u.base, "/")+"/static/"))
		}
	} else {
		var value interface{}
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		data, err = json.Marshal(prefixJSON(value, u.base))
		if err != nil {
			return nil, err
		}
	}
	u.cache.Store(name, data)
	return memoryAsset(data, info), nil
}

func prefixJSON(value interface{}, base string) interface{} {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
			return base + v
		}
		return v
	case []interface{}:
		for i, entry := range v {
			v[i] = prefixJSON(entry, base)
		}
	case map[string]interface{}:
		for key, entry := range v {
			v[key] = prefixJSON(entry, base)
		}
	}
	return value
}

type assetFile struct {
	*bytes.Reader
	info os.FileInfo
}
type assetInfo struct {
	os.FileInfo
	size int64
}

func (s assetInfo) Size() int64 { return s.size }
func memoryAsset(data []byte, info os.FileInfo) *assetFile {
	return &assetFile{bytes.NewReader(data), assetInfo{info, int64(len(data))}}
}
func (f *assetFile) Close() error                       { return nil }
func (f *assetFile) Stat() (os.FileInfo, error)         { return f.info, nil }
func (f *assetFile) Readdir(int) ([]os.FileInfo, error) { return nil, fmt.Errorf("not a directory") }
