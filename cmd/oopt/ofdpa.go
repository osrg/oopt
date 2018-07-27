package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/template"

	"github.com/spf13/viper"

	"github.com/osrg/oopt/pkg/model"
)

const (
	OFDPA_K8S_POD_CONFIG_NAME     = "ofdpa.yml"
	OFDPA_K8S_POD_CONFIG_TEMPLATE = `apiVersion: v1
kind: Pod
metadata:
  name: {{ .Name }}
spec:
  volumes:
  - name: usr
    hostPath:
      path: /usr
  - name: dev
    hostPath:
      path: /dev
  - name: etc
    hostPath:
      path: /etc
  - name: tmp
    hostPath:
      path: /tmp
  - name: lib
    hostPath:
      path: /lib
  - name: {{ .OfdpaConfigMapName }}
    configMap:
      name: {{ .OfdpaConfigMapName }}
  hostNetwork: true
  containers:
  - name: {{ .Name }}
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /usr
      name: usr
    - mountPath: /dev
      name: dev
    - mountPath: /etc
      name: etc
    - mountPath: /tmp
      name: tmp
    - mountPath: /lib
      name: lib
    - mountPath: /etc/accton
      name: {{ .OfdpaConfigMapName }}
    securityContext:
      privileged: true
    command:
    - ofagentapp
`
	OFDPA_CONFIG_TEMPLATE = `#
# ofdpa configuration for as7716-24xc
#
#
# port_mode_<logic-port>=1x100g (default) | 1x40g | 2x50g | 2x40g | 2x20g | 4x25g | 4x10g
#
# the last 8 ports (CFP2 ports) can be 200g ports by using the following config
#    port_mode_<logic-port>=2x100g
#    e.g. port_mode_38=2x100g
#
#
# adding if=XXX after port_mod_XX=XXXg can config interface type (note: ONE space between them)
#        default setting is SR4 for 100g ports at initial stage
#        valid interface type: CR, CR4, SR, SR4, LR, LR4, KR, KR4, SFI, XFI,...
# e.g.
# port_mode_68=1x100g if=CR4
#
{{ range . -}}
port_mode_{{ .OFDPAIndex }}={{ .NumChannels }}x{{ .ChannelSpeed }}g #{{ if gt .Index 16 }} module {{ else }} front {{ end -}} port {{ .Index }}
{{- if and (gt .Index 16) (even .Index) }} ; CFP2 port {{ cfp .Index }}{{ end }}
{{ if gt .Index 16 }}port_fec_{{ .OFDPAIndex }}=1
{{ end -}}
{{ end -}}
`
)

const (
	OFDPA_POD_NAME        = "ofdpa"
	OFDPA_CONFIG_MAP_NAME = "ofdpa-config"
)

var ofdpaPortMap = map[int]int{
	1:  68,
	2:  72,
	3:  76,
	4:  80,
	5:  96,
	6:  106,
	7:  110,
	8:  114,
	9:  118,
	10: 122,
	11: 126,
	12: 130,
	13: 84,
	14: 88,
	15: 92,
	16: 102,
	17: 38,
	18: 34,
	19: 46,
	20: 42,
	21: 54,
	22: 50,
	23: 62,
	24: 58,
	25: 5,
	26: 1,
	27: 13,
	28: 9,
	29: 21,
	30: 17,
	31: 25,
	32: 29,
}

type OFDPAPort struct {
	NumChannels  int
	ChannelSpeed int
	Index        int
	OFDPAIndex   int
}

func NewPort(index int, numChannels int, channelSpeed int) *OFDPAPort {
	ofdpaIndex, ok := ofdpaPortMap[index]
	if !ok {
		log.Fatalf("invalid index")
	}
	return &OFDPAPort{
		NumChannels:  numChannels,
		ChannelSpeed: channelSpeed,
		Index:        index,
		OFDPAIndex:   ofdpaIndex,
	}
}

func genOFDPAConf(writer io.Writer, ports []*OFDPAPort) error {
	t := template.New("ofdpa.conf.tmpl")
	funcMap := template.FuncMap{
		"even": func(i int) bool {
			return i%2 == 0
		},
		"cfp": func(i int) int {
			return (i + 1 - 16) / 2
		},
	}
	t = t.Funcs(funcMap)
	t, err := t.Parse(OFDPA_CONFIG_TEMPLATE)
	if err != nil {
		return err
	}
	return t.Execute(writer, ports)
}

func NewOFDPAConfigFromModel(m *model.PacketTransponder) (string, error) {
	ports := make([]*OFDPAPort, 0, len(m.Port))
	for k, v := range m.Port {
		if !strings.HasPrefix(k, "Port") {
			return "", fmt.Errorf("invalid port name: %s", k)
		}
		index, err := strconv.Atoi(k[len("Port"):])
		if err != nil {
			return "", err
		}
		ch := int(*v.BreakoutMode.NumChannels)
		speed := 100
		switch v.BreakoutMode.ChannelSpeed {
		case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_10GB:
			speed = 10
		case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_25GB:
			speed = 25
		case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_40GB:
			speed = 40
		case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_50GB:
			speed = 50
		}
		ports = append(ports, NewPort(index, ch, speed))
	}
	for i := 17; i < 33; i++ {
		ports = append(ports, NewPort(i, 1, 100))
	}

	buffer := new(bytes.Buffer)
	err := genOFDPAConf(buffer, ports)
	if err != nil {
		return "", err
	}
	return string(buffer.Bytes()), err
}

func createOFDPAPod() error {
	name := fmt.Sprintf("%s/%s", viper.GetString("git_dir"), OFDPA_K8S_POD_CONFIG_NAME)
	if _, err := os.Stat(name); err != nil {
		f, err := os.Create(name)
		defer f.Close()
		if err != nil {
			return err
		}
		t := template.Must(template.New("ofdpa.yml.tmpl").Parse(OFDPA_K8S_POD_CONFIG_TEMPLATE))
		m := struct {
			Name               string
			Image              string
			OfdpaConfigMapName string
		}{}
		m.Name = "ofdpa"
		m.Image = "debian:jessie"
		m.OfdpaConfigMapName = OFDPA_CONFIG_MAP_NAME
		if err = t.Execute(f, m); err != nil {
			return err
		}
	}

	cmd := exec.Command("kubectl", "create", "-f", name)
	return cmd.Run()
}

func RestartOFDPA(config string) error {
	err := createConfigMap(OFDPA_CONFIG_MAP_NAME, map[string]string{"ofdpa.conf": config})
	if err != nil {
		return err
	}
	err = deletePod(OFDPA_POD_NAME)
	if err != nil {
		return err
	}
	return createOFDPAPod()
}
