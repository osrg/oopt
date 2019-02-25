// Copyright (C) 2018 Nippon Telegraph and Telephone Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/osrg/oopt/pkg/model"
	"github.com/osrg/oopt/pkg/sonic"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

var current *model.PacketTransponder
var virtual bool
var dry bool

const (
	CONFIG_FILE = "config.json"
)

func RemoveContents(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, name := range names {
		err = os.RemoveAll(filepath.Join(dir, name))
		if err != nil {
			return err
		}
	}
	return nil
}

func fillDefaultValues(m *model.PacketTransponder) error {
	for _, o := range m.OpticalModule {
		if err := sonic.FillTransportDefaultConfig(o, current); err != nil {
			return err
		}
	}
	return nil
}

func persistentPreRunE(cmd *cobra.Command, args []string) error {
	data, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", viper.GetString("git_dir"), CONFIG_FILE))
	if err != nil {
		return fmt.Errorf("open: %v", err)
	}
	current = &model.PacketTransponder{}
	return model.Unmarshal(data, current)
}

func persistentPostRunE(cmd *cobra.Command, args []string) error {
	buf, err := ygot.EmitJSON(current, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
	})
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	file, err := os.Create(fmt.Sprintf("%s/%s", viper.GetString("git_dir"), CONFIG_FILE))
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	defer file.Close()
	file.Write(([]byte)(buf))
	file.Write([]byte("\n"))

	return nil
}

func validate(config *model.PacketTransponder) error {
	return nil
}

func validateFinal(config *model.PacketTransponder) error {
	err := validate(config)
	if err != nil {
		return err
	}
	usedID := map[int]string{}
	for k, v := range config.Interface {
		if c := v.OpticalModuleConnection; c != nil {
			if c.Id == nil || c.OpticalModule == nil || c.OpticalModule.Channel == nil || c.OpticalModule.Name == nil {
				return fmt.Errorf("insufficient configuration for optical module connection")
			}
			id := int(*c.Id)
			name, ok := usedID[id]
			if ok {
				return fmt.Errorf("id %d is used by multiple interfaces: %s, %s", id, k, name)
			}
			usedID[id] = k
		}
	}
	for k, v := range config.Port {
		bMode := v.BreakoutMode
		switch *bMode.NumChannels {
		case 1:
			switch bMode.ChannelSpeed {
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_40GB:
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_100GB:
			default:
				return fmt.Errorf("unsupported port speed %v for port %s", bMode.ChannelSpeed, k)
			}
		case 2:
			switch bMode.ChannelSpeed {
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_40GB:
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_50GB:
			default:
				return fmt.Errorf("port speed must be 20G, 40G or 50G for breakout(2) port %s", k)
			}
		case 4:
			switch bMode.ChannelSpeed {
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_10GB:
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_25GB:
			default:
				return fmt.Errorf("port speed must be 10G or 25G for breakout(4) port %s", k)
			}
		default:
			return fmt.Errorf("invalid num-channels %d for port %s", *bMode.NumChannels, k)
		}
	}
	return nil
}

func getPacketTransport(repo *git.Repository, commit *object.Commit) (*model.PacketTransponder, error) {
	t, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	file, err := t.File(CONFIG_FILE)
	if err != nil {
		return nil, err
	}
	json, err := (file.Contents())
	if err != nil {
		return nil, err
	}
	pt := &model.PacketTransponder{}
	err = model.Unmarshal([]byte(json), pt)
	return pt, err
}

func getSignature() *object.Signature {
	return &object.Signature{
		Name:  "yang-system",
		Email: "ishida.wataru@lab.ntt.co.jp",
		When:  time.Now(),
	}
}

func handleDiff(newConfig, oldConfig *model.PacketTransponder, diff *gnmipb.Notification) (bool, error) {
	rebootOFDPA := false

	optDiffTask := map[string][]sonic.DiffTask{}
	intfDiffTask := map[string][]sonic.DiffTask{}
	portDiffTask := map[string][]sonic.DiffTask{}

	var taskMap map[string][]sonic.DiffTask

	for _, u := range diff.Update {
		elems := u.GetPath().GetElem()
		e := elems[0]
		n := elems[1]
		switch e.Name {
		case "optical-modules":
			taskMap = optDiffTask
		case "interfaces":
			taskMap = intfDiffTask
		case "ports":
			taskMap = portDiffTask
		default:
			continue
		}
		name := n.Key["name"]
		task, ok := taskMap[name]
		if !ok {
			task = []sonic.DiffTask{}
		}
		taskMap[name] = append(task, sonic.DiffTask{sonic.DiffModified, elems[2:], u.GetVal()})
	}

	for _, d := range diff.Delete {
		elems := d.GetElem()
		e := elems[0]
		n := elems[1]
		switch e.Name {
		case "optical-modules":
			taskMap = optDiffTask
		case "interfaces":
			taskMap = intfDiffTask
		case "ports":
			taskMap = portDiffTask
		default:
			continue
		}
		name := n.Key["name"]
		task, ok := taskMap[name]
		if !ok {
			task = []sonic.DiffTask{}
		}
		taskMap[name] = append(task, sonic.DiffTask{sonic.DiffDeleted, elems[2:], nil})
	}

	for k, v := range optDiffTask {
		if err := sonic.HandleOptDiff(k, v); err != nil {
			return rebootOFDPA, err
		}
	}

	for k, v := range intfDiffTask {
		if err := sonic.HandleInterfaceDiff(newConfig, oldConfig, k, v); err != nil {
			return rebootOFDPA, err
		}
	}

	for k, v := range portDiffTask {
		reboot, err := sonic.HandlePortDiff(k, v)
		if err != nil {
			return rebootOFDPA, err
		}
		if reboot {
			rebootOFDPA = true
		}
	}

	return rebootOFDPA, nil
}

const (
	portNum          = 16
	opticalModuleNum = 8
)

func newInterface(t *model.PacketTransponder, name string) error {
	iface, err := t.NewInterface(name)
	if err != nil {
		return err
	}
	iface.Mtu = ygot.Uint16(1500)
	return nil
}

func defaultConfiguration() (*model.PacketTransponder, error) {
	d := &model.PacketTransponder{}
	for i := 1; i <= portNum; i++ {
		port, err := d.NewPort(fmt.Sprintf("Port%d", i))
		if err != nil {
			return nil, fmt.Errorf("failed to create port: %v", err)
		}
		port.BreakoutMode = &model.PacketTransponder_Port_BreakoutMode{
			ChannelSpeed: model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_100GB,
			NumChannels:  ygot.Uint8(1),
		}
		err = newInterface(d, fmt.Sprintf("Ethernet%d", i))
		if err != nil {
			return nil, err
		}
	}
	for i := 1; i <= opticalModuleNum; i++ {
		_, err := d.NewOpticalModule(fmt.Sprintf("Opt%d", i))
		if err != nil {
			return nil, fmt.Errorf("failed to create optical module: %v", err)
		}
	}
	return d, nil
}

func initConfig(force bool) error {
	c, err := defaultConfiguration()
	if err != nil {
		return err
	}
	json, err := ygot.EmitJSON(c, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
	})
	if err != nil {
		return err
	}
	first := true
redo:
	repo, err := git.PlainInit(viper.GetString("git_dir"), false)
	if err != nil {
		if force && first {
			first = false
			err = RemoveContents(viper.GetString("git_dir"))
			if err != nil {
				return err
			}
			goto redo
		}
		return err
	}
	file, err := os.Create(fmt.Sprintf("%s/%s", viper.GetString("git_dir"), CONFIG_FILE))
	if err != nil {
		return err
	}
	defer file.Close()
	file.Write(([]byte)(json))
	file.Write([]byte("\n"))

	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	_, err = tree.Add(CONFIG_FILE)
	if err != nil {
		return fmt.Errorf("git-add: %v", err)
	}
	signature := getSignature()
	_, err = tree.Commit("initial commit", &git.CommitOptions{
		Author:    signature,
		Committer: signature,
	})
	return err
}

func NewInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use: "init",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := initConfig(force)
			if err != nil {
				return err
			}
			err = persistentPreRunE(nil, nil)
			if err != nil {
				return err
			}
			err = validateFinal(current)
			if err != nil {
				return err
			}
			return rebootSystem(current)
		},
	}
	cmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "force init")
	return cmd
}

func NewDumpCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use: "dump",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", viper.GetString("git_dir"), CONFIG_FILE))
			if err != nil {
				return err
			}
			t := &model.PacketTransponder{}
			err = model.Unmarshal(data, t)
			if err != nil {
				return err
			}
			if verbose {
				err = fillDefaultValues(t)
				if err != nil {
					return err
				}
			}
			json, err := ygot.EmitJSON(t, nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
	}
	cmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose")
	return cmd
}

func NewPortCmd() *cobra.Command {
	var name string
	speeds := make([]string, 0, len(model.ΛEnum["E_OpenconfigIfEthernet_ETHERNET_SPEED"]))

	for _, v := range model.ΛEnum["E_OpenconfigIfEthernet_ETHERNET_SPEED"] {
		speeds = append(speeds, v.Name)
	}

	channelSpeedUsage := fmt.Sprintf("channel-speed [%s]", strings.Join(speeds, "|"))

	channelSpeedCmd := &cobra.Command{
		Use:       channelSpeedUsage,
		ValidArgs: speeds,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			s := args[0]
			speed := model.OpenconfigIfEthernet_ETHERNET_SPEED_UNSET
			for k, v := range model.ΛEnum["E_OpenconfigIfEthernet_ETHERNET_SPEED"] {
				if s == v.Name {
					speed = model.E_OpenconfigIfEthernet_ETHERNET_SPEED(k)
					break
				}
			}

			if speed == model.OpenconfigIfEthernet_ETHERNET_SPEED_UNSET {
				return fmt.Errorf("unknown speed: %s", s)
			}

			current.Port[name].BreakoutMode.ChannelSpeed = speed
			return nil
		},
	}

	numChannelsCmd := &cobra.Command{
		Use:       "num-channels [1|4]",
		ValidArgs: []string{"1", "4"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("specify channel number")
			}
			num, err := strconv.ParseUint(args[0], 10, 8)
			if err != nil {
				return err
			}

			// ygot doesn't catch invalid num-channels
			if num != 1 && num != 4 {
				return fmt.Errorf("supported num-channels: 1, 2 or 4")
			}
			if *current.Port[name].BreakoutMode.NumChannels == uint8(num) {
				return fmt.Errorf("num-channels is already set to %d", num)
			}
			current.Port[name].BreakoutMode.NumChannels = ygot.Uint8(uint8(num))
			portNum, err := strconv.Atoi(name[len("Port"):])
			if err != nil {
				return err
			}
			switch num {
			case 1:
				for i := 1; i <= 4; i++ {
					delete(current.Interface, fmt.Sprintf("Ethernet%d_%d", portNum, i))
				}
				err = newInterface(current, fmt.Sprintf("Ethernet%d", portNum))
				if err != nil {
					return err
				}
			case 4:
				delete(current.Interface, fmt.Sprintf("Ethernet%d", portNum))
				for i := 1; i <= 4; i++ {
					err = newInterface(current, fmt.Sprintf("Ethernet%d_%d", portNum, i))
					if err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	breakoutModeCmd := &cobra.Command{
		Use: "breakout-mode",
	}
	breakoutModeCmd.AddCommand(channelSpeedCmd, numChannelsCmd)

	clearCmd := &cobra.Command{
		Use: "clear",
		RunE: func(cmd *cobra.Command, args []string) error {
			current.Port[name].Description = nil
			return nil
		},
	}

	descriptionCmd := &cobra.Command{
		Use: "description",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				desc := current.Port[name].Description
				if desc == nil {
					return nil
				}
				fmt.Println(*desc)
				return nil
			}
			current.Port[name].Description = ygot.String(strings.Join(args, " "))
			return nil
		},
	}
	descriptionCmd.AddCommand(clearCmd)

	portCmdImpl := &cobra.Command{
		Dynamic: func(n string) (bool, error) {
			name = n
			return true, nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := persistentPreRunE(cmd, args)
			if err != nil {
				return err
			}
			if _, ok := current.Port[name]; !ok {
				return fmt.Errorf("port %s doesn't exist", name)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			json, err := ygot.EmitJSON(current.Port[cmd.Use], nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
	}
	portCmdImpl.AddCommand(breakoutModeCmd, descriptionCmd)

	portCmd := &cobra.Command{
		Use:               "port <port-name>",
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, port := range current.Port {
				json, err := ygot.EmitJSON(port, nil)
				if err != nil {
					return err
				}
				fmt.Println(json)
			}
			return nil
		},
		PersistentPostRunE: persistentPostRunE,
	}
	portCmd.AddCommand(portCmdImpl)
	return portCmd
}

func NewInterfaceCmd() *cobra.Command {
	var name string
	idCmd := &cobra.Command{
		Use:  "id",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			id, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return err
			}
			if id < 100 || id > 4000 {
				return fmt.Errorf("id must be between 100 and 4000")
			}
			current.Interface[name].OpticalModuleConnection.Id = ygot.Uint32(uint32(id))
			return nil
		},
	}

	nameCmd := &cobra.Command{
		Use:  "name",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			if _, ok := current.OpticalModule[args[0]]; !ok {
				return fmt.Errorf("not found optical module %s", args[0])
			}
			current.Interface[name].OpticalModuleConnection.OpticalModule.Name = ygot.String(args[0])
			return nil
		},
	}

	channelCmd := &cobra.Command{
		Use:       "channel",
		Args:      cobra.OnlyValidArgs,
		ValidArgs: []string{"A", "B"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			if args[0] != "A" && args[0] != "B" {
				return fmt.Errorf("unsupported channel %s, supported channels are 'A' and 'B'", args[0])
			}
			current.Interface[name].OpticalModuleConnection.OpticalModule.Channel = ygot.String(args[0])
			return nil
		},
	}

	moduleCmd := &cobra.Command{
		Use: "optical-module",
	}
	moduleCmd.AddCommand(nameCmd, channelCmd)

	clearCmd := &cobra.Command{
		Use:  "clear",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current.Interface[name].OpticalModuleConnection = nil
			return nil
		},
	}

	connectionCmd := &cobra.Command{
		Use:     "optical-module-connection",
		Aliases: []string{"connection"},
	}
	connectionCmd.AddCommand(idCmd, moduleCmd, clearCmd)

	stateCmd := &cobra.Command{
		Use:  "state",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := sonic.FillInterfaceState(name, current.Interface[name])
			if err != nil {
				return err
			}
			json, err := ygot.EmitJSON(current.Interface[name], nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	descriptionClearCmd := &cobra.Command{
		Use:  "clear",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current.Port[name].Description = nil
			return nil
		},
	}

	descriptionCmd := &cobra.Command{
		Use: "description",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				desc := current.Interface[name].Description
				if desc == nil {
					return nil
				}
				fmt.Println(*desc)
				return nil
			}
			current.Interface[name].Description = ygot.String(strings.Join(args, " "))
			return nil
		},
	}
	descriptionCmd.AddCommand(descriptionClearCmd)

	intfCmdImpl := &cobra.Command{
		Dynamic: func(n string) (bool, error) {
			name = n
			return true, nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := persistentPreRunE(cmd, args)
			if err != nil {
				return err
			}
			if _, ok := current.Interface[name]; !ok {
				return fmt.Errorf("interface %s doesn't exist", name)
			}
			if current.Interface[name].OpticalModuleConnection == nil {
				current.Interface[name].OpticalModuleConnection = &model.PacketTransponder_Interface_OpticalModuleConnection{}
			}
			if current.Interface[name].OpticalModuleConnection.OpticalModule == nil {
				current.Interface[name].OpticalModuleConnection.OpticalModule = &model.PacketTransponder_Interface_OpticalModuleConnection_OpticalModule{}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			json, err := ygot.EmitJSON(current.Interface[name], nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
	}
	intfCmdImpl.AddCommand(connectionCmd, stateCmd, descriptionCmd)

	intfCmd := &cobra.Command{
		Use:               "interface <interface-name>",
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, intf := range current.Interface {
				json, err := ygot.EmitJSON(intf, nil)
				if err != nil {
					return err
				}
				fmt.Println(json)
			}
			return nil
		},
		PersistentPostRunE: persistentPostRunE,
	}
	intfCmd.AddCommand(intfCmdImpl)
	return intfCmd
}

func NewOpticalModuleCmd() *cobra.Command {
	var name string
	var verbose bool

	grids := make([]string, 0, len(model.ΛEnum["E_PacketTransport_FrequencyGridType"]))

	for _, v := range model.ΛEnum["E_PacketTransport_FrequencyGridType"] {
		grids = append(grids, v.Name)
	}

	gridUsage := fmt.Sprintf("grid [%s]", strings.Join(grids, "|"))

	gridCmd := &cobra.Command{
		Use:       gridUsage,
		ValidArgs: grids,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			g := args[0]
			grid := model.PacketTransport_FrequencyGridType_UNSET
			for k, v := range model.ΛEnum["E_PacketTransport_FrequencyGridType"] {
				if g == v.Name {
					grid = model.E_PacketTransport_FrequencyGridType(k)
					break
				}
			}

			if grid == model.PacketTransport_FrequencyGridType_UNSET {
				return fmt.Errorf("unknown grid: %s", g)
			}

			current.OpticalModule[name].OpticalModuleFrequency.Grid = grid
			return nil
		},
	}
	channelCmd := &cobra.Command{
		Use: "channel <channel>",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			channel, err := strconv.ParseUint(args[0], 10, 8)
			if err != nil {
				return err
			}
			if channel < 1 {
				return fmt.Errorf("channel can't be 0")
			}
			current.OpticalModule[name].OpticalModuleFrequency.Channel = ygot.Uint8(uint8(channel))
			return nil
		},
	}
	frequencyCmd := &cobra.Command{
		Use: "frequency",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := persistentPreRunE(cmd, args)
			if err != nil {
				return err
			}
			if _, ok := current.OpticalModule[name]; !ok {
				return fmt.Errorf("optical-module %s doesn't exist", name)
			}
			if current.OpticalModule[name].OpticalModuleFrequency == nil {
				current.OpticalModule[name].OpticalModuleFrequency = &model.PacketTransponder_OpticalModule_OpticalModuleFrequency{}
			}
			return nil
		},
	}
	frequencyCmd.AddCommand(gridCmd, channelCmd)

	berIntervalCmd := &cobra.Command{
		Use:  "ber-interval",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			interval, err := strconv.ParseUint(args[0], 10, 32)
			if err != nil {
				return err
			}
			if interval < 5 {
				return fmt.Errorf("interval can't be less than 5 seconds")
			}
			current.OpticalModule[name].BerInterval = ygot.Uint32(uint32(interval))
			return nil
		},
	}

	enableCmd := &cobra.Command{
		Use:  "enable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current.OpticalModule[name].Enabled = ygot.Bool(true)
			return nil
		},
	}

	disableCmd := &cobra.Command{
		Use:  "disable",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current.OpticalModule[name].Enabled = ygot.Bool(false)
			return nil
		},
	}

	prbsCmd := &cobra.Command{
		Use:       "prbs",
		ValidArgs: []string{"on", "off"},
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			if args[0] == "on" {
				current.OpticalModule[name].Prbs = ygot.Bool(true)
			} else {
				current.OpticalModule[name].Prbs = ygot.Bool(false)
			}
			return nil
		},
	}

	losiCmd := &cobra.Command{
		Use:       "losi",
		ValidArgs: []string{"on", "off"},
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			if args[0] == "on" {
				current.OpticalModule[name].Losi = ygot.Bool(true)
			} else {
				current.OpticalModule[name].Losi = ygot.Bool(false)
			}
			return nil
		},
	}

	clearAllowOversubscriptionCmd := &cobra.Command{
		Use:  "clear",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current.OpticalModule[name].AllowOversubscription = nil
			return nil
		},
	}

	allowOversubscriptionCmd := &cobra.Command{
		Use:       "allow-oversubscription",
		ValidArgs: []string{"true", "false"},
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			if args[0] == "true" {
				current.OpticalModule[name].AllowOversubscription = ygot.Bool(true)
			} else {
				current.OpticalModule[name].AllowOversubscription = ygot.Bool(false)
			}
			return nil
		},
	}

	allowOversubscriptionCmd.AddCommand(clearAllowOversubscriptionCmd)

	mods := make([]string, 0, len(model.ΛEnum["E_PacketTransport_OpticalModulationType"]))

	for _, v := range model.ΛEnum["E_PacketTransport_OpticalModulationType"] {
		mods = append(mods, v.Name)
	}

	modUsage := fmt.Sprintf("modulation-type [%s]", strings.Join(mods, "|"))

	modCmd := &cobra.Command{
		Use:       modUsage,
		ValidArgs: mods,
		Args:      cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			m := args[0]
			mod := model.PacketTransport_OpticalModulationType_UNSET
			for k, v := range model.ΛEnum["E_PacketTransport_OpticalModulationType"] {
				if m == v.Name {
					mod = model.E_PacketTransport_OpticalModulationType(k)
				}
			}

			if mod == model.PacketTransport_OpticalModulationType_UNSET {
				return fmt.Errorf("unknown modulation-type: %s", m)
			}

			current.OpticalModule[name].ModulationType = mod
			return nil
		},
	}

	stateCmd := &cobra.Command{
		Use:  "state",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := ygot.DeepCopy(current.OpticalModule[name])
			if err != nil {
				return err
			}
			module := o.(*model.PacketTransponder_OpticalModule)
			if verbose {
				if err := sonic.FillTransportDefaultConfig(module, current); err != nil {
					return err
				}
			}
			if !dry {
				err = sonic.FillTransportState(name, module)
				if err != nil {
					return err
				}
			}
			json, err := ygot.EmitJSON(module, nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
	}

	clearCmd := &cobra.Command{
		Use:  "clear",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current.OpticalModule[name].Description = nil
			return nil
		},
	}

	descriptionCmd := &cobra.Command{
		Use: "description",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				desc := current.OpticalModule[name].Description
				if desc == nil {
					return nil
				}
				fmt.Println(*desc)
				return nil
			}

			current.OpticalModule[name].Description = ygot.String(strings.Join(args, " "))
			return nil
		},
	}
	descriptionCmd.AddCommand(clearCmd)

	opticalModuleCmdImpl := &cobra.Command{
		Dynamic: func(n string) (bool, error) {
			name = n
			return true, nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := persistentPreRunE(cmd, args)
			if err != nil {
				return err
			}
			if _, ok := current.OpticalModule[name]; !ok {
				return fmt.Errorf("optical-module %s doesn't exist", name)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := ygot.DeepCopy(current.OpticalModule[name])
			if err != nil {
				return err
			}
			module := o.(*model.PacketTransponder_OpticalModule)
			if verbose {
				if err := sonic.FillTransportDefaultConfig(module, current); err != nil {
					return err
				}
			}
			json, err := ygot.EmitJSON(module, nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
		PersistentPostRunE: persistentPostRunE,
	}
	opticalModuleCmdImpl.AddCommand(frequencyCmd, berIntervalCmd, prbsCmd, losiCmd, modCmd, stateCmd, descriptionCmd, enableCmd, disableCmd, allowOversubscriptionCmd)

	opticalModuleCmd := &cobra.Command{
		Use:               "optical-module <module-name>",
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, module := range current.OpticalModule {
				if verbose {
					if err := sonic.FillTransportDefaultConfig(module, current); err != nil {
						return err
					}
				}
				json, err := ygot.EmitJSON(module, nil)
				if err != nil {
					return err
				}
				fmt.Println(json)
			}
			return nil
		},
	}
	opticalModuleCmd.AddCommand(opticalModuleCmdImpl)
	opticalModuleCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose")
	return opticalModuleCmd
}

func rebootSystem(config *model.PacketTransponder) error {
	if dry {
		return nil
	}
	log.Println("restarting redis pod")
	err := RestartRedis()
	if err != nil {
		return err
	}
	// TODO wait redis boot up
	time.Sleep(time.Second * 5)
	err = sonic.ConfigureTransport(config)
	if err != nil {
		return err
	}
	log.Println("restarting transyncd pod")
	err = RestartTransyncd()
	if err != nil {
		return err
	}

	if !virtual {
		ofdpa, err := NewOFDPAConfigFromModel(config)
		if err != nil {
			return err
		}
		log.Println("restarting ofdpa pod")
		err = RestartOFDPA(ofdpa)
		if err != nil {
			return err
		}
	}
	sonicConfig, err := NewSONiCConfigFromModel(config)
	if err != nil {
		return err
	}
	err = sonicConfig.WriteToConfigDB()
	if err != nil {
		return err
	}
	bytes, err := json.Marshal(sonicConfig)
	if err != nil {
		return err
	}
	log.Println("restarting sonic pod")
	return RestartSONiC(string(bytes), virtual)
}

func commit(commitMessage string, reboot bool) error {
	err := validateFinal(current)
	if err != nil {
		return err
	}
	repo, err := git.PlainOpen(viper.GetString("git_dir"))
	if err != nil {
		return err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	signature := getSignature()
	if commitMessage == "" {
		commitMessage = fmt.Sprintf("%s", time.Now())
	}
	_, err = tree.Commit(commitMessage, &git.CommitOptions{
		All:       true,
		Author:    signature,
		Committer: signature,
	})
	if err != nil {
		return err
	}
	iter, err := repo.Log(&git.LogOptions{})
	if err != nil {
		return err
	}
	head, err := iter.Next()
	if err != nil {
		return err
	}
	t, err := getPacketTransport(repo, head)
	if err != nil {
		return err
	}
	next, err := iter.Next()
	if err != nil {
		return err
	}
	s, err := getPacketTransport(repo, next)
	if err != nil {
		return err
	}
	if reboot {
		return rebootSystem(current)
	}

	opt := &ygot.DiffPathOpt{
		MapToSinglePath: true,
	}

	diff, err := ygot.Diff(s, t, opt)
	if err != nil {
		return err
	}
	reboot, err = handleDiff(t, s, diff)
	if reboot {
		return rebootSystem(current)
	}
	return err
}

func NewCommitCmd() *cobra.Command {
	var reboot bool
	var message string
	commitCmd := &cobra.Command{
		Use:               "commit",
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("%s cmd takes no args", cmd.Use)
			}
			return commit(message, reboot)
		},
	}
	commitCmd.PersistentFlags().BoolVarP(&reboot, "reboot", "r", false, "always reboot")
	commitCmd.PersistentFlags().StringVarP(&message, "message", "m", "", "git commit message")
	return commitCmd
}

func NewRollbackCmd() *cobra.Command {
	var reboot bool
	var number int
	var message string
	rollbackCmd := &cobra.Command{
		Use:  "rollback",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("%s cmd takes no args", cmd.Use)
			}

			repo, err := git.PlainOpen(viper.GetString("git_dir"))
			if err != nil {
				return err
			}
			// TODO: ensure there is no modification to the repo
			// currently modification is silently discarded
			iter, err := repo.Log(&git.LogOptions{})
			if err != nil {
				return err
			}
			var c *object.Commit
			for i := 0; i < 2+number; i++ {
				c, err = iter.Next()
				if err != nil {
					return err
				}
			}
			s, err := getPacketTransport(repo, c)
			if err != nil {
				return err
			}
			current = s
			if err = persistentPostRunE(nil, nil); err != nil {
				return err
			}
			if message == "" {
				message = fmt.Sprintf("rollback(%d) %s", number, time.Now())
			}
			return commit(message, reboot)
		},
	}

	rollbackCmd.PersistentFlags().BoolVarP(&reboot, "reboot", "r", false, "always reboot")
	rollbackCmd.PersistentFlags().IntVarP(&number, "number", "n", 0, "configuration to return to")
	rollbackCmd.PersistentFlags().StringVarP(&message, "message", "m", "", "git commit message")
	return rollbackCmd
}

func NewDiffCmd() *cobra.Command {
	diffCmd := &cobra.Command{
		Use:               "diff",
		Args:              cobra.NoArgs,
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("'%s' cmd takes no args", cmd.Use)
			}
			err := os.Chdir(viper.GetString("git_dir"))
			if err != nil {
				return err
			}
			c := exec.Command("git", "diff", "--color")
			output, err := c.Output()
			if err != nil {
				return err
			}
			fmt.Println(string(output))
			return nil
		},
	}
	return diffCmd
}

func NewRebootCmd() *cobra.Command {
	rebootCmd := &cobra.Command{
		Use:               "reboot",
		Args:              cobra.NoArgs,
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("%s cmd takes no args", cmd.Use)
			}
			err := validateFinal(current)
			if err != nil {
				return err
			}
			return rebootSystem(current)
		},
	}
	return rebootCmd
}

func NewStopCmd() *cobra.Command {
	stopCmd := &cobra.Command{
		Use:  "stop",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := deletePod(TRANSYNCD_POD_NAME)
			if err != nil {
				return err
			}
			err = deletePod(SONIC_POD_NAME)
			if err != nil {
				return err
			}
			err = deletePod(OFDPA_POD_NAME)
			if err != nil {
				return err
			}
			return deletePod(REDIS_POD_NAME)
		},
	}
	return stopCmd
}

func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := exec.Command("kubectl", "get", "pod")
			output, err := c.Output()
			if err != nil {
				return err
			}
			fmt.Printf("%s", output)
			return nil
		},
	}
}

func NewRootCmd() *cobra.Command {
	var gitDir string
	viper.AutomaticEnv()
	viper.SetEnvPrefix("oopt")
	cobra.EnablePrefixMatching = true
	rootCmd := &cobra.Command{
		Use: "oopt",
	}

	initCmd := NewInitCmd()
	rebootCmd := NewRebootCmd()
	stopCmd := NewStopCmd()
	statusCmd := NewStatusCmd()

	dumpCmd := NewDumpCmd()
	commitCmd := NewCommitCmd()
	rollbackCmd := NewRollbackCmd()
	diffCmd := NewDiffCmd()

	portCmd := NewPortCmd()
	interfaceCmd := NewInterfaceCmd()
	opticalModuleCmd := NewOpticalModuleCmd()

	allowOversubscriptionCmd := &cobra.Command{
		Use:               "allow-oversubscription",
		PersistentPreRunE: persistentPreRunE,
		ValidArgs:         []string{"true", "false"},
		Args:              cobra.OnlyValidArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("invalid usage")
			}
			if args[0] == "true" {
				current.AllowOversubscription = ygot.Bool(true)
			} else {
				current.AllowOversubscription = ygot.Bool(false)
			}
			return nil
		},
		PersistentPostRunE: persistentPostRunE,
	}

	rootCmd.AddCommand(initCmd, dumpCmd, portCmd, interfaceCmd, opticalModuleCmd, commitCmd, rollbackCmd, rebootCmd, stopCmd, diffCmd, statusCmd, allowOversubscriptionCmd)
	flags := rootCmd.PersistentFlags()
	flags.BoolVarP(&virtual, "virtual", "", false, "virtual env")
	flags.BoolVarP(&dry, "dry", "d", false, "dry run")
	flags.StringVarP(&gitDir, "git-dir", "c", "/etc/oopt", "directory of git repo")
	viper.BindPFlag("git_dir", flags.Lookup("git-dir"))
	return rootCmd
}

func main() {
	NewRootCmd().GenBashCompletionFile("out.bash")
	NewRootCmd().Execute()
}
