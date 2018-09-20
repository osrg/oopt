package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/template"

	"github.com/spf13/viper"

	"github.com/osrg/oopt/pkg/model"
	"github.com/osrg/oopt/pkg/sonic"
)

const (
	REDIS_POD_NAME        = "redis"
	TRANSYNCD_POD_NAME    = "transyncd"
	SONIC_POD_NAME        = "sonic"
	SONIC_CONFIG_MAP_NAME = "sonic-config"
	REDIS_DIR             = "/var/run/redis"
)

const (
	VLAN_TABLE            = "VLAN"
	VLAN_MEMBER_TABLE     = "VLAN_MEMBER"
	PORT_TABLE            = "PORT_TABLE"
	DEVICE_METADATA_TABLE = "DEVICE_METADATA"
)

const (
	REDIS_K8S_POD_CONFIG_NAME    = "redis.yml"
	REDIS_K8S_POD_DEFAULT_CONFIG = `apiVersion: v1
kind: Pod
metadata:
  name: redis
spec:
  hostNetwork: true
  volumes:
  - name: redis
    hostPath:
      path: /var/run/redis
  containers:
  - name: redis
    image: redis
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['redis-server', '/usr/local/etc/redis/redis.conf']
`
	SONIC_K8S_POD_CONFIG_NAME     = "sonic.yml"
	SONIC_K8S_POD_CONFIG_TEMPLATE = `apiVersion: v1
kind: Pod
metadata:
  name: {{ .Name }}
spec:
  volumes:
  - name: redis
    hostPath:
      path: /var/run/redis
  - name: tmp
    hostPath:
      path: /tmp
  - name: {{ .SonicConfigMapName }}
    configMap:
      name: {{ .SonicConfigMapName }}
  initContainers:
  - name: init-loglevel
    image: redis
    imagePullPolicy: Never
    command: ['sh', '-c', 'for daemon in syncd:syncd intfmgrd:intfmgrd intfsyncd:intfsyncd orchagent:orchagent portsyncd:portsyncd neighsyncd:neighsyncd vlanmgrd:vlanmgrd;
do
  redis-cli -n 3 -s /var/run/redis/redis.sock hset $daemon LOGOUTPUT STDERR;
done']
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
  - name: init-configdb
    image: {{ .Image }}
    imagePullPolicy: Never
    command: ['sonic-cfggen', '-s', '/var/run/redis/redis.sock', '-j', '/root/config/config_db.json', '--write-to-db']
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    - mountPath: /root/config
      name: sonic-config
  - name: init-configdb-done
    image: redis
    imagePullPolicy: Never
    command: ['redis-cli', '-s', '/var/run/redis/redis.sock', '-n', '4', 'SET', 'CONFIG_DB_INITIALIZED', '1']
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
  containers:
  - name: syncd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    - mountPath: /tmp
      name: tmp
    securityContext:
      privileged: true
    command:
    - syncd
  - name: orchagent
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['sh', '-c', 'sleep 10 && platform=mellanox orchagent']
  - name: portsyncd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['sh', '-c', 'sleep 13 && portsyncd']
  - name: vlanmgrd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['sh', '-c', 'mount -o remount,rw /sys && sleep 15 && vlanmgrd']
  - name: intfmgrd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['sh', '-c', 'sleep 15 && intfmgrd']
  - name: intfsyncd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['sh', '-c', 'sleep 15 && intfsyncd']
  - name: neighsyncd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    securityContext:
      privileged: true
    command: ['sh', '-c', 'sleep 15 && neighsyncd']
`
	TRANSYNCD_K8S_POD_CONFIG_NAME     = "transyncd.yml"
	TRANSYNCD_K8S_POD_CONFIG_TEMPLATE = `apiVersion: v1
kind: Pod
metadata:
  name: {{ .Name }}
spec:
  volumes:
  - name: redis
    hostPath:
      path: /var/run/redis
  - name: tai
    hostPath:
      path: /etc/tai/
  containers:
  - name: transyncd
    image: {{ .Image }}
    imagePullPolicy: Never
    volumeMounts:
    - mountPath: /var/run/redis/
      name: redis
    - mountPath: /etc/tai/
      name: tai
    securityContext:
      privileged: true
    command:
    - transyncd
`
)

type DeviceMetadata struct {
	BGPAsn   int    `json:"bgp_asn"`
	Hostname string `json:"hostname"`
	Type     string `json:"type"`
	HWSKU    string `json:"hwsku"`
	Mac      string `json:"mac"`
}

func (d DeviceMetadata) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"bgp_asn":  d.BGPAsn,
		"hostname": d.Hostname,
		"type":     d.Type,
		"hwsku":    d.HWSKU,
		"mac":      d.Mac,
	}
}

var deviceMetadata = map[string]DeviceMetadata{
	// TODO make this configurable
	"localhost": DeviceMetadata{
		BGPAsn:   65100,
		Hostname: "cassini",
		Type:     "packet-transponder",
		HWSKU:    "AS7716-24XC",
		Mac:      "a8:2b:b5:b5:51:d5",
	},
}

type Port struct {
	Lanes int `json:"lanes"`
}

func (p Port) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"lanes": p.Lanes,
	}
}

type Vlan struct {
	Members []string `json:"members"`
	VID     int      `json:"vlanid"`
}

func (v Vlan) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"members": v.Members,
		"vlanid":  v.VID,
	}
}

type VlanMember struct {
	Mode string `json:"tagging_mode"`
}

func (v VlanMember) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"tagging_mode": v.Mode,
	}
}

type SONiCConfig struct {
	DeviceMetadata map[string]DeviceMetadata `json:"DEVICE_METADATA"`
	Ports          map[string]Port           `json:"PORT"`
	Vlans          map[string]Vlan           `json:"VLAN"`
	VlanMembers    map[string]VlanMember     `json:"VLAN_MEMBER"`
}

func (c *SONiCConfig) WriteToConfigDB() error {
	client, err := sonic.NewSONiCDBClient("unix", sonic.DEFAULT_REDIS_UNIX_SOCKET, sonic.CONFIG_DB)
	if err != nil {
		return err
	}
	for k, v := range c.DeviceMetadata {
		err = client.SetEntry(DEVICE_METADATA_TABLE, k, v.ToMap())
		if err != nil {
			return err
		}
	}
	for k, v := range c.Ports {
		client.SetEntry(PORT_TABLE, k, v.ToMap())
		if err != nil {
			return err
		}
	}
	for k, v := range c.Vlans {
		client.SetEntry(VLAN_TABLE, k, v.ToMap())
		if err != nil {
			return err
		}
	}
	for k, v := range c.VlanMembers {
		client.SetEntry(VLAN_MEMBER_TABLE, k, v.ToMap())
		if err != nil {
			return err
		}
	}
	return nil
}

func NewSONiCConfigFromModel(m *model.PacketTransponder) (*SONiCConfig, error) {
	config := &SONiCConfig{
		DeviceMetadata: deviceMetadata,
		Ports:          make(map[string]Port),
		Vlans:          make(map[string]Vlan),
		VlanMembers:    make(map[string]VlanMember),
	}

	for k, v := range m.Interface {
		if !strings.HasPrefix(k, "Ethernet") {
			return nil, fmt.Errorf("invalid interface name: %s", k)
		}
		elems := strings.Split(k[len("Ethernet"):], "_")
		if len(elems) == 0 || len(elems) > 2 {
			return nil, fmt.Errorf("invalid interface name: %s", k)
		}
		mainIndex, err := strconv.Atoi(elems[0])
		if err != nil {
			return nil, err
		}
		var subIndex int
		if len(elems) == 2 {
			subIndex, err = strconv.Atoi(elems[1])
			if err != nil {
				return nil, err
			}
		}

		port := Port{}

		if subIndex == 0 {
			port.Lanes = mainIndex
		} else {
			port.Lanes = 32 + 4*(mainIndex-1) + subIndex
		}

		config.Ports[k] = port

		if v.OpticalModuleConnection != nil {
			if vid := *v.OpticalModuleConnection.Id; vid > 0 {
				name := fmt.Sprintf("Vlan%d", vid)
				vlan, ok := config.Vlans[name]
				if !ok {
					vlan = Vlan{VID: int(vid)}
				}
				vlan.Members = append(vlan.Members, k)
				memberName := fmt.Sprintf("%s|%s", name, k)
				config.VlanMembers[memberName] = VlanMember{Mode: "untagged"}

				if m := v.OpticalModuleConnection.OpticalModule; m != nil {

					opticalModuleEthernetName, err := sonic.OptEthernetName(m)
					if err != nil {
						return nil, err
					}

					found := false
					for _, v := range vlan.Members {
						if v == opticalModuleEthernetName {
							found = true
						}
					}

					if !found {
						vlan.Members = append(vlan.Members, opticalModuleEthernetName)
						config.VlanMembers[fmt.Sprintf("%s|%s", name, opticalModuleEthernetName)] = VlanMember{Mode: "tagged"}
					}
				}

				config.Vlans[name] = vlan
			}
		}
	}
	// ports for optical modules
	for i := 16; i < 32; i++ {
		name := fmt.Sprintf("Ethernet%d", i+1)
		if _, ok := config.Ports[name]; ok {
			return nil, fmt.Errorf("port %s already exists", name)
		}
		config.Ports[name] = Port{Lanes: i + 1}
	}

	return config, nil
}

func createSONiCPod(virtual bool) error {
	name := fmt.Sprintf("%s/%s", viper.GetString("git_dir"), SONIC_K8S_POD_CONFIG_NAME)
	if _, err := os.Stat(name); err != nil {
		f, err := os.Create(name)
		defer f.Close()
		if err != nil {
			return err
		}
		t := template.Must(template.New("sonic.yml.tmpl").Parse(SONIC_K8S_POD_CONFIG_TEMPLATE))
		m := struct {
			Name               string
			Image              string
			SonicConfigMapName string
		}{}
		m.Name = "sonic"
		m.Image = "sonic"
		if virtual {
			m.Image = "sonic:virtual"
		}
		m.SonicConfigMapName = SONIC_CONFIG_MAP_NAME
		if err = t.Execute(f, m); err != nil {
			return err
		}
	}
	cmd := exec.Command("kubectl", "create", "-f", name)
	return cmd.Run()
}

func RestartSONiC(config string, virtual bool) error {
	err := createConfigMap(SONIC_CONFIG_MAP_NAME, map[string]string{"config_db.json": config})
	if err != nil {
		return err
	}
	err = deletePod(SONIC_POD_NAME)
	if err != nil {
		return err
	}
	return createSONiCPod(virtual)
}

func RestartRedis() error {
	if err := deletePod(REDIS_POD_NAME); err != nil {
		return err
	}

	name := fmt.Sprintf("%s/%s", viper.GetString("git_dir"), REDIS_K8S_POD_CONFIG_NAME)
	if _, err := os.Stat(name); err != nil {
		f, err := os.Create(name)
		defer f.Close()
		if err != nil {
			return err
		}
		if _, err := f.Write([]byte(REDIS_K8S_POD_DEFAULT_CONFIG)); err != nil {
			return err
		}
	}

	cmd := exec.Command("kubectl", "create", "-f", name)
	return cmd.Run()
}

func RestartTransyncd() error {
	err := deletePod(TRANSYNCD_POD_NAME)
	if err != nil {
		return err
	}

	name := fmt.Sprintf("%s/%s", viper.GetString("git_dir"), TRANSYNCD_K8S_POD_CONFIG_NAME)
	if _, err := os.Stat(name); err != nil {
		f, err := os.Create(name)
		defer f.Close()
		if err != nil {
			return err
		}
		t := template.Must(template.New("transyncd.yml.tmpl").Parse(TRANSYNCD_K8S_POD_CONFIG_TEMPLATE))
		m := struct {
			Name  string
			Image string
		}{}
		m.Name = "transyncd"
		m.Image = "transyncd"
		if virtual {
			m.Image = "transyncd:virtual"
		}
		if err = t.Execute(f, m); err != nil {
			return err
		}
	}

	cmd := exec.Command("kubectl", "create", "-f", name)
	return cmd.Run()
}
