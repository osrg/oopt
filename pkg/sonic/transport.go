package sonic

import (
	"fmt"
	"strconv"
	"strings"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/openconfig/ygot/ygot"

	"github.com/osrg/oopt/pkg/model"
)

const (
	CONFIG_TABLE      = "MODULE_CONFIG_TABLE"
	STATE_TABLE       = "MODULE_STATE_TABLE"
	MAPPING_TABLE     = "MODULE_MAPPING"
	NETIF_STATE_TABLE = "NETIF_STATE_TABLE"
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
		case "optical-module-frequency.grid":
			var grid int
			e := model.ΛEnum["E_PacketTransport_FrequencyGridType"]
			switch t.Value.Value.(*gnmipb.TypedValue_StringVal).StringVal {
			case e[int64(model.PacketTransport_FrequencyGridType_GRID_100GHZ)].Name:
				grid = 100
			case e[int64(model.PacketTransport_FrequencyGridType_GRID_50GHZ)].Name:
				grid = 50
			case e[int64(model.PacketTransport_FrequencyGridType_GRID_33GHZ)].Name:
				grid = 33
			case e[int64(model.PacketTransport_FrequencyGridType_GRID_25GHZ)].Name:
				grid = 25
			}
			entry["tx-frequency-grid"] = grid
		case "optical-module-frequency.channel":
			ch := t.Value.Value.(*gnmipb.TypedValue_UintVal).UintVal
			entry["tx-frequency-ch"] = ch
		case "ber-interval":
			interval := t.Value.Value.(*gnmipb.TypedValue_UintVal).UintVal
			entry[path] = interval
		case "prbs":
			prbs := t.Value.Value.(*gnmipb.TypedValue_BoolVal).BoolVal
			if prbs {
				entry[path] = "on"
			} else {
				entry[path] = "off"
			}
		case "losi":
			losi := t.Value.Value.(*gnmipb.TypedValue_BoolVal).BoolVal
			if losi {
				entry[path] = "on"
			} else {
				entry[path] = "off"
			}
		case "enabled":
			enabled := t.Value.Value.(*gnmipb.TypedValue_BoolVal).BoolVal
			if enabled {
				entry[path] = "on"
			} else {
				entry[path] = "off"
			}
		case "modulation-type":
			mod := t.Value.Value.(*gnmipb.TypedValue_StringVal).StringVal
			e := model.ΛEnum["E_PacketTransport_OpticalModulationType"]
			switch mod {
			case e[int64(model.PacketTransport_OpticalModulationType_DP_QPSK)].Name:
				entry[path] = "dp-qpsk"
			case e[int64(model.PacketTransport_OpticalModulationType_DP_16QAM)].Name:
				entry[path] = "dp-16qam"
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

func createCh(t *model.PacketTransponder_OpticalModule, n string) *model.PacketTransponder_OpticalModule_ChannelStats {
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

func calculateOccupancy(ch string, t *model.PacketTransponder_OpticalModule, current *model.PacketTransponder) (float32, error) {
	totalCapacity := 0
	switch t.ModulationType {
	case model.PacketTransport_OpticalModulationType_DP_QPSK:
		if ch == "A" {
			totalCapacity = 100000
		}
	case model.PacketTransport_OpticalModulationType_DP_16QAM:
		totalCapacity = 100000
	default:
		return 0, fmt.Errorf("unknown modulation type: %d", t.ModulationType)
	}
	acc := 0
	for _, v := range current.Interface {
		c := v.OpticalModuleConnection
		if c != nil && c.OpticalModule != nil {
			if c.OpticalModule.Name == nil || *t.Name != *c.OpticalModule.Name {
				continue
			}
			if c.OpticalModule.Channel == nil || ch != *c.OpticalModule.Channel {
				continue
			}
			switch v.PortSpeed {
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_100GB:
				acc += 100000
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_100MB:
				acc += 100
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_10GB:
				acc += 10000
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_10MB:
				acc += 10
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_1GB:
				acc += 1000
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_2500MB:
				acc += 2500
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_25GB:
				acc += 25000
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_40GB:
				acc += 40000
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_50GB:
				acc += 50000
			case model.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_5GB:
				acc += 5000
			default:
				return 0, fmt.Errorf("unknown speed: %d", v.PortSpeed)
			}
		}
	}

	if totalCapacity == 0 {
		if acc > 0 {
			return 0, fmt.Errorf("connection assigned to %s.%s which has no capacity. Check modulation format", *t.Name, ch)
		} else {
			return 0, nil
		}
	}

	return float32(acc) * 100 / float32(totalCapacity), nil
}

func FillTransportDefaultConfig(t *model.PacketTransponder_OpticalModule, current *model.PacketTransponder) error {
	if t.OpticalModuleFrequency == nil {
		t.OpticalModuleFrequency = &model.PacketTransponder_OpticalModule_OpticalModuleFrequency{}
	}
	if t.OpticalModuleFrequency.Channel == nil {
		ch := uint8(1)
		t.OpticalModuleFrequency.Channel = &ch
	}
	if t.OpticalModuleFrequency.Grid == model.PacketTransport_FrequencyGridType_UNSET {
		t.OpticalModuleFrequency.Grid = model.PacketTransport_FrequencyGridType_GRID_50GHZ
	}
	if t.Losi == nil {
		t.Losi = ygot.Bool(false)
	}
	if t.Prbs == nil {
		t.Prbs = ygot.Bool(false)
	}
	if t.ModulationType == model.PacketTransport_OpticalModulationType_UNSET {
		t.ModulationType = model.PacketTransport_OpticalModulationType_DP_16QAM
	}
	if t.BerInterval == nil {
		t.BerInterval = ygot.Uint32(100)
	}
	if t.Enabled == nil {
		t.Enabled = ygot.Bool(true)
	}
	if t.AllowOversubscription == nil {
		a := ygot.Bool(false)
		if current.AllowOversubscription != nil {
			a = current.AllowOversubscription
		}
		t.AllowOversubscription = a
	}
	for _, ch := range []string{"A", "B"} {
		occ, err := calculateOccupancy(ch, t, current)
		if err != nil {
			return err
		}
		createCh(t, ch).Occupancy = ygot.String(fmt.Sprintf("%f", occ))
	}
	return nil
}

func FillTransportState(name string, t *model.PacketTransponder_OpticalModule) error {
	if t == nil {
		return fmt.Errorf("model is nil")
	}
	client, err := NewSONiCDBClient("unix", DEFAULT_REDIS_UNIX_SOCKET, TRANSPORT_STATE_DB)
	if err != nil {
		return err
	}

	entry, err := client.GetEntry(MAPPING_TABLE, name)
	if err != nil {
		return err
	}

	s, ok := entry["netif"]
	if !ok {
		return nil
	}

	nid := s.([]string)[0]
	entry, err = client.GetEntry(NETIF_STATE_TABLE, nid)
	if err != nil {
		return err
	}

	if s, ok := entry["rms"]; ok {
		rms := s.(string)
		elems := strings.Split(rms, ",")
		if len(elems) != 4 {
			elems = []string{"0", "0", "0", "0"}
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
		if xq, err := trim(elems[1]); err != nil {
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

	if s, ok := entry["hd-fec-ber"]; ok {
		e := s.([]string)
		createCh(t, "A").HdFecBer = ygot.String(e[0])
		createCh(t, "B").HdFecBer = ygot.String(e[1])
	}

	if s, ok := entry["sd-fec-ber"]; ok {
		e := s.([]string)
		createCh(t, "A").SdFecBer = ygot.String(e[0])
		createCh(t, "B").SdFecBer = ygot.String(e[1])
	}

	if s, ok := entry["post-fec-ber"]; ok {
		e := s.([]string)
		createCh(t, "A").PostFecBer = ygot.String(e[0])
		createCh(t, "B").PostFecBer = ygot.String(e[1])
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
		if err = FillTransportDefaultConfig(v, m); err != nil {
			return err
		}

		ch := int(*v.OpticalModuleFrequency.Channel)
		grid := gridTypeToInt(v.OpticalModuleFrequency.Grid)
		ber := int(*v.BerInterval)

		losi := "off"
		if *v.Losi {
			losi = "on"
		}

		prbs := "off"
		if *v.Prbs {
			prbs = "on"
		}

		mod := "dp-16qam"
		if v.ModulationType == model.PacketTransport_OpticalModulationType_DP_QPSK {
			mod = "dp-qpsk"
		}

		enabled := "on"
		if !(*v.Enabled) {
			enabled = "off"
		}

		entry := map[string]interface{}{
			"index":             index - 1,
			"tx-frequency-ch":   ch,
			"tx-frequency-grid": grid,
			"losi":              losi,
			"prbs":              prbs,
			"modulation-type":   mod,
			"ber-interval":      ber,
			"enabled":           enabled,
		}
		err = client.SetEntry(CONFIG_TABLE, k, entry)
		if err != nil {
			return err
		}
	}
	return nil
}
