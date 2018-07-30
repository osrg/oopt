package sonic

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openconfig/ygot/ygot"

	"github.com/osrg/oopt/pkg/model"
)

const (
	VLAN_TABLE        = "VLAN"
	VLAN_MEMBER_TABLE = "VLAN_MEMBER"
	PORT_TABLE        = "PORT_TABLE"
)

func OptEthernetName(m *model.PacketTransponder_Interface_OpticalModuleConnection_OpticalModule) (string, error) {
	if m == nil || m.Name == nil || m.Channel == nil {
		return "", fmt.Errorf("module is nil")
	}
	name := *m.Name
	ch := *m.Channel

	index, err := strconv.Atoi(name[len("Opt"):])
	if err != nil {
		return "", err
	}
	subindex := 0
	if ch == "B" {
		subindex = 1
	}
	return fmt.Sprintf("Ethernet%d", 2*index-1+subindex+16), nil
}

func HandlePortDiff(name string, task []DiffTask) (bool, error) {
	if !strings.HasPrefix(name, "Port") {
		return false, fmt.Errorf("invalid optical-module name: %s", name)
	}
	for _, t := range task {
		switch path := t.Path.String(); path {
		case "description":
		default:
			return true, nil
		}
	}
	return false, nil
}

func HandleInterfaceDiff(newConfig, oldConfig *model.PacketTransponder, name string, task []DiffTask) error {
	if !strings.HasPrefix(name, "Ethernet") {
		return fmt.Errorf("invalid optical-module name: %s", name)
	}

	modEther := false
	modOpt := false
	delOld := false

	for _, t := range task {
		if t.Type == DiffDeleted {
			delOld = true
			break
		}
		switch path := t.Path.String(); path {
		case "optical-module-connection.optical-module.channel":
			modOpt = true
		case "optical-module-connection.optical-module.name":
			modOpt = true
		case "optical-module-connection.id":
			modOpt = true
			modEther = true
		case "mtu", "name":
		default:
			fmt.Println("unhandled task:", path)
		}
	}

	if i := newConfig.Interface[name]; i == nil || i.OpticalModuleConnection == nil {
		return nil
	}

	client, err := NewSONiCDBClient("unix", DEFAULT_REDIS_UNIX_SOCKET, CONFIG_DB)
	if err != nil {
		return err
	}

	// get current vlan
	var oldVlanName string
	var oldVlan map[string]interface{}
	var oldOptName string
	if i := oldConfig.Interface[name]; i != nil {
		if c := i.OpticalModuleConnection; c != nil && c.Id != nil {
			oldVid := *c.Id
			oldVlanName = fmt.Sprintf("Vlan%d", oldVid)
			oldVlan, err = client.GetEntry(VLAN_TABLE, oldVlanName)
			if err != nil {
				return err
			}
			if c.OpticalModule != nil {
				oldOptName, _ = OptEthernetName(c.OpticalModule)
			}
		}
	}

	if delOld {
		if oldVlanName == "" {
			return nil
		}
		err = client.ModEntry(VLAN_TABLE, oldVlanName, nil)
		if err != nil {
			return err
		}
		value, ok := oldVlan["members"]
		if ok {
			members := value.([]string)
			for _, m := range members {
				key := strings.Join([]string{oldVlanName, m}, "|")
				err = client.ModEntry(VLAN_MEMBER_TABLE, key, nil)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}

	// OpticalModuleConnection always exists
	// since we do validation before coming here
	newVid := *newConfig.Interface[name].OpticalModuleConnection.Id
	vlanName := fmt.Sprintf("Vlan%d", newVid)
	newVlan, err := client.GetEntry(VLAN_TABLE, vlanName)
	if err != nil {
		return err
	}

	entry := map[string]interface{}{}

	if len(newVlan) == 0 {
		entry["vlanid"] = newVid
	}

	var newVlanMembers []string
	m, ok := newVlan["members"]
	if ok {
		newVlanMembers = m.([]string)
	} else {
		newVlanMembers = []string{}
	}

	var optName string
	if m := newConfig.Interface[name].OpticalModuleConnection.OpticalModule; m != nil {
		optName, err = OptEthernetName(m)
		if err != nil {
			fmt.Printf("incomplete optical module connection configuration for %s\n", name)
		}
	}

	if optName == "" {
		modOpt = false
	}

	if !modOpt && !modEther {
		return nil
	}

	if modEther {
		newVlanMembers = append(newVlanMembers, name)
		if oldVlanName != "" {
			key := strings.Join([]string{oldVlanName, name}, "|")
			// Interface is currently restricted to belongs to only one VLAN
			// we can safely remove the old VLAN_MEMBER entry
			err = client.ModEntry(VLAN_MEMBER_TABLE, key, nil)
			if err != nil {
				return err
			}
		}
		key := strings.Join([]string{vlanName, name}, "|")
		err = client.SetEntry(VLAN_MEMBER_TABLE, key, map[string]interface{}{
			"tagging_mode": "untagged",
		})
		if err != nil {
			return err
		}
	}

	if modOpt {
		newVlanMembers = append(newVlanMembers, optName)
		if oldVlanName != "" && oldOptName != "" {
			key := strings.Join([]string{oldVlanName, oldOptName}, "|")
			err = client.ModEntry(VLAN_MEMBER_TABLE, key, nil)
			if err != nil {
				return err
			}
			ms := make([]string, 0, len(newVlanMembers)-1)
			for _, m := range newVlanMembers {
				if m == oldOptName {
					continue
				}
				ms = append(ms, m)
			}
			newVlanMembers = ms
		}
		key := strings.Join([]string{vlanName, optName}, "|")
		err = client.SetEntry(VLAN_MEMBER_TABLE, key, map[string]interface{}{
			"tagging_mode": "tagged",
		})
		if err != nil {
			return err
		}
	}

	entry["members"] = newVlanMembers

	if oldVlanName != "" {
		oldVlanMembers := oldVlan["members"].([]string)
		oldVlanUpdatedMembers := []string{}
		for _, v := range oldVlanMembers {
			if v == name && modEther {
				continue
			}
			if v == optName && modOpt {
				continue
			}
			oldVlanUpdatedMembers = append(oldVlanUpdatedMembers, name)
		}
		if len(oldVlanUpdatedMembers) == 0 {
			err = client.ModEntry(VLAN_TABLE, oldVlanName, nil)
			if err != nil {
				return err
			}
		} else {
			err = client.ModEntry(VLAN_TABLE, oldVlanName, map[string]interface{}{
				"members": oldVlanUpdatedMembers,
			})
		}
	}
	return client.ModEntry(VLAN_TABLE, vlanName, entry)
}

func FillInterfaceState(name string, t *model.PacketTransponder_Interface) error {
	if t == nil {
		return fmt.Errorf("model is nil")
	}
	client, err := NewSONiCDBClient("unix", DEFAULT_REDIS_UNIX_SOCKET, APPL_DB)
	if err != nil {
		return err
	}

	entry, err := client.GetEntry(PORT_TABLE, name)
	if err != nil {
		return err
	}
	if s, ok := entry["mtu"]; ok {
		mtu, err := strconv.ParseUint(s.(string), 10, 16)
		if err != nil {
			return err
		}
		t.Mtu = ygot.Uint16(uint16(mtu))
	}

	if s, ok := entry["admin_status"]; ok {
		switch s.(string) {
		case "up":
			t.AdminStatus = model.OpenconfigInterfaces_Interface_AdminStatus_UP
		case "down":
			t.AdminStatus = model.OpenconfigInterfaces_Interface_AdminStatus_DOWN
		}
	}

	if s, ok := entry["oper_status"]; ok {
		switch s.(string) {
		case "up":
			t.OperStatus = model.OpenconfigInterfaces_Interface_OperStatus_UP
		case "down":
			t.OperStatus = model.OpenconfigInterfaces_Interface_OperStatus_DOWN
		}
	}

	return nil

}
