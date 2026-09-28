package core

import (
	"context"
	"errors"
)

// HistoryFilter selects explicit builds, a repository prefix, or non-success
// builds across the repository when Numbers and Before are omitted.
type HistoryFilter struct {
	Numbers    []int64 `json:"numbers,omitempty"`
	Before     int64   `json:"before,omitempty"`
	NonSuccess bool    `json:"non_success,omitempty"`
}

type HistoryPreview struct {
	Numbers []int64 `json:"numbers"`
	Token   string  `json:"token"`
	StepIDs []int64 `json:"-"`
}

var ErrHistoryChanged = errors.New("Build history changed. Preview and confirm the selection again.")

// HistoryStore is separate from BuildStore to preserve compatibility with other stores.
// An empty token previews only; a matching token commits an atomic database deletion.
type HistoryStore interface {
	DeleteHistory(context.Context, int64, HistoryFilter, string) (*HistoryPreview, error)
}
