package sonic

import (
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"strings"
)

type DiffType int

type PathElems []*gnmipb.PathElem

func (m PathElems) String() string {
	p := []*gnmipb.PathElem(m)
	ss := make([]string, 0, len(p))
	for _, e := range p {
		n := e.GetName()
		if n != "config" {
			ss = append(ss, n)
		}
	}
	return strings.Join(ss, ".")
}

const (
	DiffAdded DiffType = iota
	DiffDeleted
	DiffModified
)

type DiffTask struct {
	Type  DiffType
	Path  PathElems
	Value *gnmipb.TypedValue
}
