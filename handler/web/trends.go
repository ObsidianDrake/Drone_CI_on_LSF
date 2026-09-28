package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed trends.js
var trendsUI []byte

//go:embed trends.css
var trendsCSS string

func adaptTrends(data []byte) ([]byte, error) {
	patches := []struct{ old, new string }{
		{`Object(He.jsx)(dh.Home,{path:"/",componentProps:{user:a},visibility:L,exact:!0}),`, `Object(He.jsx)(dh.Home,{path:"/",componentProps:{user:a},visibility:L,exact:!0}),Object(He.jsx)(uh,{path:"/monitor",component:DroneTrends,componentProps:{user:a},visibility:L,exact:!0}),`},
		{`m&&Object(He.jsx)(Ye,{className:wt("sidebar-item","search-btn")`, `m&&Object(He.jsx)(l.c,{to:"/monitor",title:"Workload trends","aria-label":"Workload trends",className:wt("sidebar-item"),activeClassName:wt("sidebar-item-active"),children:Object(He.jsx)(DroneTrendIcon,{})}),m&&Object(He.jsx)(Ye,{className:wt("sidebar-item","search-btn")`},
	}
	for _, p := range patches {
		if bytes.Count(data, []byte(p.old)) != 1 {
			return nil, fmt.Errorf("UI: workload trends compatibility check failed")
		}
		data = bytes.Replace(data, []byte(p.old), []byte(p.new), 1)
	}
	anchor := []byte("function ph(){")
	if bytes.Count(data, anchor) != 1 {
		return nil, fmt.Errorf("UI: workload trends root changed")
	}
	css, _ := json.Marshal(trendsCSS)
	extra := append([]byte("var droneTrendsCSS="+string(css)+";\n"), trendsUI...)
	extra = append(extra, anchor...)
	return bytes.Replace(data, anchor, extra, 1), nil
}
