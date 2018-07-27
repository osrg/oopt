package sonic

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openconfig/ygot/ygot"

	"github.com/osrg/oopt/pkg/model"
)

const (
	CONFIG_TABLE = "MODULE_CONFIG_TABLE"
	STATE_TABLE  = "MODULE_STATE_TABLE"
)

func gridTypeToInt(t model.E_PacketTransport_FrequencyGridType) int {
	switch t {
	case model.PacketTransport_FrequencyGridType_GRID_100GHZ:
		return 100
	case model.PacketTransport_FrequencyGridType_GRID_50GHZ:
		return 50
	case model.PacketTransport_FrequencyGridType_GRID_33GHZ:
		return 33
	case model.PacketTransport_FrequencyGridType_GRID_25GHZ:
		return 25
	}
	return 50
}

func HandleOptDiff(name string, task []DiffTask) error {
	if !strings.HasPrefix(name, "Opt") {
		return fmt.Errorf("invalid optical-module name: %s", name)
	}
	entry := map[string]interface{}{}

	for _, t := range task {
		switch path := t.Path.String(); path {
		case ".OpticalModuleFrequency":
			freq := t.Value.(*model.PacketTransponder_OpticalModule_OpticalModuleFrequency)
			grid := gridTypeToInt(freq.Grid)
			entry["rx-frequency-grid"] = grid
			entry["tx-frequency-grid"] = grid
			ch := *freq.Channel
			entry["rx-frequency-ch"] = ch
			entry["tx-frequency-ch"] = ch
		case ".OpticalModuleFrequency.Grid":
			grid := gridTypeToInt(t.Value.(model.E_PacketTransport_FrequencyGridType))
			entry["rx-frequency-grid"] = grid
			entry["tx-frequency-grid"] = grid
		case ".OpticalModuleFrequency.Channel":
			ch := t.Value.(uint8)
			entry["rx-frequency-ch"] = ch
			entry["tx-frequency-ch"] = ch
		case ".BerInterval":
			interval, ok := t.Value.(uint32)
			if !ok {
				interval = *(t.Value.(*uint32))
			}
			entry["ber-interval"] = interval
		case ".Prbs":
			prbs, ok := t.Value.(bool)
			if !ok {
				prbs = *(t.Value.(*bool))
			}
			if prbs {
				entry["prbs"] = "on"
			} else {
				entry["prbs"] = "off"
			}
		case ".Losi":
			losi, ok := t.Value.(bool)
			if !ok {
				losi = *(t.Value.(*bool))
			}
			if losi {
				entry["losi"] = "on"
			} else {
				entry["losi"] = "off"
			}
		case ".ModulationType":
			mod := t.Value.(model.E_PacketTransport_OpticalModulationType)
			switch mod {
			case model.PacketTransport_OpticalModulationType_DP_QPSK:
				entry["modulation-format"] = "dp-qpsk"
			case model.PacketTransport_OpticalModulationType_DP_16QAM:
				entry["modulation-format"] = "dp-16qam"
			}
		default:
			fmt.Println("unhandled task:", path)
		}
	}

	if len(entry) == 0 {
		return nil
	}

	client, err := NewSONiCDBClient("unix", DEFAULT_REDIS_UNIX_SOCKET, TRANSPORT_CONFIG_DB)
	if err != nil {
		return err
	}

	return client.ModEntry(CONFIG_TABLE, name, entry)
}

func FillTransportState(name string, t *model.PacketTransponder_OpticalModule) error {
	if t == nil {
		return fmt.Errorf("model is nil")
	}
	client, err := NewSONiCDBClient("unix", DEFAULT_REDIS_UNIX_SOCKET, TRANSPORT_STATE_DB)
	if err != nil {
		return err
	}

	entry, err := client.GetEntry(STATE_TABLE, name)
	if err != nil {
		return err
	}

	if s, ok := entry["rms"]; ok {
		rms := s.(string)
		elems := strings.Split(rms, ",")
		if len(elems) != 4 {
			return fmt.Errorf("wrong rms format: %s", rms)
		}
		t.OpticalModuleRms = &model.PacketTransponder_OpticalModule_OpticalModuleRms{}
		trim := func(elem string) (*uint16, error) {
			v, err := strconv.ParseUint(strings.Trim(elem, "() ,"), 10, 16)
			if err != nil {
				return nil, err
			}
			return ygot.Uint16(uint16(v)), nil
		}
		if xi, err := trim(elems[0]); err != nil {
			return err
		} else {
			t.OpticalModuleRms.Xi = xi
		}
		if xq, err := trim(elems[0]); err != nil {
			return err
		} else {
			t.OpticalModuleRms.Xq = xq
		}
		if yi, err := trim(elems[2]); err != nil {
			return err
		} else {
			t.OpticalModuleRms.Yi = yi
		}
		if yq, err := trim(elems[3]); err != nil {
			return err
		} else {
			t.OpticalModuleRms.Yq = yq
		}
	}

	if s, ok := entry["sync-error"]; ok {
		if s.(string) == "false" {
			t.SyncError = ygot.Bool(false)
		} else if s.(string) == "true" {
			t.SyncError = ygot.Bool(true)
		}
	}

	if s, ok := entry["status"]; ok {
		switch s.(string) {
		case "down":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_DOWN
		case "booting-top-half":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_BOOTING_TOP_HALF
		case "waiting-rx-signal":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_WAITING_RX_SIGNAL
		case "booting-bottom-half":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_BOOTING_BOTTOM_HALF
		case "testing":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_TESTING
		case "ready":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_READY
		case "resetting":
			t.OperationStatus = model.PacketTransport_OpticalModuleStatusType_STATE_RESETTING
		}
	}

	createCh := func(n string) *model.PacketTransponder_OpticalModule_ChannelStats {
		if t.ChannelStats == nil {
			t.ChannelStats = map[string]*model.PacketTransponder_OpticalModule_ChannelStats{}
		}
		if _, ok := t.ChannelStats[n]; !ok {
			t.ChannelStats[n] = &model.PacketTransponder_OpticalModule_ChannelStats{
				Name: ygot.String(n),
			}
		}
		return t.ChannelStats[n]
	}

	if s, ok := entry["hd-fec-ber-ch0"]; ok {
		createCh("A").HdFecBer = ygot.String(s.(string))
	}

	if s, ok := entry["sd-fec-ber-ch0"]; ok {
		createCh("A").SdFecBer = ygot.String(s.(string))
	}

	if s, ok := entry["post-fec-ber-ch0"]; ok {
		createCh("A").PostFecBer = ygot.String(s.(string))
	}

	if s, ok := entry["hd-fec-ber-ch1"]; ok {
		createCh("B").HdFecBer = ygot.String(s.(string))
	}

	if s, ok := entry["sd-fec-ber-ch1"]; ok {
		createCh("B").SdFecBer = ygot.String(s.(string))
	}

	if s, ok := entry["post-fec-ber-ch1"]; ok {
		createCh("B").PostFecBer = ygot.String(s.(string))
	}

	return nil
}

func ConfigureTransport(m *model.PacketTransponder) error {
	client, err := NewSONiCDBClient("unix", DEFAULT_REDIS_UNIX_SOCKET, TRANSPORT_CONFIG_DB)
	if err != nil {
		return err
	}

	for k, v := range m.OpticalModule {
		if !strings.HasPrefix(k, "Opt") {
			return fmt.Errorf("invalid optical-module name: %s", k)
		}
		index, err := strconv.Atoi(k[len("Opt"):])
		if err != nil {
			return err
		}
		ch := 1
		grid := 50
		// TODO adhoc default value handling
		// implement more general mechanism
		if v.OpticalModuleFrequency != nil {
			ch = int(*v.OpticalModuleFrequency.Channel)
			grid = gridTypeToInt(v.OpticalModuleFrequency.Grid)
		}
		entry := map[string]interface{}{
			"index":             index - 1,
			"rx-frequency-ch":   ch,
			"rx-frequency-grid": grid,
			"tx-frequency-ch":   ch,
			"tx-frequency-grid": grid,
			"losi":              "off",
			"prbs":              "on",
			"modulation-format": "dp-16qam",
			"ber-interval":      100,
		}
		err = client.SetEntry(CONFIG_TABLE, k, entry)
		if err != nil {
			return err
		}
	}
	return nil
}