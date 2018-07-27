package main

import (
	"fmt"
	"testing"
)

func TestNewOFDPAConfigFromMode(t *testing.T) {
	m, _ := defaultConfiguration()
	config, err := NewOFDPAConfigFromModel(m)
	fmt.Println(err)
	fmt.Println(config)
}
