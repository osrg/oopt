package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/viper"
	"text/template"
)

const (
	SONIC_K8S_CONFIG_MAP_NAME = "sonic-configmap.yml"
	CONFIG_MAP_TEMPLATE       = `apiVersion: v1
kind: ConfigMap
data:
{{- range $key, $value := .Config }}
  {{ $key }}: '{{ $value }}'
{{ end -}}
metadata:
  name: {{ .Name }}
`
)

func createConfigMap(name string, config map[string]string) error {
	filename := fmt.Sprintf("%s/%s", viper.GetString("git_dir"), fmt.Sprintf("%s.yml", name))
	if _, err := os.Stat(filename); err != nil {
		f, err := os.Create(filename)
		defer f.Close()
		if err != nil {
			return err
		}
		t := template.Must(template.New("sonic-config.yml.tmpl").Parse(CONFIG_MAP_TEMPLATE))
		m := struct {
			Name   string
			Config map[string]string
		}{}
		m.Name = name
		m.Config = config
		t.Execute(f, m)
	}
	var cmd *exec.Cmd
	if isConfigMapExists(name) {
		cmd = exec.Command("kubectl", "replace", "-f", filename)
	} else {
		cmd = exec.Command("kubectl", "create", "-f", filename)
	}
	output, err := cmd.Output()
	if err != nil {
		fmt.Println(output)
	}
	return err
}

func isConfigMapExists(name string) bool {
	cmd := exec.Command("kubectl", "get", "cm", name)
	return cmd.Run() == nil
}

func isPodRunning(name string) bool {
	cmd := exec.Command("kubectl", "get", "pod", name)
	return cmd.Run() == nil
}

func deletePod(name string) error {
	if isPodRunning(name) {
		cmd := exec.Command("kubectl", "delete", "pod", name, "--grace-period=0", "--force")
		return cmd.Run()
	}
	return nil
}
