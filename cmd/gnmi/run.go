package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"

	"github.com/openconfig/ygot/ygot"

	oopt "github.com/osrg/oopt/pkg/gnmi"
	"github.com/osrg/oopt/pkg/model"
)

var (
	current *model.PacketTransponder
	git_dir = "/etc/oopt"
)

const (
	CONFIG_FILE = "config.json"
)

func callback(newConfig ygot.ValidatedGoStruct) error {
	buf, err := ygot.EmitJSON(newConfig, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
	})
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	file, err := os.Create(fmt.Sprintf("%s/%s", git_dir, CONFIG_FILE))
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	defer file.Close()
	file.Write(([]byte)(buf))
	file.Write([]byte("\n"))

	return nil
}

func main() {
	port := flag.Int64("port", 10164, "Listen port")
	flag.Parse()

	data, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", git_dir, CONFIG_FILE))
	if err != nil {
		panic(fmt.Sprintf("open: %v", err))
	}
	current = &model.PacketTransponder{}
	model.Unmarshal(data, current)

	servermodel := oopt.NewModel(
		oopt.ModelData,
		reflect.TypeOf((*model.PacketTransponder)(nil)),
		model.SchemaTree["PacketTransponder"],
		model.Unmarshal,
		model.ΛEnum,
	)

	if err = current.Validate(); err != nil {
		panic(fmt.Sprintf("validation failed: %v", err))
	}

	json, err := ygot.EmitJSON(current, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
		Indent: "  ",
		RFC7951Config: &ygot.RFC7951JSONConfig{
			AppendModuleName: true,
		},
	})
	if err != nil {
		panic(fmt.Sprintf("EmitJSON failed: %v", err))
	}

	srv, err := oopt.NewServer(servermodel, []byte(json), *port, callback, nil)
	if err != nil {
		panic(fmt.Sprintf("NewServer() failed: %v", err))
	}
	srv.Serve()
}
