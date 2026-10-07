package web

import (
	"bytes"
	_ "embed"
	"fmt"
)

//go:embed new_build.js
var newBuildUI []byte

func adaptNewBuild(data []byte) ([]byte, error) {
	start := bytes.Index(data, []byte("bb=function(e){"))
	end := bytes.Index(data, []byte("bb.defaultProps="))
	if start < 0 || end <= start || !bytes.Contains(data[start:end], []byte(`e.preventDefault(),a(d),r()`)) {
		return nil, fmt.Errorf("UI: embedded New Build form changed")
	}
	out := append([]byte{}, data[:start]...)
	out = append(out, []byte("bb=droneNewBuildForm;\n")...)
	out = append(out, newBuildUI...)
	data = append(out, data[end:]...)
	start = bytes.Index(data, []byte("M=g,P=Object(n.useCallback)("))
	end = bytes.Index(data, []byte("[c,r,E,C,x]);return w?null:"))
	if start < 0 || end <= start || !bytes.Contains(data[start:end], []byte("New build has started successfully")) {
		return nil, fmt.Errorf("UI: embedded New Build submit handler changed")
	}
	end += len("[c,r,E,C,x]);")
	out = append([]byte{}, data[:start]...)
	out = append(out, []byte(`droneNewBuildPending=n.useRef(false),droneNewBuildHide=function(){if(!droneNewBuildPending.current)g()},M=g,P=n.useCallback(droneNewBuildRequest(r,c,C,function(build){Pt(window.location.pathname,x,build)}),[c,r,C,x]);`)...)
	data = append(out, data[end:]...)
	old := []byte(`isShowing:O,hide:g,children:Object(He.jsx)(jb,{handleSubmit:P,handleCancel:g,target:h,parameters:j})`)
	if bytes.Count(data, old) != 1 {
		return nil, fmt.Errorf("UI: embedded New Build dialog changed")
	}
	return bytes.Replace(data, old, []byte(`isShowing:O,hide:droneNewBuildHide,children:Object(He.jsx)(jb,{handleSubmit:P,handleCancel:droneNewBuildHide,onBusyChange:function(busy){droneNewBuildPending.current=busy},defaultBranch:x.branch,target:h,parameters:j})`), 1), nil
}
