package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"reflect"

	"github.com/openconfig/ygot/ygot"

	oopt "github.com/osrg/oopt/pkg/gnmi"
	"github.com/osrg/oopt/pkg/model"
)

var (
	current *model.PacketTransponder
	gitdir  = "/etc/oopt"
)

const (
	CONFIG_FILE = "config.json"
)

func main() {
	port := flag.Int64("port", 10164, "Listen port")
	flag.Parse()

	data, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", gitdir, CONFIG_FILE))
	if err != nil {
		panic(fmt.Sprintf("open: %v", err))
	}
	current = &model.PacketTransponder{}
	model.Unmarshal(data, current)

	servermodel := oopt.NewModel(
		oopt.ModelData,
		reflect.TypeOf((*model.Device)(nil)),
		model.SchemaTree["Device"],
		model.Unmarshal,
		model.ΛEnum,
	)

	d := &model.Device{
		PacketTransponder: current,
	}

	if err = d.Validate(); err != nil {
		panic(fmt.Sprintf("validation failed: %v", err))
	}

	json, err := ygot.EmitJSON(d, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
		Indent: "  ",
		RFC7951Config: &ygot.RFC7951JSONConfig{
			AppendModuleName: true,
		},
	})
	if err != nil {
		panic(fmt.Sprintf("EmitJSON failed: %v", err))
	}

	srv, err := oopt.NewServer(servermodel, []byte(json), *port, nil)
	if err != nil {
		panic(fmt.Sprintf("NewServer() failed: %v", err))
	}
	srv.Serve()
}
