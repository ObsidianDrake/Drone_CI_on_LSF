package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed history.js
var historyUI []byte

//go:embed history.css
var historyCSS string

func adaptHistory(data []byte) ([]byte, error) {
	start := bytes.Index(data, []byte("function qu(e){"))
	end := bytes.Index(data, []byte("qu.propTypes="))
	if start < 0 || end <= start || !bytes.Contains(data[start:end], []byte("Your Build List is Empty.")) {
		return nil, fmt.Errorf("UI: embedded Builds component changed")
	}
	out := make([]byte, 0, len(data)+len(historyUI))
	out = append(out, data[:start]...)
	css, _ := json.Marshal(historyCSS)
	out = append(out, []byte("var droneHistoryCSS = "+string(css)+";\n")...)
	out = append(out, historyUI...)
	out = append(out, data[end:]...)
	return out, nil
}
