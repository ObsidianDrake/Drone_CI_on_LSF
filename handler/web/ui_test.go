package web

import (
	"bytes"
	"github.com/drone/drone-ui/dist"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestUIBasePath(t *testing.T) {
	for _, base := range []string{"", "/drone", "/tools/ci"} {
		t.Run(base, func(t *testing.T) {
			u, err := newUIAssets(base)
			if err != nil {
				t.Fatal(err)
			}
			if base == "" {
				if !bytes.Contains(u.index, []byte(`src="`+u.main+`?v=`)) {
					t.Fatal("root UI missing adapted bundle version")
				}
				return
			}
			if bytes.Contains(u.index, []byte(`src="/static/`)) || bytes.Contains(u.index, []byte(`href="/static/`)) {
				t.Fatal("root-relative assets remain")
			}
			if !bytes.Contains(u.index, []byte(`src="`+base+`/static/`)) {
				t.Fatal("missing prefixed asset")
			}
			file, err := u.Open(u.main)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil {
				t.Fatal(err)
			}
			info, _ := file.Stat()
			if info.Size() != int64(len(data)) {
				t.Fatal("incorrect content length")
			}
			if !bytes.Contains(data, []byte(`basename:"`+base+`"`)) {
				t.Fatal("missing router basename")
			}
			if bytes.Contains(data, []byte(`href:"/login"`)) || bytes.Contains(data, []byte(`href:"/logout"`)) {
				t.Fatal("root auth links remain")
			}
			w := httptest.NewRecorder()
			handleLogout(u.index).ServeHTTP(w, httptest.NewRequest("GET", "/logout", nil))
			if !strings.Contains(w.Body.String(), base+"/static/") {
				t.Fatal("logout uses original UI")
			}
			w = httptest.NewRecorder()
			http.FileServer(u).ServeHTTP(w, httptest.NewRequest("GET", u.main, nil))
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
				t.Fatalf("serving transformed asset: %d", w.Code)
			}
		})
	}
}

func TestUIUpgradeFailsClosed(t *testing.T) {
	if _, err := adaptMain([]byte("incompatible bundle"), "/drone"); err == nil {
		t.Fatal("accepted unknown UI")
	}
}

func TestPromoteRemoved(t *testing.T) {
	for _, base := range []string{"", "/drone"} {
		u, err := newUIAssets(base)
		if err != nil {
			t.Fatal(err)
		}
		f, err := u.Open(u.main)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, removed := range []string{`content:"Promote"`, `content:"Debug"`, `label:"Deployments"`, `path:"/:namespace/:name/deployments"`, `id:"build-actions"`, `label:"Promote"`, `Ps(!!R.get("target"))`} {
			if bytes.Contains(data, []byte(removed)) {
				t.Fatalf("base %q still includes %s", base, removed)
			}
		}
		for _, kept := range []string{`userIsAdmin:!!(a&&a.admin)`, `e.userIsAdmin&&Object(He.jsx)(Ye`, `s("debug")},children:"Debug"`, `className:pu("cancel-button"),style:{width:88,boxSizing:"border-box"},onClick:function(){s("restart")},children:"Restart"`, `className:pu("controls"),children:Object(He.jsxs)("span",{style:{display:"inline-flex",gap:8}`} {
			if !bytes.Contains(data, []byte(kept)) {
				t.Fatalf("missing %s", kept)
			}
		}
	}
}

func TestTimeoutSaveValidation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for UI event validation")
	}
	u, err := newUIAssets("/drone")
	if err != nil {
		t.Fatal(err)
	}
	data, err := adaptMain(dist.MustLookup(u.main), "/drone")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`onClick:(function\(\)\{if\(!/\^\[0-9\]\+\$/.*?\}),children:"Save Changes"`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("missing Save Changes validation handler")
	}
	script := `const save=` + string(match[1]) + `;
 let v, calls=0, errors=0; function w(){calls++} function p(){errors++}
 for(const input of ["", " ", "abc", "1.5", "0", "-1", "1e2", "2562048", null, undefined]) {
 v={timeout_hours:input};calls=errors=0;save();if(calls!==0||errors!==1)throw new Error("invalid accepted: "+input)
 }
 for(const input of [1,"1","12","2562047"]) {
 v={timeout_hours:input};calls=errors=0;save();if(calls!==1||errors!==0)throw new Error("valid rejected: "+input)
 }`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}
