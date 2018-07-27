package sonic

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-redis/redis"
)

const (
	APPL_DB = iota
	ASIC_DB
	COUNTERS_DB
	CONFIG_DB                 = 4
	TRANSPORT_CONFIG_DB       = 7
	TRANSPORT_STATE_DB        = 8
	TRANSPORT_NOTIFICATION    = "TRANSPORT_NOTIFICATION"
	DEFAULT_REDIS_UNIX_SOCKET = "/var/run/redis/redis.sock"
)

var keySeparatorMap = map[int]string{
	APPL_DB:             ":",
	CONFIG_DB:           "|",
	TRANSPORT_CONFIG_DB: "|",
	TRANSPORT_STATE_DB:  "|",
}

var tableNameSeparatorMap = map[int]string{
	APPL_DB:             ":",
	CONFIG_DB:           "|",
	TRANSPORT_CONFIG_DB: "|",
	TRANSPORT_STATE_DB:  "|",
}

func serializeKey(db int, keys ...string) string {
	if len(keys) == 1 {
		return keys[0]
	}
	return strings.Join(keys, keySeparatorMap[db])
}

type SONiCDBClient struct {
	client *redis.Client
	db     int
}

func NewSONiCDBClient(network string, addr string, db int) (*SONiCDBClient, error) {
	client := redis.NewClient(&redis.Options{
		Network: network,
		Addr:    addr,
		DB:      db,
	})
	_, err := client.Ping().Result()
	if err != nil {
		return nil, err
	}
	return &SONiCDBClient{
		client: client,
		db:     db,
	}, nil
}

func (c *SONiCDBClient) SendNotification(channel, op, data string, message []interface{}) (int, error) {
	if message != nil {
		message = append([]interface{}{op, data}, message...)
	} else {
		message = []interface{}{op, data}
	}
	buf, err := json.Marshal(message)
	if err != nil {
		return 0, err
	}
	fmt.Println(string(buf))
	r := c.client.Publish(channel, buf)
	return int(r.Val()), r.Err()
}

func (c *SONiCDBClient) GetEntry(table string, keys ...string) (map[string]interface{}, error) {
	key := serializeKey(c.db, keys...)
	_hash := fmt.Sprintf("%s%s%s", strings.ToUpper(table), tableNameSeparatorMap[c.db], key)
	result := c.client.HGetAll(_hash)
	if err := result.Err(); err != nil {
		return nil, err
	}
	ret := make(map[string]interface{})
	for k, v := range result.Val() {
		if k[len(k)-1] == '@' {
			vs := strings.Split(v, ",")
			ret[k[:len(k)-1]] = vs
		} else {
			ret[k] = v
		}
	}
	return ret, nil
}

func (c *SONiCDBClient) GetTable(table string) (map[string]map[string]interface{}, error) {
	pattern := fmt.Sprintf("%s%s*", strings.ToUpper(table), tableNameSeparatorMap[c.db])
	keys := c.client.Keys(pattern)
	if err := keys.Err(); err != nil {
		return nil, err
	}
	t := make(map[string]map[string]interface{})
	for _, key := range keys.Val() {
		ks := strings.Split(key, keySeparatorMap[c.db])
		v, err := c.GetEntry(table, ks[1:]...)
		if err != nil {
			return nil, err
		}
		key = strings.Join(ks[1:], keySeparatorMap[c.db])
		t[key] = v
	}
	return t, nil
}

func serializeEntry(entry map[string]interface{}) map[string]interface{} {
	ret := make(map[string]interface{}, len(entry))
	if entry == nil {
		return nil
	}
	if len(entry) == 0 {
		ret["NULL"] = "NULL"
		return ret
	}
	for k, v := range entry {
		if reflect.TypeOf(v).Kind() == reflect.Slice {
			value := reflect.ValueOf(v)
			values := make([]string, 0, value.Len())
			for i := 0; i < value.Len(); i++ {
				values = append(values, fmt.Sprintf("%v", value.Index(i)))
			}
			ret[fmt.Sprintf("%s@", k)] = strings.Join(values, ",")
		} else {
			ret[k] = v
		}
	}
	return ret
}

func (c *SONiCDBClient) ModEntry(table, key string, entry map[string]interface{}) error {
	_hash := fmt.Sprintf("%s%s%s", strings.ToUpper(table), tableNameSeparatorMap[c.db], key)
	if entry == nil {
		r := c.client.Del(_hash)
		return r.Err()
	}
	r := c.client.HMSet(_hash, serializeEntry(entry))
	return r.Err()
}

func (c *SONiCDBClient) SetEntry(table, key string, entry map[string]interface{}) error {
	original, err := c.GetEntry(table, key)
	if err != nil {
		return err
	}
	err = c.ModEntry(table, key, entry)
	if err != nil {
		return err
	}
	_hash := fmt.Sprintf("%s%s%s", strings.ToUpper(table), tableNameSeparatorMap[c.db], key)
	for k, v := range original {
		_, ok := entry[k]
		if reflect.TypeOf(v).Kind() == reflect.Slice {
			k += "@"
		}
		if !ok {
			c.client.HDel(_hash, k)
		}
	}
	return nil
}
