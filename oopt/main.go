package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"bitbucket.org/ishidaw/oopt/oopt/model"
	"github.com/openconfig/ygot/ygot"

	"github.com/d4l3k/messagediff"

	"github.com/spf13/cobra"

	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

var current *model.PacketTransponder

func persistentPreRunE(cmd *cobra.Command, args []string) error {
	data, err := ioutil.ReadFile("./config/config.json")
	if err != nil {
		return fmt.Errorf("open: %v", err)
	}
	current = &model.PacketTransponder{}
	return model.Unmarshal(data, current)
}

func persistentPostRunE(cmd *cobra.Command, args []string) error {
	json, err := ygot.EmitJSON(current, &ygot.EmitJSONConfig{
		Format: ygot.RFC7951,
	})
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	file, err := os.Create("./config/config.json")
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	defer file.Close()
	file.Write(([]byte)(json))
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
		case 4:
			if bMode.ChannelSpeed != model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_10GB {
				return fmt.Errorf("port speed must be 10G for breakout port %s", k)
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
	file, err := t.File("config.json")
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

func printDiff(diff *messagediff.Diff) {
	f := func(m map[*messagediff.Path]interface{}) {
		for k, v := range m {
			fmt.Println(k, v)
			fmt.Printf("key: %p\n", k)
			for _, kn := range []messagediff.PathNode(*k) {
				switch kn.(type) {
				case messagediff.StructField:
					fmt.Println("struct:", kn)
				case messagediff.MapKey:
					fmt.Println("mapkey:", kn)
				case messagediff.SliceIndex:
					fmt.Println("index:", kn)
				}
			}
		}
	}

	fmt.Println("--added--")
	f(diff.Added)
	fmt.Println("--removed--")
	f(diff.Removed)
	fmt.Println("--modified--")
	f(diff.Modified)
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

func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use: "init",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			repo, err := git.PlainInit("./config", false)
			if err != nil {
				return err
			}
			file, err := os.Create("./config/config.json")
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
			_, err = tree.Add("config.json")
			if err != nil {
				return fmt.Errorf("git-add: %v", err)
			}
			signature := getSignature()
			_, err = tree.Commit("initial commit", &git.CommitOptions{
				Author:    signature,
				Committer: signature,
			})
			return err
		},
	}
}

func NewDumpCmd() *cobra.Command {
	return &cobra.Command{
		Use: "dump",
		Run: func(cmd *cobra.Command, args []string) {
			data, err := ioutil.ReadFile("./config/config.json")
			if err != nil {
				log.Fatalf("open: %v", err)
			}
			t := &model.PacketTransponder{}
			err = model.Unmarshal(data, t)
			if err != nil {
				log.Fatalf("unmarshal: %v", err)
			}
			json, err := ygot.EmitJSON(t, nil)
			if err != nil {
				log.Fatalf("emit: %v", err)
			}
			fmt.Println(json)
		},
	}
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
				return fmt.Errorf("supported num-channels: 1 or 4")
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
					delete(current.Interface, fmt.Sprintf("Ethernet%d-%d", portNum, i))
				}
				err = newInterface(current, fmt.Sprintf("Ethernet%d", portNum))
				if err != nil {
					return err
				}
			case 4:
				delete(current.Interface, fmt.Sprintf("Ethernet%d", portNum))
				for i := 1; i <= 4; i++ {
					err = newInterface(current, fmt.Sprintf("Ethernet%d-%d", portNum, i))
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

	portCmdImpl := &cobra.Command{
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := persistentPreRunE(cmd, args)
			if err != nil {
				return err
			}
			if _, ok := current.Port[name]; !ok {
				return fmt.Errorf("port %s doesn't exist", cmd.Use)
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
	portCmdImpl.AddCommand(breakoutModeCmd)

	portCmd := &cobra.Command{
		Use:               "port <port-name>",
		PersistentPreRunE: persistentPreRunE,
		FindHookFn: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				portCmdImpl.Use = args[0]
				name = args[0]
				cmd.AddCommand(portCmdImpl)
			}
		},
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
	return portCmd
}

func NewInterfaceCmd() *cobra.Command {
	var name string
	idCmd := &cobra.Command{
		Use: "id",
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
		Use: "name",
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
		Use: "channel",
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

	connectionCmd := &cobra.Command{
		Use:     "optical-module-connection",
		Aliases: []string{"connection"},
	}
	connectionCmd.AddCommand(idCmd, moduleCmd)

	intfCmdImpl := &cobra.Command{
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
				current.Interface[name].OpticalModuleConnection.OpticalModule = &model.PacketTransponder_Interface_OpticalModuleConnection_OpticalModule_{}
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
	intfCmdImpl.AddCommand(connectionCmd)

	intfCmd := &cobra.Command{
		Use:               "interface",
		PersistentPreRunE: persistentPreRunE,
		FindHookFn: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				intfCmdImpl.Use = args[0]
				name = args[0]
				cmd.AddCommand(intfCmdImpl)
			}
		},
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
	return intfCmd
}

func NewOpticalModuleCmd() *cobra.Command {
	var name string
	opticalModuleCmdImpl := &cobra.Command{
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
			json, err := ygot.EmitJSON(current.OpticalModule[name], nil)
			if err != nil {
				return err
			}
			fmt.Println(json)
			return nil
		},
	}
	opticalModuleCmd := &cobra.Command{
		Use:               "optical-module",
		PersistentPreRunE: persistentPreRunE,
		FindHookFn: func(cmd *cobra.Command, args []string) {
			if len(args) > 0 {
				opticalModuleCmdImpl.Use = args[0]
				name = args[0]
				cmd.AddCommand(opticalModuleCmdImpl)
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, module := range current.OpticalModule {
				json, err := ygot.EmitJSON(module, nil)
				if err != nil {
					return err
				}
				fmt.Println(json)
			}
			return nil
		},
		PersistentPostRunE: persistentPostRunE,
	}
	return opticalModuleCmd
}

func NewCommitCmd() *cobra.Command {
	commitCmd := &cobra.Command{
		Use:               "commit",
		PersistentPreRunE: persistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := validateFinal(current)
			if err != nil {
				return err
			}
			repo, err := git.PlainOpen("./config")
			if err != nil {
				return err
			}
			tree, err := repo.Worktree()
			if err != nil {
				return err
			}
			signature := getSignature()
			_, err = tree.Commit(fmt.Sprintf("%s", time.Now()), &git.CommitOptions{
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
			diff, equal := messagediff.DeepDiff(s, t)
			if !equal {
				printDiff(diff)
			} else {
				fmt.Println("equal")
			}
			return nil
		},
	}
	return commitCmd
}

func NewRootCmd() *cobra.Command {
	cobra.EnablePrefixMatching = true
	rootCmd := &cobra.Command{}
	initCmd := NewInitCmd()
	dumpCmd := NewDumpCmd()
	portCmd := NewPortCmd()
	interfaceCmd := NewInterfaceCmd()
	opticalModuleCmd := NewOpticalModuleCmd()
	commitCmd := NewCommitCmd()
	rootCmd.AddCommand(initCmd, dumpCmd, portCmd, interfaceCmd, opticalModuleCmd, commitCmd)
	return rootCmd
}

func main() {
	NewRootCmd().Execute()
}
