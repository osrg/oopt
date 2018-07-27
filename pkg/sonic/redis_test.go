package sonic

import (
	"fmt"
	"testing"
)

func TestSetEntry(t *testing.T) {
	client := NewSONiCDBClient("unix", "/var/run/redis/redis.sock", 8)
	client.SetEntry("HELLO", "WORLD", map[string]interface{}{"field": []int{1, 2, 3, 4}, "field2": []float32{1.234, 1435}})
	v, err := client.GetEntry("HELLO", "WORLD")
	fmt.Println(v, err)
	client.ModEntry("HELLO", "WORLD", map[string]interface{}{"field": []int{1, 2, 3, 4, 5}})
	v, err = client.GetEntry("HELLO", "WORLD")
	fmt.Println(v, err)
	client.SetEntry("HELLO", "WORLD", map[string]interface{}{"field": []int{1, 2, 3, 4, 5}})
	v, err = client.GetEntry("HELLO", "WORLD")
	fmt.Println(v, err)
}

func TestNotification(t *testing.T) {
	client := NewSONiCDBClient("unix", "/var/run/redis/redis.sock", 8)
	client.SendNotification(TRANSPORT_NOTIFICATION, "OP", "DATA", nil)
}
