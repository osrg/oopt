package sonic

import (
	"github.com/d4l3k/messagediff"
)

type DiffType int

const (
	DiffAdded DiffType = iota
	DiffDeleted
	DiffModified
)

type DiffTask struct {
	Type  DiffType
	Path  messagediff.Path
	Value interface{}
}
